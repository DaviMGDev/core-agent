// Package notifications is the agent layer's queued event bus, host-side per
// system spec D14. Any component publishes; any component subscribes to a
// topic; the host wakes a subscriber when its module lock is free, and queued
// events drain into one wake. A subscription may opt into the notification
// clock: its queued events then flush together on period boundaries instead
// of immediately, and a boundary with nothing queued wakes no one.
package notifications

import (
	"sync"
	"time"
)

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
// from Wake once the subscriber can receive. A clocked subscription waits for
// its bus clock's boundaries; an immediate one wakes as soon as it is queued.
type Subscription struct {
	bus        *Bus
	waker      Waker
	clocked    bool
	queue      []Event
	scheduled  bool
	delivering bool
	idle       chan struct{}
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

// WaitIdle blocks until the subscription has no queued events and no wake in
// flight: a driver calls it to let a subscriber finish rendering before it
// continues. A publish that arrives while waiting starts a new cycle, which
// WaitIdle also waits out.
func (s *Subscription) WaitIdle() {
	for {
		s.bus.mu.Lock()
		if !s.scheduled {
			s.bus.mu.Unlock()
			return
		}
		idle := s.idle
		s.bus.mu.Unlock()
		if idle == nil {
			return // invariant: scheduled implies an open idle channel
		}
		<-idle
	}
}

// Option configures a Bus.
type Option func(*Bus)

// WithClock enables the notification clock: subscriptions created with
// SubscribeClocked queue their events and flush them together on each period
// boundary. A boundary with nothing queued wakes no one. after supplies the
// boundary channels and is nil for time.After; tests inject a manual source.
func WithClock(period time.Duration, after func(time.Duration) <-chan time.Time) Option {
	return func(b *Bus) {
		b.period = period
		b.after = after
		if b.after == nil {
			b.after = time.After
		}
	}
}

// Bus is a queued publish/subscribe bus with an optional notification clock.
// It is safe for concurrent use.
type Bus struct {
	mu   sync.Mutex
	subs map[string][]*Subscription

	// Clock state: period > 0 batches clocked subscriptions' events.
	period time.Duration
	after  func(time.Duration) <-chan time.Time
	clock  sync.Once
	done   chan struct{}
	closed bool
}

// New returns an empty bus.
func New(opts ...Option) *Bus {
	b := &Bus{subs: make(map[string][]*Subscription), done: make(chan struct{})}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Subscribe appends a waker to a topic and returns its subscription.
// Subscription is host-configured: the assembler decides which component
// hears which topic (system D14). Delivery is immediate.
func (b *Bus) Subscribe(topic string, waker Waker) *Subscription {
	return b.subscribe(topic, waker, false)
}

// SubscribeClocked subscribes with the bus clock: queued events flush
// together as one wake on each period boundary, and an empty boundary wakes
// no one. Without a configured clock it behaves like Subscribe.
func (b *Bus) SubscribeClocked(topic string, waker Waker) *Subscription {
	s := b.subscribe(topic, waker, true)
	b.startClock()
	return s
}

func (b *Bus) subscribe(topic string, waker Waker, clocked bool) *Subscription {
	s := &Subscription{bus: b, waker: waker, clocked: clocked}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = append(b.subs[topic], s)
	return s
}

// startClock starts the boundary ticker once, when the first clocked
// subscription arrives. A bus with no period keeps every subscription
// immediate.
func (b *Bus) startClock() {
	b.clock.Do(func() {
		if b.period <= 0 {
			return
		}
		go b.runClock()
	})
}

// runClock flushes queued clocked subscriptions on every boundary.
func (b *Bus) runClock() {
	for {
		select {
		case <-b.done:
			return
		case <-b.after(b.period):
			b.Flush()
		}
	}
}

// Close stops the clock. Queued clocked events stay queued; Close is safe to
// call more than once.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	close(b.done)
}

// Flush is the clock's boundary: every clocked subscription with queued
// events receives one wake carrying them, and an empty subscription is left
// alone, so an idle period wakes no one. The ticker calls Flush on every
// boundary; tests drive it directly.
func (b *Bus) Flush() {
	b.mu.Lock()
	var due []*Subscription
	for _, subs := range b.subs {
		for _, s := range subs {
			if s.clocked && b.period > 0 && s.scheduled && !s.delivering && len(s.queue) > 0 {
				s.delivering = true
				due = append(due, s)
			}
		}
	}
	b.mu.Unlock()
	for _, s := range due {
		go b.deliverClocked(s)
	}
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
			s.idle = make(chan struct{})
			if !s.clocked || b.period <= 0 {
				scheduled = append(scheduled, s)
			}
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
			if s.idle != nil {
				close(s.idle)
				s.idle = nil
			}
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()
	}
}

// deliverClocked wakes one clocked subscription once, then leaves any events
// that arrived during the wake for the next boundary.
func (b *Bus) deliverClocked(s *Subscription) {
	s.waker.Wake(s)
	b.mu.Lock()
	s.delivering = false
	if len(s.queue) == 0 {
		s.scheduled = false
		if s.idle != nil {
			close(s.idle)
			s.idle = nil
		}
	}
	b.mu.Unlock()
}
