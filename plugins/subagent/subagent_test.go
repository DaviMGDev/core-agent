package subagent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// fakeLoop is the agent surface a test call reaches: it records every wake
// and answers from a script.
type fakeLoop struct {
	mu     sync.Mutex
	wakes  []wakeRequest
	answer func(w wakeRequest) (text string, job string, conversation []json.RawMessage)
}

func (f *fakeLoop) Handle(_ context.Context, req []byte) ([]byte, error) {
	var w wakeRequest
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

func (f *fakeLoop) snapshot() []wakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]wakeRequest(nil), f.wakes...)
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

func TestCallReturnsTheChildsReply(t *testing.T) {
	loop := &fakeLoop{answer: func(w wakeRequest) (string, string, []json.RawMessage) {
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
}
