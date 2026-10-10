package bash

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// newManager declares the bash tool on a fresh manager for one test.
func newManager(t *testing.T, opts Options) *toolmanager.Manager {
	t.Helper()
	m := toolmanager.New(toolmanager.Options{})
	if err := m.Registry().Declare(Tool(opts)); err != nil {
		t.Fatalf("declare bash tool: %v", err)
	}
	t.Cleanup(m.Close)
	return m
}

// runCall starts one bash call on the manager and waits for its result.
func runCall(t *testing.T, m *toolmanager.Manager, args string) toolmanager.Status {
	t.Helper()
	return m.Start(ToolName, json.RawMessage(args), 0).Wait()
}

// resultOf asserts a done job and returns its factual Result.
func resultOf(t *testing.T, st toolmanager.Status) Result {
	t.Helper()
	if st.State != toolmanager.StateDone {
		t.Fatalf("job state = %s (error %q), want done", st.State, st.Error)
	}
	result, ok := st.Result.(Result)
	if !ok {
		t.Fatalf("result = %#v, want bash.Result", st.Result)
	}
	return result
}

// TestToolDeclaration proves the tool's public surface: the registered name,
// a description that binds factual reporting, and the call schema.
func TestToolDeclaration(t *testing.T) {
	tool := Tool(Options{})
	if tool.Name != ToolName {
		t.Fatalf("tool name = %q, want %q", tool.Name, ToolName)
	}
	if tool.Run == nil {
		t.Fatal("tool has no runner")
	}
	if !strings.Contains(tool.Description, "Report only what the process produced") {
		t.Fatalf("description does not bind factual reporting: %q", tool.Description)
	}

	var schema struct {
		Type       string   `json:"type"`
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema, &schema); err != nil {
		t.Fatalf("schema is not JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Errorf("schema type = %q, want object", schema.Type)
	}
	wantTypes := map[string]string{"command": "string", "cwd": "string", "remind_ms": "integer"}
	for name, wantType := range wantTypes {
		prop, ok := schema.Properties[name]
		if !ok {
			t.Errorf("schema missing property %q: %s", name, tool.Schema)
			continue
		}
		if prop.Type != wantType {
			t.Errorf("schema property %q type = %q, want %q", name, prop.Type, wantType)
		}
	}
	if _, hasTimeout := schema.Properties["timeout_ms"]; hasTimeout {
		t.Errorf("schema still defines timeout_ms: %s", tool.Schema)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "command" {
		t.Errorf("required = %v, want [command]", schema.Required)
	}
}

// TestShellSelection proves the call runs through bash -c when bash is
// present, and falls back to sh -c when it is not, using an injected lookup
// instead of hiding a real shell.
func TestShellSelection(t *testing.T) {
	t.Run("bash runs the command", func(t *testing.T) {
		bash, err := exec.LookPath("bash")
		if err != nil {
			t.Skipf("bash is not installed: %v", err)
		}
		m := newManager(t, Options{LookPath: func(name string) (string, error) {
			if name == "bash" {
				return bash, nil
			}
			return "", exec.ErrNotFound
		}})
		// [[ ]] is bash syntax; sh would reject it, so success proves bash.
		result := resultOf(t, runCall(t, m, `{"command":"[[ 1 == 1 ]] && printf ok-bash"}`))
		if result.Stdout != "ok-bash" {
			t.Fatalf("stdout = %q, want ok-bash", result.Stdout)
		}
	})

	t.Run("sh is the fallback when bash is absent", func(t *testing.T) {
		sh, err := exec.LookPath("sh")
		if err != nil {
			t.Skipf("sh is not installed: %v", err)
		}
		m := newManager(t, Options{LookPath: func(name string) (string, error) {
			if name == "sh" {
				return sh, nil
			}
			return "", exec.ErrNotFound
		}})
		result := resultOf(t, runCall(t, m, `{"command":"printf ok-sh"}`))
		if result.Stdout != "ok-sh" {
			t.Fatalf("stdout = %q, want ok-sh", result.Stdout)
		}
	})
}

// TestCallInheritsEnvironment proves the command runs in the core's working
// directory with the core's environment.
func TestCallInheritsEnvironment(t *testing.T) {
	t.Setenv("BASH_TOOL_INHERIT", "inherited-value")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	m := newManager(t, Options{})
	args, err := json.Marshal(Call{Command: `printf '%s\n%s' "$BASH_TOOL_INHERIT" "$(pwd)"`})
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	result := resultOf(t, runCall(t, m, string(args)))
	want := "inherited-value\n" + wd
	if result.Stdout != want {
		t.Fatalf("stdout = %q, want %q", result.Stdout, want)
	}
}

// TestCallCWOverride proves the call's cwd overrides the core's working
// directory.
func TestCallCWOverride(t *testing.T) {
	dir := t.TempDir()
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	m := newManager(t, Options{})
	args, err := json.Marshal(Call{CWD: dir, Command: "pwd -P"})
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	result := resultOf(t, runCall(t, m, string(args)))
	if got := strings.TrimSpace(result.Stdout); got != want {
		t.Fatalf("cwd = %q, want %q", got, want)
	}
}

// TestCallStreamsWhileRunning proves stdout and stderr reach peep before the
// command finishes: the command blocks on a FIFO until the test has seen
// both streams in the job's output while the job is still running.
func TestCallStreamsWhileRunning(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "release")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	m := newManager(t, Options{})
	args, err := json.Marshal(Call{Command: fmt.Sprintf(
		"printf start-out; printf start-err >&2; cat < %q > /dev/null; printf end", fifo)})
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	job := m.Start(ToolName, args, 0)

	deadline := time.Now().Add(10 * time.Second)
	var st toolmanager.Status
	for {
		st = job.Peep()
		if st.State == toolmanager.StateRunning &&
			strings.Contains(st.Output, "start-out") && strings.Contains(st.Output, "start-err") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("peep never showed both streams while running: state=%s output=%q", st.State, st.Output)
		}
		runtime.Gosched()
	}

	release, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open fifo: %v", err)
	}
	if _, err := release.WriteString("go"); err != nil {
		t.Fatalf("write fifo: %v", err)
	}
	if err := release.Close(); err != nil {
		t.Fatalf("close fifo: %v", err)
	}

	st = job.Wait()
	if st.State != toolmanager.StateDone {
		t.Fatalf("job state = %s (error %q), want done", st.State, st.Error)
	}
	if !strings.Contains(st.Output, "end") {
		t.Fatalf("output = %q, want the post-release write", st.Output)
	}
}

// TestCallResultSuccess proves a successful call's result carries the exit
// code and the split streams.
func TestCallResultSuccess(t *testing.T) {
	m := newManager(t, Options{})
	result := resultOf(t, runCall(t, m, `{"command":"printf out; printf err >&2"}`))
	if result.ExitCode != 0 || result.Stdout != "out" || result.Stderr != "err" {
		t.Fatalf("result = %+v, want exit 0, stdout out, stderr err", result)
	}
}

// TestCallNonZeroExitFails proves a non-zero exit lands the job failed with
// the captured output in the error and no invented diagnosis.
func TestCallNonZeroExitFails(t *testing.T) {
	m := newManager(t, Options{})
	st := runCall(t, m, `{"command":"printf out; printf boom >&2; exit 3"}`)
	if st.State != toolmanager.StateFailed {
		t.Fatalf("state = %s, want failed", st.State)
	}
	if !strings.HasPrefix(st.Error, "bash: exit code 3: ") {
		t.Fatalf("error = %q, want the exit code prefix", st.Error)
	}
	for _, want := range []string{"out", "boom"} {
		if !strings.Contains(st.Error, want) {
			t.Errorf("error = %q, want captured %q", st.Error, want)
		}
	}
	if !strings.Contains(st.Output, "out") || !strings.Contains(st.Output, "boom") {
		t.Fatalf("output = %q, want both captured streams", st.Output)
	}
}

// waitPidFile polls a pid file until the command has written a pid.
func waitPidFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid file %s was never written", path)
		}
		runtime.Gosched()
	}
}

// waitGone polls until the pid is no longer alive.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d is still alive", pid)
		}
		runtime.Gosched()
	}
}

// waitFor polls a job until cond holds on its status.
func waitFor(t *testing.T, job *toolmanager.Job, cond func(toolmanager.Status) bool) toolmanager.Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := job.Peep()
		if cond(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition never held: state=%s output=%q error=%q", st.State, st.Output, st.Error)
		}
		runtime.Gosched()
	}
}

// TestKillTerminatesProcessTree proves a kill reaches the running command and
// its descendants, not just the shell.
func TestKillTerminatesProcessTree(t *testing.T) {
	dir := t.TempDir()
	shellPID := filepath.Join(dir, "shell.pid")
	childPID := filepath.Join(dir, "child.pid")
	m := newManager(t, Options{})
	args, err := json.Marshal(Call{Command: fmt.Sprintf(
		"echo $$ > %q; sleep 300 & echo $! > %q; wait", shellPID, childPID)})
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	job := m.Start(ToolName, args, 0)
	shell := waitPidFile(t, shellPID)
	child := waitPidFile(t, childPID)
	if err := syscall.Kill(child, 0); err != nil {
		t.Fatalf("descendant %d is not alive before the kill: %v", child, err)
	}

	m.Kill(job)
	if st := job.Wait(); st.State != toolmanager.StateKilled {
		t.Fatalf("job state = %s, want killed", st.State)
	}
	waitGone(t, shell)
	waitGone(t, child)
}

// TestKilledCallLandsKilled proves a killed call lands killed, not done, even
// after its runner returns.
func TestKilledCallLandsKilled(t *testing.T) {
	m := newManager(t, Options{})
	job := m.Start(ToolName, json.RawMessage(`{"command":"printf ready; sleep 300"}`), 0)
	waitFor(t, job, func(st toolmanager.Status) bool {
		return st.State == toolmanager.StateRunning && strings.Contains(st.Output, "ready")
	})
	m.Kill(job)
	final := job.Wait()
	if final.State != toolmanager.StateKilled {
		t.Fatalf("job state = %s (error %q), want killed", final.State, final.Error)
	}
	if final.Result != nil {
		t.Fatalf("killed job has a result: %#v", final.Result)
	}
}

// TestCallRemindUnmarshal proves Call unmarshals remind_ms correctly.
func TestCallRemindUnmarshal(t *testing.T) {
	var c Call
	if err := json.Unmarshal([]byte(`{"command":"echo hi","remind_ms":5000}`), &c); err != nil {
		t.Fatalf("unmarshal Call: %v", err)
	}
	if c.RemindMS != 5000 {
		t.Fatalf("c.RemindMS = %d, want 5000", c.RemindMS)
	}
}

// TestCallRemindDefault proves remind_ms defaults to 60 seconds.
func TestCallRemindDefault(t *testing.T) {
	if DefaultRemind != 60*time.Second {
		t.Fatalf("DefaultRemind = %s, want 60s", DefaultRemind)
	}
	if got := callRemind(Call{}, Options{}); got != DefaultRemind {
		t.Fatalf("call remind = %s, want %s", got, DefaultRemind)
	}
	if got := callRemind(Call{RemindMS: 1500}, Options{}); got != 1500*time.Millisecond {
		t.Fatalf("call remind = %s, want 1.5s", got)
	}
	if got := callRemind(Call{}, Options{Remind: 5 * time.Second}); got != 5*time.Second {
		t.Fatalf("configured remind = %s, want 5s", got)
	}
}

// TestReminderFiresWithoutKilling proves an expired reminder bound fires the reminder
// without terminating the process or failing the job.
func TestReminderFiresWithoutKilling(t *testing.T) {
	dir := t.TempDir()
	shellPID := filepath.Join(dir, "shell.pid")
	remindFired := make(chan string, 1)
	m := newManager(t, Options{
		OnRemind: func(jobID string, elapsed time.Duration) {
			select {
			case remindFired <- jobID:
			default:
			}
		},
	})
	args, err := json.Marshal(Call{RemindMS: 50, Command: fmt.Sprintf("echo $$ > %q; sleep 300", shellPID)})
	if err != nil {
		t.Fatalf("marshal call: %v", err)
	}
	job := m.Start(ToolName, args, 0)
	shell := waitPidFile(t, shellPID)

	select {
	case gotID := <-remindFired:
		if gotID != job.ID() {
			t.Fatalf("reminded job ID = %q, want %q", gotID, job.ID())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reminder notification")
	}

	// Verify the process is still running and the job is still running.
	if err := syscall.Kill(shell, 0); err != nil {
		t.Fatalf("process %d is not running after reminder: %v", shell, err)
	}
	st := job.Peep()
	if st.State != toolmanager.StateRunning {
		t.Fatalf("job state after reminder = %s, want running", st.State)
	}

	// Now explicitly kill the job and verify it terminates cleanly.
	m.Kill(job)
	final := job.Wait()
	if final.State != toolmanager.StateKilled {
		t.Fatalf("job state after kill = %s, want killed", final.State)
	}
	waitGone(t, shell)
}

func TestBashToolExecuteReturnsJobHandleSynchronously(t *testing.T) {
	m := newManager(t, Options{})
	ctx := context.Background()

	res, err := m.Execute(ctx, ToolName, json.RawMessage(`{"command":"echo hello"}`))
	if err != nil {
		t.Fatalf("Execute bash: %v", err)
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

	// Verify the background job actually runs and finishes
	job, found := m.Job(jobID)
	if !found {
		t.Fatalf("job %s not found in manager", jobID)
	}
	st := job.Wait()
	if st.State != toolmanager.StateDone {
		t.Fatalf("job state = %s, want done", st.State)
	}
}
