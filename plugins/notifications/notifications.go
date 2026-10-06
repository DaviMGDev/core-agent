// Package notifications is the agent layer's queued event bus, host-side per
// system spec D14. Any component publishes; any component subscribes to a
// topic; the host wakes a subscriber when its module lock is free, and queued
// events drain into one wake.
package notifications

import "sync"

// The layer's well-known topics (SPEC.md, "Topics").
const (
	TopicJobStarted   = "job.started"
	TopicJobTick      = "job.tick"
	TopicJobCompleted = "job.completed"
	TopicJobFailed    = "job.failed"
	TopicJobKilled    = "job.killed"
	TopicChatMessage  = "chat.message"
)

// Event is one published event: a topic and an opaque payload.
type Event struct {
	Topic   string
	Payload []byte
}

// Waker is a subscriber the bus wakes with the events queued for it. The
// host implements it around a guest's handler: the wake blocks on the
// subscriber's module lock, so a wake waits for a call in flight instead of
// preempting it. Wake must not be called concurrently for one subscriber.
type Waker interface {
	Wake(events []Event)
}

// Bus is a queued publish/subscribe bus. It is safe for concurrent use.
type Bus struct {
	mu   sync.Mutex
	subs map[string][]*subscriber
}

// New returns an empty bus.
func New() *Bus {
	return &Bus{subs: make(map[string][]*subscriber)}
}

// Subscribe appends a waker to a topic. Subscription is host-configured:
// the assembler decides which component hears which topic (system D14).
func (b *Bus) Subscribe(topic string, waker Waker) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], &subscriber{waker: waker})
}

// Publish queues the event for every subscriber of the topic and returns to
// the publisher without blocking; delivery is the host's work. Per-subscriber
// order is publish order.
func (b *Bus) Publish(topic string, payload []byte) {
	b.mu.Lock()
	var scheduled []*subscriber
	for _, s := range b.subs[topic] {
		s.queue = append(s.queue, Event{Topic: topic, Payload: append([]byte(nil), payload...)})
		if !s.scheduled {
			s.scheduled = true
			scheduled = append(scheduled, s)
		}
	}
	b.mu.Unlock()
	for _, s := range scheduled {
		go b.deliver(s)
	}
}

// deliver drains a subscriber's queue in one wake, repeating while events
// keep arriving; at most one delivery goroutine runs per subscriber.
func (b *Bus) deliver(s *subscriber) {
	for {
		b.mu.Lock()
		events := s.queue
		s.queue = nil
		if len(events) == 0 {
			s.scheduled = false
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()
		s.waker.Wake(events)
	}
}

// subscriber is one subscription: its waker, its queued events, and whether
// a delivery is in flight.
type subscriber struct {
	waker     Waker
	queue     []Event
	scheduled bool
}
