package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/DaviMGDev/core-agent/plugins/agent"
	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
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
	// Mirror the guest: a dump answer carries the turns the wake ran on.
	if w.Dump && text != "" {
		out["conversation"] = []agent.Message{
			{Role: "user", Text: w.Line},
			{Role: "assistant", Text: text},
		}
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
	sc.Step(`^a subagent is started with the brief "([^"]*)" asking for the conversation$`, stepStartSubagentConversation)
	sc.Step(`^the result carries no conversation$`, stepResultNoConversation)
	sc.Step(`^the result carries the child's conversation$`, stepResultConversation)
	sc.Step(`^a manager with a subagent tool configured with model "([^"]*)"$`, stepManagerSubagentModel)
	sc.Step(`^a manager with a subagent tool resolving models$`, stepManagerSubagentResolving)
	sc.Step(`^a subagent is started with the brief "([^"]*)" on model "([^"]*)"$`, stepStartSubagentModel)
	sc.Step(`^the child's wake names the model "([^"]*)"$`, stepChildWakeModel)
}

// newSubagentWorld wires a manager with the subagent tool over the fake agent.
func newSubagentWorld(w *world, opts subagent.Options) {
	w.subAgent = &fakeSubAgent{answer: func(w subWake) (string, string) {
		return "child: " + w.Line, ""
	}}
	w.tmRecorder = &tmRecorder{}
	w.subManager = toolmanager.New(toolmanager.Options{Publisher: w.tmRecorder})
	if err := w.subManager.Registry().Declare(subagent.Tool(w.subAgent, w.subManager, opts)); err != nil {
		w.err = err
	}
}

func stepManagerSubagentTool(ctx context.Context) error {
	newSubagentWorld(worldFrom(ctx), subagent.Options{})
	return worldFrom(ctx).err
}

func stepManagerBlockingSubagentTool(ctx context.Context) error {
	w := worldFrom(ctx)
	newSubagentWorld(w, subagent.Options{})
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

func stepManagerSubagentModel(ctx context.Context, model string) error {
	w := worldFrom(ctx)
	newSubagentWorld(w, subagent.Options{Model: model})
	return w.err
}

func stepManagerSubagentResolving(ctx context.Context) error {
	w := worldFrom(ctx)
	newSubagentWorld(w, subagent.Options{})
	if w.err != nil {
		return w.err
	}
	views, err := modelmanager.ParseConfig([]byte(sysModelConfig))
	if err != nil {
		return err
	}
	reg := modelmanager.NewRegistry()
	for _, v := range views {
		if err := reg.Register(v); err != nil {
			return err
		}
	}
	w.subAgent.answer = func(wk subWake) (string, string) {
		res, err := modelmanager.Apply(reg, modelmanager.Op{
			Kind: "respond", Model: wk.Config.Model,
			Context: []modelmanager.ContextMessage{{Role: "user", Text: wk.Line}},
		}, modelmanager.MockCaller{})
		if err != nil {
			return "model error: " + err.Error(), ""
		}
		return res.Text, ""
	}
	return nil
}

func stepStartSubagentModel(ctx context.Context, brief, model string) error {
	w := worldFrom(ctx)
	args, err := json.Marshal(map[string]string{"brief": brief, "model": model})
	if err != nil {
		return err
	}
	w.subJob = w.subManager.Start(subagent.ToolName, args, 0)
	return nil
}

func stepChildWakeModel(ctx context.Context, model string) error {
	w := worldFrom(ctx)
	wakes, err := waitSubWakes(w, 1)
	if err != nil {
		return err
	}
	cfg := wakes[0].Config
	if cfg == nil {
		return errors.New("child's wake carries no config")
	}
	if cfg.Model != model {
		return fmt.Errorf("child's model = %q, want %q", cfg.Model, model)
	}
	return nil
}

func stepStartSubagentConversation(ctx context.Context, brief string) error {
	w := worldFrom(ctx)
	args, err := json.Marshal(map[string]string{"brief": brief, "return": "conversation"})
	if err != nil {
		return err
	}
	w.subJob = w.subManager.Start(subagent.ToolName, args, 0)
	return nil
}

func stepResultNoConversation(ctx context.Context) error {
	w := worldFrom(ctx)
	st := w.subJob.Wait()
	res, ok := st.Result.(subagent.Result)
	if !ok {
		return fmt.Errorf("subagent result = %#v, want subagent.Result", st.Result)
	}
	if len(res.Conversation) != 0 {
		return fmt.Errorf("result carries %d conversation turns, want none", len(res.Conversation))
	}
	return nil
}

func stepResultConversation(ctx context.Context) error {
	w := worldFrom(ctx)
	st := w.subJob.Wait()
	res, ok := st.Result.(subagent.Result)
	if !ok {
		return fmt.Errorf("subagent result = %#v, want subagent.Result", st.Result)
	}
	if len(res.Conversation) != 2 {
		return fmt.Errorf("result carries %d conversation turns, want the child's two", len(res.Conversation))
	}
	if res.Conversation[0].Role != "user" || res.Conversation[1].Role != "assistant" {
		return fmt.Errorf("conversation roles = %q, %q; want user, assistant", res.Conversation[0].Role, res.Conversation[1].Role)
	}
	return nil
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
