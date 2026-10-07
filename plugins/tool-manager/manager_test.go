package tool_manager

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DaviMGDev/core-agent/plugins/notifications"
	"github.com/DaviMGDev/memento/runtime"
)

// recorder is a Publisher that records events for tests.
type recorder struct {
	mu     sync.Mutex
	events []recEvent
}

type recEvent struct {
	Topic   string
	Payload map[string]any
}

func (r *recorder) Publish(topic string, payload []byte) {
	var doc map[string]any
	_ = json.Unmarshal(payload, &doc)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recEvent{Topic: topic, Payload: doc})
}

func (r *recorder) count(topic string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Topic == topic {
			n++
		}
	}
	return n
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func blocking(release chan struct{}) Runner {
	return func(ctx context.Context, _ json.RawMessage, _ *Output) (any, error) {
		select {
		case <-release:
			return "released", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func TestRegistryRefusesDuplicatesUnchanged(t *testing.T) {
	r := NewRegistry()
	if err := r.Declare(Tool{Name: "read", Run: blocking(nil)}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	err := r.Declare(Tool{Name: "read", Run: blocking(nil)})
	if err == nil {
		t.Fatal("duplicate declaration was accepted")
	}
	if got := len(r.Tools()); got != 1 {
		t.Fatalf("registry has %d tools, want 1", got)
	}
}

func TestStartReturnsHandleAndKillDecidesAtOnce(t *testing.T) {
	rec := &recorder{}
	m := New(Options{Publisher: rec})
	release := make(chan struct{})
	defer close(release)
	if err := m.Registry().Declare(Tool{Name: "slow", Run: blocking(release)}); err != nil {
		t.Fatalf("Declare: %v", err)
	}

	job := m.Start("slow", nil, 0)
	if st := m.Peep(job); st.State != StateRunning {
		t.Fatalf("state after start = %s, want running", st.State)
	}
	st := m.Kill(job)
	if st.State != StateKilled {
		t.Fatalf("state after kill = %s, want killed", st.State)
	}
	waitFor(t, "job.killed", func() bool { return rec.count(notifications.TopicJobKilled) == 1 })
}

func TestRefusedStartFailsAtOnce(t *testing.T) {
	m := New(Options{})
	job := m.Start("missing", nil, 0)
	st := job.Wait()
	if st.State != StateFailed || st.Error == "" {
		t.Fatalf("refused job = {%s %q}, want failed with a reason", st.State, st.Error)
	}
}

func TestTicksCarryElapsedAndCompletedCarriesResult(t *testing.T) {
	rec := &recorder{}
	m := New(Options{Publisher: rec})
	release := make(chan struct{})
	if err := m.Registry().Declare(Tool{Name: "slow", Run: blocking(release)}); err != nil {
		t.Fatalf("Declare: %v", err)
	}

	job := m.Start("slow", nil, 10*time.Millisecond)
	waitFor(t, "a job.tick event", func() bool { return rec.count(notifications.TopicJobTick) >= 1 })
	close(release)
	st := job.Wait()
	if st.State != StateDone || st.Result != "released" {
		t.Fatalf("job = {%s %v}, want done with the runner result", st.State, st.Result)
	}
	if rec.count(notifications.TopicJobCompleted) != 1 {
		t.Fatalf("job.completed events = %d, want 1", rec.count(notifications.TopicJobCompleted))
	}
}

func TestCloseReclaimsWithoutKill(t *testing.T) {
	rec := &recorder{}
	m := New(Options{Publisher: rec})
	release := make(chan struct{})
	defer close(release)
	if err := m.Registry().Declare(Tool{Name: "slow", Run: blocking(release)}); err != nil {
		t.Fatalf("Declare: %v", err)
	}

	job := m.Start("slow", nil, 0)
	m.Close()
	st := m.Peep(job)
	if st.State != StateFailed || st.Error == "" {
		t.Fatalf("reclaimed job = {%s %q}, want failed with a reason", st.State, st.Error)
	}
	if rec.count(notifications.TopicJobKilled) != 0 {
		t.Fatal("reclamation emitted job.killed")
	}
}

func TestCloseBeforeRunnerStartsReclaims(t *testing.T) {
	m := New(Options{})
	release := make(chan struct{})
	defer close(release)
	if err := m.Registry().Declare(Tool{Name: "slow", Run: blocking(release)}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	job := m.Start("slow", nil, 0)
	m.Close() // may race the runner goroutine
	if st := m.Peep(job); st.State != StateFailed {
		t.Fatalf("state after close = %s, want failed", st.State)
	}
}

// instanceCapture records the instance the scheduler activates it on, so a
// host test can speak for a guest caller.
type instanceCapture struct {
	inst  *runtime.Instance
	ready chan struct{}
}

func (c *instanceCapture) Declarations() runtime.Declarations { return runtime.Declarations{} }
func (c *instanceCapture) Activate(inst *runtime.Instance, _ any) error {
	c.inst = inst
	close(c.ready)
	return nil
}

func captureInstance(t *testing.T) *runtime.Instance {
	t.Helper()
	c := &instanceCapture{ready: make(chan struct{})}
	s := runtime.New()
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.Insert(c, nil); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	select {
	case <-c.ready:
	case <-time.After(3 * time.Second):
		t.Fatal("capture component did not activate")
	}
	return c.inst
}

func TestStartJobAdoptsUnderTheAttributedParent(t *testing.T) {
	m := New(Options{})
	release := make(chan struct{})
	defer close(release)
	if err := m.Registry().Declare(Tool{Name: "slow", Run: blocking(release)}); err != nil {
		t.Fatalf("Declare slow: %v", err)
	}
	inst := captureInstance(t)

	parent := m.Start("slow", nil, 0)
	m.Attribute(inst, parent)
	defer m.Release(inst, parent)

	raw, err := m.StartJob(inst, []byte(`{"tool":"slow"}`))
	if err != nil {
		t.Fatalf("StartJob: %v", err)
	}
	var handle struct {
		Job string `json:"job"`
	}
	if err := json.Unmarshal(raw, &handle); err != nil {
		t.Fatalf("handle = %q: %v", raw, err)
	}
	child, ok := m.Job(handle.Job)
	if !ok {
		t.Fatalf("job %q not found", handle.Job)
	}
	if child.Depth() != 2 {
		t.Fatalf("child depth = %d, want 2", child.Depth())
	}
	if child.Caller() != inst {
		t.Fatal("child caller is not the attributed instance")
	}

	m.Kill(parent)
	if st := child.Wait(); st.State != StateKilled {
		t.Fatalf("child after parent kill = %s, want killed", st.State)
	}
}

func TestAttributionsStackForNestedTurns(t *testing.T) {
	m := New(Options{})
	release := make(chan struct{})
	defer close(release)
	if err := m.Registry().Declare(Tool{Name: "slow", Run: blocking(release)}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	inst := captureInstance(t)

	outer := m.Start("slow", nil, 0)
	m.Attribute(inst, outer)
	inner := m.Start("slow", nil, 0)
	m.Attribute(inst, inner)

	// The inner turn owns the instance; releasing the outer turn leaves it.
	m.Release(inst, outer)
	if got := m.attributed(inst); got != inner {
		t.Fatalf("attributed job = %v, want the inner turn", got)
	}
	if m.Cancelled(inst) {
		t.Fatal("attributed inner job is not killed; Cancelled must be false")
	}
	m.Kill(inner)
	if !m.Cancelled(inst) {
		t.Fatal("killed inner job: Cancelled = false, want true")
	}
	m.Release(inst, inner)
	if got := m.attributed(inst); got != nil {
		t.Fatalf("attributed job after both releases = %v, want none", got)
	}
}

func TestGuestPeepAndKillAreSubtreeOnly(t *testing.T) {
	m := New(Options{})
	release := make(chan struct{})
	defer close(release)
	if err := m.Registry().Declare(Tool{Name: "slow", Run: blocking(release)}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	inst := captureInstance(t)

	root := m.Start("slow", nil, 0)
	m.Attribute(inst, root)
	inside := m.start("slow", nil, 0, nil, root)
	outside := m.Start("slow", nil, 0)

	for _, id := range []string{inside.ID(), root.ID()} {
		if _, err := m.PeepJob(inst, []byte(`{"job":"`+id+`"}`)); err != nil {
			t.Errorf("peep %q in the subtree: %v", id, err)
		}
	}
	if _, err := m.KillJob(inst, []byte(`{"job":"`+inside.ID()+`"}`)); err != nil {
		t.Errorf("kill inside the subtree: %v", err)
	}
	for _, id := range []string{outside.ID()} {
		if _, err := m.PeepJob(inst, []byte(`{"job":"`+id+`"}`)); err == nil {
			t.Errorf("peep %q outside the subtree was allowed", id)
		}
		if _, err := m.KillJob(inst, []byte(`{"job":"`+id+`"}`)); err == nil {
			t.Errorf("kill %q outside the subtree was allowed", id)
		}
	}

	// The unattributed caller — the top-level agent — sees its whole tree.
	m.Release(inst, root)
	if _, err := m.PeepJob(inst, []byte(`{"job":"`+outside.ID()+`"}`)); err != nil {
		t.Errorf("unattributed peep: %v", err)
	}
}

func TestChildEventsGoToTheParentListener(t *testing.T) {
	rec := &recorder{}
	m := New(Options{Publisher: rec})
	release := make(chan struct{})
	defer close(release)
	if err := m.Registry().Declare(Tool{Name: "slow", Run: blocking(release)}); err != nil {
		t.Fatalf("Declare slow: %v", err)
	}
	if err := m.Registry().Declare(Tool{Name: "echo", Run: func(context.Context, json.RawMessage, *Output) (any, error) {
		return "hi", nil
	}}); err != nil {
		t.Fatalf("Declare echo: %v", err)
	}

	parent := m.Start("slow", nil, 0)
	events := m.Listen(parent)
	defer m.Unlisten(parent)

	child := m.start("echo", nil, 0, nil, parent)
	var topics []string
	deadline := time.Now().Add(3 * time.Second)
	for len(topics) < 2 && time.Now().Before(deadline) {
		select {
		case ev := <-events:
			topics = append(topics, ev.Topic)
		case <-time.After(3 * time.Second):
		}
	}
	want := []string{notifications.TopicJobStarted, notifications.TopicJobCompleted}
	if len(topics) != 2 || topics[0] != want[0] || topics[1] != want[1] {
		t.Fatalf("listener topics = %v, want %v", topics, want)
	}
	if rec.count(notifications.TopicJobStarted) != 1 {
		t.Fatalf("bus job.started = %d, want only the root job's", rec.count(notifications.TopicJobStarted))
	}
	if rec.count(notifications.TopicJobCompleted) != 0 {
		t.Fatalf("bus job.completed = %d, want the child's event to stay with the parent", rec.count(notifications.TopicJobCompleted))
	}
	if child.State() != StateDone {
		t.Fatalf("child = %s, want done", child.State())
	}
}

func TestFailingRunnerFailsTheJob(t *testing.T) {
	rec := &recorder{}
	m := New(Options{Publisher: rec})
	if err := m.Registry().Declare(Tool{Name: "boom", Run: func(context.Context, json.RawMessage, *Output) (any, error) {
		return nil, errors.New("boom")
	}}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	job := m.Start("boom", nil, 0)
	st := job.Wait()
	if st.State != StateFailed || st.Error != "boom" {
		t.Fatalf("job = {%s %q}, want failed with boom", st.State, st.Error)
	}
	if rec.count(notifications.TopicJobFailed) != 1 {
		t.Fatalf("job.failed events = %d, want 1", rec.count(notifications.TopicJobFailed))
	}
}
