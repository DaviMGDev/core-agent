// Native agent plugin: the turn loop behind the "agent-loop" key as a
// compiled memento component.
//
// Activation parses the payload into the agent's configuration and binds
// the loop itself for the terminal, the host waker, and subagent runners.
// Turns run through the shared RunTurn with dependencies wired over the
// composed system: the pipeline over the typed history/context/model keys,
// jobs over the host job service attributed to this instance (so adoption
// and visibility answer exactly as they do for the guest), and messages
// over the host publish path. Handle serves the same wake documents the
// wasm guest serves, including its silences: a bad wake or an empty line
// answers empty bytes with no error, and a failed turn logs its cause.
package agent

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

// Jobs is the job surface one agent turn reaches: starting a tool call as
// a job, attributed to the calling instance so adoption and visibility
// answer for the turn in flight.
type Jobs interface {
	StartJob(caller *runtime.Instance, req []byte) ([]byte, error)
}

// Component is the native agent plugin. Log receives lifecycle lines; nil
// discards them. Jobs is the host job service and Publish publishes one
// chat.message text; both are required — a turn that needs a missing
// service fails instead of panicking the host.
type Component struct {
	Log     io.Writer
	Jobs    Jobs
	Publish func(text string) error

	history keys.History
	models  keys.ModelRegistry
	context keys.Context
	inst    *runtime.Instance
	config  Config
	ready   bool
}

// NewComponent returns an agent component logging to log, reaching jobs
// through jobs and publishing through publish.
func NewComponent(log io.Writer, jobs Jobs, publish func(text string) error) *Component {
	return &Component{Log: log, Jobs: jobs, Publish: publish}
}

// Declarations returns the provided agent-loop key and the injected
// pipeline keys.
func (c *Component) Declarations() runtime.Declarations {
	return runtime.Declarations{
		Provide: []mcontext.AnyKey{keys.AgentLoopKey},
		Inject:  []mcontext.AnyKey{keys.HistoryKey, keys.ModelRegistryKey, keys.ContextKey},
	}
}

// Activate parses the payload configuration and binds the loop.
func (c *Component) Activate(inst *runtime.Instance, payload any) error {
	raw, err := keys.PayloadBytes(payload)
	if err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	var cfg Config
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			c.emit("agent: bad config: " + err.Error() + "\n")
			return err
		}
	}
	history, ok := runtime.Get(inst, keys.HistoryKey)
	if !ok {
		return errors.New("agent: chat-history is not available")
	}
	models, ok := runtime.Get(inst, keys.ModelRegistryKey)
	if !ok {
		return errors.New("agent: model-registry is not available")
	}
	ctx, ok := runtime.Get(inst, keys.ContextKey)
	if !ok {
		return errors.New("agent: llm-context is not available")
	}
	c.history, c.models, c.context = history, models, ctx
	c.inst = inst
	c.config = cfg
	if err := runtime.Bind[keys.AgentLoop](inst, keys.AgentLoopKey, c); err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	if err := inst.Context().RegisterEffect(func() (func() error, error) {
		return func() error {
			c.emit("agent: loop closed\n")
			return nil
		}, nil
	}); err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	c.ready = true
	return nil
}

// wakeResponse is the handler's answer: the message it spoke, the job it
// started, and — when asked — the conversation the wake ran on.
type wakeResponse struct {
	Text         string    `json:"text,omitempty"`
	Job          string    `json:"job,omitempty"`
	Conversation []Message `json:"conversation,omitempty"`
}

// Handle runs one wake through the turn loop: a user line, or the events a
// bus wake carries. Private marks a subagent turn: its speech stays in the
// child's conversation and is not published for a renderer.
func (c *Component) Handle(ctx context.Context, req []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !c.ready {
		return nil, errors.New("agent: not activated")
	}
	var wake Wake
	if err := json.Unmarshal(req, &wake); err != nil {
		return []byte{}, nil
	}
	cfg := c.config
	if wake.Config != nil {
		cfg = wake.Config.Normalized()
	}
	line := wake.Line
	if line == "" {
		line = describeEvents(wake.Events)
	}
	if line == "" {
		return []byte{}, nil
	}
	result, err := RunTurn(cfg, line, c.deps(cfg, !wake.Private))
	if err != nil {
		c.emit("agent: " + err.Error() + "\n")
		return []byte{}, nil
	}
	out := wakeResponse{Text: result.Text, Job: result.Job}
	if wake.Dump {
		if msgs, err := c.dumpConversation(cfg.Conversation); err == nil {
			out.Conversation = msgs
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return []byte{}, nil
	}
	return b, nil
}

// deps builds the turn's operations over the composed system: the pipeline
// over the typed keys, jobs over the host job service attributed to this
// instance, messages over the host publish path.
func (c *Component) deps(cfg Config, publish bool) Deps {
	return Deps{
		Append: func(role, text string) error {
			_, err := c.history.Append(cfg.Conversation, role, text)
			return err
		},
		Recent: func(n int) ([]Message, error) {
			msgs := c.history.Recent(cfg.Conversation, n)
			out := make([]Message, 0, len(msgs))
			for _, m := range msgs {
				out = append(out, Message{Role: m.Role, Text: m.Text})
			}
			return out, nil
		},
		Project: func(msgs []Message) ([]Message, error) {
			in := make([]keys.ChatMessage, 0, len(msgs))
			for _, m := range msgs {
				in = append(in, keys.ChatMessage{Role: m.Role, Text: m.Text})
			}
			kept, _ := c.context.Project(in)
			out := make([]Message, 0, len(kept))
			for _, m := range kept {
				out = append(out, Message{Role: m.Role, Text: m.Text})
			}
			return out, nil
		},
		Respond: func(text string, context []Message, tools []Tool) (Answer, error) {
			_ = text
			msgs := make([]keys.ChatMessage, 0, len(context))
			for _, m := range context {
				msgs = append(msgs, keys.ChatMessage{Role: m.Role, Text: m.Text})
			}
			surface := make([]keys.PresentedTool, 0, len(tools))
			for _, t := range tools {
				surface = append(surface, keys.PresentedTool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
			}
			answer, err := c.models.Respond(cfg.Model, msgs, surface)
			if err != nil {
				return Answer{}, err
			}
			return ParseAnswer(answer), nil
		},
		StartJob: func(tool string, args json.RawMessage) (string, error) {
			if c.Jobs == nil {
				return "", errors.New("agent: no job service configured")
			}
			req := map[string]any{"tool": tool}
			if len(args) > 0 {
				req["args"] = args
			}
			b, err := json.Marshal(req)
			if err != nil {
				return "", err
			}
			resp, err := c.Jobs.StartJob(c.inst, b)
			if err != nil {
				return "", err
			}
			var handle struct {
				Job string `json:"job"`
			}
			if err := json.Unmarshal(resp, &handle); err != nil {
				return "", fmt.Errorf("agent: job start: %w", err)
			}
			return handle.Job, nil
		},
		Publish: func(text string) error {
			if !publish {
				return nil
			}
			if c.Publish == nil {
				return errors.New("agent: no publish path configured")
			}
			return c.Publish(text)
		},
	}
}

// allTurns asks the record for the whole conversation; recent clamps to the
// length, so a bound above any real conversation returns everything.
const allTurns = 1 << 20

// dumpConversation reads a conversation back from the record.
func (c *Component) dumpConversation(conversation string) ([]Message, error) {
	msgs := c.history.Recent(conversation, allTurns)
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, Message{Role: m.Role, Text: m.Text})
	}
	return out, nil
}

// describeEvents flattens a bus wake into the line the model sees.
func describeEvents(events []Event) string {
	line := ""
	for _, e := range events {
		if line != "" {
			line += "; "
		}
		line += e.Topic
		if len(e.Payload) > 0 {
			line += " " + string(e.Payload)
		}
	}
	return line
}

// emit writes s to the log verbatim; call sites carry their own newline,
// matching the guest's log lines exactly.
func (c *Component) emit(s string) {
	if c.Log == nil || s == "" {
		return
	}
	fmt.Fprint(c.Log, s)
}
