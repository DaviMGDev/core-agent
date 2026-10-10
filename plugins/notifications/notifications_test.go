package notifications

import (
	"sync"
	"testing"
	"time"
)

// blockingWaker blocks before draining (busy) or after draining (mid-call)
// and records what it received.
type blockingWaker struct {
	mu         sync.Mutex
	wakes      [][]Event
	active     int
	maxActive  int
	blockEnter chan struct{}
	release    chan struct{}
}

func (w *blockingWaker) Wake(sub *Subscription) {
	w.mu.Lock()
	w.active++
	if w.active > w.maxActive {
		w.maxActive = w.active
	}
	w.mu.Unlock()

	if w.blockEnter != nil {
		<-w.blockEnter
	}
	events := sub.Take()
	w.mu.Lock()
	w.wakes = append(w.wakes, events)
	w.mu.Unlock()
	if w.release != nil {
		<-w.release
	}

	w.mu.Lock()
	w.active--
	w.mu.Unlock()
}

func (w *blockingWaker) snapshot() (wakes [][]Event, active, maxActive int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]Event(nil), w.wakes...), w.active, w.maxActive
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPublishQueuesWithoutBlocking(t *testing.T) {
	bus := New()
	w := &blockingWaker{release: make(chan struct{})}
	bus.Subscribe(TopicJobCompleted, w)

	done := make(chan struct{})
	go func() { bus.Publish(TopicJobCompleted, []byte("done")); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on the subscriber")
	}

	waitFor(t, "the blocked wake", func() bool {
		_, active, _ := w.snapshot()
		return active == 1
	})
	close(w.release)
}

func TestOrderIsPublishOrder(t *testing.T) {
	bus := New()
	w := &blockingWaker{blockEnter: make(chan struct{})}
	bus.Subscribe(TopicJobTick, w)

	bus.Publish(TopicJobTick, []byte("1"))
	bus.Publish(TopicJobTick, []byte("2"))
	bus.Publish(TopicJobTick, []byte("3"))
	close(w.blockEnter)

	waitFor(t, "the wake", func() bool {
		wakes, _, _ := w.snapshot()
		return len(wakes) == 1
	})
	wakes, _, _ := w.snapshot()
	if len(wakes[0]) != 3 || string(wakes[0][0].Payload) != "1" || string(wakes[0][1].Payload) != "2" || string(wakes[0][2].Payload) != "3" {
		t.Fatalf("wake events = %v, want 1,2,3", wakes[0])
	}
}

func TestWakesCoalesceWhileBusy(t *testing.T) {
	bus := New()
	w := &blockingWaker{blockEnter: make(chan struct{})}
	bus.Subscribe(TopicJobCompleted, w)

	bus.Publish(TopicJobCompleted, []byte("a"))
	bus.Publish(TopicJobCompleted, []byte("b"))
	close(w.blockEnter)

	waitFor(t, "the wake", func() bool {
		wakes, _, _ := w.snapshot()
		return len(wakes) == 1
	})
	wakes, _, _ := w.snapshot()
	if len(wakes) != 1 || len(wakes[0]) != 2 {
		t.Fatalf("wakes = %v, want one wake with two events", wakes)
	}
}

func TestWaitIdleReturnsWhenIdle(t *testing.T) {
	bus := New()
	sub := bus.Subscribe(TopicChatMessage, &blockingWaker{})
	done := make(chan struct{})
	go func() { sub.WaitIdle(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitIdle blocked on an idle subscription")
	}
}

func TestWaitIdleWaitsForTheWake(t *testing.T) {
	bus := New()
	w := &blockingWaker{release: make(chan struct{})}
	sub := bus.Subscribe(TopicChatMessage, w)
	bus.Publish(TopicChatMessage, []byte(`{"text":"hi"}`))
	waitFor(t, "the blocked wake", func() bool {
		_, active, _ := w.snapshot()
		return active == 1
	})

	done := make(chan struct{})
	go func() { sub.WaitIdle(); close(done) }()
	select {
	case <-done:
		t.Fatal("WaitIdle returned while a wake was in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(w.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitIdle did not return after the wake finished")
	}
}

func TestWaitIdleSpansEventsPublishedWhileWaiting(t *testing.T) {
	bus := New()
	w := &blockingWaker{release: make(chan struct{})}
	sub := bus.Subscribe(TopicChatMessage, w)
	bus.Publish(TopicChatMessage, []byte("1"))
	waitFor(t, "the blocked wake", func() bool {
		_, active, _ := w.snapshot()
		return active == 1
	})

	done := make(chan struct{})
	go func() { sub.WaitIdle(); close(done) }()
	bus.Publish(TopicChatMessage, []byte("2"))
	close(w.release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitIdle did not return")
	}
	if wakes, _, _ := w.snapshot(); len(wakes) != 2 {
		t.Fatalf("wakes = %d, want 2 (the wait spans both cycles)", len(wakes))
	}
}

func TestWakeNeverPreempts(t *testing.T) {
	bus := New()
	w := &blockingWaker{release: make(chan struct{})}
	bus.Subscribe(TopicJobTick, w)

	bus.Publish(TopicJobTick, []byte("first"))
	waitFor(t, "the first wake to drain", func() bool {
		wakes, _, _ := w.snapshot()
		return len(wakes) == 1
	})

	bus.Publish(TopicJobTick, []byte("second"))
	time.Sleep(20 * time.Millisecond)
	if _, active, _ := w.snapshot(); active != 1 {
		t.Fatalf("active wakes = %d, want 1", active)
	}
	close(w.release)

	waitFor(t, "the second wake", func() bool {
		wakes, _, _ := w.snapshot()
		return len(wakes) == 2
	})
	if _, _, maxActive := w.snapshot(); maxActive != 1 {
		t.Fatalf("max concurrent wakes = %d, want 1", maxActive)
	}
}

// manualClock is an injectable boundary source: the test fires one boundary
// by sending on the channel the bus reads.
type manualClock struct {
	boundaries chan time.Time
}

func newManualClock() *manualClock {
	return &manualClock{boundaries: make(chan time.Time, 16)}
}

func (c *manualClock) after(time.Duration) <-chan time.Time { return c.boundaries }

func (c *manualClock) fire() { c.boundaries <- time.Now() }

// never is a boundary source that never fires; the ticker still observes
// Close.
func never(time.Duration) <-chan time.Time { return make(chan time.Time) }

func TestClockBatchesWithinAPeriod(t *testing.T) {
	bus := New(WithClock(time.Hour, never))
	defer bus.Close()
	w := &blockingWaker{}
	bus.SubscribeClocked(TopicJobTick, w)

	bus.Publish(TopicJobTick, []byte("1"))
	bus.Publish(TopicJobTick, []byte("2"))
	if wakes, _, _ := w.snapshot(); len(wakes) != 0 {
		t.Fatalf("wakes before a boundary = %d, want 0", len(wakes))
	}

	bus.Flush()
	waitFor(t, "the batch wake", func() bool {
		wakes, _, _ := w.snapshot()
		return len(wakes) == 1
	})
	wakes, _, _ := w.snapshot()
	if len(wakes[0]) != 2 || string(wakes[0][0].Payload) != "1" || string(wakes[0][1].Payload) != "2" {
		t.Fatalf("wake events = %v, want 1,2 in one wake", wakes[0])
	}
}

func TestClockEmptyPeriodWakesNoOne(t *testing.T) {
	bus := New(WithClock(time.Hour, never))
	defer bus.Close()
	w := &blockingWaker{}
	bus.SubscribeClocked(TopicJobTick, w)

	bus.Flush()
	if wakes, _, _ := w.snapshot(); len(wakes) != 0 {
		t.Fatalf("an empty boundary woke someone: %v", wakes)
	}

	bus.Publish(TopicJobTick, []byte("1"))
	bus.Flush()
	waitFor(t, "the wake", func() bool {
		wakes, _, _ := w.snapshot()
		return len(wakes) == 1
	})

	bus.Flush()
	if wakes, _, _ := w.snapshot(); len(wakes) != 1 {
		t.Fatalf("an empty boundary woke someone: %v", wakes)
	}
}

func TestClockEventsDuringWakeAwaitTheNextBoundary(t *testing.T) {
	bus := New(WithClock(time.Hour, never))
	defer bus.Close()
	w := &blockingWaker{release: make(chan struct{})}
	bus.SubscribeClocked(TopicJobTick, w)

	bus.Publish(TopicJobTick, []byte("first"))
	bus.Flush()
	waitFor(t, "the blocked wake", func() bool {
		_, active, _ := w.snapshot()
		return active == 1
	})

	bus.Publish(TopicJobTick, []byte("second"))
	bus.Flush()
	if _, active, _ := w.snapshot(); active != 1 {
		t.Fatalf("active wakes = %d, want 1: events during a wake must wait for the next boundary", active)
	}
	if wakes, _, _ := w.snapshot(); len(wakes) != 1 {
		t.Fatalf("wakes while one is in flight = %d, want 1: the second event must wait for the next boundary", len(wakes))
	}

	close(w.release)
	waitFor(t, "the first wake to finish", func() bool {
		_, active, _ := w.snapshot()
		return active == 0
	})
	bus.Flush()
	waitFor(t, "the second boundary", func() bool {
		wakes, _, _ := w.snapshot()
		return len(wakes) == 2
	})
	wakes, _, _ := w.snapshot()
	if len(wakes[1]) != 1 || string(wakes[1][0].Payload) != "second" {
		t.Fatalf("second wake events = %v, want second", wakes[1])
	}
}

func TestClockTickerDrivesBoundaries(t *testing.T) {
	clock := newManualClock()
	bus := New(WithClock(time.Hour, clock.after))
	defer bus.Close()
	w := &blockingWaker{}
	bus.SubscribeClocked(TopicJobCompleted, w)

	bus.Publish(TopicJobCompleted, []byte("x"))
	clock.fire()
	waitFor(t, "the boundary wake", func() bool {
		wakes, _, _ := w.snapshot()
		return len(wakes) == 1
	})
}

func TestClockedWithoutAPeriodIsImmediate(t *testing.T) {
	bus := New()
	defer bus.Close()
	w := &blockingWaker{}
	bus.SubscribeClocked(TopicJobCompleted, w)

	bus.Publish(TopicJobCompleted, []byte("x"))
	waitFor(t, "the immediate wake", func() bool {
		wakes, _, _ := w.snapshot()
		return len(wakes) == 1
	})
}

func TestClockCloseStopsBoundaries(t *testing.T) {
	clock := newManualClock()
	bus := New(WithClock(time.Hour, clock.after))
	w := &blockingWaker{}
	bus.SubscribeClocked(TopicJobCompleted, w)

	bus.Close()
	bus.Publish(TopicJobCompleted, []byte("x"))
	clock.fire()
	if wakes, _, _ := w.snapshot(); len(wakes) != 0 {
		t.Fatalf("a closed bus woke someone: %v", wakes)
	}
}
