package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/DaviMGDev/core-agent/plugins/agent"
	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	contextmanager "github.com/DaviMGDev/core-agent/plugins/context-manager"
	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
	"github.com/DaviMGDev/core-agent/plugins/notifications"
	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	replchat "github.com/DaviMGDev/core-agent/plugins/repl-chat"
	spc "github.com/DaviMGDev/memento/context"
	rt "github.com/DaviMGDev/memento/runtime"
)

// systemSession drives one composed system over a pipe.
type systemSession struct {
	in      *io.PipeWriter
	out     *safeBuffer
	bus     *notifications.Bus
	done    chan error
	settled bool
	doneErr error
}

func startSystem(nick string) *systemSession {
	pr, pw := io.Pipe()
	s := &systemSession{in: pw, out: &safeBuffer{}, bus: notifications.New(), done: make(chan error, 1)}
	go func() { s.done <- runHostSystem(pr, s.out, nick, s.bus) }()
	return s
}

func (s *systemSession) send(line string) {
	done := make(chan struct{})
	go func() {
		_, _ = io.WriteString(s.in, line+"\n")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

// enter sends one line and waits for the next prompt; quit aliases return as
// soon as the line is sent.
func (s *systemSession) enter(line string) error {
	trimmed := strings.TrimSpace(line)
	before := len(s.responses())
	s.send(line)
	if trimmed == ":quit" || trimmed == ":q" || trimmed == ":exit" {
		return nil
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.responses()) > before {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("no response to %q:\n%s", line, s.out.String())
}

// responses returns the non-empty transcript segments after each prompt.
func (s *systemSession) responses() []string {
	parts := strings.Split(s.out.String(), "you> ")
	var out []string
	for _, p := range parts[1:] {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *systemSession) waitContains(marker string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(s.out.String(), marker) {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("transcript does not contain %q:\n%s", marker, s.out.String())
}

func (s *systemSession) waitDone(timeout time.Duration) error {
	if s.settled {
		return s.doneErr
	}
	select {
	case err := <-s.done:
		s.settled = true
		s.doneErr = err
		return err
	case <-time.After(timeout):
		return fmt.Errorf("system did not settle:\n%s", s.out.String())
	}
}

func (s *systemSession) stop() {
	if s == nil || s.in == nil {
		return
	}
	_ = s.in.Close()
	if s.settled {
		return
	}
	select {
	case <-s.done:
		s.settled = true
	case <-time.After(10 * time.Second):
	}
}

// --- system steps -----------------------------------------------------------

func registerSystemSteps(sc *godog.ScenarioContext) {
	sc.Step(`^the seven starter plugins composed as fibers$`, stepComposeSystem)
	sc.Step(`^the system is composed with the seven plugins and nickname "([^"]*)"$`, stepComposeSystemNick)
	sc.Step(`^the system is composed with the seven plugins$`, stepComposeDefault)
	sc.Step(`^the composition settles$`, stepCompositionSettles)
	sc.Step(`^every plugin declares its keys and activates$`, stepCompositionSettles)
	sc.Step(`^the transcript reports all seven plugins$`, stepCompositionSettles)
	sc.Step(`^repl-chat activates after chat-history, model-manager, context-manager, and the agent$`, stepActivationOrder)
	sc.Step(`^the seven (?:starter )?plugins are active$`, stepSixActive)
	sc.Step(`^the session starts$`, stepSessionStarts)
	sc.Step(`^the transcript announces "([^"]*)"$`, stepTranscriptContains)
	sc.Step(`^the transcript shows the prompt "([^"]*)"$`, stepTranscriptContains)
	sc.Step(`^a running session$`, stepRunningSession)
	sc.Step(`^a running session whose input is exhausted$`, stepRunningExhausted)
	sc.Step(`^the response names the commands "([^"]*)", "([^"]*)"$`, stepCommandResponse)
	sc.Step(`^the turn returns exactly one response line$`, stepOneResponseLine)
	sc.Step(`^no response line is produced for that turn$`, stepNoResponse)
	sc.Step(`^the user sends "([^"]*)"$`, stepUserSends)
	sc.Step(`^chat-history records a user turn and an assistant turn$`, stepRecordsTurns)
	sc.Step(`^the response names the resolved model and the context size$`, stepResponseNamesPipeline)
	sc.Step(`^the session ends$`, stepSessionEnds)
	sc.Step(`^the guest reaches end of input$`, stepReachesEOF)
	sc.Step(`^the session ends cleanly$`, stepEndsCleanly)
	sc.Step(`^unload runs the repl-chat inverse$`, stepUnloadInverse)
	sc.Step(`^the transcript reports "([^"]*)" on unload$`, stepTranscriptContains)
	sc.Step(`^a chat\.message is published with "([^"]*)"$`, stepPublishChatMessage)
	sc.Step(`^the transcript renders "([^"]*)"$`, stepTranscriptContains)
	sc.Step(`^every fiber is removed$`, stepEveryFiberRemoved)
	sc.Step(`^each plugin runs its effect inverse$`, stepEffectInverses)
	sc.Step(`^no fiber remains in the registry$`, stepNoFiber)

	// kernel admission rules (host components)
	sc.Step(`^a provider of "([^"]*)" is active$`, stepStubProvider)
	sc.Step(`^another component declares that it provides "([^"]*)"$`, stepDuplicateProvide)
	sc.Step(`^the insertion fails$`, stepInsertionFails)
	sc.Step(`^the composition is unchanged$`, stepRegistryUnchanged)
	sc.Step(`^a component that provides "([^"]*)" and injects "([^"]*)" is active$`, stepCycleFirst)
	sc.Step(`^a component that provides "([^"]*)" and injects "([^"]*)" is inserted$`, stepCycleSecond)
	sc.Step(`^the insertion fails with a dependency cycle error$`, stepCycleInsertion)
	sc.Step(`^the registry is unchanged$`, stepRegistryUnchanged)
}

func stepComposeSystem(ctx context.Context) error {
	return stepComposeSystemNick(ctx, "agent")
}

func stepComposeSystemNick(ctx context.Context, nick string) error {
	w := worldFrom(ctx)
	w.sys = startSystem(nick)
	return nil
}

func stepComposeDefault(ctx context.Context) error {
	return stepComposeSystemNick(ctx, "agent")
}

func stepCompositionSettles(ctx context.Context) error {
	w := worldFrom(ctx)
	for _, marker := range []string{
		"provider-manager: 2 provider(s) ready",
		"model-manager: 3 model view(s) ready",
		`chat-history: conversation "main" open`,
		"context-manager: window ready",
		"agent: loop ready",
		"repl: agent joined",
	} {
		if err := w.sys.waitContains(marker, 30*time.Second); err != nil {
			return err
		}
	}
	return nil
}

func stepActivationOrder(ctx context.Context) error {
	w := worldFrom(ctx)
	if err := stepCompositionSettles(ctx); err != nil {
		return err
	}
	out := w.sys.out.String()
	history := strings.Index(out, "chat-history:")
	model := strings.Index(out, "model-manager:")
	contextIdx := strings.Index(out, "context-manager:")
	agentIdx := strings.Index(out, "agent: loop ready")
	repl := strings.Index(out, "repl: agent joined")
	if repl < history || repl < model || repl < contextIdx || repl < agentIdx {
		return fmt.Errorf("repl-chat activated before its dependencies:\n%s", out)
	}
	return nil
}

func stepSixActive(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.sys == nil {
		if err := stepComposeSystemNick(ctx, "agent"); err != nil {
			return err
		}
	}
	return stepCompositionSettles(ctx)
}

func stepSessionStarts(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.sys == nil {
		w.sys = startSystem("agent")
	}
	return w.sys.waitContains("you> ", 30*time.Second)
}

func stepTranscriptContains(ctx context.Context, marker string) error {
	return worldFrom(ctx).sys.waitContains(marker, 30*time.Second)
}

func stepRunningSession(ctx context.Context) error {
	return stepSessionStarts(ctx)
}

func stepRunningExhausted(ctx context.Context) error {
	if err := stepSessionStarts(ctx); err != nil {
		return err
	}
	_ = worldFrom(ctx).sys.in.Close()
	return nil
}

// stepPublishChatMessage publishes one unprompted chat.message while the
// session waits for input.
func stepPublishChatMessage(ctx context.Context, text string) error {
	w := worldFrom(ctx)
	payload, err := json.Marshal(replchat.Message{Text: text})
	if err != nil {
		return err
	}
	w.sys.bus.Publish(notifications.TopicChatMessage, payload)
	return nil
}

func stepCommandResponse(ctx context.Context, first, second string) error {
	w := worldFrom(ctx)
	responses := w.sys.responses()
	if len(responses) == 0 {
		return errors.New("no response recorded")
	}
	last := responses[len(responses)-1]
	for _, want := range []string{first, second} {
		if !strings.Contains(last, want) {
			return fmt.Errorf("response %q does not name %q", last, want)
		}
	}
	return nil
}

func stepOneResponseLine(ctx context.Context) error {
	w := worldFrom(ctx)
	responses := w.sys.responses()
	if len(responses) == 0 {
		return errors.New("no response recorded")
	}
	if strings.Contains(responses[len(responses)-1], "\n") {
		return fmt.Errorf("response has several lines: %q", responses[len(responses)-1])
	}
	return nil
}

func stepNoResponse(ctx context.Context) error {
	w := worldFrom(ctx)
	if got := len(w.sys.responses()); got != w.responsesBefore {
		return fmt.Errorf("blank line produced a response (%d -> %d)", w.responsesBefore, got)
	}
	return nil
}

func stepUserSends(ctx context.Context, line string) error {
	w := worldFrom(ctx)
	if err := w.sys.enter(line); err != nil {
		return err
	}
	responses := w.sys.responses()
	w.firstResponse = responses[len(responses)-1]
	return nil
}

func stepRecordsTurns(ctx context.Context) error {
	w := worldFrom(ctx)
	if !strings.Contains(w.firstResponse, "(context:2)") {
		return fmt.Errorf("first response %q does not show the recorded user turn", w.firstResponse)
	}
	if err := w.sys.enter("probe"); err != nil {
		return err
	}
	responses := w.sys.responses()
	second := responses[len(responses)-1]
	if !strings.Contains(second, "(context:4)") {
		return fmt.Errorf("second response %q does not show the recorded assistant turn", second)
	}
	return nil
}

func stepResponseNamesPipeline(ctx context.Context) error {
	w := worldFrom(ctx)
	if !strings.Contains(w.firstResponse, "mock(llama-3.2)") || !strings.Contains(w.firstResponse, "(context:2)") {
		return fmt.Errorf("response %q does not name the model and context size", w.firstResponse)
	}
	return nil
}

func stepSessionEnds(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.sys == nil {
		return errors.New("no system session")
	}
	// Ending by EOF is safe whether or not the session already quit; a second
	// :quit would block on a pipe nobody reads anymore.
	_ = w.sys.in.Close()
	w.doneErr = w.sys.waitDone(30 * time.Second)
	return w.doneErr
}

func stepReachesEOF(ctx context.Context) error {
	w := worldFrom(ctx)
	w.doneErr = w.sys.waitDone(30 * time.Second)
	return w.doneErr
}

func stepEndsCleanly(ctx context.Context) error {
	if w := worldFrom(ctx); w.doneErr != nil {
		return w.doneErr
	}
	return nil
}

func stepUnloadInverse(ctx context.Context) error {
	return worldFrom(ctx).sys.waitContains("repl: session closed", 30*time.Second)
}

func stepEveryFiberRemoved(ctx context.Context) error {
	return stepSessionEnds(ctx)
}

func stepEffectInverses(ctx context.Context) error {
	w := worldFrom(ctx)
	for _, marker := range []string{
		"repl: session closed",
		"context-manager: window released",
		"chat-history: conversation closed",
		"model-manager: model views released",
		"provider-manager: providers released",
	} {
		if err := w.sys.waitContains(marker, 30*time.Second); err != nil {
			return err
		}
	}
	return nil
}

func stepNoFiber(ctx context.Context) error {
	if w := worldFrom(ctx); w.doneErr != nil {
		return w.doneErr
	}
	return nil
}

// --- kernel admission stubs -------------------------------------------------

type stubComponent struct{ decls rt.Declarations }

func (s *stubComponent) Declarations() rt.Declarations { return s.decls }

func (s *stubComponent) Activate(inst *rt.Instance, _ any) error {
	for _, k := range s.decls.Provide {
		typed, ok := k.(spc.Key[any])
		if !ok {
			continue
		}
		if err := rt.Bind(inst, typed, any([]byte("stub"))); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) key(name string) spc.Key[any] {
	if w.keys == nil {
		w.keys = map[string]spc.Key[any]{}
	}
	if k, ok := w.keys[name]; ok {
		return k
	}
	k := spc.NewKey[any](name)
	w.keys[name] = k
	return k
}

func stepStubProvider(ctx context.Context, key string) error {
	w := worldFrom(ctx)
	w.sched = rt.New()
	id, err := w.sched.Insert(&stubComponent{decls: rt.Declarations{Provide: []spc.AnyKey{w.key(key)}}}, nil)
	if err != nil {
		return err
	}
	w.countBefore = 1
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if info, ok := w.sched.Inspect(id); ok && info.State == rt.StateActive {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("stub provider %q did not activate", key)
}

func stepDuplicateProvide(ctx context.Context, key string) error {
	w := worldFrom(ctx)
	_, w.err = w.sched.Insert(&stubComponent{decls: rt.Declarations{Provide: []spc.AnyKey{w.key(key)}}}, nil)
	return nil
}

func stepInsertionFails(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.err == nil {
		return errors.New("insertion succeeded, want failure")
	}
	return nil
}

func stepRegistryUnchanged(ctx context.Context) error {
	w := worldFrom(ctx)
	if got := len(w.sched.Snapshot().Fibers); got != w.countBefore {
		return fmt.Errorf("registry has %d fibers, want %d", got, w.countBefore)
	}
	return nil
}

func stepCycleFirst(ctx context.Context, provides, injects string) error {
	w := worldFrom(ctx)
	w.sched = rt.New()
	if _, err := w.sched.Insert(&stubComponent{decls: rt.Declarations{
		Provide: []spc.AnyKey{w.key(provides)},
		Inject:  []spc.AnyKey{w.key(injects)},
	}}, nil); err != nil {
		return err
	}
	w.countBefore = 1
	return nil
}

func stepCycleSecond(ctx context.Context, provides, injects string) error {
	w := worldFrom(ctx)
	_, w.err = w.sched.Insert(&stubComponent{decls: rt.Declarations{
		Provide: []spc.AnyKey{w.key(provides)},
		Inject:  []spc.AnyKey{w.key(injects)},
	}}, nil)
	return nil
}

func stepCycleInsertion(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.err == nil || !strings.Contains(w.err.Error(), "cycle") {
		return fmt.Errorf("insertion error = %v, want a cycle error", w.err)
	}
	return nil
}

// --- the composed system (host level) ---------------------------------------

const (
	sysProviderConfig = `{"providers":[` +
		`{"name":"local","endpoint":"http://127.0.0.1:11434/v1","models":["llama-3.2"]},` +
		`{"name":"openai","endpoint":"https://api.openai.com/v1","credential":"env:OPENAI_API_KEY","models":["gpt-4o-mini"]}]}`
	sysModelConfig = `{"models":[` +
		`{"name":"fast","alias":"llama-3.2"},` +
		`{"name":"reliable","fallback":["llama-3.2","gpt-4o-mini"]},` +
		`{"name":"panel","discuss":["llama-3.2","gpt-4o-mini"]}]}`
	sysContextConfig = `{"budget":4096}`
)

// hostComponent is a Go component built by the conformance fixture.
type hostComponent struct {
	decls    rt.Declarations
	activate func(*rt.Instance) error
}

func (c *hostComponent) Declarations() rt.Declarations { return c.decls }
func (c *hostComponent) Activate(inst *rt.Instance, _ any) error {
	return c.activate(inst)
}

// fixtureTerminal is the terminal as the fixture composes it: the host drives
// the session loop and the guest-shaped handler serves one wake at a time,
// serialized by a mutex that stands in for the guest's module lock.
type fixtureTerminal struct {
	mu   sync.Mutex
	emit func(string)
	turn func(line string) (agent.TurnResult, error)
}

func (t *fixtureTerminal) handle(wake replchat.Wake) replchat.Answer {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch {
	case wake.Line != "":
		if _, err := t.turn(wake.Line); err != nil {
			return replchat.Answer{Error: err.Error()}
		}
	case len(wake.Events) > 0:
		for _, line := range replchat.Render(wake.Events) {
			t.emit(line + "\n")
		}
	}
	return replchat.Answer{}
}

// fixtureTerminalWaker routes one bus wake to the terminal handler.
type fixtureTerminalWaker struct{ term *fixtureTerminal }

func (w fixtureTerminalWaker) Wake(sub *notifications.Subscription) {
	busEvents := sub.Take()
	wake := replchat.Wake{Events: make([]replchat.Event, 0, len(busEvents))}
	for _, e := range busEvents {
		wake.Events = append(wake.Events, replchat.Event{Topic: e.Topic, Payload: json.RawMessage(e.Payload)})
	}
	_ = w.term.handle(wake)
}

// waitActive waits until every fixture fiber has activated.
func waitActive(sched *rt.Scheduler, fibers []spc.FiberID) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		active := 0
		for _, id := range fibers {
			info, ok := sched.Inspect(id)
			if !ok {
				return fmt.Errorf("fiber %d disappeared during startup", id)
			}
			if info.State == rt.StateFailed {
				return fmt.Errorf("fiber %d failed: %w", id, info.Err)
			}
			if info.State == rt.StateActive {
				active++
			}
		}
		if active == len(fibers) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for activation")
		}
		time.Sleep(time.Millisecond)
	}
}

// runHostSystem composes the seven plugins at the host level over one shared
// key registry and drives one session: the same declarations, transcript, and
// pipeline as cmd/core-agent, with the wasm ABI path exercised separately by
// that entry's end-to-end test. The terminal handler renders the chat.message
// wakes the host routes to it.
func runHostSystem(in io.Reader, out io.Writer, nick string, bus *notifications.Bus) error {
	if nick == "" {
		nick = "agent"
	}
	emit := func(s string) { _, _ = out.Write([]byte(s)) }

	sched := rt.New()
	defer sched.Close()

	key := func(name string) spc.Key[any] { return spc.NewKey[any](name) }
	providerKey, modelKey := key("provider-registry"), key("model-registry")
	historyKey, contextKey := key("chat-history"), key("llm-context")
	agentKey, replKey := key("agent-loop"), key("repl")

	providers := providermanager.NewRegistry()
	models := modelmanager.NewRegistry()
	store := chathistory.NewStore()
	contextCfg, err := contextmanager.ParseConfig([]byte(sysContextConfig))
	if err != nil {
		return err
	}
	budget := contextCfg.Budget
	if budget == 0 {
		budget = contextmanager.DefaultBudget
	}

	bind := func(inst *rt.Instance, k spc.Key[any], v string) error {
		return rt.Bind(inst, k, any([]byte(v)))
	}
	effectLog := func(inst *rt.Instance, line string) error {
		return inst.Context().RegisterEffect(func() (func() error, error) {
			return func() error { emit(line); return nil }, nil
		})
	}

	var fibers []spc.FiberID
	add := func(c rt.Component) error {
		id, err := sched.Insert(c, nil)
		if err != nil {
			return err
		}
		fibers = append(fibers, id)
		return nil
	}

	if err := add(&hostComponent{
		decls: rt.Declarations{Provide: []spc.AnyKey{providerKey}},
		activate: func(inst *rt.Instance) error {
			cfg, err := providermanager.ParseConfig([]byte(sysProviderConfig))
			if err != nil {
				return err
			}
			for _, p := range cfg {
				if err := providers.Register(p); err != nil {
					return err
				}
			}
			if err := bind(inst, providerKey, sysProviderConfig); err != nil {
				return err
			}
			emit(fmt.Sprintf("provider-manager: %d provider(s) ready\n", len(cfg)))
			return effectLog(inst, "provider-manager: providers released\n")
		},
	}); err != nil {
		return err
	}

	if err := add(&hostComponent{
		decls: rt.Declarations{Provide: []spc.AnyKey{modelKey}, Inject: []spc.AnyKey{providerKey}},
		activate: func(inst *rt.Instance) error {
			views, err := modelmanager.ParseConfig([]byte(sysModelConfig))
			if err != nil {
				return err
			}
			for _, v := range views {
				if err := models.Register(v); err != nil {
					return err
				}
			}
			if err := bind(inst, modelKey, sysModelConfig); err != nil {
				return err
			}
			emit(fmt.Sprintf("model-manager: %d model view(s) ready\n", len(views)))
			return effectLog(inst, "model-manager: model views released\n")
		},
	}); err != nil {
		return err
	}

	if err := add(&hostComponent{
		decls: rt.Declarations{Provide: []spc.AnyKey{historyKey}},
		activate: func(inst *rt.Instance) error {
			if err := store.Start("main"); err != nil {
				return err
			}
			if err := bind(inst, historyKey, "main"); err != nil {
				return err
			}
			emit("chat-history: conversation \"main\" open\n")
			return effectLog(inst, "chat-history: conversation closed\n")
		},
	}); err != nil {
		return err
	}

	if err := add(&hostComponent{
		decls: rt.Declarations{Provide: []spc.AnyKey{contextKey}, Inject: []spc.AnyKey{historyKey}},
		activate: func(inst *rt.Instance) error {
			if err := bind(inst, contextKey, sysContextConfig); err != nil {
				return err
			}
			emit(fmt.Sprintf("context-manager: window ready (budget %d)\n", budget))
			return effectLog(inst, "context-manager: window released\n")
		},
	}); err != nil {
		return err
	}

	agentCfg := agent.Config{
		Conversation: "main",
		Model:        "fast",
		Budget:       budget,
		Tools: []agent.Tool{
			{Name: "read", Description: "read a file"},
			{Name: "write", Description: "write a file"},
		},
	}
	if err := add(&hostComponent{
		decls: rt.Declarations{
			Provide: []spc.AnyKey{agentKey},
			Inject:  []spc.AnyKey{historyKey, modelKey, contextKey},
		},
		activate: func(inst *rt.Instance) error {
			if err := bind(inst, agentKey, agentCfg.Conversation); err != nil {
				return err
			}
			emit("agent: loop ready\n")
			return effectLog(inst, "agent: loop closed\n")
		},
	}); err != nil {
		return err
	}

	runTurn := func(line string) (agent.TurnResult, error) {
		return agent.RunTurn(agentCfg, line, agent.Deps{
			Append: func(role, text string) error {
				_, err := store.Append("main", role, text)
				return err
			},
			Recent: func(n int) ([]agent.Message, error) {
				recent := store.Recent("main", n)
				out := make([]agent.Message, 0, len(recent))
				for _, m := range recent {
					out = append(out, agent.Message{Role: m.Role, Text: m.Text})
				}
				return out, nil
			},
			Project: func(msgs []agent.Message) ([]agent.Message, error) {
				window := make([]contextmanager.Message, 0, len(msgs))
				for _, m := range msgs {
					window = append(window, contextmanager.Message{Role: m.Role, Text: m.Text})
				}
				projected := contextmanager.Project(window, budget)
				out := make([]agent.Message, 0, len(projected.Messages))
				for _, m := range projected.Messages {
					out = append(out, agent.Message{Role: m.Role, Text: m.Text})
				}
				return out, nil
			},
			Respond: func(text string, context []agent.Message, tools []agent.Tool) (agent.Answer, error) {
				contextMessages := make([]modelmanager.ContextMessage, 0, len(context))
				for _, m := range context {
					contextMessages = append(contextMessages, modelmanager.ContextMessage{Role: m.Role, Text: m.Text})
				}
				res, err := modelmanager.Apply(models, modelmanager.Op{
					Kind: "respond", Model: agentCfg.Model, Text: text, Context: contextMessages,
				}, modelmanager.MockCaller{})
				if err != nil {
					return agent.Answer{}, err
				}
				return agent.ParseAnswer(res.Text), nil
			},
			StartJob: func(tool string, args json.RawMessage) (string, error) {
				return "", errors.New("the host fixture composes no tool manager")
			},
			Publish: func(text string) error {
				payload, err := json.Marshal(replchat.Message{Text: text})
				if err != nil {
					return err
				}
				bus.Publish(notifications.TopicChatMessage, payload)
				return nil
			},
		})
	}
	term := &fixtureTerminal{emit: emit, turn: runTurn}

	if err := add(&hostComponent{
		decls: rt.Declarations{
			Provide: []spc.AnyKey{replKey},
			Inject:  []spc.AnyKey{agentKey},
		},
		activate: func(inst *rt.Instance) error {
			if err := bind(inst, replKey, replchat.New(nick).Nick()); err != nil {
				return err
			}
			return effectLog(inst, "repl: session closed\n")
		},
	}); err != nil {
		return err
	}

	if err := waitActive(sched, fibers); err != nil {
		return err
	}

	// The host drives the session loop; the terminal is woken for every
	// chat.message, and the driver waits for the wake to drain before the
	// next prompt so the transcript stays ordered.
	sub := bus.Subscribe(notifications.TopicChatMessage, fixtureTerminalWaker{term: term})
	session := replchat.New(nick)
	if err := session.Run(in, emit, func(line string) (string, error) {
		answer := term.handle(replchat.Wake{Line: line})
		if answer.Error != "" {
			return "", errors.New(answer.Error)
		}
		sub.WaitIdle()
		return "", nil
	}); err != nil {
		return err
	}
	sub.WaitIdle()

	for i := len(fibers) - 1; i >= 0; i-- {
		if err := sched.Remove(fibers[i]); err != nil {
			return err
		}
	}
	deadline := time.Now().Add(10 * time.Second)
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
			return errors.New("timed out waiting for unload")
		}
		time.Sleep(time.Millisecond)
	}
}
