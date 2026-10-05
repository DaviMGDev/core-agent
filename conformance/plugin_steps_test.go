package conformance

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	contextmanager "github.com/DaviMGDev/core-agent/plugins/context-manager"
	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	replchat "github.com/DaviMGDev/core-agent/plugins/repl-chat"
)

func registerPluginSteps(sc *godog.ScenarioContext) {
	// provider-manager
	sc.Step(`^an empty provider registry$`, stepEmptyProviderRegistry)
	sc.Step(`^a provider "([^"]*)" at "([^"]*)" with credential "([^"]*)" is registered$`, stepRegisterProviderWithCredential)
	sc.Step(`^a provider with an empty name is registered$`, stepRegisterEmptyName)
	sc.Step(`^a provider "([^"]*)" at "([^"]*)" is registered$`, stepRegisterProvider)
	sc.Step(`^a registry with provider "([^"]*)"$`, stepRegistryWithProvider)
	sc.Step(`^another provider "([^"]*)" is registered$`, stepRegisterAnotherProvider)
	sc.Step(`^a registry with providers "([^"]*)" then "([^"]*)"$`, stepRegistryWithProviders)
	sc.Step(`^the registry contains "([^"]*)"$`, stepRegistryContains)
	sc.Step(`^the registry does not contain "([^"]*)"$`, stepRegistryNotContains)
	sc.Step(`^registration fails$`, stepRegistrationFails)
	sc.Step(`^the registry still has one provider$`, stepRegistryHasOne)
	sc.Step(`^the list is \[(.*)\]$`, stepProviderList)
	sc.Step(`^the provider "([^"]*)" is removed$`, stepRemoveProvider)
	sc.Step(`^a config payload with providers "([^"]*)" and "([^"]*)"$`, stepConfigPayload)
	sc.Step(`^a config payload that is not valid JSON$`, stepInvalidConfigPayload)
	sc.Step(`^the config is parsed$`, stepParseConfig)
	sc.Step(`^the parsed list has (\d+) providers$`, stepParsedCount)
	sc.Step(`^the first provider is "([^"]*)"$`, stepFirstProvider)
	sc.Step(`^parsing fails$`, stepParsingFails)
	sc.Step(`^a provider "([^"]*)" at "([^"]*)" serving "([^"]*)" is registered$`, stepRegisterProviderServing)
	sc.Step(`^"([^"]*)" is served by "([^"]*)"$`, stepServedBy)
	sc.Step(`^"([^"]*)" has no provider$`, stepNoProvider)

	// model-manager
	sc.Step(`^an empty model registry$`, stepEmptyModelRegistry)
	sc.Step(`^a view "([^"]*)" sets both alias and fallback$`, stepBrokenView)
	sc.Step(`^a registry with view "([^"]*)" as alias of "([^"]*)"$`, stepRegistryWithAlias)
	sc.Step(`^a view "([^"]*)" as alias of "([^"]*)"$`, stepViewAlias)
	sc.Step(`^another view "([^"]*)" is registered$`, stepAnotherView)
	sc.Step(`^the name "([^"]*)" is resolved$`, stepResolveName)
	sc.Step(`^the resolution mode is "([^"]*)"$`, stepResolutionMode)
	sc.Step(`^the targets are \[(.*)\]$`, stepTargets)
	sc.Step(`^a registry with view "([^"]*)" as fallback \[(.*)\]$`, stepRegistryWithFallback)
	sc.Step(`^a registry with view "([^"]*)" as discuss \[(.*)\]$`, stepRegistryWithDiscuss)
	sc.Step(`^a view "([^"]*)" as fallback \[(.*)\]$`, stepViewFallback)
	sc.Step(`^resolution fails with a cycle error$`, stepCycleError)
	sc.Step(`^the model "([^"]*)" is asked to respond$`, stepAskToRespond)
	sc.Step(`^the model caller fails for "([^"]*)"$`, stepCallerFailsFor)
	sc.Step(`^no model caller is configured$`, stepNoCaller)
	sc.Step(`^the answer comes from "([^"]*)"$`, stepAnswerFrom)
	sc.Step(`^the answer text is "([^"]*)"$`, stepAnswerText)
	sc.Step(`^the response fails$`, stepResponseFails)
	sc.Step(`^a mock provider "([^"]*)" serving "([^"]*)" is registered$`, stepRegisterMockProvider)

	// chat-history
	sc.Step(`^a conversation "([^"]*)"$`, stepConversation)
	sc.Step(`^an empty store$`, stepEmptyStore)
	sc.Step(`^the user turn "([^"]*)" is appended$`, stepAppendUserTurn)
	sc.Step(`^the assistant turn "([^"]*)" is appended$`, stepAppendAssistantTurn)
	sc.Step(`^the conversation has (\d+) turns$`, stepConversationTurns)
	sc.Step(`^the first turn is "([^"]*): ([^"]*)"$`, stepNthTurn(0))
	sc.Step(`^the second turn is "([^"]*): ([^"]*)"$`, stepNthTurn(1))
	sc.Step(`^a turn with role "([^"]*)" is appended$`, stepAppendRole)
	sc.Step(`^the append fails$`, stepAppendFails)
	sc.Step(`^the user turn "([^"]*)" is appended to "([^"]*)"$`, stepAppendUserTurnTo)
	sc.Step(`^the store lists \[(.*)\]$`, stepStoreLists)
	sc.Step(`^a conversation "([^"]*)" with turns (.*)$`, stepConversationWithTurns)
	sc.Step(`^the last (\d+) turns are requested$`, stepRecentTurns)
	sc.Step(`^the turns are \[(.*)\]$`, stepTurnTexts)
	sc.Step(`^a store with conversations "([^"]*)" then "([^"]*)"$`, stepStoreConversations)
	sc.Step(`^the conversation list is \[(.*)\]$`, stepConversationList)

	// context-manager
	sc.Step(`^a history with messages (.*) and budget (\d+)$`, stepHistoryBudget)
	sc.Step(`^an empty history and budget (\d+)$`, stepEmptyHistoryBudget)
	sc.Step(`^the window is projected$`, stepProject)
	sc.Step(`^the window keeps \[(.*)\]$`, stepWindowKeeps)
	sc.Step(`^no messages are dropped$`, stepNoDrops)
	sc.Step(`^one message is dropped$`, stepOneDrop)
	sc.Step(`^the window is empty$`, stepWindowEmpty)

	// repl-chat library
	sc.Step(`^a session with nickname "([^"]*)"$`, stepSession)
	sc.Step(`^the user enters "([^"]*)"$`, stepUserEnters)
	sc.Step(`^the user enters a blank line$`, stepUserEntersBlank)
	sc.Step(`^the reply names "([^"]*)" and "([^"]*)"$`, stepReplyNames)
	sc.Step(`^the reply does not end the session$`, stepReplyNotQuit)
	sc.Step(`^the reply ends the session$`, stepReplyQuit)
	sc.Step(`^the reply has no text$`, stepReplyNoText)
	sc.Step(`^the reply has exactly one line$`, stepReplyOneLine)
	sc.Step(`^the reply is the responder's result$`, stepReplyIsResponder)
	sc.Step(`^the responder fails for one turn$`, stepResponderFailsOnce)
	sc.Step(`^the transcript reports the error$`, stepTranscriptError)
	sc.Step(`^the session is still running$`, stepSessionRunning)
	sc.Step(`^the reply names turn (\d+)$`, stepReplyNamesTurn)
	sc.Step(`^the transcript announces "([^"]*)" at the start$`, stepTranscriptAnnounces)
}

// --- provider-manager -------------------------------------------------------

func stepEmptyProviderRegistry(ctx context.Context) error {
	w := worldFrom(ctx)
	w.providers = providermanager.NewRegistry()
	return nil
}

func stepRegisterProviderWithCredential(ctx context.Context, name, endpoint, cred string) error {
	return registerProvider(ctx, name, endpoint, cred)
}

func stepRegisterProvider(ctx context.Context, name, endpoint string) error {
	return registerProvider(ctx, name, endpoint, "env:TEST_KEY")
}

func registerProvider(ctx context.Context, name, endpoint, cred string) error {
	w := worldFrom(ctx)
	if w.providers == nil {
		w.providers = providermanager.NewRegistry()
	}
	w.err = w.providers.Register(providermanager.Provider{Name: name, Endpoint: endpoint, Credential: cred})
	return nil
}

func stepRegisterEmptyName(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.providers == nil {
		w.providers = providermanager.NewRegistry()
	}
	w.err = w.providers.Register(providermanager.Provider{Name: "", Endpoint: "https://x.example", Credential: "env:X"})
	return nil
}

func stepRegistryWithProvider(ctx context.Context, name string) error {
	stepEmptyProviderRegistry(ctx)
	return registerProvider(ctx, name, "https://api.example.com/v1", "env:TEST_KEY")
}

func stepRegisterAnotherProvider(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	w.err = w.providers.Register(providermanager.Provider{Name: name, Endpoint: "https://other.example/v1", Credential: "env:OTHER_KEY"})
	return nil
}

func stepRegistryWithProviders(ctx context.Context, first, second string) error {
	stepEmptyProviderRegistry(ctx)
	if err := registerProvider(ctx, first, "https://api.example.com/v1", "env:TEST_KEY"); err != nil {
		return err
	}
	return registerProvider(ctx, second, "https://local.example/v1", "env:LOCAL_KEY")
}

func stepRegistryContains(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return fmt.Errorf("registration failed: %w", w.err)
	}
	if _, ok := w.providers.Get(name); !ok {
		return fmt.Errorf("registry does not contain %q", name)
	}
	return nil
}

func stepRegistryNotContains(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	if _, ok := w.providers.Get(name); ok {
		return fmt.Errorf("registry still contains %q", name)
	}
	return nil
}

func stepRegistrationFails(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.err == nil {
		return errors.New("registration succeeded, want failure")
	}
	return nil
}

func stepRegistryHasOne(ctx context.Context) error {
	w := worldFrom(ctx)
	if n := w.providers.Len(); n != 1 {
		return fmt.Errorf("registry has %d providers, want 1", n)
	}
	return nil
}

func stepProviderList(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	var names []string
	for _, p := range w.providers.List() {
		names = append(names, p.Name)
	}
	if got := quotedList(want); !equalStrings(names, got) {
		return fmt.Errorf("list = %v, want %v", names, got)
	}
	return nil
}

func stepRemoveProvider(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	w.err = w.providers.Remove(name)
	return nil
}

func stepConfigPayload(ctx context.Context, first, second string) error {
	w := worldFrom(ctx)
	w.config = []byte(fmt.Sprintf(
		`{"providers":[{"name":%q,"endpoint":"https://a.example/v1","credential":"env:A"},{"name":%q,"endpoint":"https://b.example/v1","credential":"env:B"}]}`,
		first, second))
	return nil
}

func stepInvalidConfigPayload(ctx context.Context) error {
	worldFrom(ctx).config = []byte("not json")
	return nil
}

func stepParseConfig(ctx context.Context) error {
	w := worldFrom(ctx)
	w.parsed, w.err = providermanager.ParseConfig(w.config)
	return nil
}

func stepParsedCount(ctx context.Context, n int) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return w.err
	}
	if len(w.parsed) != n {
		return fmt.Errorf("parsed %d providers, want %d", len(w.parsed), n)
	}
	return nil
}

func stepFirstProvider(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	if len(w.parsed) == 0 || w.parsed[0].Name != name {
		return fmt.Errorf("first provider = %v, want %q", w.parsed, name)
	}
	return nil
}

func stepParsingFails(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.err == nil {
		return errors.New("parsing succeeded, want failure")
	}
	return nil
}

// --- model-manager ----------------------------------------------------------

func stepEmptyModelRegistry(ctx context.Context) error {
	w := worldFrom(ctx)
	w.models = modelmanager.NewRegistry()
	return nil
}

func stepBrokenView(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	if w.models == nil {
		w.models = modelmanager.NewRegistry()
	}
	w.err = w.models.Register(modelmanager.View{Name: name, Alias: "a", Fallback: []string{"b"}})
	return nil
}

func stepRegistryWithAlias(ctx context.Context, name, target string) error {
	stepEmptyModelRegistry(ctx)
	return stepViewAlias(ctx, name, target)
}

func stepViewAlias(ctx context.Context, name, target string) error {
	w := worldFrom(ctx)
	if w.models == nil {
		w.models = modelmanager.NewRegistry()
	}
	w.err = w.models.Register(modelmanager.View{Name: name, Alias: target})
	return nil
}

func stepAnotherView(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	w.err = w.models.Register(modelmanager.View{Name: name, Alias: "other"})
	return nil
}

func stepResolveName(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	w.res, w.err = w.models.Resolve(name)
	return nil
}

func stepResolutionMode(ctx context.Context, mode string) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return w.err
	}
	if string(w.res.Mode) != mode {
		return fmt.Errorf("mode = %q, want %q", w.res.Mode, mode)
	}
	return nil
}

func stepTargets(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return w.err
	}
	if got := quotedList(want); !equalStrings(w.res.Models, got) {
		return fmt.Errorf("targets = %v, want %v", w.res.Models, got)
	}
	return nil
}

func stepRegistryWithFallback(ctx context.Context, name, targets string) error {
	stepEmptyModelRegistry(ctx)
	return stepViewFallback(ctx, name, targets)
}

func stepViewFallback(ctx context.Context, name, targets string) error {
	w := worldFrom(ctx)
	if w.models == nil {
		w.models = modelmanager.NewRegistry()
	}
	w.err = w.models.Register(modelmanager.View{Name: name, Fallback: quotedList(targets)})
	return nil
}

func stepRegistryWithDiscuss(ctx context.Context, name, targets string) error {
	stepEmptyModelRegistry(ctx)
	w := worldFrom(ctx)
	w.err = w.models.Register(modelmanager.View{Name: name, Discuss: quotedList(targets)})
	return nil
}

func stepCycleError(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.err == nil || !strings.Contains(w.err.Error(), "cycle") {
		return fmt.Errorf("resolution error = %v, want a cycle error", w.err)
	}
	return nil
}

// --- chat-history -----------------------------------------------------------

func stepConversation(ctx context.Context, id string) error {
	w := worldFrom(ctx)
	w.store = chathistory.NewStore()
	w.conv = id
	return nil
}

func stepEmptyStore(ctx context.Context) error {
	w := worldFrom(ctx)
	w.store = chathistory.NewStore()
	return nil
}

func stepAppendUserTurn(ctx context.Context, text string) error {
	return appendTurn(ctx, chathistory.RoleUser, text)
}

func stepAppendAssistantTurn(ctx context.Context, text string) error {
	return appendTurn(ctx, chathistory.RoleAssistant, text)
}

func appendTurn(ctx context.Context, role, text string) error {
	w := worldFrom(ctx)
	if w.store == nil {
		w.store = chathistory.NewStore()
	}
	_, w.err = w.store.Append(w.conv, role, text)
	return nil
}

func stepConversationTurns(ctx context.Context, n int) error {
	w := worldFrom(ctx)
	if got := w.store.Len(w.conv); got != n {
		return fmt.Errorf("conversation has %d turns, want %d", got, n)
	}
	return nil
}

func stepNthTurn(i int) func(context.Context, string, string) error {
	return func(ctx context.Context, role, text string) error {
		w := worldFrom(ctx)
		msgs := w.store.Messages(w.conv)
		if len(msgs) <= i {
			return fmt.Errorf("conversation has %d turns, want at least %d", len(msgs), i+1)
		}
		if msgs[i].Role != role || msgs[i].Text != text {
			return fmt.Errorf("turn %d = %s: %s, want %s: %s", i, msgs[i].Role, msgs[i].Text, role, text)
		}
		return nil
	}
}

func stepAppendRole(ctx context.Context, role string) error {
	w := worldFrom(ctx)
	if w.store == nil {
		w.store = chathistory.NewStore()
	}
	_, w.err = w.store.Append(w.conv, role, "x")
	return nil
}

func stepAppendFails(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.err == nil {
		return errors.New("append succeeded, want failure")
	}
	return nil
}

func stepAppendUserTurnTo(ctx context.Context, text, id string) error {
	w := worldFrom(ctx)
	if w.store == nil {
		w.store = chathistory.NewStore()
	}
	_, w.err = w.store.Append(id, chathistory.RoleUser, text)
	return nil
}

func stepStoreLists(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if got := quotedList(want); !equalStrings(w.store.Conversations(), got) {
		return fmt.Errorf("store lists %v, want %v", w.store.Conversations(), got)
	}
	return nil
}

func stepConversationWithTurns(ctx context.Context, id, turns string) error {
	stepConversation(ctx, id)
	for _, text := range quotedList(turns) {
		if err := appendTurn(ctx, chathistory.RoleUser, text); err != nil {
			return err
		}
	}
	return nil
}

func stepRecentTurns(ctx context.Context, n int) error {
	w := worldFrom(ctx)
	w.turns = w.store.Recent(w.conv, n)
	return nil
}

func stepTurnTexts(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	var texts []string
	for _, m := range w.turns {
		texts = append(texts, m.Text)
	}
	if got := quotedList(want); !equalStrings(texts, got) {
		return fmt.Errorf("turns = %v, want %v", texts, got)
	}
	return nil
}

func stepStoreConversations(ctx context.Context, first, second string) error {
	stepEmptyStore(ctx)
	w := worldFrom(ctx)
	for _, id := range []string{first, second} {
		if err := w.store.Start(id); err != nil {
			return err
		}
	}
	return nil
}

func stepConversationList(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if got := quotedList(want); !equalStrings(w.store.Conversations(), got) {
		return fmt.Errorf("conversation list = %v, want %v", w.store.Conversations(), got)
	}
	return nil
}

// --- context-manager --------------------------------------------------------

func stepHistoryBudget(ctx context.Context, messages string, budget int) error {
	w := worldFrom(ctx)
	w.history = nil
	for _, text := range quotedList(messages) {
		w.history = append(w.history, contextmanager.Message{Text: text})
	}
	w.budget = budget
	return nil
}

func stepEmptyHistoryBudget(ctx context.Context, budget int) error {
	w := worldFrom(ctx)
	w.history = nil
	w.budget = budget
	return nil
}

func stepProject(ctx context.Context) error {
	w := worldFrom(ctx)
	w.window = contextmanager.Project(w.history, w.budget)
	return nil
}

func stepWindowKeeps(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	var texts []string
	for _, m := range w.window.Messages {
		texts = append(texts, m.Text)
	}
	if got := quotedList(want); !equalStrings(texts, got) {
		return fmt.Errorf("window keeps %v, want %v", texts, got)
	}
	return nil
}

func stepNoDrops(ctx context.Context) error {
	if w := worldFrom(ctx); w.window.Dropped != 0 {
		return fmt.Errorf("dropped = %d, want 0", w.window.Dropped)
	}
	return nil
}

func stepOneDrop(ctx context.Context) error {
	if w := worldFrom(ctx); w.window.Dropped != 1 {
		return fmt.Errorf("dropped = %d, want 1", w.window.Dropped)
	}
	return nil
}

func stepWindowEmpty(ctx context.Context) error {
	if w := worldFrom(ctx); len(w.window.Messages) != 0 {
		return fmt.Errorf("window keeps %d messages, want none", len(w.window.Messages))
	}
	return nil
}

// --- repl-chat (library) ----------------------------------------------------

func stepSession(ctx context.Context, nick string) error {
	w := worldFrom(ctx)
	w.nick = nick
	w.session = replchat.New(nick)
	return nil
}

func (w *world) responder() func(string) (string, error) {
	return func(line string) (string, error) {
		if w.failNext {
			w.failNext = false
			return "", errors.New("pipeline down")
		}
		return fmt.Sprintf("turn %d: %s", w.session.Turn(), line), nil
	}
}

func stepUserEnters(ctx context.Context, line string) error {
	w := worldFrom(ctx)
	if w.sys != nil {
		return w.sys.enter(line)
	}
	if w.session == nil {
		w.session = replchat.New("agent")
	}
	w.lastLine = line
	w.reply, w.err = w.session.Handle(line, w.responder())
	return nil
}

func stepUserEntersBlank(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.sys != nil {
		w.responsesBefore = len(w.sys.responses())
		w.sys.send("")
		return nil
	}
	if w.session == nil {
		w.session = replchat.New("agent")
	}
	w.reply, w.err = w.session.Handle("   ", w.responder())
	return nil
}

func stepReplyNames(ctx context.Context, first, second string) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return w.err
	}
	for _, want := range []string{first, second} {
		if !strings.Contains(w.reply.Text, want) {
			return fmt.Errorf("reply %q does not name %q", w.reply.Text, want)
		}
	}
	return nil
}

func stepReplyNotQuit(ctx context.Context) error {
	if w := worldFrom(ctx); w.reply.Quit {
		return errors.New("reply ended the session")
	}
	return nil
}

func stepReplyQuit(ctx context.Context) error {
	if w := worldFrom(ctx); !w.reply.Quit {
		return errors.New("reply did not end the session")
	}
	return nil
}

func stepReplyNoText(ctx context.Context) error {
	if w := worldFrom(ctx); w.reply.Text != "" {
		return fmt.Errorf("reply = %q, want no text", w.reply.Text)
	}
	return nil
}

func stepReplyOneLine(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return w.err
	}
	if strings.Contains(w.reply.Text, "\n") {
		return fmt.Errorf("reply has %d lines", strings.Count(w.reply.Text, "\n")+1)
	}
	if w.reply.Text == "" {
		return errors.New("reply is empty")
	}
	return nil
}

func stepReplyIsResponder(ctx context.Context) error {
	w := worldFrom(ctx)
	want := fmt.Sprintf("turn %d: %s", w.session.Turn(), w.lastLine)
	if w.reply.Text != want {
		return fmt.Errorf("reply = %q, want %q", w.reply.Text, want)
	}
	return nil
}

func stepResponderFailsOnce(ctx context.Context) error {
	w := worldFrom(ctx)
	w.events = nil
	w.failNext = true
	err := w.session.Run(strings.NewReader("bad\n:quit\n"), func(e string) {
		w.events = append(w.events, e)
	}, w.responder())
	if err != nil {
		return err
	}
	return nil
}

func stepTranscriptError(ctx context.Context) error {
	w := worldFrom(ctx)
	if joined := strings.Join(w.events, ""); !strings.Contains(joined, "repl: pipeline down") {
		return fmt.Errorf("transcript does not report the error:\n%s", joined)
	}
	return nil
}

func stepSessionRunning(ctx context.Context) error {
	w := worldFrom(ctx)
	if n := strings.Count(strings.Join(w.events, ""), "you> "); n < 2 {
		return fmt.Errorf("session stopped after the error: %d prompts", n)
	}
	return nil
}

func stepReplyNamesTurn(ctx context.Context, turn int) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return w.err
	}
	if marker := fmt.Sprintf("turn %d:", turn); !strings.Contains(w.reply.Text, marker) {
		return fmt.Errorf("reply %q does not name %q", w.reply.Text, marker)
	}
	return nil
}

func stepTranscriptAnnounces(ctx context.Context, prefix string) error {
	w := worldFrom(ctx)
	if !strings.Contains(w.sys.out.String(), prefix) {
		return fmt.Errorf("transcript does not announce %q:\n%s", prefix, w.sys.out.String())
	}
	return nil
}

// --- provider → model mapping ----------------------------------------------

func stepRegisterProviderServing(ctx context.Context, name, endpoint, model string) error {
	w := worldFrom(ctx)
	if w.providers == nil {
		w.providers = providermanager.NewRegistry()
	}
	w.err = w.providers.Register(providermanager.Provider{
		Name:       name,
		Endpoint:   endpoint,
		Credential: "env:TEST_KEY",
		Models:     []string{model},
	})
	return nil
}

func stepServedBy(ctx context.Context, model, name string) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return w.err
	}
	p, ok := w.providers.ProviderFor(model)
	if !ok || p.Name != name {
		return fmt.Errorf("ProviderFor(%q) = %+v, %v; want %q", model, p, ok, name)
	}
	return nil
}

func stepNoProvider(ctx context.Context, model string) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return w.err
	}
	if p, ok := w.providers.ProviderFor(model); ok {
		return fmt.Errorf("ProviderFor(%q) = %+v, want no provider", model, p)
	}
	return nil
}

// --- model-manager response ------------------------------------------------

// stubCaller stands in for the provider transport in library scenarios.
type stubCaller struct{ failOn map[string]bool }

func (c *stubCaller) Complete(model string, _ []modelmanager.ContextMessage) (string, error) {
	if c.failOn[model] {
		return "", fmt.Errorf("call %s failed", model)
	}
	return "answer from " + model, nil
}

func stepAskToRespond(ctx context.Context, model string) error {
	w := worldFrom(ctx)
	caller := w.caller
	if !w.noCaller && caller == nil {
		caller = modelmanager.MockCaller{}
	}
	w.answer, w.err = modelmanager.Apply(w.models, modelmanager.Op{Kind: "respond", Model: model}, caller)
	return nil
}

func stepCallerFailsFor(ctx context.Context, model string) error {
	w := worldFrom(ctx)
	stub, _ := w.caller.(*stubCaller)
	if stub == nil {
		stub = &stubCaller{}
		w.caller = stub
	}
	if stub.failOn == nil {
		stub.failOn = map[string]bool{}
	}
	stub.failOn[model] = true
	return nil
}

func stepNoCaller(ctx context.Context) error {
	worldFrom(ctx).noCaller = true
	return nil
}

func stepAnswerFrom(ctx context.Context, model string) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return fmt.Errorf("respond failed: %w", w.err)
	}
	if w.answer.Model != model {
		return fmt.Errorf("answer model = %q, want %q", w.answer.Model, model)
	}
	return nil
}

func stepAnswerText(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	if w.err != nil {
		return fmt.Errorf("respond failed: %w", w.err)
	}
	if w.answer.Text != want {
		return fmt.Errorf("answer text = %q, want %q", w.answer.Text, want)
	}
	return nil
}

func stepRegisterMockProvider(ctx context.Context, name, model string) error {
	w := worldFrom(ctx)
	if w.providers == nil {
		w.providers = providermanager.NewRegistry()
	}
	w.err = w.providers.Register(providermanager.Provider{Name: name, Mock: true, Models: []string{model}})
	return nil
}

func stepResponseFails(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.err == nil {
		return errors.New("respond succeeded, want failure")
	}
	return nil
}
