package agent

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/DaviMGDev/core-agent/plugins/notifications"
)

// hostAgent is the loop as the host composes it: one Turn both the terminal
// and the bus waker call.
type hostAgent struct {
	mu      sync.Mutex
	turns   []string
	spoke   []string
	answer  Answer
	publish func(string) error
}

func (a *hostAgent) Turn(line string) (TurnResult, error) {
	a.mu.Lock()
	a.turns = append(a.turns, line)
	a.mu.Unlock()
	deps := Deps{
		Append:   func(role, text string) error { return nil },
		Recent:   func(n int) ([]Message, error) { return nil, nil },
		Project:  func(msgs []Message) ([]Message, error) { return msgs, nil },
		Respond:  func(text string, context []Message, tools []Tool) (Answer, error) { return a.answer, nil },
		StartJob: func(tool string, args json.RawMessage) (string, error) { return "job-1", nil },
		Publish: func(text string) error {
			a.mu.Lock()
			a.spoke = append(a.spoke, text)
			a.mu.Unlock()
			if a.publish != nil {
				return a.publish(text)
			}
			return nil
		},
	}
	return RunTurn(Config{}, line, deps)
}

func (a *hostAgent) snapshot() (turns, spoke []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.turns...), append([]string(nil), a.spoke...)
}

// agentWaker is the host waker: a bus wake runs one turn with the events.
type agentWaker struct {
	agent *hostAgent
}

func (w agentWaker) Wake(sub *notifications.Subscription) {
	events := sub.Take()
	line := ""
	for _, e := range events {
		if line != "" {
			line += "; "
		}
		line += e.Topic + " " + string(e.Payload)
	}
	if line == "" {
		return
	}
	_, _ = w.agent.Turn(line)
}

// collectWaker records chat.message events.
type collectWaker struct {
	mu     sync.Mutex
	events []string
}

func (w *collectWaker) Wake(sub *notifications.Subscription) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, e := range sub.Take() {
		w.events = append(w.events, string(e.Payload))
	}
}

func (w *collectWaker) got() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.events...)
}

func TestTerminalAndWakerWakeTheSameLoop(t *testing.T) {
	bus := notifications.New()
	messages := &collectWaker{}
	bus.Subscribe(notifications.TopicChatMessage, messages)

	agent := &hostAgent{
		answer:  Answer{Text: "hello"},
		publish: func(text string) error { bus.Publish(notifications.TopicChatMessage, []byte(text)); return nil },
	}
	bus.Subscribe(notifications.TopicJobCompleted, agentWaker{agent: agent})

	// The terminal path: a user line wakes the loop.
	if _, err := agent.Turn("hi"); err != nil {
		t.Fatalf("terminal turn: %v", err)
	}

	// The waker path: a job completion wakes the same loop.
	agent.mu.Lock()
	agent.answer = Answer{Text: "job done"}
	agent.mu.Unlock()
	bus.Publish(notifications.TopicJobCompleted, []byte(`{"job":"job-1"}`))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		turns, spoke := agent.snapshot()
		if len(turns) == 2 && len(spoke) == 2 && len(messages.got()) == 2 {
			if turns[0] != "hi" {
				t.Fatalf("first turn = %q, want the user line", turns[0])
			}
			if spoke[0] != "hello" || spoke[1] != "job done" {
				t.Fatalf("spoke = %v, want hello then job done", spoke)
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	turns, spoke := agent.snapshot()
	t.Fatalf("turns = %v, spoke = %v, messages = %v; want both paths to wake the loop", turns, spoke, messages.got())
}
