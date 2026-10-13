// Package agent implements the agent's turn loop, split out of repl-chat
// (system D14, charter binding): a turn runs from wake to quiescence, the
// model may answer, start a job, or say nothing, and a turn yields at most
// one chat.message.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
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
	// FreezeJobs pauses the background job subsystem, forcing tools to execute
	// synchronously within the turn and preventing early turn exits on jobs.
	FreezeJobs   bool              `json:"freeze_jobs,omitempty"`
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
	Role       string `json:"role,omitempty"`
	Text       string `json:"text"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// Conversation roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

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
	Text       string          `json:"text,omitempty"`
	Tool       string          `json:"tool,omitempty"`
	Args       json.RawMessage `json:"args,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Silence    bool            `json:"silence,omitempty"`
}

// ParseAnswer interprets one model answer: a JSON directive carrying a tool call
// or silence is recognized and parsed; anything else (including assistant speech
// that embeds conversational fields or discusses JSON) is treated as speech.
func ParseAnswer(text string) Answer {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") {
		if a, ok := parseLeadingDirective(trimmed); ok {
			return a
		}
	}
	return Answer{Text: text}
}

func parseLeadingDirective(s string) (Answer, bool) {
	inString := false
	escaped := false
	depth := 0
	end := -1

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}

		if c == '"' {
			inString = true
			continue
		}

		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
	}

	if end == -1 {
		return Answer{}, false
	}

	block := s[:end+1]
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(block), &raw); err != nil {
		return Answer{}, false
	}

	// Conversational JSON fields signal speech, not a directive
	if _, hasText := raw["text"]; hasText {
		return Answer{}, false
	}
	if _, hasContent := raw["content"]; hasContent {
		return Answer{}, false
	}
	if _, hasMessage := raw["message"]; hasMessage {
		return Answer{}, false
	}
	if _, hasDesc := raw["description"]; hasDesc {
		return Answer{}, false
	}
	if _, hasNote := raw["note"]; hasNote {
		return Answer{}, false
	}

	// Empty object {} represents silence only when whole message is empty/whitespace
	if len(raw) == 0 {
		if strings.TrimSpace(s[end+1:]) == "" {
			return Answer{Silence: true}, true
		}
		return Answer{}, false
	}

	// Silence directive
	if sRaw, hasSilence := raw["silence"]; hasSilence {
		var isSilent bool
		if err := json.Unmarshal(sRaw, &isSilent); err == nil && isSilent {
			if strings.TrimSpace(s[end+1:]) == "" {
				return Answer{Silence: true}, true
			}
		}
	}

	// Tool directive
	if tRaw, hasTool := raw["tool"]; hasTool {
		var toolName string
		if err := json.Unmarshal(tRaw, &toolName); err == nil && strings.TrimSpace(toolName) != "" {
			a := Answer{Tool: strings.TrimSpace(toolName)}
			if aRaw, hasArgs := raw["args"]; hasArgs && json.Valid(aRaw) {
				a.Args = aRaw
			}
			if idRaw, hasID := raw["tool_call_id"]; hasID {
				_ = json.Unmarshal(idRaw, &a.ToolCallID)
			}
			return a, true
		}
	}

	return Answer{}, false
}

// Deps are the operations one turn reaches: the composed system's pipeline
// and the host-side job surface.
type Deps struct {
	Append   func(role, text string) error
	Recent   func(n int) ([]Message, error)
	Project  func(msgs []Message) ([]Message, error)
	Respond  func(text string, context []Message, tools []Tool) (Answer, error)
	Execute  func(tool string, args json.RawMessage) (any, error)
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
	if cfg.FreezeJobs {
		b.WriteString("Tools execute synchronously and blocking within your turn. To call a tool, emit a single JSON directive {\"tool\": \"<name>\", \"args\": {...}} with no surrounding commentary; inspect tool results and continue until producing your answer. User lines must always receive a response and never silence.\n")
		b.WriteString("Report tool failures factually — tool, state, and error as observed — without inventing a diagnosis.")
		return Message{Role: SystemRole, Text: b.String()}
	}
	b.WriteString("Wakes arrive for user lines and background job events (ticks, completions, failures, kills), batched by the notification clock; unprompted job tick events indicate work is actively progressing in the background — you MUST answer unprompted job ticks with silence ({}); never re-issue an identical command while a job is running; user lines must always receive a response and never silence; on terminal job events (job.completed, job.failed, job.killed), you MUST proactively report the job outcome, status, and output to the user and NEVER answer with silence; only non-actionable intermediate background ticks or background events that need no action answer with silence — silence ends the turn with no message.\n")
	b.WriteString("To call a tool, emit a single JSON directive {\"tool\": \"<name>\", \"args\": {...}} with no surrounding commentary; execute multiple commands one by one across turns; never invent job IDs, fake statuses, or simulated execution outputs — query active jobs using the peep or jobs tools.\n")
	b.WriteString("Report tool failures factually — tool, state, and error as observed — without inventing a diagnosis.")
	return Message{Role: SystemRole, Text: b.String()}
}

// DescribeEvents flattens a bus wake into the line the model sees.
func DescribeEvents(events []Event) string {
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

// RunTurn runs one turn from wake to quiescence for an interactive user line.
func RunTurn(cfg Config, line string, deps Deps) (TurnResult, error) {
	return RunTurnWake(cfg, Wake{Line: line}, deps)
}

// RunTurnWake runs one turn from wake to quiescence: append the turn, project the
// agent's own context, ask the model with the presented tool surface under
// the assembled system message, and act on the answer. Synchronous tool calls
// execute in-turn and update context until the model speaks or goes silent.
// A tool call that falls back to StartJob ends the turn without a message;
// the job's completion or a tick wakes the next turn. For autonomous event
// wakes (wake.Line == "" with Events), silence ends the turn without a message.
// For interactive user turns (wake.Line != ""), silence is rejected/overridden
// with the tool result or a fallback acknowledgment so user turns are never
// silently dropped. The system message is assembled per turn — it travels with
// the request, never into the record.
func RunTurnWake(cfg Config, wake Wake, deps Deps) (TurnResult, error) {
	cfg = cfg.Normalized()
	isUserTurn := wake.Line != ""
	line := wake.Line
	if line == "" {
		line = DescribeEvents(wake.Events)
	}
	if line == "" {
		return TurnResult{}, nil
	}
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

	const maxSteps = 10
	var lastToolResult string
	for step := 0; step < maxSteps; step++ {
		answer, err := deps.Respond(line, context, presented)
		if err != nil {
			if lastToolResult != "" && lastToolResult != "{}" {
				answer = Answer{Text: lastToolResult}
			} else {
				return TurnResult{}, err
			}
		}
		if answer.Tool == "" && !answer.Silence && answer.Text != "" {
			if parsed := ParseAnswer(answer.Text); parsed.Tool != "" || parsed.Silence {
				answer = parsed
			}
		}
		if answer.Tool != "" {
			if deps.Execute != nil {
				targetTool := resolveCall(cfg, answer.Tool)
				res, execErr := deps.Execute(targetTool, answer.Args)
				if !cfg.FreezeJobs && execErr == nil {
					if doc, ok := res.(map[string]any); ok && (doc["status"] == "started" || doc["status"] == "running") && doc["job"] != nil {
						jobID, _ := doc["job"].(string)
						callText := formatToolCallWithJob(answer.Tool, answer.Args, jobID)
						_ = deps.Append("assistant", callText)
						return TurnResult{Job: jobID}, nil
					}
				}
				callText := formatToolCall(answer.Tool, answer.Args)
				resText := formatToolResult(res, execErr)
				lastToolResult = resText
				context = append(context, Message{Role: RoleAssistant, Text: callText, ToolCallID: answer.ToolCallID})
				context = append(context, Message{Role: RoleTool, Text: resText, ToolCallID: answer.ToolCallID})
				continue
			}
			if !cfg.FreezeJobs && deps.StartJob != nil {
				job, err := deps.StartJob(resolveCall(cfg, answer.Tool), answer.Args)
				if err != nil {
					return TurnResult{}, err
				}
				callText := formatToolCallWithJob(answer.Tool, answer.Args, job)
				_ = deps.Append("assistant", callText)
				return TurnResult{Job: job}, nil
			}
			if cfg.FreezeJobs {
				return TurnResult{}, errors.New("agent: background jobs are frozen; synchronous tool execution (deps.Execute) is required")
			}
			return TurnResult{}, errors.New("agent: no execution path configured for tool call")
		}
		if answer.Silence || strings.TrimSpace(answer.Text) == "" {
			if !isUserTurn && !hasTerminalJobEvent(wake.Events) {
				return TurnResult{}, nil
			}
			if lastToolResult != "" && lastToolResult != "{}" {
				answer.Text = lastToolResult
			} else if !isUserTurn && hasTerminalJobEvent(wake.Events) {
				answer.Text = fallbackJobEventReport(wake.Events)
			} else {
				answer.Text = "Acknowledged. Work is in progress."
			}
		}
		text := strings.TrimSpace(answer.Text)
		text = strings.ReplaceAll(text, "\r\n", "\n")
		if err := deps.Append("assistant", text); err != nil {
			return TurnResult{}, err
		}
		if err := deps.Publish(text); err != nil {
			return TurnResult{}, err
		}
		return TurnResult{Spoke: true, Text: text}, nil
	}
	return TurnResult{}, errors.New("agent: exceeded maximum tool iterations in turn")
}

func formatToolCall(tool string, args json.RawMessage) string {
	return formatToolCallWithJob(tool, args, "")
}

func formatToolCallWithJob(tool string, args json.RawMessage, jobID string) string {
	doc := map[string]any{"tool": tool}
	if len(args) > 0 {
		doc["args"] = args
	}
	if jobID != "" {
		doc["job"] = jobID
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return fmt.Sprintf(`{"tool":%q}`, tool)
	}
	return string(b)
}

func formatToolResult(res any, err error) string {
	if err != nil {
		doc := map[string]any{"error": err.Error()}
		b, _ := json.Marshal(doc)
		return string(b)
	}
	if res == nil {
		return "{}"
	}
	b, jsonErr := json.Marshal(res)
	if jsonErr != nil {
		return fmt.Sprint(res)
	}
	return string(b)
}

func hasTerminalJobEvent(events []Event) bool {
	for _, e := range events {
		switch e.Topic {
		case "job.completed", "job.failed", "job.killed":
			return true
		}
	}
	return false
}

func fallbackJobEventReport(events []Event) string {
	var parts []string
	for _, e := range events {
		switch e.Topic {
		case "job.completed":
			var doc struct {
				Job    string `json:"job"`
				Result any    `json:"result"`
			}
			if err := json.Unmarshal(e.Payload, &doc); err == nil && doc.Job != "" {
				if m, ok := doc.Result.(map[string]any); ok {
					msg := fmt.Sprintf("Job %s completed", doc.Job)
					if ec, hasEC := m["exit_code"]; hasEC {
						msg += fmt.Sprintf(" with exit code %v", ec)
					}
					if out, ok := m["stdout"].(string); ok && strings.TrimSpace(out) != "" {
						msg += fmt.Sprintf(":\n%s", strings.TrimSpace(out))
					} else if errStr, ok := m["stderr"].(string); ok && strings.TrimSpace(errStr) != "" {
						msg += fmt.Sprintf(":\n%s", strings.TrimSpace(errStr))
					}
					parts = append(parts, msg)
					continue
				} else if s, ok := doc.Result.(string); ok && s != "" {
					parts = append(parts, fmt.Sprintf("Job %s completed: %s", doc.Job, s))
					continue
				}
				parts = append(parts, fmt.Sprintf("Job %s completed.", doc.Job))
			} else {
				parts = append(parts, "Background job completed.")
			}
		case "job.failed":
			var doc struct {
				Job   string `json:"job"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(e.Payload, &doc); err == nil && doc.Job != "" {
				if doc.Error != "" {
					parts = append(parts, fmt.Sprintf("Job %s failed: %s", doc.Job, doc.Error))
				} else {
					parts = append(parts, fmt.Sprintf("Job %s failed.", doc.Job))
				}
			} else {
				parts = append(parts, "Background job failed.")
			}
		case "job.killed":
			var doc struct {
				Job string `json:"job"`
			}
			if err := json.Unmarshal(e.Payload, &doc); err == nil && doc.Job != "" {
				parts = append(parts, fmt.Sprintf("Job %s was killed.", doc.Job))
			} else {
				parts = append(parts, "Background job was killed.")
			}
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}
	return "Background job finished."
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
