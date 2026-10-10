package replchat

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DaviMGDev/core-agent/plugins/agent"
	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	contextmanager "github.com/DaviMGDev/core-agent/plugins/context-manager"
	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// scriptJobs is a job service that refuses every call: the terminal tests
// never call tools.
type scriptJobs struct{}

func (scriptJobs) StartJob(_ *runtime.Instance, _ []byte) ([]byte, error) {
	return nil, errNoJobs
}

type scriptError string

func (e scriptError) Error() string { return string(e) }

const errNoJobs = scriptError("repl-chat test: no job service")

// stack composes the native pipeline behind the terminal: the record, the
// window, a mock provider, the model service, the agent loop, and the
// terminal with a capturing publish path.
type stack struct {
	sched    *runtime.Scheduler
	terminal *Component
	publish  *publishCapture
	log      *lockedBuffer
}

type publishCapture struct {
	text []string
}

func (p *publishCapture) publish(text string) error {
	p.text = append(p.text, text)
	return nil
}

func composeStack(t *testing.T, agentPayload, nick string) *stack {
	t.Helper()
	var log lockedBuffer
	pub := &publishCapture{}
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
	waitActive(t, sched, insert(providermanager.NewComponent(&log), `{"providers":[{"name":"mock","mock":true,"models":["fast"]}]}`))
	waitActive(t, sched, insert(modelmanager.NewComponent(&log), `{"models":[]}`))
	waitActive(t, sched, insert(chathistory.NewComponent(&log), "main"))
	waitActive(t, sched, insert(contextmanager.NewComponent(&log), `{"budget":4096}`))
	waitActive(t, sched, insert(agent.NewComponent(&log, scriptJobs{}, pub.publish), agentPayload))
	term := NewComponent(&log)
	waitActive(t, sched, insert(term, nick))
	return &stack{sched: sched, terminal: term, publish: pub, log: &log}
}

func answerOf(t *testing.T, raw []byte) Answer {
	t.Helper()
	var out Answer
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("answer decode %q: %v", raw, err)
	}
	return out
}

// TestLineRunsOneAgentTurn proves a user line reaches the agent and the
// answer carries no text: the turn's message arrives as a chat.message.
func TestLineRunsOneAgentTurn(t *testing.T) {
	ctx := context.Background()
	st := composeStack(t, `{"conversation":"main","model":"fast","budget":10}`, "tester")

	raw, err := st.terminal.Handle(ctx, []byte(`{"line":"hello"}`))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if answer := answerOf(t, raw); answer.Error != "" {
		t.Fatalf("answer = %+v, want no error", answer)
	}
	if texts := st.publish.text; len(texts) != 1 || !strings.Contains(texts[0], "mock(fast)") {
		t.Fatalf("published = %v, want the turn's message", texts)
	}
}

// TestEventsRenderToTheLog proves a bus wake renders its chat.message
// events to the log and ignores anything else.
func TestEventsRenderToTheLog(t *testing.T) {
	ctx := context.Background()
	st := composeStack(t, `{"conversation":"main","model":"fast","budget":10}`, "tester")

	raw, err := st.terminal.Handle(ctx, []byte(`{"events":[{"topic":"chat.message","payload":{"text":"hi there"}},{"topic":"job.tick","payload":{}},{"topic":"chat.message","payload":{"text":""}}]}`))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if answer := answerOf(t, raw); answer.Error != "" {
		t.Fatalf("answer = %+v, want no error", answer)
	}
	if got := st.log.String(); !strings.Contains(got, "hi there\n") {
		t.Fatalf("log missing the rendered line:\n%s", got)
	}
}

// TestFailedTurnRidesTheAnswer proves an agent failure reaches the session
// in the answer: a model no provider serves answers nothing, and the
// terminal reports the no-response refusal instead of succeeding silently.
func TestFailedTurnRidesTheAnswer(t *testing.T) {
	ctx := context.Background()
	st := composeStack(t, `{"conversation":"main","model":"nosuch","budget":10}`, "tester")

	raw, err := st.terminal.Handle(ctx, []byte(`{"line":"hello"}`))
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if answer := answerOf(t, raw); !strings.Contains(answer.Error, "no response") {
		t.Fatalf("answer = %+v, want the no-response refusal", answer)
	}
}

// TestEmptyWakeAnswersEmpty proves a bad wake and an empty wake answer
// empty bytes with no error.
func TestEmptyWakeAnswersEmpty(t *testing.T) {
	ctx := context.Background()
	st := composeStack(t, `{"conversation":"main","model":"fast","budget":10}`, "tester")

	for _, req := range []string{`not json`, `{}`, `{"events":[]}`} {
		raw, err := st.terminal.Handle(ctx, []byte(req))
		if err != nil {
			t.Fatalf("handle %q errored: %v, want an empty answer", req, err)
		}
		if len(raw) != 0 {
			t.Fatalf("handle %q = %s, want no answer", req, raw)
		}
	}
}

// TestUnloadClosesTheSession proves the effect inverse logs the leave
// message.
func TestUnloadClosesTheSession(t *testing.T) {
	st := composeStack(t, `{"conversation":"main","model":"fast","budget":10}`, "tester")

	var ids []mcontext.FiberID
	for _, f := range st.sched.Snapshot().Fibers {
		ids = append(ids, f.ID)
	}
	for i := len(ids) - 1; i >= 0; i-- {
		if err := st.sched.Remove(ids[i]); err != nil {
			t.Fatalf("remove %d: %v", ids[i], err)
		}
	}
	waitGone(t, st.sched, ids...)
	if got := st.log.String(); !strings.Contains(got, LeaveMessage) {
		t.Fatalf("log missing the leave message:\n%s", got)
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
