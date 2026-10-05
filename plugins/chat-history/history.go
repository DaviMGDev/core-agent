// Package chathistory keeps the conversation record behind the
// "chat-history" key: append turns, list them, and serve recent windows for
// ongoing discussions. The first cut is in-memory.
package chathistory

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Roles the record understands.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one recorded turn.
type Message struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// Store is a concurrency-safe in-memory conversation store.
type Store struct {
	mu    sync.Mutex
	order []string
	conv  map[string][]Message
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{conv: make(map[string][]Message)}
}

// ValidRole reports whether role is recorded.
func ValidRole(role string) bool {
	return role == RoleUser || role == RoleAssistant
}

// Start creates an empty conversation if it does not exist.
func (s *Store) Start(conversation string) error {
	if strings.TrimSpace(conversation) == "" {
		return errors.New("chat-history: empty conversation id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.conv[conversation]; !ok {
		s.order = append(s.order, conversation)
		s.conv[conversation] = nil
	}
	return nil
}

// Append records one turn, starting the conversation if needed.
func (s *Store) Append(conversation, role, text string) (Message, error) {
	if strings.TrimSpace(conversation) == "" {
		return Message{}, errors.New("chat-history: empty conversation id")
	}
	if !ValidRole(role) {
		return Message{}, fmt.Errorf("chat-history: unknown role %q", role)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.conv[conversation]; !ok {
		s.order = append(s.order, conversation)
	}
	m := Message{Role: role, Text: text}
	s.conv[conversation] = append(s.conv[conversation], m)
	return m, nil
}

// Messages returns a copy of the conversation's turns in append order.
func (s *Store) Messages(conversation string) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.conv[conversation])
}

// Recent returns the last n turns in append order. n <= 0 returns none and
// n beyond the length returns everything.
func (s *Store) Recent(conversation string, n int) []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.conv[conversation]
	if n <= 0 || len(msgs) == 0 {
		return nil
	}
	if n > len(msgs) {
		n = len(msgs)
	}
	return clone(msgs[len(msgs)-n:])
}

// Len returns the number of turns in the conversation.
func (s *Store) Len(conversation string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conv[conversation])
}

// Conversations returns conversation ids in creation order.
func (s *Store) Conversations() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.order))
	copy(out, s.order)
	return out
}

func clone(msgs []Message) []Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]Message, len(msgs))
	copy(out, msgs)
	return out
}

// Op is one operation requested over the conversation record.
type Op struct {
	Kind         string `json:"op"`
	Conversation string `json:"conversation"`
	Role         string `json:"role,omitempty"`
	Text         string `json:"text,omitempty"`
	N            int    `json:"n,omitempty"`
}

// Result is the response of an operation.
type Result struct {
	Turn     int       `json:"turn,omitempty"`
	Messages []Message `json:"messages,omitempty"`
}

// Apply runs one operation against the store: "append" records a turn and
// returns the conversation length; "recent" serves the last n turns.
func Apply(store *Store, op Op) (Result, error) {
	switch op.Kind {
	case "append":
		if _, err := store.Append(op.Conversation, op.Role, op.Text); err != nil {
			return Result{}, err
		}
		return Result{Turn: store.Len(op.Conversation)}, nil
	case "recent":
		return Result{Messages: store.Recent(op.Conversation, op.N)}, nil
	default:
		return Result{}, fmt.Errorf("chat-history: unknown operation %q", op.Kind)
	}
}
