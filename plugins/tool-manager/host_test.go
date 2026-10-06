package tool_manager

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/DaviMGDev/core-agent/internal/wasmtest"
	"github.com/DaviMGDev/core-agent/plugins/notifications"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/plugins/wasm"
	"github.com/DaviMGDev/memento/runtime"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type busWaker struct {
	mu     sync.Mutex
	events []notifications.Event
}

func (w *busWaker) Wake(sub *notifications.Subscription) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, sub.Take()...)
}

func (w *busWaker) topics() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.events))
	for _, e := range w.events {
		out = append(out, e.Topic)
	}
	return out
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func waitActive(t *testing.T, s *runtime.Scheduler, id mcontext.FiberID) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if info, ok := s.Inspect(id); ok && info.State == runtime.StateActive {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fiber %v did not become active", id)
}

func TestGuestStartReturnsHandleThroughImports(t *testing.T) {
	ctx := context.Background()
	bus := notifications.New()
	waker := &busWaker{}
	bus.Subscribe(notifications.TopicJobStarted, waker)
	bus.Subscribe(notifications.TopicJobCompleted, waker)

	m := New(Options{Publisher: bus})
	if err := m.Registry().Declare(Tool{
		Name: "echo",
		Run: func(context.Context, json.RawMessage, *Output) (any, error) {
			return "hi", nil
		},
	}); err != nil {
		t.Fatalf("Declare: %v", err)
	}

	engine, err := wasm.NewEngine(ctx, wasm.WithHostServices(m))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close(ctx)

	var logged lockedBuffer
	comp, err := wasm.NewComponent(ctx, engine, wasmtest.JobGuest(`{"tool":"echo"}`, 1024), wasm.WithLogWriter(&logged))
	if err != nil {
		t.Fatalf("NewComponent: %v", err)
	}

	s := runtime.New()
	defer s.Close()
	id, err := s.Insert(comp, nil)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	waitActive(t, s, id)

	var handle struct {
		Job string `json:"job"`
	}
	if err := json.Unmarshal([]byte(logged.String()), &handle); err != nil {
		t.Fatalf("guest log = %q, want a job handle document: %v", logged.String(), err)
	}
	if handle.Job == "" {
		t.Fatalf("handle = %+v, want a job id", handle)
	}

	job, ok := m.Job(handle.Job)
	if !ok {
		t.Fatalf("manager does not know job %q", handle.Job)
	}
	if st := job.Wait(); st.State != StateDone || st.Result != "hi" {
		t.Fatalf("job = {%s %v}, want done with hi", st.State, st.Result)
	}

	// Two subscriptions share one waker, so their wakes may interleave; the
	// per-subscription order is what the bus guarantees.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		topics := waker.topics()
		if contains(topics, notifications.TopicJobStarted) && contains(topics, notifications.TopicJobCompleted) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("bus events = %v, want started and completed", waker.topics())
}
