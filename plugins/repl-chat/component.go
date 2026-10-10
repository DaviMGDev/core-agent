// Native repl-chat plugin: the terminal behind the "repl" key as a
// compiled memento component.
//
// Activation binds the session nickname. The host drives the session loop;
// the component serves the same wake handler the wasm guest serves: a user
// line runs one agent turn through the injected loop (the answer carries no
// text — the turn's message arrives as a chat.message wake), and the events
// a bus wake carries render to the log. Failures ride the answer, so the
// session reports them and continues; an empty wake answers empty bytes.
package replchat

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

// Component is the native repl-chat plugin. Log receives lifecycle lines
// and rendered wakes; nil discards them.
type Component struct {
	Log   io.Writer
	loop  keys.AgentLoop
	ready bool
}

// NewComponent returns a repl-chat component logging to log.
func NewComponent(log io.Writer) *Component {
	return &Component{Log: log}
}

// Declarations returns the provided repl key and the injected agent-loop
// key the terminal runs its lines through.
func (c *Component) Declarations() runtime.Declarations {
	return runtime.Declarations{
		Provide: []mcontext.AnyKey{keys.ReplKey},
		Inject:  []mcontext.AnyKey{keys.AgentLoopKey},
	}
}

// Activate binds the session nickname from the payload.
func (c *Component) Activate(inst *runtime.Instance, payload any) error {
	raw, err := keys.PayloadBytes(payload)
	if err != nil {
		return fmt.Errorf("repl-chat: %w", err)
	}
	loop, ok := runtime.Get(inst, keys.AgentLoopKey)
	if !ok {
		return errors.New("repl-chat: agent-loop is not available")
	}
	c.loop = loop
	if err := runtime.Bind(inst, keys.ReplKey, New(string(raw)).Nick()); err != nil {
		return fmt.Errorf("repl-chat: %w", err)
	}
	if err := inst.Context().RegisterEffect(func() (func() error, error) {
		return func() error {
			c.emit(LeaveMessage + "\n")
			return nil
		}, nil
	}); err != nil {
		return fmt.Errorf("repl-chat: %w", err)
	}
	c.ready = true
	return nil
}

// agentWake is the agent's request document for one terminal line: the line
// only, so the turn runs on the activation configuration.
type agentWake struct {
	Line string `json:"line"`
}

// Handle serves one wake: a user line runs one agent turn; the events a bus
// wake carries render to the log.
func (c *Component) Handle(ctx context.Context, req []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !c.ready {
		return nil, errors.New("repl-chat: not activated")
	}
	var wake Wake
	if err := json.Unmarshal(req, &wake); err != nil {
		return []byte{}, nil
	}
	answer := Answer{}
	switch {
	case wake.Line != "":
		if err := c.runLine(ctx, wake.Line); err != nil {
			answer.Error = err.Error()
		}
	case len(wake.Events) > 0:
		for _, line := range Render(wake.Events) {
			c.emit(line + "\n")
		}
	default:
		return []byte{}, nil
	}
	out, err := json.Marshal(answer)
	if err != nil {
		return []byte{}, nil
	}
	return out, nil
}

// runLine hands one user line to the agent and waits for the turn to
// finish. The message it speaks is published as chat.message, so the
// terminal renders it from the wake — this answer carries no text.
func (c *Component) runLine(ctx context.Context, line string) error {
	req, err := json.Marshal(agentWake{Line: line})
	if err != nil {
		return err
	}
	if c.loop == nil {
		return errors.New("repl-chat: agent loop is not available")
	}
	resp, err := c.loop.Handle(ctx, req)
	if err != nil {
		return err
	}
	// The turn's message arrives as a chat.message wake, so the answer
	// carries no text — but an empty answer means the turn failed, the
	// same no-response refusal the guest's invoke reports.
	if len(resp) == 0 {
		return errors.New("invoke agent-loop: no response")
	}
	return nil
}

// emit writes s to the log verbatim; call sites carry their own newline,
// matching the guest's log lines exactly.
func (c *Component) emit(s string) {
	if c.Log == nil || s == "" {
		return
	}
	fmt.Fprint(c.Log, s)
}
