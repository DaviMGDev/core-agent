package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/DaviMGDev/core-agent/internal/link"
	"github.com/DaviMGDev/core-agent/plugins/agent"
	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	"github.com/DaviMGDev/core-agent/plugins/notifications"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// linkStreamTopics are the job transitions the link streams as they happen.
// Ticks are absent: they wake the agent but never cross the link.
var linkStreamTopics = []string{
	notifications.TopicJobStarted,
	notifications.TopicJobCompleted,
	notifications.TopicJobFailed,
	notifications.TopicJobKilled,
}

// linkWakeTopics are the job events that wake the agent through the
// notification clock. A start is absent: the turn that started the job just
// ended. Ticks wake the agent without crossing the link.
var linkWakeTopics = []string{
	notifications.TopicJobTick,
	notifications.TopicJobCompleted,
	notifications.TopicJobFailed,
	notifications.TopicJobKilled,
}

// linkAllTurns asks chat-history for a conversation's whole record; recent
// clamps to the length, so a bound above any real conversation means all.
const linkAllTurns = 1 << 20

// agentAnswer is the agent guest's handler answer: the message the turn
// spoke, and the job it started.
type agentAnswer struct {
	Text string `json:"text,omitempty"`
	Job  string `json:"job,omitempty"`
}

// jobEvent is the tool manager's job payload shape.
type jobEvent struct {
	Job    string          `json:"job"`
	Tool   string          `json:"tool,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// linkWriter serializes outbound lines: the request loop and the job wakes
// share one stream, and a line never interleaves with another.
type linkWriter struct {
	mu  sync.Mutex
	out io.Writer
	log io.Writer
}

// line writes one encoded message; a write that fails is reported to the
// logs (the screen notices the dead pipe itself).
func (w *linkWriter) line(v any) {
	b, err := link.EncodeLine(v)
	if err != nil {
		fmt.Fprintf(w.log, "core-agent: link: %v\n", err)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.out.Write(b); err != nil {
		fmt.Fprintf(w.log, "core-agent: link: %v\n", err)
	}
}

// linkFrontend serves the TUI link: one JSON line in, events out. It is the
// screen's terminal — the way repl-chat renders for the REPL — so it emits
// each turn's speech from the agent's answer (attributed to the chat being
// driven) and streams job transitions from the bus.
type linkFrontend struct {
	ctx     context.Context
	session *composedSession
	out     *linkWriter

	jobMu sync.Mutex        // guards jobs
	jobs  map[string]string // job id -> the chat whose turn started it
}

// runLink serves the TUI link on in/out over a composed session; guest logs
// go to logs, because stdout carries link lines only.
func runLink(ctx context.Context, in io.Reader, out io.Writer, logs io.Writer, cfg sessionConfig) error {
	s, err := composeSession(ctx, cfg, logs)
	if err != nil {
		return err
	}
	frontend := &linkFrontend{
		ctx:     ctx,
		session: s,
		out:     &linkWriter{out: out, log: logs},
		jobs:    make(map[string]string),
	}
	frontend.attach(s)
	err = frontend.serve(in)
	if closeErr := s.Close(); err == nil {
		err = closeErr
	}
	return err
}

// serve reads one request per line until EOF.
func (f *linkFrontend) serve(in io.Reader) error {
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		req, err := link.DecodeRequest(sc.Bytes())
		if err != nil {
			f.out.line(link.Failure{Kind: link.KindError, Error: err.Error()})
			continue
		}
		switch req.Kind {
		case link.KindDeliver:
			f.deliver(req.Chat, req.Text)
		case link.KindLoad:
			f.load(req.Chat)
		case link.KindJobs:
			f.jobsAnswer()
		case link.KindPeek:
			f.peek(req.Job)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("core-agent: link: %w", err)
	}
	return nil
}

// deliver runs one agent turn on the named chat and streams the speech back.
func (f *linkFrontend) deliver(chat, text string) {
	cfg := f.session.agentConfig.Normalized()
	cfg.Conversation = chat
	f.session.gate.Lock()
	answer, err := f.invoke(agent.Wake{Line: text, Config: &cfg})
	f.session.gate.Unlock()
	if err != nil {
		f.out.line(link.Failure{Kind: link.KindError, Error: err.Error()})
		return
	}
	if answer.Job != "" {
		f.jobMu.Lock()
		f.jobs[answer.Job] = chat
		f.jobMu.Unlock()
	}
	if answer.Text != "" {
		f.out.line(link.Message{Kind: link.KindMessage, Chat: chat, Role: "assistant", Text: answer.Text})
	}
}

// load answers a load request with the chat's recorded turns.
func (f *linkFrontend) load(chat string) {
	req, err := json.Marshal(chathistory.Op{Kind: "recent", Conversation: chat, N: linkAllTurns})
	if err != nil {
		f.out.line(link.Failure{Kind: link.KindError, Error: err.Error()})
		return
	}
	resp, err := f.session.history.Handle(f.ctx, req)
	if err != nil {
		f.out.line(link.Failure{Kind: link.KindError, Error: err.Error()})
		return
	}
	var result chathistory.Result
	if err := json.Unmarshal(resp, &result); err != nil {
		f.out.line(link.Failure{Kind: link.KindError, Error: err.Error()})
		return
	}
	turns := make([]link.Turn, 0, len(result.Messages))
	for _, m := range result.Messages {
		turns = append(turns, link.Turn{Role: m.Role, Text: m.Text})
	}
	f.out.line(link.Loaded{Kind: link.KindLoaded, Chat: chat, Messages: turns})
}

// jobsAnswer answers a jobs request with the launch's jobs in creation
// order, each linked to its parent so the screen can draw the tree.
func (f *linkFrontend) jobsAnswer() {
	jobs := f.session.manager.Jobs()
	infos := make([]link.JobInfo, 0, len(jobs))
	for _, job := range jobs {
		infos = append(infos, jobInfo(job))
	}
	f.out.line(link.Jobs{Kind: link.KindJobs, Jobs: infos})
}

// peek answers a peek request with one job's state, last tick, and output so
// far; an unknown id is refused as a factual error.
func (f *linkFrontend) peek(jobID string) {
	job, ok := f.session.manager.Job(jobID)
	if !ok {
		f.out.line(link.Failure{Kind: link.KindError, Error: fmt.Sprintf("unknown job %q", jobID)})
		return
	}
	st := job.Peep()
	peek := link.Peek{
		Kind:   link.KindPeek,
		Job:    st.ID,
		Tool:   st.Tool,
		State:  string(st.State),
		AgeMS:  st.Elapsed.Milliseconds(),
		Output: st.Output,
		Error:  st.Error,
	}
	if parent := job.Parent(); parent != nil {
		peek.Parent = parent.ID()
	}
	if !st.LastTick.IsZero() {
		peek.LastTick = st.LastTick.UTC().Format(time.RFC3339Nano)
		peek.IdleMS = time.Since(st.LastTick).Milliseconds()
	}
	if st.Result != nil {
		if b, err := json.Marshal(st.Result); err == nil {
			peek.Result = b
		}
	}
	f.out.line(peek)
}

// jobInfo projects one job's status onto the link's row shape: identity,
// state, tree link, and timing.
func jobInfo(job *toolmanager.Job) link.JobInfo {
	st := job.Peep()
	info := link.JobInfo{
		Job:   st.ID,
		Tool:  st.Tool,
		State: string(st.State),
		AgeMS: st.Elapsed.Milliseconds(),
	}
	if parent := job.Parent(); parent != nil {
		info.Parent = parent.ID()
	}
	if !st.LastTick.IsZero() {
		info.LastTick = st.LastTick.UTC().Format(time.RFC3339Nano)
		info.IdleMS = time.Since(st.LastTick).Milliseconds()
	}
	return info
}

// invoke runs one agent turn and reads its answer.
func (f *linkFrontend) invoke(wake agent.Wake) (agentAnswer, error) {
	req, err := json.Marshal(wake)
	if err != nil {
		return agentAnswer{}, err
	}
	resp, err := f.session.agent.Handle(f.ctx, req)
	if err != nil {
		return agentAnswer{}, err
	}
	var answer agentAnswer
	if err := json.Unmarshal(resp, &answer); err != nil {
		return agentAnswer{}, fmt.Errorf("core-agent: link: reading agent answer: %w", err)
	}
	return answer, nil
}

// jobID returns the job a job payload names, or "".
func jobID(payload []byte) string {
	var ev jobEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return ""
	}
	return ev.Job
}

// jobChat returns the chat whose turn started a job, when the link saw it
// start.
func (f *linkFrontend) jobChat(job string) string {
	f.jobMu.Lock()
	defer f.jobMu.Unlock()
	return f.jobs[job]
}

// attach subscribes the link's job surfaces: state transitions stream
// immediately, and the agent wakes on the notification clock so a burst of
// job events flushes as one batched turn.
func (f *linkFrontend) attach(s *composedSession) {
	for _, topic := range linkStreamTopics {
		s.bus.Subscribe(topic, linkStream{f: f})
	}
	for _, topic := range linkWakeTopics {
		s.bus.SubscribeClocked(topic, linkWake{f: f})
	}
}

// linkStream is the link's transition subscription: every started,
// completed, failed, and killed line crosses as it happens.
type linkStream struct{ f *linkFrontend }

func (w linkStream) Wake(sub *notifications.Subscription) {
	for _, e := range sub.Take() {
		w.f.jobLine(e.Topic, e.Payload)
	}
}

// linkWake is the link's agent subscription, clocked: each boundary wakes the
// agent with every job event queued since the last one, and an empty boundary
// wakes no one. The batch runs on the chat that started the job, so the
// reply lands where the job was launched; a job the link never saw start runs
// on the launch default.
type linkWake struct{ f *linkFrontend }

func (w linkWake) Wake(sub *notifications.Subscription) {
	busEvents := sub.Take()
	wake := make([]agent.Event, 0, len(busEvents))
	chat := ""
	for _, e := range busEvents {
		if id := jobID(e.Payload); id != "" {
			if c := w.f.jobChat(id); c != "" {
				chat = c
			}
		}
		wake = append(wake, agent.Event{Topic: e.Topic, Payload: json.RawMessage(e.Payload)})
	}
	if len(wake) == 0 {
		return
	}
	cfg := w.f.session.agentConfig.Normalized()
	if chat != "" {
		cfg.Conversation = chat
	}
	w.f.session.gate.Lock()
	answer, err := w.f.invoke(agent.Wake{Events: wake, Config: &cfg})
	w.f.session.gate.Unlock()
	if err != nil {
		w.f.out.line(link.Failure{Kind: link.KindError, Error: err.Error()})
		return
	}
	if answer.Text != "" {
		w.f.out.line(link.Message{Kind: link.KindMessage, Chat: cfg.Conversation, Role: "assistant", Text: answer.Text})
	}
}

// jobLine renders one job transition as a stream line.
func (f *linkFrontend) jobLine(topic string, payload []byte) {
	var ev jobEvent
	_ = json.Unmarshal(payload, &ev)
	job := link.Job{Kind: link.KindJob, Job: ev.Job}
	switch topic {
	case notifications.TopicJobStarted:
		job.Event = "started"
		job.Tool = ev.Tool
	case notifications.TopicJobCompleted:
		job.Event = "completed"
		job.Detail = describeResult(ev.Result)
	case notifications.TopicJobFailed:
		job.Event = "failed"
		job.Detail = ev.Error
	case notifications.TopicJobKilled:
		job.Event = "killed"
	default:
		return
	}
	f.out.line(job)
}

// describeResult renders a job result: a string result as itself, any other
// JSON as its compact document.
func describeResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
