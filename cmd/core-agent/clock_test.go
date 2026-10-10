package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DaviMGDev/core-agent/internal/link"
	"github.com/DaviMGDev/core-agent/plugins/notifications"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// manualClock is an injectable boundary source: fire delivers one boundary to
// the bus ticker, so the tests never sleep for a period.
type manualClock struct {
	boundaries chan time.Time
}

func newManualClock() *manualClock {
	return &manualClock{boundaries: make(chan time.Time, 16)}
}

func (c *manualClock) after(time.Duration) <-chan time.Time { return c.boundaries }

func (c *manualClock) fire() { c.boundaries <- time.Now() }

// messageCollector records the chat.message texts it receives.
type messageCollector struct {
	mu    sync.Mutex
	texts []string
}

func (c *messageCollector) Wake(sub *notifications.Subscription) {
	for _, e := range sub.Take() {
		var payload struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(e.Payload, &payload)
		c.mu.Lock()
		c.texts = append(c.texts, payload.Text)
		c.mu.Unlock()
	}
}

func (c *messageCollector) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.texts...)
}

// clockedSession composes a mock-provider session on a manual clock: the
// clock fires only when the test says so.
func clockedSession(t *testing.T, clock *manualClock) *composedSession {
	t.Helper()
	resolved := testResolved(t)
	cfg := sessionFrom(t, resolved, "tester")
	mockDoc, err := resolved.MockProviders()
	if err != nil {
		t.Fatalf("MockProviders: %v", err)
	}
	cfg.providers = string(mockDoc)
	cfg.clock = time.Hour
	cfg.clockAfter = clock.after
	s, err := composeSession(context.Background(), cfg, io.Discard)
	if err != nil {
		t.Fatalf("composeSession: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// waitUntil polls until cond holds; the manual clock makes the transition
// itself deterministic, the poll only observes the async wake.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestREPLWakesBatchOnTheClock proves the REPL's agent waker is clocked: two
// completed jobs queue without a wake, and one boundary flushes them as a
// single turn whose reply names both jobs.
func TestREPLWakesBatchOnTheClock(t *testing.T) {
	clock := newManualClock()
	s := clockedSession(t, clock)
	s.attachAgentWaker(io.Discard)
	seen := &messageCollector{}
	s.bus.Subscribe(notifications.TopicChatMessage, seen)

	if err := s.manager.Registry().Declare(toolmanager.Tool{Name: "ok", Run: okRunner}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	s.manager.Start("ok", nil, 0).Wait()
	s.manager.Start("ok", nil, 0).Wait()

	if texts := seen.snapshot(); len(texts) != 0 {
		t.Fatalf("messages before a boundary = %v, want none", texts)
	}
	clock.fire()
	waitUntil(t, "one batched wake", func() bool { return len(seen.snapshot()) == 1 })
	texts := seen.snapshot()
	if len(texts) != 1 {
		t.Fatalf("messages after one boundary = %d, want 1", len(texts))
	}
	for _, want := range []string{"job-1", "job-2", "job.completed"} {
		if !strings.Contains(texts[0], want) {
			t.Errorf("batched reply missing %q: %q", want, texts[0])
		}
	}
}

// TestLinkStreamsImmediatelyAndWakesOnTheClock proves the link's two job
// surfaces: transitions cross as they happen, while the agent wake waits for
// the clock and then flushes the queued events as one message.
func TestLinkStreamsImmediatelyAndWakesOnTheClock(t *testing.T) {
	clock := newManualClock()
	s := clockedSession(t, clock)
	var out transcript
	f := &linkFrontend{
		ctx:     context.Background(),
		session: s,
		out:     &linkWriter{out: &out, log: io.Discard},
		jobs:    make(map[string]string),
	}
	f.attach(s)

	if err := s.manager.Registry().Declare(toolmanager.Tool{Name: "ok", Run: okRunner}); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	s.manager.Start("ok", nil, 0).Wait()
	s.manager.Start("ok", nil, 0).Wait()

	waitUntil(t, "the immediate stream lines", func() bool {
		return strings.Count(out.String(), `"kind":"job"`) == 4
	})
	if got := strings.Count(out.String(), `"kind":"message"`); got != 0 {
		t.Fatalf("messages before a boundary = %d, want 0:\n%s", got, out.String())
	}

	clock.fire()
	waitUntil(t, "one batched wake message", func() bool {
		return strings.Count(out.String(), `"kind":"message"`) == 1
	})
	messages := pull[link.Message](linkLines(t, out.String()))
	if len(messages) != 1 {
		t.Fatalf("messages after one boundary = %d, want 1:\n%s", len(messages), out.String())
	}
	for _, want := range []string{"job-1", "job-2"} {
		if !strings.Contains(messages[0].Text, want) {
			t.Errorf("batched reply missing %q: %q", want, messages[0].Text)
		}
	}
}
