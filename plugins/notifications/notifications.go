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

// Waker is a subscriber the bus wakes. Wake is called at most once per
// subscription at a time. The host implements it around a guest's handler:
// Wake blocks until the subscriber can receive (its module lock), then drains
// the queued events with sub.Take — so events that arrive while the
// subscriber is busy join the same wake instead of causing another, and a
// wake never preempts a call in flight.
type Waker interface {
	Wake(sub *Subscription)
}

// Subscription is one subscriber's queue. Take drains it; the host calls it
// from Wake once the subscriber can receive.
type Subscription struct {
	bus       *Bus
	waker     Waker
	queue     []Event
	scheduled bool
}

// Take returns the events queued for the subscription and empties the queue.
// It is called by the waker inside Wake, under the subscriber's own lock.
func (s *Subscription) Take() []Event {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	events := s.queue
	s.queue = nil
	return events
}

// Bus is a queued publish/subscribe bus. It is safe for concurrent use.
type Bus struct {
	mu   sync.Mutex
	subs map[string][]*Subscription
}

// New returns an empty bus.
func New() *Bus {
	return &Bus{subs: make(map[string][]*Subscription)}
}

// Subscribe appends a waker to a topic and returns its subscription.
// Subscription is host-configured: the assembler decides which component
// hears which topic (system D14).
func (b *Bus) Subscribe(topic string, waker Waker) *Subscription {
	s := &Subscription{bus: b, waker: waker}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], s)
	return s
}

// Publish queues the event for every subscriber of the topic and returns to
// the publisher without blocking; delivery is the host's work. Per-subscriber
// order is publish order.
func (b *Bus) Publish(topic string, payload []byte) {
	b.mu.Lock()
	var scheduled []*Subscription
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

// deliver wakes a subscriber, repeating while events keep arriving; at most
// one delivery goroutine runs per subscription.
func (b *Bus) deliver(s *Subscription) {
	for {
		s.waker.Wake(s)
		b.mu.Lock()
		if len(s.queue) == 0 {
			s.scheduled = false
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()
	}
}
