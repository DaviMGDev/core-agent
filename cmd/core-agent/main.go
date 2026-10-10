// Command core-agent drives the composed system.
//
// It composes the seven native starter plugins with memento, one fiber
// per plugin through the scheduler, and serves one of two surfaces: the
// REPL (stdin/stdout, host-driven, woken for chat.message) or the TUI link
// (JSON lines on stdin/stdout, system spec "TUI Link"). It unloads on exit.
// The entry drives the scheduler directly instead of the loader because the
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

	"github.com/DaviMGDev/core-agent/internal/config"
	"github.com/DaviMGDev/core-agent/plugins/agent"
	"github.com/DaviMGDev/core-agent/plugins/bash"
	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	contextmanager "github.com/DaviMGDev/core-agent/plugins/context-manager"
	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
	"github.com/DaviMGDev/core-agent/plugins/notifications"
	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	provideropenai "github.com/DaviMGDev/core-agent/plugins/provider-openai"
	replchat "github.com/DaviMGDev/core-agent/plugins/repl-chat"
	"github.com/DaviMGDev/core-agent/plugins/subagent"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// sessionConfig is one composed session's configuration: the nickname, the
// payloads handed to the starter plugins, and the host credential resolver the
// model-manager exchange substitutes through. Tests override the provider document to
// point at a local server, which is what turns the deterministic stub into a
// real provider call. onComposed, when set, receives the tool manager once
// the plugins are active: the entry's tests drive the host side (peep,
// kill) while the session runs.
type sessionConfig struct {
	nick        string
	providers   string
	models      string
	history     string
	context     string
	agent       string
	credentials func(name string) (string, bool)
	onComposed  func(*toolmanager.Manager)
	// clock is the notification clock's period: queued job events flush
	// together on its boundaries. Zero delivers immediately.
	clock time.Duration
	// clockAfter supplies the clock's boundary channels; nil means
	// time.After. Tests inject a manual clock.
	clockAfter func(time.Duration) <-chan time.Time
}

// loadSession resolves the .core/ layer into a session configuration: it
// seeds the user scope when missing (never overwriting), loads and merges the
// layer, splits the payloads, swaps the provider document in mock mode, and
// applies -nick last (defaults < user < project < env < flags).
func loadSession(mock bool, nick string) (sessionConfig, error) {
	opts := config.Options{}
	userDir, _, err := config.Dirs(opts)
	if err != nil {
		return sessionConfig{}, err
	}
	if err := config.EnsureDefaults(userDir); err != nil {
		return sessionConfig{}, err
	}
	resolved, err := config.Load(opts)
	if err != nil {
		return sessionConfig{}, err
	}
	payloads, err := resolved.Payloads()
	if err != nil {
		return sessionConfig{}, err
	}
	if mock {
		mockDoc, err := resolved.MockProviders()
		if err != nil {
			return sessionConfig{}, err
		}
		payloads.Providers = string(mockDoc)
	}
	if nick != "" {
		payloads.Nick = nick
	}
	return sessionConfig{
		nick:      payloads.Nick,
		providers: payloads.Providers,
		models:    payloads.Models,
		history:   payloads.History,
		context:   payloads.Context,
		agent:     payloads.Agent,
		clock:     time.Duration(resolved.Settings.Clock.PeriodMS) * time.Millisecond,
		credentials: func(name string) (string, bool) {
			return resolved.Resolve(name, nil)
		},
	}, nil
}

// plugin describes one starter plugin instance: its reference name, its
// native component, and its activation payload.
type plugin struct {
	ref     string
	comp    runtime.Component
	payload any
}

// starterPlugins returns the seven native plugins in dependency order, REPL
// last. provider-openai sits right after provider-manager: it injects the
// live registry and registers its provider at activation. Its payload is
// empty, so it registers the standard OpenAI provider; a same-named config
// entry is left untouched by design. The agent entry carries the raw agent
// payload here; the caller swaps in the tool-augmented document at insert.
func starterPlugins(logs io.Writer, resolve func(name string) (string, bool), manager *toolmanager.Manager, publish func(text string) error, cfg sessionConfig) []plugin {
	mmComp := modelmanager.NewComponent(logs)
	mmComp.ResolveCredential = resolve
	return []plugin{
		{"provider-manager", providermanager.NewComponent(logs), cfg.providers},
		{"provider-openai", provideropenai.NewComponent(logs), ""},
		{"model-manager", mmComp, cfg.models},
		{"chat-history", chathistory.NewComponent(logs), cfg.history},
		{"context-manager", contextmanager.NewComponent(logs), cfg.context},
		{"agent", agent.NewComponent(logs, manager, publish), cfg.agent},
		{"repl-chat", replchat.NewComponent(logs), cfg.nick},
	}
}

func main() {
	nick := flag.String("nick", "", "nickname the REPL announces")
	mock := flag.Bool("mock", false, "answer with an in-process mock LLM instead of calling a provider")
	tui := flag.Bool("tui", false, "serve the TUI link (JSON lines) on stdin/stdout instead of the REPL")
	flag.Parse()

	cfg, err := loadSession(*mock, *nick)
	if err != nil {
		fmt.Fprintln(os.Stderr, "core-agent:", err)
		os.Exit(1)
	}
	ctx := context.Background()
	if *tui {
		// Component logs go to stderr: stdout carries link lines only.
		err = runLink(ctx, os.Stdin, os.Stdout, os.Stderr, cfg)
	} else {
		err = runConfig(ctx, os.Stdin, os.Stdout, cfg)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "core-agent:", err)
		os.Exit(1)
	}
}

// composedSession is one live composition: the seven plugins active on the
// scheduler plus the host-side agent layer. A surface driver works the
// session and closes it to unload.
type composedSession struct {
	ctx         context.Context
	bus         *notifications.Bus
	manager     *toolmanager.Manager
	gate        *sync.Mutex
	sched       *runtime.Scheduler
	fibers      []mcontext.FiberID
	agent       *agent.Component
	history     *chathistory.Component
	terminal    *replchat.Component
	agentConfig agent.Config
}

// Close reclaims the jobs and unloads the tree. It is the only path that
// ends a session; call it once.
func (s *composedSession) Close() error {
	s.manager.Close()
	s.bus.Close()
	var unloadErr error
	for i := len(s.fibers) - 1; i >= 0; i-- {
		if err := s.sched.Remove(s.fibers[i]); err != nil {
			unloadErr = fmt.Errorf("core-agent: unload fiber %d: %w", s.fibers[i], err)
			break
		}
	}
	if unloadErr == nil {
		unloadErr = waitGone(s.sched, s.fibers)
	}
	s.sched.Close()
	return unloadErr
}

// composeSession composes the seven native plugins from cfg and wires the
// agent layer: the tool manager as the host job service, the subagent tool
// over the agent loop, and the shared call gate. Component logs go to logs.
// Egress is open; credential references are resolved through the session's
// credential resolver (the auth store or the host environment) by the
// model-manager exchange, so a reference crosses and never a secret.
func composeSession(ctx context.Context, cfg sessionConfig, logs io.Writer) (*composedSession, error) {
	resolve := cfg.credentials
	if resolve == nil {
		resolve = os.LookupEnv
	}

	bus := notifications.New(notifications.WithClock(cfg.clock, cfg.clockAfter))
	manager := toolmanager.New(toolmanager.Options{Publisher: bus})
	// The gate spans attribution to release for every agent call, so a job
	// scoped to one turn cannot answer for the next.
	callGate := &sync.Mutex{}
	// The agent publishes its turns as chat.message events, like the guest
	// did through the publish import; private subagent turns skip it.
	publish := func(text string) error {
		payload, err := json.Marshal(map[string]string{"text": text})
		if err != nil {
			return err
		}
		bus.Publish(notifications.TopicChatMessage, payload)
		return nil
	}
	sched := runtime.New()
	abort := func(err error) (*composedSession, error) {
		sched.Close()
		return nil, err
	}

	plugins := starterPlugins(logs, resolve, manager, publish, cfg)
	byRef := make(map[string]runtime.Component, len(plugins))
	for _, p := range plugins {
		byRef[p.ref] = p.comp
	}

	// The agent is the loop every caller reaches: the terminal invokes it, and
	// the host waker wakes it for its root jobs' events. The manager lists the
	// subagent tool over it, so a delegation is a job whose runner drives the
	// child's turns.
	agentComp, ok := byRef["agent"].(*agent.Component)
	if !ok {
		return abort(errors.New("core-agent: agent plugin is not the native loop"))
	}
	agentComp.Execute = func(tool string, args json.RawMessage) (any, error) {
		return manager.Execute(ctx, tool, args)
	}
	if err := manager.Registry().Declare(subagent.Tool(agentComp, manager, subagent.Options{Call: callGate})); err != nil {
		return abort(fmt.Errorf("core-agent: subagent tool: %w", err))
	}
	// The bash tool joins the registry beside the subagent: one call is one
	// job running a shell command in the core's environment, result factual.
	if err := manager.Registry().Declare(bash.Tool(bash.Options{})); err != nil {
		return abort(fmt.Errorf("core-agent: bash tool: %w", err))
	}
	// The job surface joins the registry beside the subagent: every tool in
	// the registry is presented to the agent natively, so the model can list,
	// observe, and kill the launch's jobs instead of narrating control it does
	// not have.
	for _, t := range toolmanager.JobTools(manager) {
		if err := manager.Registry().Declare(t); err != nil {
			return abort(fmt.Errorf("core-agent: %s tool: %w", t.Name, err))
		}
	}

	agentPayloadDoc, err := agentPayload(cfg.agent, manager.Registry())
	if err != nil {
		return abort(err)
	}

	// Inserts are strictly sequenced with activation between them, in
	// dependency order: an activating worker reads the scheduler's fiber
	// table through runtime.Get, which races with the loop's own table
	// writes while it still has inserts to process.
	fibers := make([]mcontext.FiberID, 0, len(plugins))
	for _, p := range plugins {
		payload := p.payload
		if p.ref == "agent" {
			payload = agentPayloadDoc
		}
		id, err := sched.Insert(p.comp, payload)
		if err != nil {
			return abort(fmt.Errorf("core-agent: %s: %w", p.ref, err))
		}
		fibers = append(fibers, id)
		if err := waitActive(sched, fibers); err != nil {
			return abort(err)
		}
	}
	var agentCfg agent.Config
	if err := json.Unmarshal([]byte(agentPayloadDoc), &agentCfg); err != nil {
		return abort(fmt.Errorf("core-agent: agent payload: %w", err))
	}
	if cfg.onComposed != nil {
		cfg.onComposed(manager)
	}
	historyComp, ok := byRef["chat-history"].(*chathistory.Component)
	if !ok {
		return abort(errors.New("core-agent: chat-history plugin is not the native record"))
	}
	terminalComp, ok := byRef["repl-chat"].(*replchat.Component)
	if !ok {
		return abort(errors.New("core-agent: repl-chat plugin is not the native terminal"))
	}
	return &composedSession{
		ctx:         ctx,
		bus:         bus,
		manager:     manager,
		gate:        callGate,
		sched:       sched,
		fibers:      fibers,
		agent:       agentComp,
		history:     historyComp,
		terminal:    terminalComp,
		agentConfig: agentCfg,
	}, nil
}

// runConfig drives one REPL session on in/out over a composed session, as
// D15 describes: the host reads stdin, the terminal is woken for every
// chat.message, and the tree unloads before returning.
func runConfig(ctx context.Context, in io.Reader, out io.Writer, cfg sessionConfig) error {
	if cfg.nick == "" {
		cfg.nick = config.DefaultNick
	}
	s, err := composeSession(ctx, cfg, out)
	if err != nil {
		return err
	}
	s.attachAgentWaker(out)

	// The terminal is woken for every chat.message — a prompted reply and an
	// unprompted message take the same path — while the host drives the
	// session loop.
	sub := s.bus.Subscribe(notifications.TopicChatMessage, terminalWaker{ctx: ctx, comp: s.terminal, log: out})
	session := replchat.New(cfg.nick)
	emit := func(text string) { _, _ = out.Write([]byte(text)) }
	if err := session.Run(in, emit, func(line string) (string, error) {
		// The terminal's line reaches the agent, so it takes the same gate.
		s.gate.Lock()
		answer, err := invokeTerminal(ctx, s.terminal, replchat.Wake{Line: line})
		s.gate.Unlock()
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
		_ = s.Close()
		return fmt.Errorf("core-agent: session: %w", err)
	}
	sub.WaitIdle()
	return s.Close()
}

// agentPayload builds the top-level agent configuration: the resolved .core/
// layer payload augmented with every tool currently in the registry, schemas
// included. The entry injects the surface at composition so the top-level
// agent can call tools natively.
func agentPayload(raw string, registry *toolmanager.Registry) (string, error) {
	var c agent.Config
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return "", fmt.Errorf("core-agent: agent payload: %w", err)
		}
	}
	existing := make(map[string]bool, len(c.Tools))
	for _, t := range c.Tools {
		existing[t.Name] = true
	}
	for _, t := range registry.Tools() {
		if !existing[t.Name] {
			c.Tools = append(c.Tools, agent.Tool{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Schema,
			})
			existing[t.Name] = true
		}
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("core-agent: agent payload: %w", err)
	}
	return string(b), nil
}

// jobWakeTopics are the job events that wake the top-level agent. A start is
// not among them: the turn that started the job just ended.
var jobWakeTopics = []string{
	notifications.TopicJobTick,
	notifications.TopicJobCompleted,
	notifications.TopicJobFailed,
	notifications.TopicJobKilled,
}

// attachAgentWaker subscribes the host's agent waker to the job wake topics
// through the notification clock: queued events flush together on each
// boundary, and a boundary with nothing queued wakes no one.
func (s *composedSession) attachAgentWaker(log io.Writer) {
	for _, topic := range jobWakeTopics {
		s.bus.SubscribeClocked(topic, agentWaker{ctx: s.ctx, comp: s.agent, log: log, gate: s.gate})
	}
}

// agentWaker wakes the top-level agent with one batch of its root jobs'
// events; the LLM decides whether to speak.
type agentWaker struct {
	ctx  context.Context
	comp *agent.Component
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

// invokeTerminal sends one wake to the terminal and reads its answer.
func invokeTerminal(ctx context.Context, comp *replchat.Component, wake replchat.Wake) (replchat.Answer, error) {
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

// terminalWaker wakes the terminal with one batch of chat.message events:
// rendering is the terminal's work, the host only routes the wake.
type terminalWaker struct {
	ctx  context.Context
	comp *replchat.Component
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
