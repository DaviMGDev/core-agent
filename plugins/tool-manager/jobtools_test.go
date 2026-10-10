package tool_manager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// jobToolSet declares the job surface plus a scripted ok tool on a fresh
// manager.
func jobToolSet(t *testing.T, pub Publisher) *Manager {
	t.Helper()
	m := New(Options{Publisher: pub})
	tools := append(JobTools(m), Tool{
		Name: "ok",
		Run: func(context.Context, json.RawMessage, *Output) (any, error) {
			return "fine", nil
		},
	})
	for _, tool := range tools {
		if err := m.Registry().Declare(tool); err != nil {
			t.Fatalf("Declare(%s): %v", tool.Name, err)
		}
	}
	return m
}

// runJobTool runs one declared job tool over a caller job.
func runJobTool(t *testing.T, m *Manager, name, args string, caller *Job) (any, error) {
	t.Helper()
	tool, ok := m.Registry().Lookup(name)
	if !ok {
		t.Fatalf("tool %q is not declared", name)
	}
	var raw json.RawMessage
	if args != "" {
		raw = json.RawMessage(args)
	}
	return tool.Run(context.Background(), raw, &Output{job: caller})
}

// rootCaller is a synthetic top-level call job: no parent, so the whole
// launch is visible.
func rootCaller() *Job { return &Job{id: "call-root", state: StateRunning} }

// subCaller is a synthetic subagent call job: its parent scopes visibility.
func subCaller(parent *Job) *Job {
	return &Job{id: "call-sub", state: StateRunning, parent: parent}
}

// TestJobToolsListPeepAndKill proves the surface maps to registry
// operations: creation order in jobs, state/last tick/output in peep, and
// the new state plus the job.killed event in kill.
func TestJobToolsListPeepAndKill(t *testing.T) {
	pub := &recorder{}
	release := make(chan struct{})
	m := jobToolSet(t, pub)
	defer close(release)
	if err := m.Registry().Declare(Tool{Name: "slow", Run: func(ctx context.Context, _ json.RawMessage, out *Output) (any, error) {
		_, _ = out.Write([]byte("working\n"))
		select {
		case <-release:
			return "released", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}); err != nil {
		t.Fatalf("Declare(slow): %v", err)
	}

	first := m.Start("ok", nil, 0)
	first.Wait()
	slow := m.Start("slow", nil, 0)
	waitFor(t, "the job's output", func() bool {
		return strings.Contains(m.Peep(slow).Output, "working")
	})

	call := rootCaller()

	got, err := runJobTool(t, m, JobsToolName, "", call)
	if err != nil {
		t.Fatalf("jobs: %v", err)
	}
	rows, ok := got.([]jobRow)
	if !ok || len(rows) != 2 {
		t.Fatalf("jobs result = %#v, want two rows", got)
	}
	if rows[0].Job != first.ID() || rows[0].State != string(StateDone) {
		t.Fatalf("first row = %+v, want %s done", rows[0], first.ID())
	}
	if rows[1].Job != slow.ID() || rows[1].State != string(StateRunning) {
		t.Fatalf("second row = %+v, want %s running", rows[1], slow.ID())
	}

	got, err = runJobTool(t, m, PeepToolName, `{"job":"`+slow.ID()+`"}`, call)
	if err != nil {
		t.Fatalf("peep: %v", err)
	}
	peek, ok := got.(statusDoc)
	if !ok || peek.State != string(StateRunning) || !strings.Contains(peek.Output, "working") {
		t.Fatalf("peek = %#v, want the running job with its output", got)
	}

	got, err = runJobTool(t, m, KillToolName, `{"job":"`+slow.ID()+`"}`, call)
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	killed, ok := got.(statusDoc)
	if !ok || killed.State != string(StateKilled) {
		t.Fatalf("kill result = %#v, want the new state killed", got)
	}
	if state := slow.Wait().State; state != StateKilled {
		t.Fatalf("job state after kill = %q, want killed", state)
	}
	waitFor(t, "the job.killed event", func() bool { return pub.count("job.killed") == 1 })
}

// TestJobToolsRefuseUnknownAndOutOfScope proves a failed id yields a factual
// error, never a model narrative: unknown ids, missing ids, and jobs outside
// the caller's subtree are all refused.
func TestJobToolsRefuseUnknownAndOutOfScope(t *testing.T) {
	m := jobToolSet(t, &recorder{})
	root := m.Start("ok", nil, 0)
	root.Wait()
	child := m.start("ok", nil, 0, nil, root)
	child.Wait()
	sibling := m.Start("ok", nil, 0)
	sibling.Wait()
	call := subCaller(root)

	if _, err := runJobTool(t, m, PeepToolName, `{"job":"job-99"}`, call); err == nil || !strings.Contains(err.Error(), "unknown job") {
		t.Fatalf("unknown peep error = %v, want a factual unknown-job error", err)
	}
	if _, err := runJobTool(t, m, KillToolName, `{}`, call); err == nil || !strings.Contains(err.Error(), "job is required") {
		t.Fatalf("empty kill error = %v, want a required-job error", err)
	}
	if _, err := runJobTool(t, m, PeepToolName, `{"job":"`+sibling.ID()+`"}`, call); err == nil || !strings.Contains(err.Error(), "outside the caller's subtree") {
		t.Fatalf("out-of-scope peep error = %v, want the subtree refusal", err)
	}
}

// TestJobToolsScopeTheListing proves the tree scoping: the top-level agent
// sees the whole launch, a subagent only the subtree it runs under.
func TestJobToolsScopeTheListing(t *testing.T) {
	m := jobToolSet(t, &recorder{})
	root := m.Start("ok", nil, 0)
	root.Wait()
	child := m.start("ok", nil, 0, nil, root)
	child.Wait()
	sibling := m.Start("ok", nil, 0)
	sibling.Wait()

	got, err := runJobTool(t, m, JobsToolName, "", rootCaller())
	if err != nil {
		t.Fatalf("jobs (top level): %v", err)
	}
	if rows, ok := got.([]jobRow); !ok || len(rows) != 3 {
		t.Fatalf("top-level rows = %#v, want all three jobs", got)
	}

	got, err = runJobTool(t, m, JobsToolName, "", subCaller(root))
	if err != nil {
		t.Fatalf("jobs (subagent): %v", err)
	}
	rows, ok := got.([]jobRow)
	if !ok || len(rows) != 2 || rows[0].Job != root.ID() || rows[1].Job != child.ID() {
		t.Fatalf("subagent rows = %+v, want only %s and %s", rows, root.ID(), child.ID())
	}
}
