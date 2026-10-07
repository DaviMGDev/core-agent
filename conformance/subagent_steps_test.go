package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/DaviMGDev/core-agent/plugins/agent"
	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
	"github.com/DaviMGDev/core-agent/plugins/subagent"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
	"github.com/DaviMGDev/memento/runtime"
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
	mu       sync.Mutex
	wakes    []subWake
	answer   func(w subWake) (text, job string)
	block    chan struct{}
	recurse  bool
	inst     *runtime.Instance
	manager  *toolmanager.Manager
	children []string
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
	if a.recurse && len(w.Events) == 0 && w.Line != "" && a.inst != nil && a.manager != nil {
		raw, err := a.manager.StartJob(a.inst, []byte(`{"tool":"subagent","args":{"brief":"recurse"}}`))
		if err != nil {
			return nil, err
		}
		var handle struct {
			Job string `json:"job"`
		}
		if err := json.Unmarshal(raw, &handle); err != nil {
			return nil, err
		}
		a.mu.Lock()
		a.children = append(a.children, handle.Job)
		a.mu.Unlock()
		return json.Marshal(map[string]any{"job": handle.Job})
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

// deepest returns the deepest job the fake agent has started. Nested turns
// can append their handles out of order, so the tree decides, not the list.
func (a *fakeSubAgent) deepest(m *toolmanager.Manager) (*toolmanager.Job, bool) {
	a.mu.Lock()
	ids := append([]string(nil), a.children...)
	a.mu.Unlock()
	var best *toolmanager.Job
	for _, id := range ids {
		job, ok := m.Job(id)
		if !ok {
			continue
		}
		if best == nil || job.Depth() > best.Depth() {
			best = job
		}
	}
	return best, best != nil
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
	sc.Step(`^a manager with a recursing subagent tool$`, stepManagerRecursingSubagent)
	sc.Step(`^the subagent chain is started$`, stepStartSubagentChain)
	sc.Step(`^the innermost job fails with a depth reason$`, stepInnermostDepthFailure)
	sc.Step(`^a manager with a parent job and a child job$`, stepManagerParentChild)
	sc.Step(`^a manager with a running parent job and child job$`, stepManagerRunningParentChild)
	sc.Step(`^the parent job is killed$`, stepKillParent)
	sc.Step(`^the child job is killed at once$`, stepChildKilled)
	sc.Step(`^the child job completes$`, stepChildCompletes)
	sc.Step(`^the parent's listener receives the child's events$`, stepParentListenerEvents)
	sc.Step(`^the bus saw only the root job's start$`, stepBusRootOnly)
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

// instCapture records the instance the scheduler activates it on, so a
// subagent scenario can speak for a guest caller.
type instCapture struct {
	inst  *runtime.Instance
	ready chan struct{}
}

func (c *instCapture) Declarations() runtime.Declarations { return runtime.Declarations{} }
func (c *instCapture) Activate(inst *runtime.Instance, _ any) error {
	c.inst = inst
	close(c.ready)
	return nil
}

func captureInstance() (*runtime.Instance, *runtime.Scheduler, error) {
	c := &instCapture{ready: make(chan struct{})}
	s := runtime.New()
	if _, err := s.Insert(c, nil); err != nil {
		_ = s.Close()
		return nil, nil, err
	}
	select {
	case <-c.ready:
	case <-time.After(3 * time.Second):
		_ = s.Close()
		return nil, nil, errors.New("capture component did not activate")
	}
	return c.inst, s, nil
}

func stepManagerRecursingSubagent(ctx context.Context) error {
	w := worldFrom(ctx)
	newSubagentWorld(w, subagent.Options{})
	if w.err != nil {
		return w.err
	}
	inst, sched, err := captureInstance()
	if err != nil {
		return err
	}
	w.subSched = sched
	w.subInst = inst
	w.subAgent.inst = inst
	w.subAgent.manager = w.subManager
	w.subAgent.recurse = true
	return nil
}

func stepStartSubagentChain(ctx context.Context) error {
	w := worldFrom(ctx)
	raw, err := w.subManager.StartJob(w.subInst, []byte(`{"tool":"subagent","args":{"brief":"recurse"}}`))
	if err != nil {
		return err
	}
	var handle struct {
		Job string `json:"job"`
	}
	if err := json.Unmarshal(raw, &handle); err != nil {
		return err
	}
	job, ok := w.subManager.Job(handle.Job)
	if !ok {
		return fmt.Errorf("chain root %q not found", handle.Job)
	}
	w.subJob = job
	return nil
}

func stepInnermostDepthFailure(ctx context.Context) error {
	w := worldFrom(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if job, ok := w.subAgent.deepest(w.subManager); ok && job.Depth() > subagent.DefaultDepth {
			st := job.Wait()
			if st.State != toolmanager.StateFailed {
				return fmt.Errorf("job at depth %d = %s, want failed", job.Depth(), st.State)
			}
			if !strings.Contains(st.Error, "depth") {
				return fmt.Errorf("job at depth %d error = %q, want a depth reason", job.Depth(), st.Error)
			}
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return errors.New("no job past the depth bound was started")
}

// setupParentChild builds a root job and a job adopted under it, both of the
// given child tool. The root is a blocking "slow" job, so it stays running.
func setupParentChild(w *world, childTool string) error {
	w.tmRecorder = &tmRecorder{}
	w.subManager = toolmanager.New(toolmanager.Options{Publisher: w.tmRecorder})
	w.subBlock = make(chan struct{})
	block := w.subBlock
	if err := w.subManager.Registry().Declare(toolmanager.Tool{Name: "slow", Run: func(ctx context.Context, _ json.RawMessage, _ *toolmanager.Output) (any, error) {
		select {
		case <-block:
			return "released", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}); err != nil {
		return err
	}
	if err := w.subManager.Registry().Declare(toolmanager.Tool{Name: "echo", Run: func(context.Context, json.RawMessage, *toolmanager.Output) (any, error) {
		return "hi", nil
	}}); err != nil {
		return err
	}
	inst, sched, err := captureInstance()
	if err != nil {
		return err
	}
	w.subSched, w.subInst = sched, inst
	w.subJob = w.subManager.Start("slow", nil, 0)
	w.subManager.Attribute(inst, w.subJob)
	w.subEvents = w.subManager.Listen(w.subJob)
	raw, err := w.subManager.StartJob(inst, []byte(`{"tool":"`+childTool+`"}`))
	if err != nil {
		return err
	}
	var handle struct {
		Job string `json:"job"`
	}
	if err := json.Unmarshal(raw, &handle); err != nil {
		return err
	}
	job, ok := w.subManager.Job(handle.Job)
	if !ok {
		return fmt.Errorf("child job %q not found", handle.Job)
	}
	w.subChild = job
	return nil
}

func stepManagerParentChild(ctx context.Context) error {
	return setupParentChild(worldFrom(ctx), "echo")
}

func stepManagerRunningParentChild(ctx context.Context) error {
	return setupParentChild(worldFrom(ctx), "slow")
}

func stepKillParent(ctx context.Context) error {
	w := worldFrom(ctx)
	w.subManager.Kill(w.subJob)
	return nil
}

func stepChildKilled(ctx context.Context) error {
	w := worldFrom(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if w.subChild.State() == toolmanager.StateKilled {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("child job = %s, want killed", w.subChild.State())
}

func stepChildCompletes(ctx context.Context) error {
	w := worldFrom(ctx)
	if st := w.subChild.Wait(); st.State != toolmanager.StateDone {
		return fmt.Errorf("child job = %s (%s), want done", st.State, st.Error)
	}
	return nil
}

func stepParentListenerEvents(ctx context.Context) error {
	w := worldFrom(ctx)
	var topics []string
	deadline := time.Now().Add(3 * time.Second)
	for len(topics) < 2 && time.Now().Before(deadline) {
		select {
		case ev := <-w.subEvents:
			topics = append(topics, ev.Topic)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if len(topics) != 2 || topics[0] != "job.started" || topics[1] != "job.completed" {
		return fmt.Errorf("parent listener topics = %v, want the child's start and completion", topics)
	}
	return nil
}

func stepBusRootOnly(ctx context.Context) error {
	w := worldFrom(ctx)
	if got := len(w.tmRecorder.byTopic("job.started")); got != 1 {
		return fmt.Errorf("bus job.started = %d, want only the root job's", got)
	}
	if got := len(w.tmRecorder.byTopic("job.completed")); got != 0 {
		return fmt.Errorf("bus job.completed = %d, want the child's event to stay with the parent", got)
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
