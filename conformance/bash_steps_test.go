package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cucumber/godog"

	"github.com/DaviMGDev/core-agent/plugins/bash"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// registerBashSteps wires the bash feature scenarios to the real tool.
func registerBashSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a bash tool on a manager$`, stepBashTool)
	sc.Step(`^a bash tool resolving "([^"]*)"$`, stepBashToolResolving)
	sc.Step(`^a bash tool resolving only "([^"]*)"$`, stepBashToolResolving)
	sc.Step(`^bash runs "([^"]*)"$`, stepBashRuns)
	sc.Step(`^bash runs a command that blocks after writing "([^"]*)"$`, stepBashRunsBlocking)
	sc.Step(`^peep shows "([^"]*)" while the bash job is running$`, stepBashPeepWhileRunning)
	sc.Step(`^the blocked command is released$`, stepBashRelease)
	sc.Step(`^the bash job reaches done$`, stepBashJobDone)
	sc.Step(`^the bash result carries exit code (\d+), stdout "([^"]*)", and stderr "([^"]*)"$`, stepBashResultAll)
	sc.Step(`^the bash result carries stdout "([^"]*)"$`, stepBashResultStdout)
	sc.Step(`^the bash job reaches failed$`, stepBashJobFailed)
	sc.Step(`^the bash error carries "([^"]*)" and "([^"]*)"$`, stepBashErrorCarries)
	sc.Step(`^bash runs a command that spawns a descendant and waits$`, stepBashRunsDescendant)
	sc.Step(`^the bash job is killed$`, stepBashJobKilled)
	sc.Step(`^the bash job is killed at once$`, stepBashJobKilledAtOnce)
	sc.Step(`^the spawned descendant is gone$`, stepBashDescendantGone)
	sc.Step(`^bash runs a command that hangs with remind_ms (\d+)$`, stepBashRunsRemind)
	sc.Step(`^the reminder fires while the bash job stays running$`, stepBashReminderFiresWhileRunning)
	sc.Step(`^the command's process is still running$`, stepBashProcessStillRunning)
	sc.Step(`^the command's process is gone$`, stepBashProcessGone)
}

// declareBash declares the bash tool on a fresh manager; look resolves the
// shell when injected.
func declareBash(ctx context.Context, look func(string) (string, error)) error {
	w := worldFrom(ctx)
	m := toolmanager.New(toolmanager.Options{})
	opts := bash.Options{
		LookPath: look,
		OnRemind: func(jobID string, elapsed time.Duration) {
			w.bashReminded = true
		},
	}
	if err := m.Registry().Declare(bash.Tool(opts)); err != nil {
		return err
	}
	if w.bashManager != nil {
		w.bashManager.Close()
	}
	w.bashManager = m
	return nil
}

func stepBashTool(ctx context.Context) error {
	return declareBash(ctx, nil)
}

// stepBashToolResolving pins the shell lookup to one real shell: a bash tool
// resolving "bash" proves bash -c is used, and resolving only "sh" proves the
// fallback.
func stepBashToolResolving(ctx context.Context, shell string) error {
	path, err := exec.LookPath(shell)
	if err != nil {
		return fmt.Errorf("%s is not installed: %w", shell, err)
	}
	return declareBash(ctx, func(name string) (string, error) {
		if name == shell {
			return path, nil
		}
		return "", exec.ErrNotFound
	})
}

// startBashCall starts one call on the scenario's manager.
func startBashCall(ctx context.Context, call bash.Call) error {
	w := worldFrom(ctx)
	if w.bashManager == nil {
		return errors.New("no bash manager declared")
	}
	args, err := json.Marshal(call)
	if err != nil {
		return err
	}
	w.bashJob = w.bashManager.Start(bash.ToolName, args, 0)
	return nil
}

func stepBashRuns(ctx context.Context, command string) error {
	return startBashCall(ctx, bash.Call{Command: command})
}

func stepBashRunsBlocking(ctx context.Context, marker string) error {
	w := worldFrom(ctx)
	dir, err := os.MkdirTemp("", "bash-conf-")
	if err != nil {
		return err
	}
	w.bashDir = dir
	fifo := filepath.Join(dir, "release")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		return err
	}
	w.bashFIFO = fifo
	return startBashCall(ctx, bash.Call{
		Command: fmt.Sprintf("printf '%s'; cat < %q > /dev/null; printf done", marker, fifo),
	})
}

func stepBashPeepWhileRunning(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := w.bashJob.Peep()
		if st.State == toolmanager.StateRunning && strings.Contains(st.Output, want) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("peep never showed %q while running: state=%s output=%q", want, st.State, st.Output)
		}
		runtime.Gosched()
	}
}

func stepBashRelease(ctx context.Context) error {
	w := worldFrom(ctx)
	f, err := os.OpenFile(w.bashFIFO, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("go"); err != nil {
		return err
	}
	return f.Close()
}

func stepBashJobDone(ctx context.Context) error {
	w := worldFrom(ctx)
	st := w.bashJob.Wait()
	if st.State != toolmanager.StateDone {
		return fmt.Errorf("bash job state = %s (error %q), want done", st.State, st.Error)
	}
	result, ok := st.Result.(bash.Result)
	if !ok {
		return fmt.Errorf("result = %#v, want bash.Result", st.Result)
	}
	w.bashResult = result
	return nil
}

func stepBashResultAll(ctx context.Context, code int, stdout, stderr string) error {
	w := worldFrom(ctx)
	result := w.bashResult
	if result.ExitCode != code || result.Stdout != stdout || result.Stderr != stderr {
		return fmt.Errorf("result = %+v, want exit %d, stdout %q, stderr %q", result, code, stdout, stderr)
	}
	return nil
}

func stepBashResultStdout(ctx context.Context, stdout string) error {
	if err := stepBashJobDone(ctx); err != nil {
		return err
	}
	w := worldFrom(ctx)
	if w.bashResult.Stdout != stdout {
		return fmt.Errorf("result stdout = %q, want %q", w.bashResult.Stdout, stdout)
	}
	return nil
}

func stepBashJobFailed(ctx context.Context) error {
	w := worldFrom(ctx)
	st := w.bashJob.Wait()
	if st.State != toolmanager.StateFailed {
		return fmt.Errorf("bash job state = %s, want failed", st.State)
	}
	w.bashError = st.Error
	return nil
}

func stepBashErrorCarries(ctx context.Context, first, second string) error {
	w := worldFrom(ctx)
	for _, want := range []string{first, second} {
		if !strings.Contains(w.bashError, want) {
			return fmt.Errorf("bash error = %q, want it to carry %q", w.bashError, want)
		}
	}
	return nil
}

func stepBashRunsDescendant(ctx context.Context) error {
	w := worldFrom(ctx)
	dir, err := os.MkdirTemp("", "bash-conf-")
	if err != nil {
		return err
	}
	w.bashDir = dir
	shellPID := filepath.Join(dir, "shell.pid")
	childPID := filepath.Join(dir, "child.pid")
	if err := startBashCall(ctx, bash.Call{
		Command: fmt.Sprintf("echo $$ > %q; sleep 300 & echo $! > %q; wait", shellPID, childPID),
	}); err != nil {
		return err
	}
	shell, err := pollPIDFile(shellPID)
	if err != nil {
		return err
	}
	child, err := pollPIDFile(childPID)
	if err != nil {
		return err
	}
	w.bashShellPID, w.bashChildPID = shell, child
	return nil
}

func stepBashJobKilled(ctx context.Context) error {
	w := worldFrom(ctx)
	w.bashKillStatus = w.bashManager.Kill(w.bashJob)
	return nil
}

func stepBashJobKilledAtOnce(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.bashKillStatus.State != toolmanager.StateKilled {
		return fmt.Errorf("kill status = %s, want killed", w.bashKillStatus.State)
	}
	return nil
}

func stepBashDescendantGone(ctx context.Context) error {
	w := worldFrom(ctx)
	return pollGone(w.bashChildPID)
}

func stepBashRunsRemind(ctx context.Context, ms int) error {
	w := worldFrom(ctx)
	dir, err := os.MkdirTemp("", "bash-conf-")
	if err != nil {
		return err
	}
	w.bashDir = dir
	pidFile := filepath.Join(dir, "shell.pid")
	if err := startBashCall(ctx, bash.Call{
		Command:  fmt.Sprintf("echo $$ > %q; sleep 300", pidFile),
		RemindMS: int64(ms),
	}); err != nil {
		return err
	}
	shell, err := pollPIDFile(pidFile)
	if err != nil {
		return err
	}
	w.bashShellPID = shell
	return nil
}

func stepBashReminderFiresWhileRunning(ctx context.Context) error {
	w := worldFrom(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if w.bashReminded {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("reminder never fired")
		}
		time.Sleep(10 * time.Millisecond)
	}
	st := w.bashJob.Peep()
	if st.State != toolmanager.StateRunning {
		return fmt.Errorf("bash job state = %s, want running", st.State)
	}
	return nil
}

func stepBashProcessStillRunning(ctx context.Context) error {
	w := worldFrom(ctx)
	if err := syscall.Kill(w.bashShellPID, 0); err != nil {
		return fmt.Errorf("pid %d is not running: %w", w.bashShellPID, err)
	}
	return nil
}

func stepBashProcessGone(ctx context.Context) error {
	w := worldFrom(ctx)
	return pollGone(w.bashShellPID)
}

// pollPIDFile polls a pid file until the command has written a pid.
func pollPIDFile(path string) (int, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return pid, nil
			}
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("pid file %s was never written", path)
		}
		runtime.Gosched()
	}
}

// pollGone polls until the pid is no longer alive.
func pollGone(pid int) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("pid %d is still alive", pid)
		}
		runtime.Gosched()
	}
}
