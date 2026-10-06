package conformance

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/DaviMGDev/core-agent/plugins/notifications"
)

// testWaker is a Waker for scenarios: it records wake batches, can block
// before the drain (busy subscriber) or after it (call in flight), and
// tracks how many wakes are active at once.
type testWaker struct {
	mu         sync.Mutex
	wakes      [][]notifications.Event
	active     int
	maxActive  int
	finished   bool
	blockEnter chan struct{}
	release    chan struct{}
	closeOnce  sync.Once
}

func (w *testWaker) Wake(sub *notifications.Subscription) {
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
	w.finished = true
	w.mu.Unlock()
}

func (w *testWaker) releaseAll() {
	w.closeOnce.Do(func() {
		if w.blockEnter != nil {
			close(w.blockEnter)
		}
		if w.release != nil {
			close(w.release)
		}
	})
}

func (w *testWaker) snapshot() (wakes [][]notifications.Event, active, maxActive int, finished bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][]notifications.Event(nil), w.wakes...), w.active, w.maxActive, w.finished
}

func waitWakes(w *testWaker, n int) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if wakes, _, _, _ := w.snapshot(); len(wakes) >= n {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	wakes, active, _, _ := w.snapshot()
	return fmt.Errorf("subscriber has %d wakes (active %d), want %d", len(wakes), active, n)
}

func payloads(events []notifications.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, string(e.Payload))
	}
	return out
}

func registerNotificationsSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a subscriber of "([^"]*)" that never returns$`, stepSubscriberNeverReturns)
	sc.Step(`^a subscriber of "([^"]*)" that is busy$`, stepSubscriberBusy)
	sc.Step(`^a subscriber of "([^"]*)"$`, stepSubscriber)
	sc.Step(`^a subscriber that is mid-call$`, stepSubscriberMidCall)
	sc.Step(`^subscribers of every job topic and of "([^"]*)"$`, stepSubscribersAllTopics)
	sc.Step(`^another component publishes "([^"]*)"$`, stepPublish)
	sc.Step(`^three events are published to "([^"]*)"$`, stepPublishThree)
	sc.Step(`^two events are published to "([^"]*)"$`, stepPublishTwo)
	sc.Step(`^the subscriber returns$`, stepSubscriberReturns)
	sc.Step(`^an event is published to its topic$`, stepPublishItsTopic)
	sc.Step(`^one event is published to each topic$`, stepPublishEachTopic)
	sc.Step(`^the publish returns without waiting for the subscriber$`, stepPublishReturned)
	sc.Step(`^the subscriber receives them in publish order$`, stepOrdered)
	sc.Step(`^the subscriber is woken once$`, stepWokenOnce)
	sc.Step(`^the wake carries both events$`, stepWakeBoth)
	sc.Step(`^the wake waits until the call returns$`, stepWakeWaits)
	sc.Step(`^the call is never interrupted$`, stepNeverInterrupted)
	sc.Step(`^each subscriber receives its topic's event$`, stepEachTopicEvent)
}

func stepSubscriberNeverReturns(ctx context.Context, topic string) error {
	w := worldFrom(ctx)
	w.bus = notifications.New()
	w.waker = &testWaker{release: make(chan struct{})}
	w.sub = w.bus.Subscribe(topic, w.waker)
	return nil
}

func stepSubscriberBusy(ctx context.Context, topic string) error {
	w := worldFrom(ctx)
	w.bus = notifications.New()
	w.waker = &testWaker{blockEnter: make(chan struct{})}
	w.sub = w.bus.Subscribe(topic, w.waker)
	return nil
}

func stepSubscriber(ctx context.Context, topic string) error {
	w := worldFrom(ctx)
	w.bus = notifications.New()
	w.waker = &testWaker{}
	w.sub = w.bus.Subscribe(topic, w.waker)
	return nil
}

func stepSubscriberMidCall(ctx context.Context) error {
	w := worldFrom(ctx)
	w.bus = notifications.New()
	w.waker = &testWaker{release: make(chan struct{})}
	w.sub = w.bus.Subscribe("job.tick", w.waker)
	w.bus.Publish("job.tick", []byte("seed"))
	return waitWakes(w.waker, 1)
}

func stepSubscribersAllTopics(ctx context.Context, chat string) error {
	w := worldFrom(ctx)
	w.bus = notifications.New()
	w.subWakers = make(map[string]*testWaker)
	topics := []string{
		notifications.TopicJobStarted,
		notifications.TopicJobTick,
		notifications.TopicJobCompleted,
		notifications.TopicJobFailed,
		notifications.TopicJobKilled,
		chat,
	}
	for _, topic := range topics {
		tw := &testWaker{}
		w.bus.Subscribe(topic, tw)
		w.subWakers[topic] = tw
	}
	return nil
}

func stepPublish(ctx context.Context, topic string) error {
	w := worldFrom(ctx)
	done := make(chan struct{})
	go func() {
		w.bus.Publish(topic, []byte("payload"))
		close(done)
	}()
	select {
	case <-done:
		w.publishDone = true
	case <-time.After(3 * time.Second):
		w.publishDone = false
	}
	return nil
}

func stepPublishThree(ctx context.Context, topic string) error {
	w := worldFrom(ctx)
	for _, p := range []string{"1", "2", "3"} {
		w.bus.Publish(topic, []byte(p))
	}
	return nil
}

func stepPublishTwo(ctx context.Context, topic string) error {
	w := worldFrom(ctx)
	w.bus.Publish(topic, []byte("a"))
	w.bus.Publish(topic, []byte("b"))
	return nil
}

func stepSubscriberReturns(ctx context.Context) error {
	w := worldFrom(ctx)
	w.waker.releaseAll()
	return nil
}

func stepPublishItsTopic(ctx context.Context) error {
	w := worldFrom(ctx)
	w.bus.Publish("job.tick", []byte("second"))
	return nil
}

func stepPublishEachTopic(ctx context.Context) error {
	w := worldFrom(ctx)
	for topic := range w.subWakers {
		w.bus.Publish(topic, []byte(topic))
	}
	return nil
}

func stepPublishReturned(ctx context.Context) error {
	w := worldFrom(ctx)
	if !w.publishDone {
		return fmt.Errorf("publish did not return while the subscriber was blocked")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, active, _, finished := w.waker.snapshot()
		if finished {
			return fmt.Errorf("the blocking subscriber returned; publish may have waited")
		}
		if active == 1 {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fmt.Errorf("the subscriber never entered a blocked wake")
}

func stepOrdered(ctx context.Context) error {
	w := worldFrom(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		wakes, _, _, _ := w.waker.snapshot()
		var got []string
		for _, batch := range wakes {
			got = append(got, payloads(batch)...)
		}
		if len(got) >= 3 {
			if want := []string{"1", "2", "3"}; !equalStrings(got, want) {
				return fmt.Errorf("received %v, want %v", got, want)
			}
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fmt.Errorf("subscriber did not receive three events")
}

func stepWokenOnce(ctx context.Context) error {
	w := worldFrom(ctx)
	if err := waitWakes(w.waker, 1); err != nil {
		return err
	}
	wakes, _, _, _ := w.waker.snapshot()
	if len(wakes) != 1 {
		return fmt.Errorf("wakes = %d, want 1", len(wakes))
	}
	return nil
}

func stepWakeBoth(ctx context.Context) error {
	w := worldFrom(ctx)
	wakes, _, _, _ := w.waker.snapshot()
	if len(wakes) != 1 {
		return fmt.Errorf("wakes = %d, want 1", len(wakes))
	}
	if got, want := payloads(wakes[0]), []string{"a", "b"}; !equalStrings(got, want) {
		return fmt.Errorf("wake payloads = %v, want %v", got, want)
	}
	return nil
}

func stepWakeWaits(ctx context.Context) error {
	w := worldFrom(ctx)
	wakes, active, _, _ := w.waker.snapshot()
	if len(wakes) != 1 || active != 1 {
		return fmt.Errorf("before release: wakes = %d (active %d), want 1 in flight", len(wakes), active)
	}
	w.waker.releaseAll()
	if err := waitWakes(w.waker, 2); err != nil {
		return err
	}
	wakes, _, _, _ = w.waker.snapshot()
	if got, want := payloads(wakes[1]), []string{"second"}; !equalStrings(got, want) {
		return fmt.Errorf("second wake payloads = %v, want %v", got, want)
	}
	return nil
}

func stepNeverInterrupted(ctx context.Context) error {
	w := worldFrom(ctx)
	_, _, maxActive, _ := w.waker.snapshot()
	if maxActive != 1 {
		return fmt.Errorf("max concurrent wakes = %d, want 1", maxActive)
	}
	return nil
}

func stepEachTopicEvent(ctx context.Context) error {
	w := worldFrom(ctx)
	for topic, w := range w.subWakers {
		if err := waitWakes(w, 1); err != nil {
			return fmt.Errorf("%s: %w", topic, err)
		}
		wakes, _, _, _ := w.snapshot()
		if len(wakes) != 1 || len(wakes[0]) != 1 {
			return fmt.Errorf("%s: wakes = %v, want one event in one wake", topic, wakes)
		}
		ev := wakes[0][0]
		if ev.Topic != topic || string(ev.Payload) != topic {
			return fmt.Errorf("%s: event = {%s %q}, want {%s %q}", topic, ev.Topic, ev.Payload, topic, topic)
		}
	}
	return nil
}
