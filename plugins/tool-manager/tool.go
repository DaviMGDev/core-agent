// Package tool-manager is the agent layer's tool registry and job engine,
// host-side per system spec D14. Every tool call is a job: start returns a
// handle at once, peep observes, kill is accepted at any point, and the job's
// life is queued → running → done | failed | killed.
package tool_manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Tool is one callable operation. The owner declares it: name, description,
// and argument schema; the manager lists it.
type Tool struct {
	Name        string
	Description string
	// Schema is the argument schema, opaque to the manager.
	Schema json.RawMessage
	// Run executes one call. It runs in its own goroutine and observes ctx
	// for kill: returning at its next safe point is the contract.
	Run Runner
	// Background marks tools that launch an asynchronous lifecycle (like
	// bash or subagent). When executed via Manager.Execute, the manager starts
	// a Job and returns {"job":"job-X","status":"started"} synchronously.
	Background bool
}

// Runner executes a tool call. out collects output-so-far for peep; the
// returned value is the job's result.
type Runner func(ctx context.Context, args json.RawMessage, out *Output) (any, error)

// Output is the job's write-through output buffer: what the runner writes is
// what peep returns, even mid-run.
type Output struct {
	job *Job
}

func (o *Output) Write(p []byte) (int, error) {
	if o == nil || o.job == nil {
		return len(p), nil
	}
	o.job.appendOutput(p)
	return len(p), nil
}

// Job returns the job this output belongs to: the runner's own handle, for
// the tree, the caller attribution, and the parent listener.
func (o *Output) Job() *Job {
	if o == nil {
		return nil
	}
	return o.job
}

// Registry holds the callable tools. A duplicate name is refused and the
// registry stays unchanged, the same discipline the kernel uses for keys.
type Registry struct {
	mu    sync.Mutex
	tools map[string]Tool
	order []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Declare registers a tool. A missing name, a missing runner, or a duplicate
// name is refused with an error; the registry is left unchanged.
func (r *Registry) Declare(t Tool) error {
	if t.Name == "" {
		return errors.New("tool name is required")
	}
	if t.Run == nil {
		return fmt.Errorf("tool %q has no runner", t.Name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[t.Name]; exists {
		return fmt.Errorf("duplicate tool name %q", t.Name)
	}
	r.tools[t.Name] = t
	r.order = append(r.order, t.Name)
	return nil
}

// Lookup returns the tool with the given name.
func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tools[name]
	return t, ok
}

// Tools lists the declared tools in declaration order.
func (r *Registry) Tools() []Tool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Tool, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.tools[name])
	}
	return out
}

// names lists the declared tool names in declaration order.
func (r *Registry) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// outputBuffer is the job's accumulated output, guarded by the job's mutex.
type outputBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *outputBuffer) write(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Write(p)
}

func (b *outputBuffer) string() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
