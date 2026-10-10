package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DaviMGDev/core-agent/internal/link"
	"github.com/DaviMGDev/core-agent/plugins/notifications"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// linkLines parses the link's stdout into the typed messages it carried.
func linkLines(t *testing.T, stream string) []any {
	t.Helper()
	var lines []any
	for _, raw := range strings.Split(stream, "\n") {
		if raw == "" {
			continue
		}
		var probe struct {
			Kind link.Kind `json:"kind"`
		}
		if err := json.Unmarshal([]byte(raw), &probe); err != nil {
			t.Fatalf("link line %q: %v", raw, err)
		}
		switch probe.Kind {
		case link.KindMessage:
			var m link.Message
			if err := json.Unmarshal([]byte(raw), &m); err != nil {
				t.Fatalf("message %q: %v", raw, err)
			}
			lines = append(lines, m)
		case link.KindLoaded:
			var l link.Loaded
			if err := json.Unmarshal([]byte(raw), &l); err != nil {
				t.Fatalf("loaded %q: %v", raw, err)
			}
			lines = append(lines, l)
		case link.KindJob:
			var j link.Job
			if err := json.Unmarshal([]byte(raw), &j); err != nil {
				t.Fatalf("job %q: %v", raw, err)
			}
			lines = append(lines, j)
		case link.KindJobs:
			var js link.Jobs
			if err := json.Unmarshal([]byte(raw), &js); err != nil {
				t.Fatalf("jobs %q: %v", raw, err)
			}
			lines = append(lines, js)
		case link.KindPeek:
			var pk link.Peek
			if err := json.Unmarshal([]byte(raw), &pk); err != nil {
				t.Fatalf("peek %q: %v", raw, err)
			}
			lines = append(lines, pk)
		case link.KindError:
			var f link.Failure
			if err := json.Unmarshal([]byte(raw), &f); err != nil {
				t.Fatalf("failure %q: %v", raw, err)
			}
			lines = append(lines, f)
		default:
			t.Fatalf("unknown kind %q in %q", probe.Kind, raw)
		}
	}
	return lines
}

// pull filters the parsed lines by type.
func pull[T any](lines []any) []T {
	var out []T
	for _, v := range lines {
		if tv, ok := v.(T); ok {
			out = append(out, tv)
		}
	}
	return out
}

// linkSession boots a link session on input and returns stdout and logs.
func linkSession(t *testing.T, input string) (string, string) {
	t.Helper()
	t.Setenv("CORE_DIR", t.TempDir())
	cfg, err := loadSession(true, "tester")
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	var out, logs transcript
	if err := runLink(context.Background(), strings.NewReader(input), &out, &logs, cfg); err != nil {
		t.Fatalf("runLink: %v", err)
	}
	return out.String(), logs.String()
}

// TestRunLinkBootsAndExitsOnEOF proves the link mode composes the seven
// plugins, emits nothing for an empty stream, and exits cleanly on EOF.
func TestRunLinkBootsAndExitsOnEOF(t *testing.T) {
	out, logs := linkSession(t, "")
	if out != "" {
		t.Errorf("link output = %q, want empty on an empty stream", out)
	}
	for _, want := range []string{
		`provider-manager: 1 provider(s) ready`,
		`provider-openai: provider "openai" registered`,
		`model-manager: 2 model view(s) ready`,
		`chat-history: conversation`,
		`context-manager: window ready`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("composition logs missing %q:\n%s", want, logs)
		}
	}
}

// TestRunLinkDeliversOnTheNamedChat proves one delivered line runs one agent
// turn on that chat: the speech crosses the link tagged with the chat, and
// chat-history holds both the user and the assistant turn.
func TestRunLinkDeliversOnTheNamedChat(t *testing.T) {
	out, _ := linkSession(t,
		`{"kind":"deliver","chat":"alpha","text":"hello link"}`+"\n"+
			`{"kind":"load","chat":"alpha"}`+"\n")
	lines := linkLines(t, out)

	messages := pull[link.Message](lines)
	if len(messages) != 1 {
		t.Fatalf("message lines = %d, want exactly 1:\n%s", len(messages), out)
	}
	m := messages[0]
	if m.Chat != "alpha" || m.Role != "assistant" {
		t.Errorf("message = %+v, want an assistant message on alpha", m)
	}
	if want := "mock(gemma4:cloud): hello link (context:2)"; m.Text != want {
		t.Errorf("message text = %q, want %q", m.Text, want)
	}

	loaded := pull[link.Loaded](lines)
	if len(loaded) != 1 || loaded[0].Chat != "alpha" {
		t.Fatalf("load answers = %+v, want one for alpha", loaded)
	}
	if len(loaded[0].Messages) != 2 {
		t.Fatalf("recorded turns = %d, want 2:\n%+v", len(loaded[0].Messages), loaded[0].Messages)
	}
	if got := loaded[0].Messages[0]; got.Role != "user" || got.Text != "hello link" {
		t.Errorf("first turn = %+v, want the user line", got)
	}
	if got := loaded[0].Messages[1]; got.Role != "assistant" || got.Text != m.Text {
		t.Errorf("second turn = %+v, want the assistant reply", got)
	}
}

// TestRunLinkLoadsTurnsInOrderAndUnknownChatsEmpty proves a load answers a
// chat's turns in append order, and an unknown chat answers with none.
func TestRunLinkLoadsTurnsInOrderAndUnknownChatsEmpty(t *testing.T) {
	out, _ := linkSession(t,
		`{"kind":"deliver","chat":"alpha","text":"first"}`+"\n"+
			`{"kind":"deliver","chat":"alpha","text":"second"}`+"\n"+
			`{"kind":"load","chat":"alpha"}`+"\n"+
			`{"kind":"load","chat":"missing"}`+"\n")
	loaded := pull[link.Loaded](linkLines(t, out))
	if len(loaded) != 2 {
		t.Fatalf("load answers = %d, want 2:\n%s", len(loaded), out)
	}
	alpha := loaded[0]
	if alpha.Chat != "alpha" || len(alpha.Messages) != 4 {
		t.Fatalf("alpha load = %+v, want 4 turns", alpha)
	}
	wantRoles := []string{"user", "assistant", "user", "assistant"}
	wantTexts := []string{"first", "", "second", ""}
	for i, m := range alpha.Messages {
		if m.Role != wantRoles[i] {
			t.Errorf("turn %d role = %q, want %q", i, m.Role, wantRoles[i])
		}
		if wantTexts[i] != "" && m.Text != wantTexts[i] {
			t.Errorf("turn %d text = %q, want %q", i, m.Text, wantTexts[i])
		}
	}
	if !strings.Contains(alpha.Messages[1].Text, "first") || !strings.Contains(alpha.Messages[3].Text, "second") {
		t.Errorf("assistant turns out of append order: %+v", alpha.Messages)
	}
	if missing := loaded[1]; missing.Chat != "missing" || len(missing.Messages) != 0 {
		t.Errorf("missing chat load = %+v, want an empty record", missing)
	}
}

// writeLine sends one request line to a running session.
func writeLine(t *testing.T, pw *io.PipeWriter, line string) {
	t.Helper()
	if _, err := pw.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write %q: %v", line, err)
	}
}

// waitOut blocks until the output contains the marker.
func waitOut(t *testing.T, out *transcript, marker string) {
	t.Helper()
	waitFor(t, "output containing "+marker, func() bool { return strings.Contains(out.String(), marker) }, out)
}

// waitFor blocks until cond holds; on timeout it reports what and dumps out.
func waitFor(t *testing.T, what string, cond func() bool, out *transcript) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s:\n%s", what, out.String())
}

// okRunner is a tool that succeeds immediately.
func okRunner(context.Context, json.RawMessage, *toolmanager.Output) (any, error) {
	return "fine", nil
}

// failRunner is a tool that fails immediately.
func failRunner(context.Context, json.RawMessage, *toolmanager.Output) (any, error) {
	return nil, errors.New("boom")
}

// blockingRunner is a tool that runs until released or killed.
func blockingRunner(release <-chan struct{}) toolmanager.Runner {
	return func(ctx context.Context, args json.RawMessage, out *toolmanager.Output) (any, error) {
		select {
		case <-release:
			return "released", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// startDrivenLink boots a link session on a pipe with the in-process mock
// provider and returns the pipe, the manager (once composed), the outputs,
// and a stop function that ends the session and asserts a clean return.
func startDrivenLink(t *testing.T) (*io.PipeWriter, *toolmanager.Manager, *transcript, *transcript, func()) {
	t.Helper()
	t.Setenv("CORE_DIR", t.TempDir())
	cfg, err := loadSession(true, "tester")
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	return startDrivenLinkWith(t, cfg)
}

// startDrivenLinkWith is startDrivenLink over a caller-supplied session
// configuration (a scripted provider, say).
func startDrivenLinkWith(t *testing.T, cfg sessionConfig) (*io.PipeWriter, *toolmanager.Manager, *transcript, *transcript, func()) {
	t.Helper()
	managerReady := make(chan *toolmanager.Manager, 1)
	cfg.onComposed = func(m *toolmanager.Manager) { managerReady <- m }

	var out, logs transcript
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runLink(context.Background(), pr, &out, &logs, cfg) }()
	manager := <-managerReady

	// A load synchronizes: the session answers only once serve runs, and
	// serve starts after the job subscriptions exist.
	writeLine(t, pw, `{"kind":"load","chat":"sync"}`)
	waitOut(t, &out, `"chat":"sync"`)

	stop := func() {
		t.Helper()
		_ = pw.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("runLink: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("session did not end:\n%s", out.String())
		}
	}
	return pw, manager, &out, &logs, stop
}

// TestRunLinkAttributesSpeechEverywhere proves the prompted turn's speech is
// tagged with the delivered chat and the unprompted job-wake speech is tagged
// with the launch's default conversation.
func TestRunLinkAttributesSpeechEverywhere(t *testing.T) {
	pw, manager, out, _, stop := startDrivenLink(t)
	defer stop()

	writeLine(t, pw, `{"kind":"deliver","chat":"alpha","text":"prompted"}`)
	waitOut(t, out, `"chat":"alpha"`)

	if err := manager.Registry().Declare(toolmanager.Tool{Name: "ok", Run: okRunner}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	manager.Start("ok", nil, 0)
	waitOut(t, out, "job.completed")

	messages := pull[link.Message](linkLines(t, out.String()))
	var prompted, unprompted bool
	for _, m := range messages {
		switch {
		case m.Chat == "alpha":
			prompted = true
		case m.Chat == "main" && strings.Contains(m.Text, "job.completed"):
			unprompted = true
		}
	}
	if !prompted {
		t.Errorf("no message tagged alpha (the delivered chat):\n%s", out.String())
	}
	if !unprompted {
		t.Errorf("no unprompted message tagged the default chat main:\n%s", out.String())
	}
}

// TestRunLinkStreamsEveryJobTransitionOnce proves started, completed, failed,
// and killed each cross the link exactly once per job.
func TestRunLinkStreamsEveryJobTransitionOnce(t *testing.T) {
	_, manager, out, _, stop := startDrivenLink(t)
	defer stop()

	release := make(chan struct{})
	for _, tool := range []toolmanager.Tool{
		{Name: "ok", Run: okRunner},
		{Name: "fail", Run: failRunner},
		{Name: "slow", Run: blockingRunner(release)},
	} {
		if err := manager.Registry().Declare(tool); err != nil {
			t.Fatalf("Declare(%s): %v", tool.Name, err)
		}
	}

	manager.Start("ok", nil, 0)
	manager.Start("fail", nil, 0)
	slow := manager.Start("slow", nil, 0)
	waitOut(t, out, `"tool":"slow"`)
	waitOut(t, out, `"event":"failed"`)
	manager.Kill(slow)
	waitOut(t, out, `"event":"killed"`)
	waitOut(t, out, `"event":"completed"`)
	close(release)

	jobs := pull[link.Job](linkLines(t, out.String()))
	counts := make(map[string]int)
	starts := make(map[string]string)
	events := make(map[string][]string)
	for _, j := range jobs {
		counts[j.Job+"/"+j.Event]++
		events[j.Job] = append(events[j.Job], j.Event)
		if j.Event == "started" {
			starts[j.Job] = j.Tool
		}
	}
	for transition, n := range counts {
		if n != 1 {
			t.Errorf("transition %s crossed %d times, want once", transition, n)
		}
	}
	byTool := make(map[string]string)
	for id, tool := range starts {
		byTool[tool] = id
	}
	want := map[string][]string{
		"ok":   {"started", "completed"},
		"fail": {"started", "failed"},
		"slow": {"started", "killed"},
	}
	for tool, wantEvents := range want {
		id, ok := byTool[tool]
		if !ok {
			t.Errorf("no started transition for tool %s", tool)
			continue
		}
		got := events[id]
		// Cross-topic delivery order is not a bus guarantee (only
		// per-subscriber order is), and the link subscribes per topic —
		// so a completed line may precede its started line. Compare as
		// sets: the same transitions, each exactly once.
		if !sameTransitions(got, wantEvents) {
			t.Errorf("job %s (%s) transitions = %v, want %v in any order", id, tool, got, wantEvents)
		}
	}
}

// sameTransitions reports whether got and want carry the same transitions
// with the same multiplicities, in any order.
func sameTransitions(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[string]int, len(got))
	for _, e := range got {
		counts[e]++
	}
	for _, e := range want {
		counts[e]--
		if counts[e] < 0 {
			return false
		}
	}
	return true
}

// TestRunLinkNeverStreamsTicksAndWritesWholeLines proves a ticking job's
// ticks stay off the link while its wakes race the request loop, and every
// line the link emitted is a whole JSON message.
func TestRunLinkNeverStreamsTicksAndWritesWholeLines(t *testing.T) {
	pw, manager, out, _, stop := startDrivenLink(t)
	defer stop()

	release := make(chan struct{})
	if err := manager.Registry().Declare(toolmanager.Tool{Name: "slow", Run: blockingRunner(release)}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	manager.Start("slow", nil, 50*time.Millisecond)
	for i := 0; i < 6; i++ {
		writeLine(t, pw, `{"kind":"deliver","chat":"c`+string(rune('0'+i))+`","text":"turn `+string(rune('0'+i))+`"}`)
	}

	// Six deliver replies and a tick wake race through the link; the tick
	// wake is proven by the mock echoing its wake line.
	waitFor(t, "deliver replies racing a tick wake", func() bool {
		return strings.Contains(out.String(), "job.tick") &&
			strings.Count(out.String(), `"kind":"message"`) >= 7
	}, out)
	for _, j := range pull[link.Job](linkLines(t, out.String())) {
		if j.Event == "tick" {
			t.Fatalf("a tick crossed the link: %+v", j)
		}
	}
	close(release)
	waitOut(t, out, `"event":"completed"`)
}

// TestRunLinkAnswersJobsAndPeek proves the read-only job queries: jobs lists
// the launch in creation order with tree links, peek returns state, last
// tick, and output so far, and an unknown id answers a factual error.
func TestRunLinkAnswersJobsAndPeek(t *testing.T) {
	pw, manager, out, _, stop := startDrivenLink(t)
	defer stop()

	release := make(chan struct{})
	defer close(release)
	if err := manager.Registry().Declare(toolmanager.Tool{Name: "slow", Run: func(ctx context.Context, _ json.RawMessage, o *toolmanager.Output) (any, error) {
		_, _ = o.Write([]byte("half a report\n"))
		select {
		case <-release:
			return "released", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}); err != nil {
		t.Fatalf("Declare: %v", err)
	}

	root := manager.Start("slow", nil, 0)
	child := manager.StartChild(root, "slow", nil, 5*time.Millisecond)
	waitFor(t, "the root's output", func() bool {
		return strings.Contains(manager.Peep(root).Output, "half a report")
	}, out)
	waitFor(t, "the child's first tick", func() bool {
		return !manager.Peep(child).LastTick.IsZero()
	}, out)

	writeLine(t, pw, `{"kind":"jobs"}`)
	waitOut(t, out, `"kind":"jobs"`)
	answers := pull[link.Jobs](linkLines(t, out.String()))
	if len(answers) != 1 {
		t.Fatalf("jobs answers = %d, want 1:\n%s", len(answers), out.String())
	}
	rows := answers[0].Jobs
	if len(rows) != 2 {
		t.Fatalf("jobs rows = %+v, want two", rows)
	}
	if rows[0].Job != "job-1" || rows[0].State != "running" || rows[0].Parent != "" {
		t.Errorf("root row = %+v, want job-1 running at the top", rows[0])
	}
	if rows[1].Job != "job-2" || rows[1].Parent != "job-1" || rows[1].LastTick == "" {
		t.Errorf("child row = %+v, want job-2 linked to job-1 with a tick", rows[1])
	}

	writeLine(t, pw, `{"kind":"peek","job":"job-1"}`)
	waitOut(t, out, `"kind":"peek"`)
	peeks := pull[link.Peek](linkLines(t, out.String()))
	if len(peeks) != 1 {
		t.Fatalf("peek answers = %d, want 1:\n%s", len(peeks), out.String())
	}
	if peeks[0].State != "running" || peeks[0].Tool != "slow" || !strings.Contains(peeks[0].Output, "half a report") {
		t.Errorf("peek = %+v, want the running job with its output", peeks[0])
	}

	writeLine(t, pw, `{"kind":"peek","job":"job-99"}`)
	waitOut(t, out, "unknown job")
	found := false
	for _, f := range pull[link.Failure](linkLines(t, out.String())) {
		if strings.Contains(f.Error, "unknown job") {
			found = true
		}
	}
	if !found {
		t.Errorf("no factual unknown-job error in:\n%s", out.String())
	}
}

// chunkWriter records every Write it receives as one chunk.
type chunkWriter struct {
	mu     sync.Mutex
	chunks [][]byte
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.chunks = append(w.chunks, append([]byte(nil), p...))
	return len(p), nil
}

// TestLinkWriterWritesWholeLines proves concurrent writers cannot interleave:
// every chunk that reaches the stream is exactly one parseable line.
func TestLinkWriterWritesWholeLines(t *testing.T) {
	stream := &chunkWriter{}
	w := &linkWriter{out: stream, log: io.Discard}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				w.line(link.Message{
					Kind: link.KindMessage,
					Chat: "c" + string(rune('0'+g)),
					Role: "assistant",
					Text: "line one\nline two",
				})
			}
		}(g)
	}
	wg.Wait()

	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.chunks) != 8*25 {
		t.Fatalf("writes = %d, want %d", len(stream.chunks), 8*25)
	}
	for _, chunk := range stream.chunks {
		if strings.Count(string(chunk), "\n") != 1 || !strings.HasSuffix(string(chunk), "\n") {
			t.Fatalf("chunk %q is not exactly one line", chunk)
		}
		var m link.Message
		if err := json.Unmarshal(chunk, &m); err != nil {
			t.Fatalf("chunk %q: %v", chunk, err)
		}
	}
}

// TestRunLinkRoutesJobWakeSpeechToTheStartingChat proves a job started by a
// delivered turn wakes the agent on that chat: the unprompted speech returns
// tagged with the chat, not with the launch default.
func TestRunLinkRoutesJobWakeSpeechToTheStartingChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		answer := "ok"
		switch line := lastUserMessage(raw); {
		case strings.HasPrefix(line, "delegate"):
			answer = `{"tool":"subagent","args":{"brief":"say hi"}}`
		case strings.HasPrefix(line, "say hi"):
			answer = "hi"
		case strings.HasPrefix(line, "job.completed"):
			answer = "the delegation finished"
		}
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, answer)
	}))
	defer srv.Close()

	cfg := sessionFrom(t, testResolved(t), "tester")
	cfg.providers = providerDoc(testProvider{Name: "local", Endpoint: srv.URL, Models: []string{"gemma4:cloud"}})
	pw, _, out, _, stop := startDrivenLinkWith(t, cfg)
	defer stop()

	writeLine(t, pw, `{"kind":"deliver","chat":"chat-7","text":"delegate the count"}`)
	waitOut(t, out, `"event":"completed"`)
	waitFor(t, "the wake speech on the delivered chat", func() bool {
		for _, m := range pull[link.Message](linkLines(t, out.String())) {
			if m.Chat == "chat-7" && strings.Contains(m.Text, "delegation finished") {
				return true
			}
		}
		return false
	}, out)
}

// TestRunLinkAnswersMalformedLinesAndSurvives proves a malformed line is
// answered with an error and the loop keeps serving the next request.
func TestRunLinkAnswersMalformedLinesAndSurvives(t *testing.T) {
	out, _ := linkSession(t,
		"you> hello\n"+
			`{"kind":"deliver","chat":"alpha","text":"alive"}`+"\n")
	lines := linkLines(t, out)
	failures := pull[link.Failure](lines)
	if len(failures) != 1 {
		t.Fatalf("failure lines = %d, want 1:\n%s", len(failures), out)
	}
	messages := pull[link.Message](lines)
	if len(messages) != 1 || messages[0].Chat != "alpha" || !strings.Contains(messages[0].Text, "alive") {
		t.Fatalf("the loop did not serve the next deliver:\n%s", out)
	}
}

// TestDescribeResultUnwrapsAndTruncates proves describeResult unwraps reply fields
// without raw JSON escapes and truncates long results with a marker.
func TestDescribeResultUnwrapsAndTruncates(t *testing.T) {
	// Unwraps reply field without JSON quotes/braces.
	res := describeResult(json.RawMessage(`{"reply":"clean unescaped reply text"}`))
	if res != "clean unescaped reply text" {
		t.Errorf("describeResult = %q, want 'clean unescaped reply text'", res)
	}

	// Long output is truncated with marker.
	longStr := strings.Repeat("abcdefghij ", 20) // 220 chars
	payload, _ := json.Marshal(map[string]any{"reply": longStr})
	truncated := describeResult(payload)
	if !strings.Contains(truncated, "... [") || !strings.Contains(truncated, "chars truncated]") {
		t.Errorf("truncated = %q, want truncation marker", truncated)
	}
	if len(truncated) >= len(longStr) {
		t.Errorf("truncated length %d not smaller than original %d", len(truncated), len(longStr))
	}
}

// TestJobStartedCarriesArgsBrief proves job.started events format a brief
// of the tool arguments into the detail field.
func TestJobStartedCarriesArgsBrief(t *testing.T) {
	var buf bytes.Buffer
	f := &linkFrontend{
		out: &linkWriter{out: &buf},
	}
	payload, _ := json.Marshal(map[string]any{
		"job":  "job-1",
		"tool": "bash",
		"args": map[string]any{"command": "echo hello world"},
	})
	f.jobLine(notifications.TopicJobStarted, payload)

	var job link.Job
	if err := json.Unmarshal(buf.Bytes(), &job); err != nil {
		t.Fatalf("unmarshal job: %v", err)
	}
	if job.Event != "started" {
		t.Errorf("job.Event = %q, want started", job.Event)
	}
	if job.Tool != "bash" {
		t.Errorf("job.Tool = %q, want bash", job.Tool)
	}
	if job.Detail != "echo hello world" {
		t.Errorf("job.Detail = %q, want 'echo hello world'", job.Detail)
	}
}
