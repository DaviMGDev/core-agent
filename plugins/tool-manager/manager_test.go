package tool_manager

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DaviMGDev/core-agent/plugins/notifications"
)

// recorder is a Publisher that records events for tests.
type recorder struct {
	mu     sync.Mutex
	events []Event
}

type Event struct {
	Topic   string
	Payload map[string]any
}

func (r *recorder) Publish(topic string, payload []byte) {
	var doc map[string]any
	_ = json.Unmarshal(payload, &doc)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, Event{Topic: topic, Payload: doc})
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
