package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	contextmanager "github.com/DaviMGDev/core-agent/plugins/context-manager"
	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// scriptJobs is a scripted job service: StartJob records the request and
// answers a canned handle.
type scriptJobs struct {
	mu    sync.Mutex
	calls []string
	id    string
}

func (s *scriptJobs) StartJob(_ *runtime.Instance, req []byte) ([]byte, error) {
	var r struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args,omitempty"`
	}
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.calls = append(s.calls, r.Tool)
	s.mu.Unlock()
	return json.Marshal(map[string]string{"job": s.id})
}

func (s *scriptJobs) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// stack composes the native pipeline the agent turns on: an in-memory
// record, a window, a registry with one mock provider, the model service,
// and the agent with scripted jobs and a capturing publish path.
type stack struct {
	sched   *runtime.Scheduler
	history *chathistory.Component
	agent   *Component
	publish *publishCapture
	jobs    *scriptJobs
	log     *lockedBuffer
}

type publishCapture struct {
	mu   sync.Mutex
	text []string
}

func (p *publishCapture) publish(text string) error {
	p.mu.Lock()
	p.text = append(p.text, text)
	p.mu.Unlock()
	return nil
}

func (p *publishCapture) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.text...)
}

func composeStack(t *testing.T, agentPayload string) *stack {
	t.Helper()
	var log lockedBuffer
	pub := &publishCapture{}
	jobs := &scriptJobs{id: "job-1"}
	history := chathistory.NewComponent(&log)
	ctxMgr := contextmanager.NewComponent(&log)
	pm := providermanager.NewComponent(&log)
	mm := modelmanager.NewComponent(&log)
	ag := NewComponent(&log, jobs, pub.publish)

	sched := runtime.New()
	t.Cleanup(func() { _ = sched.Close() })

	insert := func(comp runtime.Component, payload string) mcontext.FiberID {
		t.Helper()
		id, err := sched.Insert(comp, payload)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		return id
	}
	// Inserts are strictly sequenced with activation between them: an
	// activating worker reads the scheduler's fiber table through
	// runtime.Get, which races with the loop's own table writes while it
	// still has inserts to process. Sequencing keeps the loop parked
	// while a worker resolves its keys.
	waitActive(t, sched, insert(pm, `{"providers":[{"name":"mock","mock":true,"models":["fast"]}]}`))
	waitActive(t, sched, insert(mm, `{"models":[]}`))
	waitActive(t, sched, insert(history, "main"))
	waitActive(t, sched, insert(ctxMgr, `{"budget":4096}`))
	waitActive(t, sched, insert(ag, agentPayload))
	return &stack{sched: sched, history: history, agent: ag, publish: pub, jobs: jobs, log: &log}
}

func handleWake(t *testing.T, ctx context.Context, ag *Component, wake Wake) wakeResponse {
	t.Helper()
	b, err := json.Marshal(wake)
	if err != nil {
		t.Fatalf("wake marshal: %v", err)
	}
	raw, err := ag.Handle(ctx, b)
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	var out wakeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("answer decode %q: %v", raw, err)
	}
	return out
}

// TestTurnYieldsOnePublishedMessage runs one mock turn end to end: the
// model's answer is appended to the record and published once.
func TestTurnYieldsOnePublishedMessage(t *testing.T) {
	ctx := context.Background()
	st := composeStack(t, `{"conversation":"main","model":"fast","budget":10}`)

	out := handleWake(t, ctx, st.agent, Wake{Line: "hello"})
	if out.Text == "" || out.Job != "" {
		t.Fatalf("answer = %+v, want spoken text and no job", out)
	}
	if texts := st.publish.all(); len(texts) != 1 || texts[0] != out.Text {
		t.Fatalf("published = %v, want exactly the spoken text", texts)
	}
	if !strings.Contains(out.Text, "mock(fast)") || !strings.Contains(out.Text, "hello") {
		t.Fatalf("answer = %q, want the mock reply to the line", out.Text)
	}

	raw, err := st.history.Handle(ctx, []byte(`{"op":"recent","conversation":"main","n":10}`))
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	var res struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("recent decode: %v", err)
	}
	if len(res.Messages) != 2 || res.Messages[0].Role != "user" || res.Messages[1].Role != "assistant" {
		t.Fatalf("record = %+v, want the user line and the answer", res.Messages)
	}
}

// TestPrivateTurnStaysInTheChildConversation proves the subagent path: a
// private turn with its own config speaks into the child's conversation and
// publishes nothing.
func TestPrivateTurnStaysInTheChildConversation(t *testing.T) {
	ctx := context.Background()
	st := composeStack(t, `{"conversation":"main","model":"fast","budget":10}`)

	child := &Config{Conversation: "subagent-job-1", Model: "fast", Budget: 5}
	out := handleWake(t, ctx, st.agent, Wake{Line: "brief", Config: child, Private: true})
	if out.Text == "" {
		t.Fatalf("answer = %+v, want the child's spoken text", out)
	}
	if texts := st.publish.all(); len(texts) != 0 {
		t.Fatalf("published = %v, want nothing for a private turn", texts)
	}

	raw, err := st.history.Handle(ctx, []byte(`{"op":"recent","conversation":"subagent-job-1","n":10}`))
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	var res struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("recent decode: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("child record = %+v, want the brief and the answer", res.Messages)
	}
}

// TestEveryModelRequestCarriesOneSystemMessage proves the prompt crosses
// the whole native pipeline untouched: a scripted provider records the
// request the turn sends, and exactly one system-role message leads it —
// naming the presented tool — with the user turn following.
func TestEveryModelRequestCarriesOneSystemMessage(t *testing.T) {
	var (
		mu   sync.Mutex
		body []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = b
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"scripted"}}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	var log lockedBuffer
	pub := &publishCapture{}
	jobs := &scriptJobs{id: "job-1"}
	sched := runtime.New()
	t.Cleanup(func() { _ = sched.Close() })
	insert := func(comp runtime.Component, payload string) mcontext.FiberID {
		t.Helper()
		id, err := sched.Insert(comp, payload)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		return id
	}
	endpoint, _ := json.Marshal(map[string]any{"providers": []any{map[string]any{
		"name": "scripted", "endpoint": srv.URL, "models": []string{"m"},
	}}})
	agentPayload, _ := json.Marshal(map[string]any{
		"conversation": "main", "model": "m", "budget": 10,
		"tools": []any{map[string]any{"name": "read", "description": "read a file"}},
	})
	waitActive(t, sched, insert(providermanager.NewComponent(&log), string(endpoint)))
	waitActive(t, sched, insert(modelmanager.NewComponent(&log), `{"models":[]}`))
	waitActive(t, sched, insert(chathistory.NewComponent(&log), "main"))
	waitActive(t, sched, insert(contextmanager.NewComponent(&log), `{"budget":4096}`))
	ag := NewComponent(&log, jobs, pub.publish)
	waitActive(t, sched, insert(ag, string(agentPayload)))

	out := handleWake(t, ctx, ag, Wake{Line: "hello"})
	if out.Text != "scripted" {
		t.Fatalf("answer = %+v, want the scripted reply", out)
	}

	mu.Lock()
	defer mu.Unlock()
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("request decode %s: %v", body, err)
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != "system" {
		t.Fatalf("request messages = %+v, want the system message first", req.Messages)
	}
	system := 0
	for _, m := range req.Messages {
		if m.Role == "system" {
			system++
			if !strings.Contains(m.Content, "read") {
				t.Errorf("system message does not name the presented tool:\n%s", m.Content)
			}
		}
	}
	if system != 1 {
		t.Fatalf("request carries %d system messages, want exactly one", system)
	}
	if req.Messages[1].Role != "user" || req.Messages[1].Content != "hello" {
		t.Fatalf("request messages = %+v, want the user turn after the system message", req.Messages)
	}
}

// TestEmptyWakeAnswersEmpty proves a bad wake and an empty line answer
// empty bytes with no error, like the guest's no-response path.
func TestEmptyWakeAnswersEmpty(t *testing.T) {
	ctx := context.Background()
	st := composeStack(t, `{"conversation":"main","model":"fast","budget":10}`)

	for _, req := range []string{`not json`, `{}`, `{"events":[]}`} {
		raw, err := st.agent.Handle(ctx, []byte(req))
		if err != nil {
			t.Fatalf("handle %q errored: %v, want an empty answer", req, err)
		}
		if len(raw) != 0 {
			t.Fatalf("handle %q = %s, want no answer", req, raw)
		}
	}
}

// TestBadActivationConfigFails proves an invalid payload fails the load.
// The pipeline providers are inserted first: activation gates on inject
// satisfaction, so the failure must come from parsing, not from waiting.
func TestBadActivationConfigFails(t *testing.T) {
	var log lockedBuffer
	ag := NewComponent(&log, &scriptJobs{}, nil)

	sched := runtime.New()
	defer sched.Close()
	insert := func(comp runtime.Component, payload string) mcontext.FiberID {
		t.Helper()
		id, err := sched.Insert(comp, payload)
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		return id
	}
	waitActive(t, sched, insert(providermanager.NewComponent(&log), `{"providers":[]}`))
	waitActive(t, sched, insert(modelmanager.NewComponent(&log), `{"models":[]}`))
	waitActive(t, sched, insert(chathistory.NewComponent(&log), "main"))
	waitActive(t, sched, insert(contextmanager.NewComponent(&log), `{"budget":4096}`))
	id, err := sched.Insert(ag, `{"conversation":`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	waitFailed(t, sched, id)
	if got := log.String(); !strings.Contains(got, "agent: bad config") {
		t.Fatalf("log missing the bad-config line:\n%s", got)
	}
}

// TestUnloadClosesTheLoop proves the effect inverse logs the close.
func TestUnloadClosesTheLoop(t *testing.T) {
	st := composeStack(t, `{"conversation":"main","model":"fast","budget":10}`)

	var ids []mcontext.FiberID
	for _, f := range st.sched.Snapshot().Fibers {
		ids = append(ids, f.ID)
	}
	// Unload in reverse insertion order, like the entry: dependents
	// deactivate while their providers are still alive.
	for i := len(ids) - 1; i >= 0; i-- {
		id := ids[i]
		if err := st.sched.Remove(id); err != nil {
			t.Fatalf("remove %d: %v", id, err)
		}
	}
	waitGone(t, st.sched, ids...)
	if got := st.log.String(); !strings.Contains(got, "agent: loop closed") {
		t.Fatalf("log missing the close line:\n%s", got)
	}
}

// lockedBuffer is a concurrency-safe transcript sink: activating workers
// log concurrently, and the shared log must not race.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.WriteString(string(p))
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitActive(t *testing.T, sched *runtime.Scheduler, ids ...mcontext.FiberID) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		active := 0
		for _, id := range ids {
			info, ok := sched.Inspect(id)
			if !ok {
				t.Fatalf("fiber %d disappeared during startup", id)
			}
			if info.State == runtime.StateFailed {
				t.Fatalf("fiber %d failed: %v", id, info.Err)
			}
			if info.State == runtime.StateActive {
				active++
			}
		}
		if active == len(ids) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for activation")
}

func waitFailed(t *testing.T, sched *runtime.Scheduler, ids ...mcontext.FiberID) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		failed := 0
		for _, id := range ids {
			info, ok := sched.Inspect(id)
			if !ok {
				t.Fatalf("fiber %d disappeared during startup", id)
			}
			if info.State == runtime.StateFailed {
				failed++
			}
		}
		if failed == len(ids) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for activation failure")
}

func waitGone(t *testing.T, sched *runtime.Scheduler, ids ...mcontext.FiberID) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gone := true
		for _, id := range ids {
			if _, ok := sched.Inspect(id); ok {
				gone = false
				break
			}
		}
		if gone {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for unload")
}
