// Package subagent makes an agent callable as a tool: the manager job whose
// runner drives one child agent loop with its own conversation, model, and
// budget (charter, issue #5). One implementation serves the top-level agent
// and every subagent — this runner reaches the same handler the terminal and
// the host waker reach.
package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/DaviMGDev/core-agent/plugins/agent"
	"github.com/DaviMGDev/core-agent/plugins/notifications"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
	"github.com/DaviMGDev/memento/runtime"
)

// Defaults for the subagent tool.
const (
	// ToolName is the registered tool's name.
	ToolName = "subagent"
	// DefaultDepth bounds the job tree: the top level is 0, and a call whose
	// job depth exceeds the bound fails.
	DefaultDepth = 2
)

// Agent is the agent-loop surface one subagent turn reaches: the same handler
// the terminal and the host waker call.
type Agent interface {
	Handle(ctx context.Context, req []byte) ([]byte, error)
}

// Jobs is the manager surface a subagent call needs: the registry for the
// child's tool surface, the parent listener, and the caller attribution that
// maps cancellation and child adoption to the job in flight.
type Jobs interface {
	Registry() *toolmanager.Registry
	Listen(job *toolmanager.Job) <-chan toolmanager.Event
	Unlisten(job *toolmanager.Job)
	Attribute(caller *runtime.Instance, job *toolmanager.Job)
	Release(caller *runtime.Instance, job *toolmanager.Job)
}

// Options configure the subagent tool.
type Options struct {
	// Depth bounds the job tree: a call whose job depth exceeds it fails
	// like any failed job. Zero keeps DefaultDepth.
	Depth int
	// Model is the child's default model view.
	Model string
	// Budget is the child's default context budget.
	Budget int
}

// depth returns the configured bound.
func (o Options) depth() int {
	if o.Depth <= 0 {
		return DefaultDepth
	}
	return o.Depth
}

// Call is one subagent request document.
type Call struct {
	Brief  string `json:"brief"`
	Return string `json:"return,omitempty"` // "reply" (default) or "conversation"
	Model  string `json:"model,omitempty"`
	Budget int    `json:"budget,omitempty"`
}

// Result is a completed subagent call: the final reply, and — when the caller
// asked — the child's whole conversation.
type Result struct {
	Reply        string          `json:"reply,omitempty"`
	Conversation []agent.Message `json:"conversation,omitempty"`
}

// wakeResponse is the agent's answer.
type wakeResponse struct {
	Text         string          `json:"text,omitempty"`
	Job          string          `json:"job,omitempty"`
	Conversation []agent.Message `json:"conversation,omitempty"`
}

// Tool returns the manager tool whose runner calls loop as a subagent.
func Tool(loop Agent, jobs Jobs, opts Options) toolmanager.Tool {
	return toolmanager.Tool{
		Name:        ToolName,
		Description: "call an agent with a brief and wait for its reply",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"brief":{"type":"string","description":"the child's only input"},` +
			`"return":{"type":"string","enum":["reply","conversation"]},` +
			`"model":{"type":"string"},` +
			`"budget":{"type":"integer"}},` +
			`"required":["brief"]}`),
		Run: func(ctx context.Context, args json.RawMessage, out *toolmanager.Output) (any, error) {
			return run(ctx, loop, jobs, opts, args, out)
		},
	}
}

// run drives one subagent call from the brief to the child's final reply.
func run(ctx context.Context, loop Agent, jobs Jobs, opts Options, args json.RawMessage, out *toolmanager.Output) (any, error) {
	var call Call
	if err := json.Unmarshal(args, &call); err != nil {
		return nil, fmt.Errorf("subagent: %w", err)
	}
	if strings.TrimSpace(call.Brief) == "" {
		return nil, errors.New("subagent: brief is required")
	}
	job := out.Job()
	if job == nil {
		return nil, errors.New("subagent: no job")
	}
	if d := job.Depth(); d > opts.depth() {
		return nil, fmt.Errorf("subagent: depth %d exceeds the bound %d", d, opts.depth())
	}

	wantConversation := call.Return == "conversation"
	cfg := childConfig(job, call, jobs, opts)
	events := jobs.Listen(job)
	defer jobs.Unlisten(job)

	req := agent.Wake{Line: call.Brief, Config: &cfg, Dump: wantConversation, Private: true}
	for {
		resp, err := turn(ctx, loop, jobs, job, req)
		if err != nil {
			return nil, err
		}
		if resp.Text != "" {
			res := Result{Reply: resp.Text}
			if wantConversation {
				res.Conversation = resp.Conversation
			}
			return res, nil
		}
		ev, ok := nextEvent(ctx, events)
		if !ok {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, errors.New("subagent: no listener")
		}
		req = agent.Wake{
			Events:  []agent.Event{{Topic: ev.Topic, Payload: json.RawMessage(ev.Payload)}},
			Config:  &cfg,
			Dump:    wantConversation,
			Private: true,
		}
	}
}

// turn invokes one child turn, attributing the job to the caller's instance
// for the duration of the call: cancellation and child adoption answer for
// the turn in flight, and nothing else.
func turn(ctx context.Context, loop Agent, jobs Jobs, job *toolmanager.Job, req agent.Wake) (wakeResponse, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return wakeResponse{}, fmt.Errorf("subagent: %w", err)
	}
	if caller := job.Caller(); caller != nil {
		jobs.Attribute(caller, job)
		defer jobs.Release(caller, job)
	}
	resp, err := loop.Handle(ctx, b)
	if err != nil {
		return wakeResponse{}, err
	}
	var out wakeResponse
	if err := json.Unmarshal(resp, &out); err != nil {
		return wakeResponse{}, fmt.Errorf("subagent: reading answer: %w", err)
	}
	return out, nil
}

// nextEvent waits for the next child event that wakes a turn. A start is not
// a wake reason: the turn that started the job just ended.
func nextEvent(ctx context.Context, events <-chan toolmanager.Event) (toolmanager.Event, bool) {
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return toolmanager.Event{}, false
			}
			if ev.Topic == notifications.TopicJobStarted {
				continue
			}
			return ev, true
		case <-ctx.Done():
			return toolmanager.Event{}, false
		}
	}
}

// childConfig is the child's turn configuration: its own conversation, the
// configured model and budget, and the registry's tool surface.
func childConfig(job *toolmanager.Job, call Call, jobs Jobs, opts Options) agent.Config {
	model := opts.Model
	if call.Model != "" {
		model = call.Model
	}
	budget := opts.Budget
	if call.Budget > 0 {
		budget = call.Budget
	}
	cfg := agent.Config{
		Conversation: "subagent-" + job.ID(),
		Model:        model,
		Budget:       budget,
	}
	for _, t := range jobs.Registry().Tools() {
		cfg.Tools = append(cfg.Tools, agent.Tool{Name: t.Name, Description: t.Description})
	}
	return cfg.Normalized()
}
