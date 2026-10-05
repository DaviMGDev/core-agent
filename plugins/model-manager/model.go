// Package modelmanager manages model views: aliases, fallback chains, and
// discussion groups resolved over the provider registry. Resolution and
// response selection are fixed here; the model call itself arrives through a
// Caller, which the wasm guest implements over the loader's HTTP transport.
// Orchestration of a discussion is still deferred.
package modelmanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Mode identifies how a name resolves.
type Mode string

const (
	// ModeModel is a name with no registered view.
	ModeModel Mode = "model"
	// ModeAlias resolves to one target.
	ModeAlias Mode = "alias"
	// ModeFallback resolves to ordered targets tried in turn.
	ModeFallback Mode = "fallback"
	// ModeDiscuss resolves to ordered participants that talk before one
	// response is returned.
	ModeDiscuss Mode = "discuss"
)

// View is a custom model: exactly one of Alias, Fallback, or Discuss is set.
type View struct {
	Name     string   `json:"name"`
	Alias    string   `json:"alias,omitempty"`
	Fallback []string `json:"fallback,omitempty"`
	Discuss  []string `json:"discuss,omitempty"`
}

// Resolution is the result of resolving a name: a mode and the concrete
// model names, flattened and in order.
type Resolution struct {
	Mode   Mode
	Models []string
}

// Validate checks the one-mode rule and the names involved.
func Validate(v View) error {
	if strings.TrimSpace(v.Name) == "" {
		return errors.New("model-manager: empty view name")
	}
	modes := 0
	if v.Alias != "" {
		modes++
	}
	if len(v.Fallback) > 0 {
		modes++
	}
	if len(v.Discuss) > 0 {
		modes++
	}
	if modes != 1 {
		return fmt.Errorf("model-manager: view %q must set exactly one of alias, fallback, discuss", v.Name)
	}
	for _, n := range append(append([]string{}, v.Fallback...), v.Discuss...) {
		if strings.TrimSpace(n) == "" {
			return fmt.Errorf("model-manager: view %q lists an empty target", v.Name)
		}
	}
	return nil
}

// Registry stores views in insertion order.
type Registry struct {
	views map[string]View
	order []string
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{views: make(map[string]View)}
}

// Register validates and appends a view.
func (r *Registry) Register(v View) error {
	if err := Validate(v); err != nil {
		return err
	}
	if _, ok := r.views[v.Name]; ok {
		return fmt.Errorf("model-manager: duplicate view %q", v.Name)
	}
	r.views[v.Name] = v
	r.order = append(r.order, v.Name)
	return nil
}

// Get returns the view named name.
func (r *Registry) Get(name string) (View, bool) {
	v, ok := r.views[name]
	return v, ok
}

// Len returns the number of registered views.
func (r *Registry) Len() int { return len(r.order) }

// List returns the views in insertion order.
func (r *Registry) List() []View {
	out := make([]View, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.views[name])
	}
	return out
}

// Resolve resolves a name to its mode and ordered, flattened targets. An
// unregistered name is a plain model; a cycle is refused.
func (r *Registry) Resolve(name string) (Resolution, error) {
	return r.resolve(name, map[string]bool{})
}

func (r *Registry) resolve(name string, path map[string]bool) (Resolution, error) {
	if path[name] {
		return Resolution{}, fmt.Errorf("model-manager: cycle at %q", name)
	}
	v, ok := r.views[name]
	if !ok {
		return Resolution{Mode: ModeModel, Models: []string{name}}, nil
	}
	path[name] = true
	defer delete(path, name)

	switch {
	case v.Alias != "":
		target, err := r.resolve(v.Alias, path)
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{Mode: ModeAlias, Models: target.Models}, nil
	case len(v.Fallback) > 0:
		models, err := r.resolveAll(v.Fallback, path)
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{Mode: ModeFallback, Models: models}, nil
	default:
		models, err := r.resolveAll(v.Discuss, path)
		if err != nil {
			return Resolution{}, err
		}
		return Resolution{Mode: ModeDiscuss, Models: models}, nil
	}
}

func (r *Registry) resolveAll(names []string, path map[string]bool) ([]string, error) {
	var out []string
	for _, name := range names {
		res, err := r.resolve(name, path)
		if err != nil {
			return nil, err
		}
		out = append(out, res.Models...)
	}
	return out, nil
}

type config struct {
	Models []View `json:"models"`
}

// ParseConfig reads a {"models":[...]} payload. An empty payload is an empty
// configuration.
func ParseConfig(payload []byte) ([]View, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, nil
	}
	var c config
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("model-manager: parsing config: %w", err)
	}
	return c.Models, nil
}

// Op is one operation requested over the view registry.
type Op struct {
	Kind    string           `json:"op"`
	Name    string           `json:"name,omitempty"`
	Model   string           `json:"model,omitempty"`
	Text    string           `json:"text,omitempty"`
	Context []ContextMessage `json:"context,omitempty"`
}

// ContextMessage is one turn supplied as context to a response.
type ContextMessage struct {
	Role string `json:"role,omitempty"`
	Text string `json:"text"`
}

// Result is the response of an operation.
type Result struct {
	Mode   Mode     `json:"mode,omitempty"`
	Models []string `json:"models,omitempty"`
	Model  string   `json:"model,omitempty"`
	Text   string   `json:"text,omitempty"`
}

// Caller performs one model completion. The implementation owns provider
// resolution and transport; the registry owns view resolution.
type Caller interface {
	Complete(model string, messages []ContextMessage) (string, error)
}

// MockCaller answers without a provider: it echoes the model, the last user
// message, and the context size. It implements Caller, so a composition mocks
// the LLM by config alone — a provider marked mock — with no network at all.
type MockCaller struct{}

// Complete returns the deterministic mock reply.
func (MockCaller) Complete(model string, messages []ContextMessage) (string, error) {
	text := ""
	for _, m := range messages {
		if strings.EqualFold(m.Role, "user") {
			text = m.Text
		}
	}
	if text == "" && len(messages) > 0 {
		text = messages[len(messages)-1].Text
	}
	if text == "" {
		return fmt.Sprintf("mock(%s) (context:%d)", model, len(messages)), nil
	}
	return fmt.Sprintf("mock(%s): %s (context:%d)", model, text, len(messages)), nil
}

// Apply runs one operation against the registry: "resolve" returns the
// flattened resolution of a name; "respond" resolves the requested model and
// answers through the caller. A fallback view tries its targets in order and
// returns the first answer; every other view answers with its first concrete
// model. A response without a caller is refused.
func Apply(r *Registry, op Op, caller Caller) (Result, error) {
	switch op.Kind {
	case "resolve":
		res, err := r.Resolve(op.Name)
		if err != nil {
			return Result{}, err
		}
		return Result{Mode: res.Mode, Models: res.Models}, nil
	case "respond":
		res, err := r.Resolve(op.Model)
		if err != nil {
			return Result{}, err
		}
		if len(res.Models) == 0 {
			return Result{}, fmt.Errorf("model-manager: %q resolves to no model", op.Model)
		}
		if caller == nil {
			return Result{}, errors.New("model-manager: no model caller configured")
		}
		if res.Mode == ModeFallback {
			var lastErr error
			for _, model := range res.Models {
				text, err := caller.Complete(model, op.Context)
				if err == nil {
					return Result{Model: model, Text: text}, nil
				}
				lastErr = err
			}
			return Result{}, fmt.Errorf("model-manager: fallback %q exhausted: %w", op.Model, lastErr)
		}
		text, err := caller.Complete(res.Models[0], op.Context)
		if err != nil {
			return Result{}, err
		}
		return Result{Model: res.Models[0], Text: text}, nil
	default:
		return Result{}, fmt.Errorf("model-manager: unknown operation %q", op.Kind)
	}
}
