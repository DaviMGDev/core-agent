package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/DaviMGDev/core-agent/internal/link"
	"github.com/DaviMGDev/core-agent/plugins/agent"
	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	"github.com/DaviMGDev/core-agent/plugins/notifications"
)

// linkJobTopics are the job events the link hears: every state transition the
// screen streams, plus ticks — which wake the agent but never cross the link.
var linkJobTopics = []string{
	notifications.TopicJobStarted,
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
	for _, topic := range linkJobTopics {
		s.bus.Subscribe(topic, frontend)
	}
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

// Wake is the link's job subscription: stream each state transition, wake
// the agent with the batch (a start is not a wake: the turn that started the
// job just ended), and stream the unprompted speech back. The batch runs on
// the chat that started the job, so the reply lands where the job was
// launched; a job the link never saw start runs on the launch default.
func (f *linkFrontend) Wake(sub *notifications.Subscription) {
	busEvents := sub.Take()
	var wake []agent.Event
	chat := ""
	for _, e := range busEvents {
		switch e.Topic {
		case notifications.TopicJobTick:
			// Ticks wake the agent; they never paint the screen.
		default:
			f.jobLine(e.Topic, e.Payload)
		}
		if id := jobID(e.Payload); id != "" {
			if c := f.jobChat(id); c != "" {
				chat = c
			}
		}
		if e.Topic != notifications.TopicJobStarted {
			wake = append(wake, agent.Event{Topic: e.Topic, Payload: json.RawMessage(e.Payload)})
		}
	}
	if len(wake) == 0 {
		return
	}
	cfg := f.session.agentConfig.Normalized()
	if chat != "" {
		cfg.Conversation = chat
	}
	f.session.gate.Lock()
	answer, err := f.invoke(agent.Wake{Events: wake, Config: &cfg})
	f.session.gate.Unlock()
	if err != nil {
		f.out.line(link.Failure{Kind: link.KindError, Error: err.Error()})
		return
	}
	if answer.Text != "" {
		f.out.line(link.Message{Kind: link.KindMessage, Chat: cfg.Conversation, Role: "assistant", Text: answer.Text})
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
