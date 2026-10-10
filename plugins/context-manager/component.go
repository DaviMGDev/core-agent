// Native context-manager plugin: the bounded context window behind the
// "llm-context" key as a compiled memento component.
//
// Activation parses the payload ({"budget":<runes>}, DefaultBudget when
// unset) and binds the projection service. Like the wasm guest, the plugin
// declares the chat-history inject for activation ordering; the projection
// itself is a pure function of the turns each request carries. Handle
// serves the same operation documents the wasm guest serves (project),
// including its failure shape: a failed operation answers empty bytes with
// no error.
package contextmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/DaviMGDev/core-agent/internal/keys"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// Component is the native context-manager plugin. Log receives lifecycle
// lines; nil discards them.
type Component struct {
	Log    io.Writer
	budget int
	ready  bool
}

// NewComponent returns a context-manager component logging to log.
func NewComponent(log io.Writer) *Component {
	return &Component{Log: log}
}

// Declarations returns the provided llm-context key and the injected
// chat-history key (activation ordering, as the guest declares).
func (c *Component) Declarations() runtime.Declarations {
	return runtime.Declarations{
		Provide: []mcontext.AnyKey{keys.ContextKey},
		Inject:  []mcontext.AnyKey{keys.HistoryKey},
	}
}

// Activate parses the payload budget and binds the projection service.
func (c *Component) Activate(inst *runtime.Instance, payload any) error {
	raw, err := keys.PayloadBytes(payload)
	if err != nil {
		return fmt.Errorf("context-manager: %w", err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		c.emit("context-manager: " + err.Error() + "\n")
		return err
	}
	c.budget = cfg.Budget
	if c.budget == 0 {
		c.budget = DefaultBudget
	}
	if err := runtime.Bind[keys.Context](inst, keys.ContextKey, contextAdapter{budget: c.budget}); err != nil {
		return fmt.Errorf("context-manager: %w", err)
	}
	if err := inst.Context().RegisterEffect(func() (func() error, error) {
		return func() error {
			c.emit("context-manager: window released\n")
			return nil
		}, nil
	}); err != nil {
		return fmt.Errorf("context-manager: %w", err)
	}
	c.emit(fmt.Sprintf("context-manager: window ready (budget %d)\n", c.budget))
	c.ready = true
	return nil
}

// Handle serves one window operation document and returns the result
// document: the same contract the wasm guest's handler serves.
func (c *Component) Handle(ctx context.Context, req []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !c.ready {
		return nil, errors.New("context-manager: not activated")
	}
	var op Op
	if err := json.Unmarshal(req, &op); err != nil {
		return []byte{}, nil
	}
	result, err := Apply(c.budget, op)
	if err != nil {
		return []byte{}, nil
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("context-manager: %w", err)
	}
	return out, nil
}

// emit writes s to the log verbatim; call sites carry their own newline,
// matching the guest's log lines exactly.
func (c *Component) emit(s string) {
	if c.Log == nil || s == "" {
		return
	}
	fmt.Fprint(c.Log, s)
}

// contextAdapter implements keys.Context over a configured rune budget.
type contextAdapter struct {
	budget int
}

func (a contextAdapter) Project(messages []keys.ChatMessage) ([]keys.ChatMessage, int) {
	history := make([]Message, 0, len(messages))
	for _, m := range messages {
		history = append(history, Message{Role: m.Role, Text: m.Text})
	}
	w := Project(history, a.budget)
	kept := make([]keys.ChatMessage, 0, len(w.Messages))
	for _, m := range w.Messages {
		kept = append(kept, keys.ChatMessage{Role: m.Role, Text: m.Text})
	}
	return kept, w.Dropped
}
