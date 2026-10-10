package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// recordingDeps captures what one turn did.
type recordingDeps struct {
	appended  []Message
	published []string
	jobs      []string
	presented []Tool
	responded []Message
	answer    Answer
}

func (r *recordingDeps) deps() Deps {
	return Deps{
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
}

func TestTurnSpeaksOnce(t *testing.T) {
	r := &recordingDeps{answer: Answer{Text: "hello\nthere"}}
	res, err := RunTurn(Config{}, "hi", r.deps())
	if err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if !res.Spoke || res.Text != "hello there" {
		t.Fatalf("result = %+v, want one flattened message", res)
	}
	if len(r.appended) != 2 || r.appended[0].Role != "user" || r.appended[1].Role != "assistant" {
		t.Fatalf("appends = %+v, want a user and an assistant turn", r.appended)
	}
	if len(r.published) != 1 || r.published[0] != "hello there" {
		t.Fatalf("published = %v, want one chat.message", r.published)
	}
}

func TestTurnSilenceSpeaksNothing(t *testing.T) {
	for _, answer := range []Answer{{}, {Silence: true}, {Text: "  "}} {
		r := &recordingDeps{answer: answer}
		res, err := RunTurn(Config{}, "hi", r.deps())
		if err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
		if res.Spoke || len(r.published) != 0 {
			t.Fatalf("answer %+v: result = %+v, published = %v; want silence", answer, res, r.published)
		}
		if len(r.appended) != 1 {
			t.Fatalf("answer %+v: appends = %+v, want the user turn only", answer, r.appended)
		}
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
