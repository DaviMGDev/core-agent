// Command core-agent drives the composed system.
//
// It registers the five starter plugin guests with memento, composes one
// fiber per plugin through the scheduler, hosts the REPL session, and
// unloads on exit. The entry drives the scheduler directly instead of the
// loader because the REPL's activation hosts the interactive session, which
// outlives the loader's quiescence window (the pattern the kernel documents
// in examples/chat).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	contextmanager "github.com/DaviMGDev/core-agent/plugins/context-manager"
	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	replchat "github.com/DaviMGDev/core-agent/plugins/repl-chat"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/plugins/wasm"
	"github.com/DaviMGDev/memento/runtime"
)

// Defaults for the entry tree: JSON config for the management plugins and a
// nickname for the REPL (system spec D10). Credentials are references, never
// secrets (provider-manager spec PM2).
const (
	// DefaultNick is the REPL nickname when -nick is empty.
	DefaultNick = "agent"

	providerConfig = `{"providers":[` +
		`{"name":"local","endpoint":"http://127.0.0.1:11434/v1","credential":"env:CORE_AGENT_LOCAL_KEY","models":["llama-3.2"]},` +
		`{"name":"openai","endpoint":"https://api.openai.com/v1","credential":"env:OPENAI_API_KEY","models":["gpt-4o-mini"]}]}`

	modelConfig = `{"models":[` +
		`{"name":"fast","alias":"llama-3.2"},` +
		`{"name":"reliable","fallback":["llama-3.2","gpt-4o-mini"]},` +
		`{"name":"panel","discuss":["llama-3.2","gpt-4o-mini"]}]}`

	historyConversation = "main"
	contextConfig       = `{"budget":4096}`
)

// sessionConfig is one composed session's configuration: the nickname and the
// payloads handed to the five plugins. Tests override the provider document to
// point at a local server, which is what turns the deterministic stub into a
// real provider call.
type sessionConfig struct {
	nick      string
	providers string
	models    string
	history   string
	context   string
}

// defaultConfig returns the shipped configuration for a session named nick.
func defaultConfig(nick string) sessionConfig {
	if nick == "" {
		nick = DefaultNick
	}
	return sessionConfig{
		nick:      nick,
		providers: providerConfig,
		models:    modelConfig,
		history:   historyConversation,
		context:   contextConfig,
	}
}

// plugin describes one starter plugin instance.
type plugin struct {
	ref     string
	wasm    []byte
	payload any
	stdio   bool
}

// starterPlugins returns the five plugins in dependency order, REPL last.
func starterPlugins(cfg sessionConfig) []plugin {
	return []plugin{
		{"provider-manager", providermanager.Wasm, cfg.providers, false},
		{"model-manager", modelmanager.Wasm, cfg.models, false},
		{"chat-history", chathistory.Wasm, cfg.history, false},
		{"context-manager", contextmanager.Wasm, cfg.context, false},
		{"repl-chat", replchat.Wasm, cfg.nick, true},
	}
}

func main() {
	nick := flag.String("nick", DefaultNick, "nickname the REPL announces")
	flag.Parse()
	if err := run(context.Background(), os.Stdin, os.Stdout, *nick); err != nil {
		fmt.Fprintln(os.Stderr, "core-agent:", err)
		os.Exit(1)
	}
}

// run composes the five plugins, hosts one REPL session on in/out, and
// unloads everything before returning.
func run(ctx context.Context, in io.Reader, out io.Writer, nick string) error {
	return runConfig(ctx, in, out, defaultConfig(nick))
}

// runConfig composes the five plugins from cfg, hosts one REPL session on
// in/out, and unloads everything before returning. Egress is open; credential
// references in request headers are resolved from the host environment, so a
// guest holds `env:NAME` and never a secret.
func runConfig(ctx context.Context, in io.Reader, out io.Writer, cfg sessionConfig) error {
	if cfg.nick == "" {
		cfg.nick = DefaultNick
	}

	engine, err := wasm.NewEngine(ctx, wasm.WithHTTPCredentialResolver(os.LookupEnv))
	if err != nil {
		return fmt.Errorf("core-agent: engine: %w", err)
	}
	defer engine.Close(ctx)

	keys := wasm.NewKeyRegistry()
	sched := runtime.New()
	defer sched.Close()

	fibers := make([]mcontext.FiberID, 0, 5)
	for _, p := range starterPlugins(cfg) {
		opts := []wasm.ComponentOption{
			wasm.WithKeyRegistry(keys),
			wasm.WithLogWriter(out),
			wasm.WithModuleName(p.ref),
		}
		if p.stdio {
			opts = append(opts, wasm.WithStdin(in), wasm.WithStdout(out), wasm.WithStderr(out))
		}
		comp, err := wasm.NewComponent(ctx, engine, p.wasm, opts...)
		if err != nil {
			return fmt.Errorf("core-agent: %s: %w", p.ref, err)
		}
		id, err := sched.Insert(comp, p.payload)
		if err != nil {
			return fmt.Errorf("core-agent: %s: %w", p.ref, err)
		}
		fibers = append(fibers, id)
	}

	if err := waitSession(sched, fibers); err != nil {
		return err
	}

	for i := len(fibers) - 1; i >= 0; i-- {
		if err := sched.Remove(fibers[i]); err != nil {
			return fmt.Errorf("core-agent: unload fiber %d: %w", fibers[i], err)
		}
	}
	return waitGone(sched, fibers)
}

// waitSession waits until the REPL fiber settles: active once the session
// ended, or failed. Every other fiber must stay healthy meanwhile.
func waitSession(sched *runtime.Scheduler, fibers []mcontext.FiberID) error {
	repl := fibers[len(fibers)-1]
	for {
		for _, id := range fibers {
			info, ok := sched.Inspect(id)
			if !ok {
				return fmt.Errorf("core-agent: fiber %d disappeared during startup", id)
			}
			if info.State == runtime.StateFailed {
				return fmt.Errorf("core-agent: plugin fiber %d failed: %w", id, info.Err)
			}
		}
		if info, ok := sched.Inspect(repl); ok && info.State == runtime.StateActive {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitGone waits for every removed fiber to disappear.
func waitGone(sched *runtime.Scheduler, fibers []mcontext.FiberID) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		gone := true
		for _, id := range fibers {
			if _, ok := sched.Inspect(id); ok {
				gone = false
				break
			}
		}
		if gone {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("core-agent: timed out waiting for unload")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
