// Package contextmanager projects a recorded conversation into the bounded
// window an LLM call would receive. The projection is a pure function of
// turns and budget; the guest owns the ABI.
package contextmanager

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// DefaultBudget is the guest's budget when the payload leaves it unset.
const DefaultBudget = 4096

// Message is one turn in the window.
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// Window is a budgeted projection of a conversation.
type Window struct {
	// Messages are the kept turns in append order.
	Messages []Message
	// Dropped counts the omitted turns.
	Dropped int
	// Budget is the rune budget the window was projected against.
	Budget int
}

// Project keeps the newest messages that fit budget — counted in runes of
// message text — and always keeps the newest message, even when it alone
// exceeds the budget. Kept messages preserve append order.
func Project(history []Message, budget int) Window {
	w := Window{Budget: budget}
	if len(history) == 0 {
		return w
	}
	var kept []Message
	total := 0
	for i := len(history) - 1; i >= 0; i-- {
		m := history[i]
		cost := len([]rune(m.Text))
		if len(kept) == 0 {
			kept = append(kept, m)
			total += cost
			continue
		}
		if budget <= 0 || total+cost > budget {
			break
		}
		kept = append(kept, m)
		total += cost
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	w.Messages = kept
	w.Dropped = len(history) - len(kept)
	return w
}

// Config is the guest's activation payload: {"budget":<runes>}.
type Config struct {
	Budget int `json:"budget"`
}

// ParseConfig reads a {"budget":N} payload. An empty payload is the zero
// config; the guest applies DefaultBudget for a zero budget.
func ParseConfig(payload []byte) (Config, error) {
	var c Config
	if len(bytes.TrimSpace(payload)) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return Config{}, fmt.Errorf("context-manager: parsing config: %w", err)
	}
	return c, nil
}

// Op is one operation requested over the context window.
type Op struct {
	Kind     string    `json:"op"`
	Messages []Message `json:"messages,omitempty"`
}

// Result is the response of an operation.
type Result struct {
	Messages []Message `json:"messages,omitempty"`
	Dropped  int       `json:"dropped,omitempty"`
}

// Apply runs one operation against the configured budget: "project" builds
// the bounded window over the supplied turns.
func Apply(budget int, op Op) (Result, error) {
	switch op.Kind {
	case "project":
		w := Project(op.Messages, budget)
		return Result{Messages: w.Messages, Dropped: w.Dropped}, nil
	default:
		return Result{}, fmt.Errorf("context-manager: unknown operation %q", op.Kind)
	}
}
