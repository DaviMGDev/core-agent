// Package bash runs shell commands as tool-manager jobs (charter, issue #22):
// one call is one job, the command's output streams to the job's write-through
// buffer while it runs, and the result is factual — exit code, stdout, stderr.
// This is the host-side component D14 places beside the guest plugins.
package bash

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"

	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// Defaults for the bash tool.
const (
	// ToolName is the registered tool's name.
	ToolName = "bash"
	// DefaultRemind is the reminder bound for a call that does not set remind_ms.
	DefaultRemind = 60 * time.Second
)

// Options configure the bash tool.
type Options struct {
	// LookPath resolves a shell binary; nil keeps exec.LookPath. Tests inject
	// it to prove the bash → sh fallback without hiding a real shell.
	LookPath func(string) (string, error)
	// Remind is the default bound for a call that does not set remind_ms.
	// Zero keeps DefaultRemind.
	Remind time.Duration
	// OnRemind, when non-nil, is invoked when the reminder bound elapses for
	// a running command.
	OnRemind func(jobID string, elapsed time.Duration)
	// WaitDelay bounds the wait after a cancellation for the process's I/O to
	// close; zero keeps two seconds.
	WaitDelay time.Duration
}

// remind returns the configured default reminder bound.
func (o Options) remind() time.Duration {
	if o.Remind > 0 {
		return o.Remind
	}
	return DefaultRemind
}

// waitDelay returns the configured post-cancellation wait.
func (o Options) waitDelay() time.Duration {
	if o.WaitDelay > 0 {
		return o.WaitDelay
	}
	return 2 * time.Second
}

// Call is one bash request document.
type Call struct {
	Command  string `json:"command"`
	CWD      string `json:"cwd,omitempty"`
	RemindMS int64  `json:"remind_ms,omitempty"`
}

// Result is a finished call's factual document: what the process produced.
type Result struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// Tool returns the manager tool that runs one shell command per job.
func Tool(opts Options) toolmanager.Tool {
	return toolmanager.Tool{
		Name:       ToolName,
		Background: true,
		Description: "run a shell command as one job and return its factual result — exit code, stdout, stderr. " +
			"Report only what the process produced; never invent a diagnosis for a failure.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"command":{"type":"string","description":"the shell command to run"},` +
			`"cwd":{"type":"string","description":"the working directory; the core's directory when unset"},` +
			`"remind_ms":{"type":"integer","description":"the reminder bound in milliseconds; 60000 when unset"}},` +
			`"required":["command"]}`),
		Run: func(ctx context.Context, args json.RawMessage, out *toolmanager.Output) (any, error) {
			return run(ctx, opts, args, out)
		},
	}
}

// run executes one call: resolve the shell, run the command in its own process
// group, and stream both streams to the job's output while collecting them.
func run(ctx context.Context, opts Options, args json.RawMessage, out *toolmanager.Output) (any, error) {
	var call Call
	if err := json.Unmarshal(args, &call); err != nil {
		return nil, fmt.Errorf("bash: %w", err)
	}
	if strings.TrimSpace(call.Command) == "" {
		return nil, errors.New("bash: command is required")
	}
	shell, err := resolveShell(opts)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, shell, "-c", call.Command)
	// A call runs in its own process group so a kill reaches the command's
	// descendants, not just the shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = opts.waitDelay()
	cmd.Dir = call.CWD
	// stdout and stderr go to the job's write-through buffer as they arrive,
	// merged in arrival order, and to their own buffers for the result.
	var stdout, stderr bytes.Buffer
	cmd.Stdout = io.MultiWriter(&stdout, out)
	cmd.Stderr = io.MultiWriter(&stderr, out)

	remind := callRemind(call, opts)
	if remind > 0 && opts.OnRemind != nil {
		timer := time.NewTimer(remind)
		defer timer.Stop()
		go func() {
			select {
			case <-timer.C:
				jobID := ""
				if j := out.Job(); j != nil {
					jobID = j.ID()
				}
				opts.OnRemind(jobID, remind)
			case <-ctx.Done():
			}
		}()
	}

	runErr := cmd.Run()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	result := Result{ExitCode: code, Stdout: stdout.String(), Stderr: stderr.String()}

	if ctx.Err() != nil {
		// The job was killed or reclaimed while the call ran: the engine
		// already decided the state, so report the cancellation factually.
		return nil, ctx.Err()
	}
	if runErr == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		detail := fmt.Sprintf("exit code %d", code)
		if code < 0 {
			detail = runErr.Error()
		}
		return result, failure(detail, capturedOutput(result))
	}
	return nil, fmt.Errorf("bash: %w", runErr)
}

// capturedOutput joins a result's streams for a factual failure message.
func capturedOutput(result Result) string {
	parts := make([]string, 0, 2)
	if out := strings.TrimSpace(result.Stdout); out != "" {
		parts = append(parts, out)
	}
	if errOut := strings.TrimSpace(result.Stderr); errOut != "" {
		parts = append(parts, errOut)
	}
	return strings.Join(parts, "\n")
}

// failure builds a factual failure message: the observed state plus the
// captured output, when the process wrote any.
func failure(detail, output string) error {
	msg := "bash: " + detail
	if out := strings.TrimSpace(output); out != "" {
		msg += ": " + out
	}
	return errors.New(msg)
}

// callRemind resolves the reminder bound: the call's remind_ms when set, the
// configured default otherwise.
func callRemind(call Call, opts Options) time.Duration {
	if call.RemindMS > 0 {
		return time.Duration(call.RemindMS) * time.Millisecond
	}
	return opts.remind()
}

// resolveShell picks the shell: bash when present, sh otherwise.
func resolveShell(opts Options) (string, error) {
	look := opts.LookPath
	if look == nil {
		look = exec.LookPath
	}
	if path, err := look("bash"); err == nil {
		return path, nil
	}
	if path, err := look("sh"); err == nil {
		return path, nil
	}
	return "", errors.New("bash: no shell found: neither bash nor sh is on PATH")
}
