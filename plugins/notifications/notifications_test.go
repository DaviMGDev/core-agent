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
