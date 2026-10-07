package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/DaviMGDev/core-agent/plugins/agent"
	"github.com/DaviMGDev/core-agent/plugins/subagent"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// subWake is the agent request the subagent runner sends.
type subWake struct {
	Line   string        `json:"line,omitempty"`
	Events []subEvent    `json:"events,omitempty"`
	Config *agent.Config `json:"config,omitempty"`
	Dump   bool          `json:"dump,omitempty"`
}

// subEvent is one bus event in a wake.
type subEvent struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// fakeSubAgent is the agent-loop surface a subagent scenario calls: it records
// the wakes it receives and answers from a script.
type fakeSubAgent struct {
	mu     sync.Mutex
	wakes  []subWake
	answer func(w subWake) (text, job string)
	block  chan struct{}
}

func (a *fakeSubAgent) Handle(ctx context.Context, req []byte) ([]byte, error) {
	var w subWake
	if err := json.Unmarshal(req, &w); err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.wakes = append(a.wakes, w)
	a.mu.Unlock()
	if a.block != nil {
		select {
		case <-a.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	text, job := "", ""
	if a.answer != nil {
		text, job = a.answer(w)
	}
	out := map[string]any{}
	if text != "" {
		out["text"] = text
	}
	if job != "" {
		out["job"] = job
	}
	return json.Marshal(out)
}

func (a *fakeSubAgent) snapshot() []subWake {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]subWake(nil), a.wakes...)
}

func registerSubagentSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a manager with a subagent tool$`, stepManagerSubagentTool)
	sc.Step(`^a manager with a blocking subagent tool$`, stepManagerBlockingSubagentTool)
	sc.Step(`^a subagent is started with the brief "([^"]*)"$`, stepStartSubagent)
	sc.Step(`^the subagent job reaches done with reply "([^"]*)"$`, stepSubagentDoneWithReply)
	sc.Step(`^peep reports the subagent job running$`, stepSubagentRunning)
	sc.Step(`^the subagent job is killed$`, stepKillSubagent)
	sc.Step(`^the subagent job is killed at once$`, stepSubagentKilled)
	sc.Step(`^the child's first wake carries only "([^"]*)"$`, stepChildFirstWakeBrief)
	sc.Step(`^the child's conversation is its own$`, stepChildConversationOwn)
	sc.Step(`^the two calls use different conversations$`, stepTwoConversations)
}

// newSubagentWorld wires a manager with the subagent tool over the fake agent.
func newSubagentWorld(w *world) {
	w.subAgent = &fakeSubAgent{answer: func(w subWake) (string, string) {
		return "child: " + w.Line, ""
	}}
	w.tmRecorder = &tmRecorder{}
	w.subManager = toolmanager.New(toolmanager.Options{Publisher: w.tmRecorder})
	if err := w.subManager.Registry().Declare(subagent.Tool(w.subAgent, w.subManager, subagent.Options{})); err != nil {
		w.err = err
	}
}

func stepManagerSubagentTool(ctx context.Context) error {
	newSubagentWorld(worldFrom(ctx))
	return worldFrom(ctx).err
}

func stepManagerBlockingSubagentTool(ctx context.Context) error {
	w := worldFrom(ctx)
	newSubagentWorld(w)
	if w.err != nil {
		return w.err
	}
	w.subBlock = make(chan struct{})
	w.subAgent.block = w.subBlock
	return nil
}

func stepStartSubagent(ctx context.Context, brief string) error {
	w := worldFrom(ctx)
	args, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return err
	}
	w.subJob = w.subManager.Start(subagent.ToolName, args, 0)
	return nil
}

func stepSubagentDoneWithReply(ctx context.Context, reply string) error {
	w := worldFrom(ctx)
	status := w.subJob.Wait()
	if status.State != toolmanager.StateDone {
		return fmt.Errorf("subagent job = %s (%s), want done", status.State, status.Error)
	}
	res, ok := status.Result.(subagent.Result)
	if !ok {
		return fmt.Errorf("subagent result = %#v, want subagent.Result", status.Result)
	}
	if res.Reply != reply {
		return fmt.Errorf("subagent reply = %q, want %q", res.Reply, reply)
	}
	return nil
}

func stepSubagentRunning(ctx context.Context) error {
	w := worldFrom(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if w.subJob.State() == toolmanager.StateRunning {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("subagent job = %s, want running", w.subJob.State())
}

func stepKillSubagent(ctx context.Context) error {
	w := worldFrom(ctx)
	w.subManager.Kill(w.subJob)
	return nil
}

func stepSubagentKilled(ctx context.Context) error {
	w := worldFrom(ctx)
	if st := w.subJob.State(); st != toolmanager.StateKilled {
		return fmt.Errorf("subagent job = %s, want killed", st)
	}
	return nil
}

// waitSubWakes waits until the fake agent has seen n wakes.
func waitSubWakes(w *world, n int) ([]subWake, error) {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if wakes := w.subAgent.snapshot(); len(wakes) >= n {
			return wakes, nil
		}
		time.Sleep(time.Millisecond)
	}
	return nil, fmt.Errorf("child saw %d wakes, want %d", len(w.subAgent.snapshot()), n)
}

func stepChildFirstWakeBrief(ctx context.Context, brief string) error {
	w := worldFrom(ctx)
	wakes, err := waitSubWakes(w, 1)
	if err != nil {
		return err
	}
	first := wakes[0]
	if first.Line != brief || len(first.Events) != 0 {
		return fmt.Errorf("child's first wake = {%q, %d events}, want only the brief %q", first.Line, len(first.Events), brief)
	}
	return nil
}

func stepChildConversationOwn(ctx context.Context) error {
	w := worldFrom(ctx)
	wakes, err := waitSubWakes(w, 1)
	if err != nil {
		return err
	}
	cfg := wakes[0].Config
	if cfg == nil || cfg.Conversation == "" {
		return fmt.Errorf("child's wake carries no conversation: %+v", cfg)
	}
	if cfg.Conversation == "main" {
		return fmt.Errorf("child runs on the parent's conversation %q", cfg.Conversation)
	}
	return nil
}

func stepTwoConversations(ctx context.Context) error {
	w := worldFrom(ctx)
	wakes, err := waitSubWakes(w, 2)
	if err != nil {
		return err
	}
	first, second := wakes[0].Config, wakes[1].Config
	if first == nil || second == nil || first.Conversation == "" || second.Conversation == "" {
		return fmt.Errorf("calls carry conversations %+v and %+v, want both set", first, second)
	}
	if first.Conversation == second.Conversation {
		return fmt.Errorf("both calls use conversation %q, want their own", first.Conversation)
	}
	return nil
}
