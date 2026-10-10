// Native chat-history plugin: the conversation record behind the
// "chat-history" key as a compiled memento component.
//
// Activation opens the payload's conversation (the default when empty) in a
// fresh in-memory store and binds the record. Handle serves the same
// operation documents the wasm guest serves (append, recent), including its
// failure shape: a failed operation answers empty bytes with no error, and
// — like the guest — logs nothing for it.
package chathistory

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

// defaultConversation is the conversation the payload leaves unset.
const defaultConversation = "default"

// Component is the native chat-history plugin. Log receives lifecycle
// lines; nil discards them.
type Component struct {
	Log   io.Writer
	store *Store
}

// NewComponent returns a chat-history component logging to log.
func NewComponent(log io.Writer) *Component {
	return &Component{Log: log, store: NewStore()}
}

// Declarations returns the provided chat-history key.
func (c *Component) Declarations() runtime.Declarations {
	return runtime.Declarations{Provide: []mcontext.AnyKey{keys.HistoryKey}}
}

// Activate opens the payload's conversation and binds the record.
func (c *Component) Activate(inst *runtime.Instance, payload any) error {
	raw, err := keys.PayloadBytes(payload)
	if err != nil {
		return fmt.Errorf("chat-history: %w", err)
	}
	id := string(raw)
	if id == "" {
		id = defaultConversation
	}
	if c.store == nil {
		c.store = NewStore()
	}
	if err := c.store.Start(id); err != nil {
		c.emit("chat-history: " + err.Error() + "\n")
		return err
	}
	if err := runtime.Bind[keys.History](inst, keys.HistoryKey, historyAdapter{c.store}); err != nil {
		return fmt.Errorf("chat-history: %w", err)
	}
	if err := inst.Context().RegisterEffect(func() (func() error, error) {
		return func() error {
			c.emit("chat-history: conversation closed\n")
			return nil
		}, nil
	}); err != nil {
		return fmt.Errorf("chat-history: %w", err)
	}
	c.emit(fmt.Sprintf("chat-history: conversation %q open\n", id))
	return nil
}

// Handle serves one record operation document and returns the result
// document: the same contract the wasm guest's handler serves.
func (c *Component) Handle(ctx context.Context, req []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.store == nil {
		return nil, errors.New("chat-history: not activated")
	}
	var op Op
	if err := json.Unmarshal(req, &op); err != nil {
		return []byte{}, nil
	}
	result, err := Apply(c.store, op)
	if err != nil {
		return []byte{}, nil
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("chat-history: %w", err)
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

// historyAdapter implements keys.History over a *Store.
type historyAdapter struct {
	s *Store
}

func (a historyAdapter) Append(conversation, role, text string) (int, error) {
	if _, err := a.s.Append(conversation, role, text); err != nil {
		return 0, err
	}
	return a.s.Len(conversation), nil
}

func (a historyAdapter) Recent(conversation string, n int) []keys.ChatMessage {
	msgs := a.s.Recent(conversation, n)
	out := make([]keys.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, keys.ChatMessage{Role: m.Role, Text: m.Text})
	}
	return out
}
