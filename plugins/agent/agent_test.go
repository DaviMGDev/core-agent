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
