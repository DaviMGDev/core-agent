package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"strconv"
	"sync"
	"testing"

	"github.com/DaviMGDev/core-agent/plugins/agent"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// fakeLoop is the agent surface a test call reaches: it records every wake
// and answers from a script.
type fakeLoop struct {
	mu     sync.Mutex
	wakes  []agent.Wake
	answer func(w agent.Wake) (text string, job string, conversation []agent.Message)
}

func (f *fakeLoop) Handle(_ context.Context, req []byte) ([]byte, error) {
	var w agent.Wake
	if err := json.Unmarshal(req, &w); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.wakes = append(f.wakes, w)
	f.mu.Unlock()
	out := map[string]any{}
	if f.answer != nil {
		text, job, conv := f.answer(w)
		if text != "" {
			out["text"] = text
		}
		if job != "" {
			out["job"] = job
		}
		if conv != nil {
			out["conversation"] = conv
		}
	}
	return json.Marshal(out)
}

func (f *fakeLoop) snapshot() []agent.Wake {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.Wake(nil), f.wakes...)
}

// newManager returns a manager with the subagent tool declared over loop.
func newManager(loop Agent, opts Options) (*toolmanager.Manager, error) {
	m := toolmanager.New(toolmanager.Options{})
	return m, m.Registry().Declare(Tool(loop, m, opts))
}

func TestToolDeclaresItsSurface(t *testing.T) {
	m, err := newManager(&fakeLoop{}, Options{})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	tool, ok := m.Registry().Lookup(ToolName)
	if !ok {
		t.Fatalf("registry does not hold %q", ToolName)
	}
	if tool.Description == "" || len(tool.Schema) == 0 {
		t.Fatalf("tool = %+v, want a description and a schema", tool)
	}
}

func TestChildConfigCarriesArgumentSchemas(t *testing.T) {
	loop := &fakeLoop{answer: func(w agent.Wake) (string, string, []agent.Message) {
		return "done", "", nil
	}}
	m, err := newManager(loop, Options{})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	err = m.Registry().Declare(toolmanager.Tool{
		Name:        "read",
		Description: "read a file",
		Schema:      schema,
		Run:         func(_ context.Context, _ json.RawMessage, _ *toolmanager.Output) (any, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("Declare read: %v", err)
	}

	job := m.Start(ToolName, json.RawMessage(`{"brief":"read the file"}`), 0)
	if st := job.Wait(); st.State != toolmanager.StateDone {
		t.Fatalf("job = {%s %q}, want done", st.State, st.Error)
	}

	wakes := loop.snapshot()
	if len(wakes) != 1 || wakes[0].Config == nil {
		t.Fatalf("wakes = %+v, want one configured wake", wakes)
	}
	var found *agent.Tool
	for i, tool := range wakes[0].Config.Tools {
		if tool.Name == "read" {
			found = &wakes[0].Config.Tools[i]
		}
	}
	if found == nil {
		t.Fatalf("child tools = %+v, want the registry's read", wakes[0].Config.Tools)
	}
	if found.Description != "read a file" || string(found.Parameters) != string(schema) {
		t.Fatalf("child tool = %+v, want name, description, and the argument schema copied", *found)
	}
}

func TestBriefIsRequired(t *testing.T) {
	m, err := newManager(&fakeLoop{}, Options{})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	job := m.Start(ToolName, json.RawMessage(`{}`), 0)
	st := job.Wait()
	if st.State != toolmanager.StateFailed || st.Error == "" {
		t.Fatalf("job = {%s %q}, want failed with a reason", st.State, st.Error)
	}
}

func TestCallRunsOnItsOwnConversation(t *testing.T) {
	loop := &fakeLoop{answer: func(w agent.Wake) (string, string, []agent.Message) {
		return "child: " + w.Line, "", nil
	}}
	m, err := newManager(loop, Options{})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	for _, brief := range []string{"first", "second"} {
		job := m.Start(ToolName, json.RawMessage(`{"brief":`+strconv.Quote(brief)+`}`), 0)
		if st := job.Wait(); st.State != toolmanager.StateDone {
			t.Fatalf("job = %s (%s), want done", st.State, st.Error)
		}
	}
	wakes := loop.snapshot()
	if len(wakes) != 2 {
		t.Fatalf("wakes = %d, want 2", len(wakes))
	}
	for i, w := range wakes {
		if w.Line != []string{"first", "second"}[i] {
			t.Errorf("wake %d line = %q, want only the brief", i, w.Line)
		}
		if w.Config == nil || w.Config.Conversation == "" || w.Config.Conversation == "main" {
			t.Fatalf("wake %d config = %+v, want its own conversation", i, w.Config)
		}
	}
	if wakes[0].Config.Conversation == wakes[1].Config.Conversation {
		t.Fatalf("both calls use %q, want their own", wakes[0].Config.Conversation)
	}
}

func TestCallReturnsTheChildsReply(t *testing.T) {
	loop := &fakeLoop{answer: func(w agent.Wake) (string, string, []agent.Message) {
		return "child: " + w.Line, "", nil
	}}
	m, err := newManager(loop, Options{})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	job := m.Start(ToolName, json.RawMessage(`{"brief":"do the thing"}`), 0)
	st := job.Wait()
	if st.State != toolmanager.StateDone {
		t.Fatalf("job = %s (%s), want done", st.State, st.Error)
	}
	res, ok := st.Result.(Result)
	if !ok {
		t.Fatalf("result = %#v, want subagent.Result", st.Result)
	}
	if res.Reply != "child: do the thing" {
		t.Fatalf("reply = %q, want the child's answer", res.Reply)
	}
	if len(res.Conversation) != 0 {
		t.Fatalf("default result carries %d conversation turns, want none", len(res.Conversation))
	}
}

func TestChildConfigCarriesTheModel(t *testing.T) {
	loop := &fakeLoop{answer: func(w agent.Wake) (string, string, []agent.Message) {
		return "child: " + w.Line, "", nil
	}}
	m, err := newManager(loop, Options{Model: "reliable"})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	if st := m.Start(ToolName, json.RawMessage(`{"brief":"default"}`), 0).Wait(); st.State != toolmanager.StateDone {
		t.Fatalf("default call = %s (%s), want done", st.State, st.Error)
	}
	if st := m.Start(ToolName, json.RawMessage(`{"brief":"override","model":"fast"}`), 0).Wait(); st.State != toolmanager.StateDone {
		t.Fatalf("override call = %s (%s), want done", st.State, st.Error)
	}
	wakes := loop.snapshot()
	if len(wakes) != 2 {
		t.Fatalf("wakes = %d, want 2", len(wakes))
	}
	if wakes[0].Config == nil || wakes[0].Config.Model != "reliable" {
		t.Fatalf("default call model = %+v, want reliable", wakes[0].Config)
	}
	if wakes[1].Config == nil || wakes[1].Config.Model != "fast" {
		t.Fatalf("override call model = %+v, want fast", wakes[1].Config)
	}
}

func TestCallReturnsTheConversationWhenAsked(t *testing.T) {
	loop := &fakeLoop{answer: func(w agent.Wake) (string, string, []agent.Message) {
		if !w.Dump {
			return "child: " + w.Line, "", nil
		}
		return "child: " + w.Line, "", []agent.Message{
			{Role: "user", Text: w.Line},
			{Role: "assistant", Text: "child: " + w.Line},
		}
	}}
	m, err := newManager(loop, Options{})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	job := m.Start(ToolName, json.RawMessage(`{"brief":"do the thing","return":"conversation"}`), 0)
	st := job.Wait()
	if st.State != toolmanager.StateDone {
		t.Fatalf("job = %s (%s), want done", st.State, st.Error)
	}
	res, ok := st.Result.(Result)
	if !ok {
		t.Fatalf("result = %#v, want subagent.Result", st.Result)
	}
	if len(res.Conversation) != 2 || res.Conversation[0].Role != "user" || res.Conversation[1].Role != "assistant" {
		t.Fatalf("conversation = %+v, want the child's user and assistant turns", res.Conversation)
	}
}

func TestSubagentToolExecuteReturnsJobHandleSynchronously(t *testing.T) {
	loop := &fakeLoop{answer: func(w agent.Wake) (string, string, []agent.Message) {
		return "child: " + w.Line, "", nil
	}}
	m, err := newManager(loop, Options{})
	if err != nil {
		t.Fatalf("Declare: %v", err)
	}
	res, err := m.Execute(context.Background(), ToolName, json.RawMessage(`{"brief":"say hello"}`))
	if err != nil {
		t.Fatalf("Execute subagent: %v", err)
	}
	doc, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("res = %#v, want map[string]any", res)
	}
	if doc["status"] != "started" {
		t.Fatalf("status = %v, want 'started'", doc["status"])
	}
	jobID, ok := doc["job"].(string)
	if !ok || !strings.HasPrefix(jobID, "job-") {
		t.Fatalf("job ID = %v, want 'job-X'", doc["job"])
	}
	job, found := m.Job(jobID)
	if !found {
		t.Fatalf("job %s not found in manager", jobID)
	}
	st := job.Wait()
	if st.State != toolmanager.StateDone {
		t.Fatalf("job state = %s, want done", st.State)
	}
}
