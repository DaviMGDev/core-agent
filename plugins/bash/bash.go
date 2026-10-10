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
	// DefaultTimeout bounds a call whose document does not set timeout_ms.
	DefaultTimeout = 60 * time.Second
)

// Options configure the bash tool.
type Options struct {
	// LookPath resolves a shell binary; nil keeps exec.LookPath. Tests inject
	// it to prove the bash → sh fallback without hiding a real shell.
	LookPath func(string) (string, error)
	// Timeout is the default bound for a call that does not set timeout_ms.
	// Zero keeps DefaultTimeout.
	Timeout time.Duration
	// WaitDelay bounds the wait after a cancellation for the process's I/O to
	// close; zero keeps two seconds.
	WaitDelay time.Duration
}

// timeout returns the configured default bound.
func (o Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return DefaultTimeout
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
	Command   string `json:"command"`
	CWD       string `json:"cwd,omitempty"`
	TimeoutMS int64  `json:"timeout_ms,omitempty"`
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
		Name: ToolName,
		Description: "run a shell command as one job and return its factual result — exit code, stdout, stderr. " +
			"Report only what the process produced; never invent a diagnosis for a failure.",
		Schema: json.RawMessage(`{"type":"object","properties":{` +
			`"command":{"type":"string","description":"the shell command to run"},` +
			`"cwd":{"type":"string","description":"the working directory; the core's directory when unset"},` +
			`"timeout_ms":{"type":"integer","description":"the time bound in milliseconds; 60000 when unset"}},` +
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
	timeout := callTimeout(call, opts)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, shell, "-c", call.Command)
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

	runErr := cmd.Run()
	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	result := Result{ExitCode: code, Stdout: stdout.String(), Stderr: stderr.String()}

	if runCtx.Err() == context.DeadlineExceeded {
		return result, failure(fmt.Sprintf("timeout after %s", timeout), capturedOutput(result))
	}
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

// callTimeout resolves the bound: the call's timeout_ms when set, the
// configured default otherwise.
func callTimeout(call Call, opts Options) time.Duration {
	if call.TimeoutMS > 0 {
		return time.Duration(call.TimeoutMS) * time.Millisecond
	}
	return opts.timeout()
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
