// Package agent implements the agent's turn loop, split out of repl-chat
// (system D14, charter binding): a turn runs from wake to quiescence, the
// model may answer, start a job, or say nothing, and a turn yields at most
// one chat.message.
package agent

import (
	"encoding/json"
	"strings"
)

// Defaults for an agent's configuration.
const (
	DefaultConversation = "main"
	DefaultModel        = "fast"
	DefaultBudget       = 10
)

// Tool is one callable the agent may present to the model. Parameters is the
// callable's argument schema (a JSON Schema document); it travels with the
// label through hiding and renaming, so the model can call the tool natively.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Config is one agent's configuration: its own conversation, model, budget,
// and the tool surface it may present.
type Config struct {
	Conversation string            `json:"conversation,omitempty"`
	Model        string            `json:"model,omitempty"`
	Budget       int               `json:"budget,omitempty"`
	Tools        []Tool            `json:"tools,omitempty"`
	Hidden       []string          `json:"hidden,omitempty"`
	Renamed      map[string]string `json:"renamed,omitempty"`
}

// Normalized fills the defaults.
func (c Config) Normalized() Config {
	if c.Conversation == "" {
		c.Conversation = DefaultConversation
	}
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.Budget <= 0 {
		c.Budget = DefaultBudget
	}
	return c
}

// Message is one conversation turn.
type Message struct {
	Role string `json:"role,omitempty"`
	Text string `json:"text"`
}

// Wake is one host call into the agent: a user line, or the events a bus wake
// carries. Config overrides the activation configuration for this wake only —
// a subagent's own conversation, model, and budget — and Dump asks the answer
// to carry the conversation the wake ran on. Private marks a subagent turn:
// its speech stays in the child's conversation and is not published for a
// renderer.
type Wake struct {
	Line    string  `json:"line,omitempty"`
	Events  []Event `json:"events,omitempty"`
	Config  *Config `json:"config,omitempty"`
	Dump    bool    `json:"dump,omitempty"`
	Private bool    `json:"private,omitempty"`
}

// Event is one bus event in a wake.
type Event struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Answer is the model's reply: speak, call a tool, or stay silent. A model
// answer whose text parses as a JSON directive carries the tool call; any
// other text is speech.
type Answer struct {
	Text    string          `json:"text,omitempty"`
	Tool    string          `json:"tool,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
	Silence bool            `json:"silence,omitempty"`
}

// ParseAnswer interprets one model answer: a JSON object with tool/silence
// fields is a directive; anything else is speech.
func ParseAnswer(text string) Answer {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") {
		var a Answer
		if err := json.Unmarshal([]byte(trimmed), &a); err == nil && (a.Tool != "" || a.Silence) {
			return a
		}
	}
	return Answer{Text: text}
}

// Deps are the operations one turn reaches: the composed system's pipeline
// and the host-side job surface.
type Deps struct {
	Append   func(role, text string) error
	Recent   func(n int) ([]Message, error)
	Project  func(msgs []Message) ([]Message, error)
	Respond  func(text string, context []Message, tools []Tool) (Answer, error)
	StartJob func(tool string, args json.RawMessage) (string, error)
	Publish  func(text string) error
}

// TurnResult is what one turn produced.
type TurnResult struct {
	// Spoke reports that the turn yielded a chat.message.
	Spoke bool
	// Text is the message, when it spoke.
	Text string
	// Job is the started job's handle, when the model called a tool.
	Job string
}

// SystemRole is the role of the assembled system message: the one message
// per turn that is the agent's own content, never part of the record.
const SystemRole = "system"

// SystemMessage assembles the turn's system message from the activation
// config and the presented tool surface: the agent's identity and only
// capabilities, the honesty rule, the wake policy, and the failure policy.
// The tools named are exactly the presented ones — hidden tools pruned,
// renamed tools relabeled — so the model can neither reach nor claim more.
func SystemMessage(cfg Config) Message {
	presented := Present(cfg)
	var b strings.Builder
	b.WriteString("You are the turn loop of the core-agent system. Your only capabilities are the tools listed below")
	if len(presented) == 0 {
		b.WriteString(": none are presented this turn — answer from the conversation, or answer with silence.")
	} else {
		b.WriteString(":\n")
		for _, t := range presented {
			b.WriteString("  - " + t.Name)
			if strings.TrimSpace(t.Description) != "" {
				b.WriteString(": " + t.Description)
			}
			b.WriteString("\n")
		}
		b.WriteString("Call a tool only when the wake asks for it or you cannot answer otherwise; a brief delegated to you is yours to answer — do not hand it to another agent.\n")
	}
	b.WriteString("Do not claim capabilities you were not given, and never assert a result no tool returned.\n")
	b.WriteString("Wakes arrive for user lines and background job events (ticks, completions, failures, kills), batched by the notification clock; when a wake needs no action, answer with silence — silence ends the turn with no message.\n")
	b.WriteString("Report tool failures factually — tool, state, and error as observed — without inventing a diagnosis.")
	return Message{Role: SystemRole, Text: b.String()}
}

// RunTurn runs one turn from wake to quiescence: append the line, project the
// agent's own context, ask the model with the presented tool surface under
// the assembled system message, and act on the answer. A tool call starts a
// job and ends the turn without a message; the job's completion or a tick
// wakes the next turn. Silence is a legitimate outcome; at most one
// chat.message leaves a turn. The system message is assembled per turn — it
// travels with the request, never into the record.
func RunTurn(cfg Config, line string, deps Deps) (TurnResult, error) {
	cfg = cfg.Normalized()
	if err := deps.Append("user", line); err != nil {
		return TurnResult{}, err
	}
	recent, err := deps.Recent(cfg.Budget)
	if err != nil {
		return TurnResult{}, err
	}
	projected, err := deps.Project(recent)
	if err != nil {
		return TurnResult{}, err
	}
	presented := Present(cfg)
	context := append([]Message{SystemMessage(cfg)}, projected...)
	answer, err := deps.Respond(line, context, presented)
	if err != nil {
		return TurnResult{}, err
	}
	if answer.Tool != "" {
		job, err := deps.StartJob(resolveCall(cfg, answer.Tool), answer.Args)
		if err != nil {
			return TurnResult{}, err
		}
		return TurnResult{Job: job}, nil
	}
	if answer.Silence || strings.TrimSpace(answer.Text) == "" {
		return TurnResult{}, nil
	}
	text := oneLine(answer.Text)
	if err := deps.Append("assistant", text); err != nil {
		return TurnResult{}, err
	}
	if err := deps.Publish(text); err != nil {
		return TurnResult{}, err
	}
	return TurnResult{Spoke: true, Text: text}, nil
}

// Present returns the tool surface the model sees this turn: configured tools
// only, hidden ones pruned, renamed ones relabeled. The agent never invents a
// callable the registry does not hold.
func Present(cfg Config) []Tool {
	cfg = cfg.Normalized()
	hidden := make(map[string]bool, len(cfg.Hidden))
	for _, h := range cfg.Hidden {
		hidden[h] = true
	}
	out := make([]Tool, 0, len(cfg.Tools))
	for _, t := range cfg.Tools {
		if hidden[t.Name] {
			continue
		}
		if alias, ok := cfg.Renamed[t.Name]; ok && alias != "" {
			t.Name = alias
		}
		out = append(out, t)
	}
	return out
}

// resolveCall maps a called label back to the registry name: the model
// speaks in presented labels, the job runs the registry's tool.
func resolveCall(cfg Config, called string) string {
	for name, alias := range cfg.Renamed {
		if alias == called {
			return name
		}
	}
	return called
}

// oneLine flattens embedded line breaks so a message is always one line.
func oneLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
}
