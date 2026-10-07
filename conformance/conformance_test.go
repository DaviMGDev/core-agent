// Package conformance runs the Gherkin contract in specs/ and
// plugins/*/specs/ against the implementation with Godog.
package conformance

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/DaviMGDev/core-agent/plugins/agent"
	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	contextmanager "github.com/DaviMGDev/core-agent/plugins/context-manager"
	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
	"github.com/DaviMGDev/core-agent/plugins/notifications"
	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	replchat "github.com/DaviMGDev/core-agent/plugins/repl-chat"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
	spc "github.com/DaviMGDev/memento/context"
	rt "github.com/DaviMGDev/memento/runtime"
)

// TestFeatures runs every system and plugin feature file against the
// implementation. Plugin scenarios exercise the host-testable libraries;
// system scenarios compose the real wasm guests.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "conformance",
		ScenarioInitializer: InitializeScenario,
		Options: &godog.Options{
			Format: "pretty",
			Strict: true,
			Paths: []string{
				"../specs/features/composition.feature",
				"../specs/features/repl.feature",
				"../specs/features/config.feature",
				"../plugins/repl-chat/specs/features/repl-chat.feature",
				"../plugins/provider-manager/specs/features/provider-manager.feature",
				"../plugins/model-manager/specs/features/model-manager.feature",
				"../plugins/chat-history/specs/features/chat-history.feature",
				"../plugins/context-manager/specs/features/context-manager.feature",
				"../plugins/notifications/specs/features/notifications.feature",
				"../plugins/tool-manager/specs/features/tool-manager.feature",
				"../plugins/subagent/specs/features/subagent.feature",
				"../plugins/agent/specs/features/agent.feature",
			},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("conformance scenarios failed")
	}
}

// InitializeScenario wires a fresh world into every scenario.
func InitializeScenario(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return context.WithValue(ctx, worldKey{}, newWorld()), nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		worldFrom(ctx).close()
		return ctx, nil
	})
	registerPluginSteps(sc)
	registerNotificationsSteps(sc)
	registerToolManagerSteps(sc)
	registerSubagentSteps(sc)
	registerAgentSteps(sc)
	registerSystemSteps(sc)
	registerConfigSteps(sc)
}

type worldKey struct{}

func worldFrom(ctx context.Context) *world { return ctx.Value(worldKey{}).(*world) }

// safeBuffer is a concurrency-safe transcript sink.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// world is the state one scenario observes.
type world struct {
	nick string

	// provider-manager
	providers *providermanager.Registry
	config    []byte
	parsed    []providermanager.Provider

	// model-manager
	models   *modelmanager.Registry
	res      modelmanager.Resolution
	caller   modelmanager.Caller
	noCaller bool
	answer   modelmanager.Result

	// chat-history
	store *chathistory.Store
	conv  string
	msgs  []chathistory.Message
	turns []chathistory.Message

	// context-manager
	history []contextmanager.Message
	budget  int
	window  contextmanager.Window

	// repl-chat library
	session     *replchat.Session
	reply       replchat.Reply
	events      []string
	failNext    bool
	lastLine    string
	transcripts int

	// notifications
	bus         *notifications.Bus
	sub         *notifications.Subscription
	waker       *testWaker
	subWakers   map[string]*testWaker
	publishDone bool

	// agent
	agentCfg agent.Config
	agentRes agent.TurnResult
	agentRec *agentRecorder

	// tool-manager
	tmRegistry *toolmanager.Registry
	tmManager  *toolmanager.Manager
	tmRecorder *tmRecorder
	tmJob      *toolmanager.Job
	tmRelease  chan struct{}
	tmDeclErr  error

	// subagent
	subAgent   *fakeSubAgent
	subManager *toolmanager.Manager
	subJob     *toolmanager.Job
	subBlock   chan struct{}
	subInst    *rt.Instance
	subSched   *rt.Scheduler
	subChild   *toolmanager.Job
	subEvents  <-chan toolmanager.Event

	// shared
	err error

	// system fixture
	sys             *systemSession
	firstResponse   string
	responsesBefore int
	doneErr         error

	// kernel-level admission
	sched       *rt.Scheduler
	keys        map[string]spc.Key[any]
	countBefore int
}

func newWorld() *world { return &world{conv: "main"} }

func (w *world) close() {
	if w.sys != nil {
		w.sys.stop()
	}
	if w.sched != nil {
		w.sched.Close()
	}
	if w.waker != nil {
		w.waker.releaseAll()
	}
	for _, tw := range w.subWakers {
		tw.releaseAll()
	}
	if w.tmRelease != nil {
		select {
		case <-w.tmRelease:
		default:
			close(w.tmRelease)
		}
	}
	if w.tmManager != nil {
		w.tmManager.Close()
	}
	if w.subBlock != nil {
		select {
		case <-w.subBlock:
		default:
			close(w.subBlock)
		}
	}
	if w.subManager != nil {
		w.subManager.Close()
	}
	if w.subSched != nil {
		_ = w.subSched.Close()
	}
}

// quotedList extracts the doubly quoted strings of a step argument, so
// Gherkin lists like ["a", "b"] or "a", "b" share one parser.
func quotedList(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, '"')
		if i < 0 {
			return out
		}
		s = s[i+1:]
		j := strings.IndexByte(s, '"')
		if j < 0 {
			return out
		}
		out = append(out, s[:j])
		s = s[j+1:]
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
