package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// recordingDeps captures what one turn did.
type recordingDeps struct {
	appended   []Message
	published  []string
	jobs       []string
	presented  []Tool
	responded  []Message
	answer     Answer
	answers    []Answer
	executed   []string
	useExecute bool
	executeFn  func(tool string, args json.RawMessage) (any, error)
}

func (r *recordingDeps) deps() Deps {
	d := Deps{
		Append: func(role, text string) error {
			r.appended = append(r.appended, Message{Role: role, Text: text})
			return nil
		},
		Recent: func(n int) ([]Message, error) {
			return []Message{{Role: "user", Text: "earlier"}}, nil
		},
		Project: func(msgs []Message) ([]Message, error) {
			return msgs, nil
		},
		Respond: func(text string, context []Message, tools []Tool) (Answer, error) {
			r.presented = tools
			r.responded = context
			if len(r.answers) > 0 {
				ans := r.answers[0]
				r.answers = r.answers[1:]
				return ans, nil
			}
			return r.answer, nil
		},
		StartJob: func(tool string, args json.RawMessage) (string, error) {
			r.jobs = append(r.jobs, tool)
			return "job-1", nil
		},
		Publish: func(text string) error {
			r.published = append(r.published, text)
			return nil
		},
	}
	if r.useExecute || r.executeFn != nil {
		d.Execute = func(tool string, args json.RawMessage) (any, error) {
			r.executed = append(r.executed, tool)
			if r.executeFn != nil {
				return r.executeFn(tool, args)
			}
			return map[string]any{"status": "ok"}, nil
		}
	}
	return d
}

func TestTurnSpeaksOnce(t *testing.T) {
	r := &recordingDeps{answer: Answer{Text: "hello\nthere"}}
	res, err := RunTurn(Config{}, "hi", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if !res.Spoke || res.Text != "hello\nthere" {
		t.Fatalf("result = %+v, want multiline preserved", res)
	}
	if len(r.appended) != 2 || r.appended[0].Role != "user" || r.appended[1].Role != "assistant" {
		t.Fatalf("appends = %+v, want a user and an assistant turn", r.appended)
	}
	if len(r.published) != 1 || r.published[0] != "hello\nthere" {
		t.Fatalf("published = %v, want one chat.message", r.published)
	}
}

func TestTurnSilenceSpeaksNothingOnEventWake(t *testing.T) {
	for _, answer := range []Answer{{}, {Silence: true}, {Text: "  "}} {
		r := &recordingDeps{answer: answer}
		res, RunErr := RunTurnWake(Config{}, Wake{Events: []Event{{Topic: "job.tick"}}}, r.deps())
		if RunErr != nil {
			t.Fatalf("RunTurnWake: %v", RunErr)
		}
		if res.Spoke || len(r.published) != 0 {
			t.Fatalf("answer %+v: result = %+v, published = %v; want silence", answer, res, r.published)
		}
		if len(r.appended) != 1 {
			t.Fatalf("answer %+v: appends = %+v, want the event turn only", answer, r.appended)
		}
	}
}

func TestTurnTerminalJobEventsOverrideSilence(t *testing.T) {
	cases := []struct {
		name     string
		event    Event
		expected string
	}{
		{
			name: "job.completed with output",
			event: Event{
				Topic:   "job.completed",
				Payload: json.RawMessage(`{"job":"job-1","result":{"exit_code":0,"stdout":"hello world"}}`),
			},
			expected: "Job job-1 completed with exit code 0:\nhello world",
		},
		{
			name: "job.failed with error",
			event: Event{
				Topic:   "job.failed",
				Payload: json.RawMessage(`{"job":"job-2","error":"command failed"}`),
			},
			expected: "Job job-2 failed: command failed",
		},
		{
			name: "job.killed",
			event: Event{
				Topic:   "job.killed",
				Payload: json.RawMessage(`{"job":"job-3"}`),
			},
			expected: "Job job-3 was killed.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, answer := range []Answer{{}, {Silence: true}, {Text: "   "}} {
				r := &recordingDeps{answer: answer}
				res, err := RunTurnWake(Config{}, Wake{Events: []Event{tc.event}}, r.deps())
				if err != nil {
					t.Fatalf("RunTurnWake: %v", err)
				}
				if !res.Spoke || res.Text == "" {
					t.Fatalf("answer %+v: expected spoken result, got result=%+v", answer, res)
				}
				if res.Text != tc.expected {
					t.Fatalf("res.Text = %q, want %q", res.Text, tc.expected)
				}
				if len(r.published) != 1 || r.published[0] != tc.expected {
					t.Fatalf("published = %v, want [%q]", r.published, tc.expected)
				}
				if len(r.appended) != 2 || r.appended[0].Role != "user" || r.appended[1].Role != "assistant" {
					t.Fatalf("appends = %+v, want user event and assistant response", r.appended)
				}
			}
		})
	}
}

func TestTurnUserWakeOverridesSilence(t *testing.T) {
	for _, answer := range []Answer{{}, {Silence: true}, {Text: "  "}} {
		r := &recordingDeps{answer: answer}
		res, err := RunTurn(Config{}, "hi", r.deps())
		if err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		if !res.Spoke || res.Text == "" {
			t.Fatalf("answer %+v: result = %+v; want user wake to override silence with acknowledgment", answer, res)
		}
		if len(r.published) != 1 || r.published[0] != res.Text {
			t.Fatalf("published = %v, want one chat.message with fallback acknowledgment", r.published)
		}
		if len(r.appended) != 2 || r.appended[0].Role != "user" || r.appended[1].Role != "assistant" {
			t.Fatalf("appends = %+v, want user and assistant turns", r.appended)
		}
	}
}

func TestTurnUserWakeToolResultOverridesSilence(t *testing.T) {
	r := &recordingDeps{
		answers: []Answer{
			{Tool: "peep", Args: json.RawMessage(`{"job":"job-1"}`)},
			{Silence: true},
		},
		useExecute: true,
		executeFn: func(tool string, args json.RawMessage) (any, error) {
			return map[string]any{"job": "job-1", "state": "running"}, nil
		},
	}
	res, err := RunTurn(Config{}, "call peep", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if !res.Spoke || !strings.Contains(res.Text, "running") {
		t.Fatalf("result = %+v, want tool result spoken rather than silence", res)
	}
	if len(r.published) != 1 {
		t.Fatalf("published = %v, want 1 published message", r.published)
	}
	if len(r.appended) != 2 || r.appended[0].Role != "user" || r.appended[1].Role != "assistant" {
		t.Fatalf("appends = %+v, want user and assistant turns in history", r.appended)
	}
}

func TestToolCallEndsTheTurnWithoutAMessage(t *testing.T) {
	r := &recordingDeps{answer: Answer{Tool: "read", Args: json.RawMessage(`{"path":"/x"}`)}}
	res, err := RunTurn(Config{}, "read /x", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Job != "job-1" || res.Spoke || len(r.published) != 0 {
		t.Fatalf("result = %+v, published = %v; want a job and no message", res, r.published)
	}
	if len(r.jobs) != 1 || r.jobs[0] != "read" {
		t.Fatalf("jobs = %v, want read", r.jobs)
	}
}

func TestInFlightBackgroundJobRetainedInContextAcrossUserTurns(t *testing.T) {
	var history []Message
	d := Deps{
		Append: func(role, text string) error {
			history = append(history, Message{Role: role, Text: text})
			return nil
		},
		Recent: func(n int) ([]Message, error) {
			return history, nil
		},
		Project: func(msgs []Message) ([]Message, error) {
			return msgs, nil
		},
		Publish: func(text string) error {
			return nil
		},
		Execute: func(tool string, args json.RawMessage) (any, error) {
			if tool == "bash" {
				return map[string]any{"job": "job-1", "status": "started"}, nil
			}
			if tool == "peep" {
				return map[string]any{"job": "job-1", "state": "running"}, nil
			}
			return map[string]any{"status": "ok"}, nil
		},
	}

	// Turn 1: user requests sleep 70, model launches bash tool
	d.Respond = func(text string, context []Message, tools []Tool) (Answer, error) {
		return Answer{Tool: "bash", Args: json.RawMessage(`{"command":"sleep 70"}`)}, nil
	}
	res1, err := RunTurn(Config{}, "call sleep 70", d)
	if err != nil {
		t.Fatalf("Turn 1 failed: %v", err)
	}
	if res1.Job != "job-1" {
		t.Fatalf("Turn 1 job = %q, want job-1", res1.Job)
	}

	// Verify history contains both the user prompt and the assistant job launch
	if len(history) != 2 || history[0].Role != RoleUser || history[1].Role != RoleAssistant {
		t.Fatalf("history after Turn 1 = %+v, want user prompt and assistant job launch", history)
	}
	if !strings.Contains(history[1].Text, "job-1") || !strings.Contains(history[1].Text, "bash") {
		t.Fatalf("history[1] = %s, want job-1 and bash recorded", history[1].Text)
	}

	// Turn 2: user asks to peep, context must receive Turn 1's job launch
	var turn2Context []Message
	var step int
	d.Respond = func(text string, context []Message, tools []Tool) (Answer, error) {
		turn2Context = context
		step++
		if step == 1 {
			return Answer{Tool: "peep", Args: json.RawMessage(`{"job":"job-1"}`)}, nil
		}
		return Answer{Text: "job-1 is still running"}, nil
	}
	res2, err := RunTurn(Config{}, "call peep", d)
	if err != nil {
		t.Fatalf("Turn 2 failed: %v", err)
	}
	if !res2.Spoke || !strings.Contains(res2.Text, "job-1 is still running") {
		t.Fatalf("Turn 2 result = %+v, want job-1 response", res2)
	}

	// Verify that Turn 2 context included Turn 1's assistant job launch
	foundJobInTurn2 := false
	for _, m := range turn2Context {
		if m.Role == RoleAssistant && strings.Contains(m.Text, "job-1") {
			foundJobInTurn2 = true
			break
		}
	}
	if !foundJobInTurn2 {
		t.Fatalf("turn2Context did not contain Turn 1's assistant job launch: %+v", turn2Context)
	}
}

func TestRenamedCallMapsBackToTheRegistryName(t *testing.T) {
	r := &recordingDeps{answer: Answer{Tool: "delegate", Args: json.RawMessage(`{"brief":"sum"}`)}}
	cfg := Config{
		Tools:   []Tool{{Name: "subagent"}},
		Renamed: map[string]string{"subagent": "delegate"},
	}
	res, err := RunTurn(cfg, "delegate this", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Job != "job-1" || len(r.jobs) != 1 || r.jobs[0] != "subagent" {
		t.Fatalf("result = %+v, jobs = %v; want one job for the registry name subagent", res, r.jobs)
	}
}

func TestRespondReceivesThePresentedSurface(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"brief":{"type":"string"}},"required":["brief"]}`)
	r := &recordingDeps{answer: Answer{Text: "done"}}
	cfg := Config{
		Tools: []Tool{
			{Name: "subagent", Description: "call an agent", Parameters: schema},
			{Name: "bash"},
		},
		Hidden:  []string{"bash"},
		Renamed: map[string]string{"subagent": "delegate"},
	}
	if _, err := RunTurn(cfg, "hi", r.deps()); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(r.presented) != 1 {
		t.Fatalf("presented = %+v, want the one visible tool", r.presented)
	}
	tool := r.presented[0]
	if tool.Name != "delegate" || tool.Description != "call an agent" {
		t.Fatalf("presented = %+v, want the renamed label and its description", tool)
	}
	if string(tool.Parameters) != string(schema) {
		t.Fatalf("presented parameters = %s, want the argument schema", tool.Parameters)
	}
}

func TestSystemMessageNamesExactlyThePresentedTools(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"brief":{"type":"string"}},"required":["brief"]}`)
	r := &recordingDeps{answer: Answer{Text: "done"}}
	cfg := Config{
		Tools: []Tool{
			{Name: "subagent", Description: "call an agent", Parameters: schema},
			{Name: "bash", Description: "run a command"},
			{Name: "jobs", Description: "list jobs"},
		},
		Hidden:  []string{"bash"},
		Renamed: map[string]string{"subagent": "delegate"},
	}
	if _, err := RunTurn(cfg, "hi", r.deps()); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(r.responded) == 0 {
		t.Fatal("no context reached the model")
	}
	system := 0
	for _, m := range r.responded {
		if m.Role == SystemRole {
			system++
		}
	}
	if system != 1 {
		t.Fatalf("context carries %d system messages, want exactly one", system)
	}
	first := r.responded[0]
	if first.Role != SystemRole {
		t.Fatalf("first context message has role %q, want the system message first", first.Role)
	}
	for _, name := range []string{"delegate", "jobs"} {
		if !strings.Contains(first.Text, name) {
			t.Errorf("system message does not name presented tool %q:\n%s", name, first.Text)
		}
	}
	for _, name := range []string{"bash", "subagent"} {
		if strings.Contains(first.Text, name) {
			t.Errorf("system message names %q, which is hidden or pre-rename:\n%s", name, first.Text)
		}
	}
	for _, area := range []string{"capabilities", "silence", "factually", "delegated"} {
		if !strings.Contains(first.Text, area) {
			t.Errorf("system message misses the %q policy:\n%s", area, first.Text)
		}
	}
}

func TestSystemMessageInstructsSilenceOnJobTicksAndNoDuplicates(t *testing.T) {
	msg := SystemMessage(Config{})
	for _, phrase := range []string{
		"unprompted job tick",
		"silence ({})",
		"never re-issue an identical command",
	} {
		if !strings.Contains(msg.Text, phrase) {
			t.Errorf("SystemMessage missing %q:\n%s", phrase, msg.Text)
		}
	}
}

func TestSystemMessageInstructsReportingOnTerminalJobEvents(t *testing.T) {
	msg := SystemMessage(Config{})
	for _, phrase := range []string{
		"terminal job events",
		"job.completed",
		"proactively report",
		"NEVER answer with silence",
	} {
		if !strings.Contains(msg.Text, phrase) {
			t.Errorf("SystemMessage missing %q:\n%s", phrase, msg.Text)
		}
	}
}

func TestParseAnswer(t *testing.T) {
	if a := ParseAnswer(`{"tool":"read"}`); a.Tool != "read" {
		t.Fatalf("directive = %+v, want tool read", a)
	}
	if a := ParseAnswer(`{"silence":true}`); !a.Silence {
		t.Fatalf("directive = %+v, want silence", a)
	}
	if a := ParseAnswer(`hello`); a.Text != "hello" || a.Tool != "" {
		t.Fatalf("speech = %+v, want text hello", a)
	}
	if a := ParseAnswer(`{"text":"hello"}`); a.Text != `{"text":"hello"}` {
		t.Fatalf("a JSON object without a directive stays speech: %+v", a)
	}
}

func TestParseAnswerExtractsDirectiveWithSurroundingSpeechAndExtraKeys(t *testing.T) {
	// Mixed tool directive with extra keys and trailing conversational text
	raw := `{"args":{"command":"sleep 10"},"job":"job-2","tool":"bash"}` + "\n\n" +
		`Hello! I've started both sleep commands for you. Is there anything else you need?`

	a := ParseAnswer(raw)
	if a.Tool != "bash" {
		t.Fatalf("Tool = %q, want bash", a.Tool)
	}
	if !strings.Contains(string(a.Args), "sleep 10") {
		t.Fatalf("Args = %s, want sleep 10", string(a.Args))
	}
}

func TestTurnExecutesExtractedDirectiveFromMixedSpeech(t *testing.T) {
	mixed := `{"args":{"command":"sleep 10"},"job":"job-2","tool":"bash"}` + "\n\n" +
		`Hello! I've started both sleep commands for you.`

	r := &recordingDeps{
		answer:     Answer{Text: mixed},
		useExecute: true,
		executeFn: func(tool string, args json.RawMessage) (any, error) {
			return map[string]any{"job": "job-2", "status": "started"}, nil
		},
	}
	res, err := RunTurn(Config{}, "hi", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Job != "job-2" {
		t.Fatalf("res.Job = %q, want job-2", res.Job)
	}
	if res.Spoke || len(r.published) != 0 {
		t.Fatalf("tool call leaked as speech: res=%+v, published=%v", res, r.published)
	}
	if len(r.executed) != 1 || r.executed[0] != "bash" {
		t.Fatalf("executed = %v, want bash", r.executed)
	}
}

func TestConversationalJSONIsNotHijackedAsDirective(t *testing.T) {
	cases := []string{
		`Here is a tool configuration: {"tool":"read","args":{"path":"/tmp"}}`,
		`To silence the agent, output {"silence": true} in your response.`,
		"```json\n{\"tool\": \"bash\", \"args\": {\"command\": \"ls\"}}\n```",
		`{"tool": "custom-tool", "description": "A tool configuration object", "enabled": true}`,
		`{"text": "I can run the tool for you", "tool": "read"}`,
		`{"silence": true, "note": "explaining silence syntax"}`,
	}
	for _, text := range cases {
		ans := ParseAnswer(text)
		if ans.Tool != "" || ans.Silence {
			t.Fatalf("ParseAnswer(%q) was hijacked as directive: %+v", text, ans)
		}
		if ans.Text != text {
			t.Fatalf("ParseAnswer(%q).Text = %q, want original text preserved", text, ans.Text)
		}
	}
}

func TestPresentNeverInventsAndProjects(t *testing.T) {
	cfg := Config{
		Tools:   []Tool{{Name: "read"}, {Name: "write"}, {Name: "bash"}},
		Hidden:  []string{"bash"},
		Renamed: map[string]string{"write": "store"},
	}
	got := Present(cfg)
	if len(got) != 2 || got[0].Name != "read" || got[1].Name != "store" {
		t.Fatalf("presented = %+v, want read and store only", got)
	}
	for _, tool := range got {
		if strings.HasPrefix(tool.Name, "invented") {
			t.Fatalf("presented = %+v, must never invent tools", got)
		}
	}
}

func TestPresentCarriesParametersThroughHideAndRename(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"brief":{"type":"string"}},"required":["brief"]}`)
	cfg := Config{
		Tools: []Tool{
			{Name: "subagent", Description: "call an agent", Parameters: schema},
			{Name: "read", Description: "read a file", Parameters: json.RawMessage(`{"type":"object"}`)},
			{Name: "bash", Parameters: json.RawMessage(`{"type":"object"}`)},
		},
		Hidden:  []string{"bash"},
		Renamed: map[string]string{"subagent": "delegate"},
	}
	got := Present(cfg)
	if len(got) != 2 || got[0].Name != "delegate" || got[1].Name != "read" {
		t.Fatalf("presented = %+v, want delegate and read only", got)
	}
	if string(got[0].Parameters) != string(schema) {
		t.Fatalf("renamed tool parameters = %s, want the schema traveling with the label", got[0].Parameters)
	}
	if string(got[1].Parameters) != `{"type":"object"}` {
		t.Fatalf("kept tool parameters = %s, want them unchanged", got[1].Parameters)
	}
}

func TestTurnExecutesToolSynchronouslyInTurn(t *testing.T) {
	r := &recordingDeps{
		answers: []Answer{
			{Tool: "calc", Args: json.RawMessage(`{"a":2,"b":3}`)},
			{Text: "the result is 5"},
		},
		executeFn: func(tool string, args json.RawMessage) (any, error) {
			if tool != "calc" {
				t.Fatalf("execute tool = %q, want calc", tool)
			}
			return map[string]int{"sum": 5}, nil
		},
	}
	res, err := RunTurn(Config{}, "calculate 2+3", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	// Proves turn was not yielded early as a job
	if res.Job != "" {
		t.Fatalf("res.Job = %q, want empty", res.Job)
	}
	if !res.Spoke || res.Text != "the result is 5" {
		t.Fatalf("res = %+v, want spoke 'the result is 5'", res)
	}
	if len(r.executed) != 1 || r.executed[0] != "calc" {
		t.Fatalf("executed tools = %v, want ['calc']", r.executed)
	}
	if len(r.published) != 1 || r.published[0] != "the result is 5" {
		t.Fatalf("published = %v, want ['the result is 5']", r.published)
	}
	// Verify final assistant speech landed in appends
	if len(r.appended) != 2 || r.appended[1].Role != "assistant" || r.appended[1].Text != "the result is 5" {
		t.Fatalf("appended = %+v, want user and assistant final speech", r.appended)
	}
	// Verify context seen on second prompt contained the tool call and tool result
	if len(r.responded) < 3 {
		t.Fatalf("responded context length = %d, want at least 3", len(r.responded))
	}
	lastContext := r.responded[len(r.responded)-1]
	if lastContext.Role != RoleTool || !strings.Contains(lastContext.Text, `"sum":5`) {
		t.Fatalf("last context item = %+v, want tool result with RoleTool containing sum:5", lastContext)
	}
}

func TestTurnExecutesMultipleToolsInSameTurn(t *testing.T) {
	r := &recordingDeps{
		answers: []Answer{
			{Tool: "step1", Args: json.RawMessage(`{"val":1}`)},
			{Tool: "step2", Args: json.RawMessage(`{"val":2}`)},
			{Text: "both finished"},
		},
		executeFn: func(tool string, args json.RawMessage) (any, error) {
			return map[string]string{"tool": tool, "status": "done"}, nil
		},
	}
	res, err := RunTurn(Config{}, "start sequence", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if !res.Spoke || res.Text != "both finished" {
		t.Fatalf("res = %+v, want spoke 'both finished'", res)
	}
	if len(r.executed) != 2 || r.executed[0] != "step1" || r.executed[1] != "step2" {
		t.Fatalf("executed tools = %v, want ['step1', 'step2']", r.executed)
	}
	if len(r.published) != 1 || r.published[0] != "both finished" {
		t.Fatalf("published = %v, want ['both finished']", r.published)
	}
}

func TestTurnAppendsToolRoleAndCallID(t *testing.T) {
	r := &recordingDeps{
		answers: []Answer{
			{Tool: "lookup", Args: json.RawMessage(`{"q":"status"}`), ToolCallID: "call_abc123"},
			{Text: "status is active"},
		},
		executeFn: func(tool string, args json.RawMessage) (any, error) {
			return map[string]string{"result": "active"}, nil
		},
	}
	_, err := RunTurn(Config{}, "check status", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(r.responded) < 3 {
		t.Fatalf("responded context length = %d, want >= 3", len(r.responded))
	}
	assistantMsg := r.responded[len(r.responded)-2]
	if assistantMsg.Role != RoleAssistant || assistantMsg.ToolCallID != "call_abc123" {
		t.Fatalf("assistantMsg = %+v, want role=assistant tool_call_id=call_abc123", assistantMsg)
	}
	toolMsg := r.responded[len(r.responded)-1]
	if toolMsg.Role != RoleTool || toolMsg.ToolCallID != "call_abc123" {
		t.Fatalf("toolMsg = %+v, want role=tool tool_call_id=call_abc123", toolMsg)
	}
}

func TestMessageSupportsToolRoleAndCallID(t *testing.T) {
	m := Message{
		Role:       RoleTool,
		Text:       `{"status":"ok"}`,
		ToolCallID: "call_123",
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var unmarshaled Message
	if err := json.Unmarshal(b, &unmarshaled); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if unmarshaled.Role != RoleTool || unmarshaled.ToolCallID != "call_123" {
		t.Fatalf("unmarshaled = %+v, want role=%q tool_call_id=%q", unmarshaled, RoleTool, "call_123")
	}

	// Optional tool_call_id is omitted when empty
	m2 := Message{Role: RoleUser, Text: "hi"}
	b2, err := json.Marshal(m2)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b2), "tool_call_id") {
		t.Fatalf("b2 = %s, want tool_call_id omitted", string(b2))
	}
}

func TestFreezeJobsExecutesToolsSynchronouslyWithoutSpawningJob(t *testing.T) {
	r := &recordingDeps{
		answers: []Answer{
			{Tool: "runner", Args: json.RawMessage(`{"cmd":"ls"}`)},
			{Text: "command output received"},
		},
		executeFn: func(tool string, args json.RawMessage) (any, error) {
			// Even if tool returns job-like document, FreezeJobs ensures it is treated as a normal inline tool result
			return map[string]any{"status": "started", "job": "job-999", "stdout": "file1.txt"}, nil
		},
	}
	cfg := Config{FreezeJobs: true}
	res, err := RunTurn(cfg, "run command", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Job != "" {
		t.Fatalf("res.Job = %q, want empty when FreezeJobs is true", res.Job)
	}
	if !res.Spoke || res.Text != "command output received" {
		t.Fatalf("res = %+v, want Spoke=true Text='command output received'", res)
	}
	if len(r.executed) != 1 || r.executed[0] != "runner" {
		t.Fatalf("executed = %v, want ['runner']", r.executed)
	}
}

func TestFreezeJobsRejectsStartJobFallback(t *testing.T) {
	deps := Deps{
		Append: func(role, text string) error { return nil },
		Recent: func(n int) ([]Message, error) { return nil, nil },
		Project: func(msgs []Message) ([]Message, error) { return msgs, nil },
		Respond: func(text string, context []Message, tools []Tool) (Answer, error) {
			return Answer{Tool: "any"}, nil
		},
		StartJob: func(tool string, args json.RawMessage) (string, error) {
			return "job-1", nil
		},
	}
	cfg := Config{FreezeJobs: true}
	_, err := RunTurn(cfg, "do something", deps)
	if err == nil {
		t.Fatalf("RunTurn with FreezeJobs=true and only StartJob should return error")
	}
	if !strings.Contains(err.Error(), "background jobs are frozen") {
		t.Fatalf("err = %q, want 'background jobs are frozen'", err.Error())
	}
}

func TestFreezeJobsSystemPromptOmitsJobTickInstructions(t *testing.T) {
	cfg := Config{FreezeJobs: true}
	sys := SystemMessage(cfg)
	if strings.Contains(sys.Text, "job tick events") {
		t.Fatalf("system message when FreezeJobs=true contains job tick events: %s", sys.Text)
	}
	if strings.Contains(sys.Text, "peep or jobs tools") {
		t.Fatalf("system message when FreezeJobs=true contains peep or jobs tools: %s", sys.Text)
	}
	if !strings.Contains(sys.Text, "Tools execute synchronously and blocking within your turn") {
		t.Fatalf("system message when FreezeJobs=true missing synchronous execution instructions: %s", sys.Text)
	}
}

func TestFreezeJobsHidesJobToolsFromPresentedSurface(t *testing.T) {
	cfg := Config{
		FreezeJobs: true,
		Tools: []Tool{
			{Name: "bash", Description: "run a command"},
			{Name: "jobs", Description: "list jobs"},
			{Name: "peep", Description: "peep job"},
			{Name: "kill", Description: "kill job"},
		},
	}
	presented := Present(cfg)
	if len(presented) != 1 || presented[0].Name != "bash" {
		t.Fatalf("Present() = %+v, want only bash (jobs, peep, kill hidden)", presented)
	}

	sys := SystemMessage(cfg)
	if strings.Contains(sys.Text, "jobs:") || strings.Contains(sys.Text, "peep:") || strings.Contains(sys.Text, "kill:") {
		t.Fatalf("SystemMessage contains job tools: %s", sys.Text)
	}
}

func TestFreezeJobsRejectsCallsToJobToolsAsUnknown(t *testing.T) {
	r := &recordingDeps{
		answers: []Answer{
			{Tool: "peep", Args: json.RawMessage(`{"job":"job-1"}`)},
			{Text: "handled rejection"},
		},
	}
	cfg := Config{
		FreezeJobs: true,
		Tools: []Tool{
			{Name: "bash", Description: "run a command"},
		},
	}
	res, err := RunTurn(cfg, "check job", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if res.Text != "handled rejection" {
		t.Fatalf("res.Text = %q, want 'handled rejection'", res.Text)
	}
	var toolResult string
	for _, m := range r.responded {
		if m.Role == RoleTool {
			toolResult = m.Text
		}
	}
	if !strings.Contains(toolResult, `Error: unknown tool "peep"`) {
		t.Fatalf("toolResult = %q, want 'Error: unknown tool \"peep\"'", toolResult)
	}
}



