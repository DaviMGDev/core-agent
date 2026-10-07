// Command core-agent drives the composed system.
//
// It registers the six starter plugin guests with memento, composes one
// fiber per plugin through the scheduler, drives the terminal session — the
// host reads stdin and is woken for chat.message — and unloads on exit. The
// entry drives the scheduler directly instead of the loader because the
// interactive session outlives the loader's quiescence window (the pattern
// the kernel documents in examples/chat).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/DaviMGDev/core-agent/plugins/agent"
	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	contextmanager "github.com/DaviMGDev/core-agent/plugins/context-manager"
	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
	"github.com/DaviMGDev/core-agent/plugins/notifications"
	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	replchat "github.com/DaviMGDev/core-agent/plugins/repl-chat"
	"github.com/DaviMGDev/core-agent/plugins/subagent"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
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
		`{"name":"local","endpoint":"http://127.0.0.1:11434/v1","models":["llama-3.2"]},` +
		`{"name":"openai","endpoint":"https://api.openai.com/v1","credential":"env:OPENAI_API_KEY","models":["gpt-4o-mini"]}]}`

	// mockProviderConfig backs -mock: one in-process mock provider serving the
	// models the default views resolve to, so a session answers without a
	// provider and without a single request leaving the process.
	mockProviderConfig = `{"providers":[{"name":"mock","mock":true,"models":["llama-3.2","gpt-4o-mini"]}]}`

	modelConfig = `{"models":[` +
		`{"name":"fast","alias":"llama-3.2"},` +
		`{"name":"reliable","fallback":["llama-3.2","gpt-4o-mini"]},` +
		`{"name":"panel","discuss":["llama-3.2","gpt-4o-mini"]}]}`

	historyConversation = "main"
	contextConfig       = `{"budget":4096}`
	agentConfig         = `{"conversation":"main","model":"fast"}`
)

// sessionConfig is one composed session's configuration: the nickname and the
// payloads handed to the six plugins. Tests override the provider document to
// point at a local server, which is what turns the deterministic stub into a
// real provider call. onComposed, when set, receives the tool manager once
// the six plugins are active: the entry's tests drive the host side (peep,
// kill) while the session runs.
type sessionConfig struct {
	nick       string
	providers  string
	models     string
	history    string
	context    string
	agent      string
	onComposed func(*toolmanager.Manager)
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
		agent:     agentConfig,
	}
}

// busServices adapts the notification bus to the loader's host-services
// contract: guest job imports reach the manager and guest publishes reach the
// bus like any other publisher.
type busServices struct {
	bus     *notifications.Bus
	manager *toolmanager.Manager
}

func (s busServices) StartJob(caller *runtime.Instance, req []byte) ([]byte, error) {
	return s.manager.StartJob(caller, req)
}

func (s busServices) PeepJob(caller *runtime.Instance, req []byte) ([]byte, error) {
	return s.manager.PeepJob(caller, req)
}

func (s busServices) KillJob(caller *runtime.Instance, req []byte) ([]byte, error) {
	return s.manager.KillJob(caller, req)
}

func (s busServices) Publish(topic string, payload []byte) error {
	s.bus.Publish(topic, payload)
	return nil
}

func (s busServices) Cancelled(caller *runtime.Instance) bool {
	return s.manager.Cancelled(caller)
}

// plugin describes one starter plugin instance.
type plugin struct {
	ref     string
	wasm    []byte
	payload any
}

// starterPlugins returns the six plugins in dependency order, REPL last.
func starterPlugins(cfg sessionConfig) []plugin {
	return []plugin{
		{"provider-manager", providermanager.Wasm, cfg.providers},
		{"model-manager", modelmanager.Wasm, cfg.models},
		{"chat-history", chathistory.Wasm, cfg.history},
		{"context-manager", contextmanager.Wasm, cfg.context},
		{"agent", agent.Wasm, cfg.agent},
		{"repl-chat", replchat.Wasm, cfg.nick},
	}
}

func main() {
	nick := flag.String("nick", DefaultNick, "nickname the REPL announces")
	mock := flag.Bool("mock", false, "answer with an in-process mock LLM instead of calling a provider")
	flag.Parse()

	cfg := defaultConfig(*nick)
	if *mock {
		cfg.providers = mockProviderConfig
	}
	if err := runConfig(context.Background(), os.Stdin, os.Stdout, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "core-agent:", err)
		os.Exit(1)
	}
}

// runConfig composes the six plugins from cfg, drives one REPL session on
// in/out, and unloads everything before returning. Egress is open; credential
// references in request headers are resolved from the host environment, so a
// guest holds `env:NAME` and never a secret.
func runConfig(ctx context.Context, in io.Reader, out io.Writer, cfg sessionConfig) error {
	if cfg.nick == "" {
		cfg.nick = DefaultNick
	}

	bus := notifications.New()
	manager := toolmanager.New(toolmanager.Options{Publisher: bus})
	// The gate spans attribution to release for every agent call, so a job
	// scoped to one turn cannot answer for the next.
	callGate := &sync.Mutex{}
	engine, err := wasm.NewEngine(ctx,
		wasm.WithHTTPCredentialResolver(os.LookupEnv),
		wasm.WithHostServices(busServices{bus: bus, manager: manager}),
	)
	if err != nil {
		return fmt.Errorf("core-agent: engine: %w", err)
	}
	defer engine.Close(ctx)

	keys := wasm.NewKeyRegistry()
	sched := runtime.New()
	defer sched.Close()

	plugins := starterPlugins(cfg)
	comps := make([]*wasm.WASMComponent, 0, len(plugins))
	fibers := make([]mcontext.FiberID, 0, len(plugins))
	for _, p := range plugins {
		comp, err := wasm.NewComponent(ctx, engine, p.wasm,
			wasm.WithKeyRegistry(keys),
			wasm.WithLogWriter(out),
			wasm.WithModuleName(p.ref),
		)
		if err != nil {
			return fmt.Errorf("core-agent: %s: %w", p.ref, err)
		}
		id, err := sched.Insert(comp, p.payload)
		if err != nil {
			return fmt.Errorf("core-agent: %s: %w", p.ref, err)
		}
		comps = append(comps, comp)
		fibers = append(fibers, id)
	}

	if err := waitActive(sched, fibers); err != nil {
		return err
	}

	// The agent is the loop every caller reaches: the terminal invokes it, and
	// the host waker wakes it for its root jobs' events. The manager lists the
	// subagent tool over it, so a delegation is a job whose runner drives the
	// child's turns.
	agentComp := comps[len(comps)-2]
	if err := manager.Registry().Declare(subagent.Tool(agentComp, manager, subagent.Options{Call: callGate})); err != nil {
		return fmt.Errorf("core-agent: subagent tool: %w", err)
	}
	for _, topic := range jobWakeTopics {
		bus.Subscribe(topic, agentWaker{ctx: ctx, comp: agentComp, log: out, gate: callGate})
	}
	if cfg.onComposed != nil {
		cfg.onComposed(manager)
	}

	// The terminal guest is woken for every chat.message — a prompted reply
	// and an unprompted message take the same path — while the host drives
	// the session loop; the guest's module lock is free between wakes.
	terminal := comps[len(comps)-1]
	sub := bus.Subscribe(notifications.TopicChatMessage, terminalWaker{ctx: ctx, comp: terminal, log: out})
	session := replchat.New(cfg.nick)
	emit := func(s string) { _, _ = out.Write([]byte(s)) }
	if err := session.Run(in, emit, func(line string) (string, error) {
		// The terminal's line reaches the agent, so it takes the same gate.
		callGate.Lock()
		answer, err := invokeTerminal(ctx, terminal, replchat.Wake{Line: line})
		callGate.Unlock()
		if err != nil {
			return "", err
		}
		if answer.Error != "" {
			return "", errors.New(answer.Error)
		}
		// The turn's message is published; let the wake render it before the
		// next prompt so the transcript stays ordered.
		sub.WaitIdle()
		return "", nil
	}); err != nil {
		return fmt.Errorf("core-agent: session: %w", err)
	}
	sub.WaitIdle()

	// Reclaim the jobs before unloading the guests they call into.
	manager.Close()

	for i := len(fibers) - 1; i >= 0; i-- {
		if err := sched.Remove(fibers[i]); err != nil {
			return fmt.Errorf("core-agent: unload fiber %d: %w", fibers[i], err)
		}
	}
	return waitGone(sched, fibers)
}

// jobWakeTopics are the job events that wake the top-level agent. A start is
// not among them: the turn that started the job just ended.
var jobWakeTopics = []string{
	notifications.TopicJobTick,
	notifications.TopicJobCompleted,
	notifications.TopicJobFailed,
	notifications.TopicJobKilled,
}

// agentWaker wakes the top-level agent with one batch of its root jobs'
// events; the LLM decides whether to speak.
type agentWaker struct {
	ctx  context.Context
	comp *wasm.WASMComponent
	log  io.Writer
	gate sync.Locker
}

func (w agentWaker) Wake(sub *notifications.Subscription) {
	busEvents := sub.Take()
	wake := agent.Wake{Events: make([]agent.Event, 0, len(busEvents))}
	for _, e := range busEvents {
		wake.Events = append(wake.Events, agent.Event{Topic: e.Topic, Payload: json.RawMessage(e.Payload)})
	}
	if len(wake.Events) == 0 {
		return
	}
	req, err := json.Marshal(wake)
	if err != nil {
		return
	}
	if w.gate != nil {
		w.gate.Lock()
		defer w.gate.Unlock()
	}
	if _, err := w.comp.Handle(w.ctx, req); err != nil {
		fmt.Fprintf(w.log, "agent: wake: %v\n", err)
	}
}

// invokeTerminal sends one wake to the terminal guest and reads its answer.
func invokeTerminal(ctx context.Context, comp *wasm.WASMComponent, wake replchat.Wake) (replchat.Answer, error) {
	req, err := json.Marshal(wake)
	if err != nil {
		return replchat.Answer{}, err
	}
	resp, err := comp.Handle(ctx, req)
	if err != nil {
		return replchat.Answer{}, err
	}
	var answer replchat.Answer
	if err := json.Unmarshal(resp, &answer); err != nil {
		return replchat.Answer{}, fmt.Errorf("core-agent: reading terminal answer: %w", err)
	}
	return answer, nil
}

// terminalWaker wakes the terminal guest with one batch of chat.message
// events: rendering is the guest's work, the host only routes the wake.
type terminalWaker struct {
	ctx  context.Context
	comp *wasm.WASMComponent
	log  io.Writer
}

func (w terminalWaker) Wake(sub *notifications.Subscription) {
	busEvents := sub.Take()
	wake := replchat.Wake{Events: make([]replchat.Event, 0, len(busEvents))}
	for _, e := range busEvents {
		wake.Events = append(wake.Events, replchat.Event{Topic: e.Topic, Payload: json.RawMessage(e.Payload)})
	}
	if _, err := invokeTerminal(w.ctx, w.comp, wake); err != nil {
		fmt.Fprintf(w.log, "repl: wake: %v\n", err)
	}
}

// waitActive waits until every fiber has activated; the first failure aborts.
func waitActive(sched *runtime.Scheduler, fibers []mcontext.FiberID) error {
	for {
		active := 0
		for _, id := range fibers {
			info, ok := sched.Inspect(id)
			if !ok {
				return fmt.Errorf("core-agent: fiber %d disappeared during startup", id)
			}
			if info.State == runtime.StateFailed {
				return fmt.Errorf("core-agent: plugin fiber %d failed: %w", id, info.Err)
			}
			if info.State == runtime.StateActive {
				active++
			}
		}
		if active == len(fibers) {
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
