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

	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// tmEvent is one recorded job event.
type tmEvent struct {
	topic   string
	payload map[string]any
}

// tmRecorder is a Publisher that records job events for scenarios.
type tmRecorder struct {
	mu     sync.Mutex
	events []tmEvent
}

func (r *tmRecorder) Publish(topic string, payload []byte) {
	var doc map[string]any
	_ = json.Unmarshal(payload, &doc)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, tmEvent{topic: topic, payload: doc})
}

func (r *tmRecorder) snapshot() []tmEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tmEvent(nil), r.events...)
}

func (r *tmRecorder) byTopic(topic string) []tmEvent {
	var out []tmEvent
	for _, e := range r.snapshot() {
		if e.topic == topic {
			out = append(out, e)
		}
	}
	return out
}

func blockingRunner(release chan struct{}) toolmanager.Runner {
	return func(ctx context.Context, args json.RawMessage, out *toolmanager.Output) (any, error) {
		select {
		case <-release:
			return "released", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func chattyRunner(release chan struct{}) toolmanager.Runner {
	return func(ctx context.Context, args json.RawMessage, out *toolmanager.Output) (any, error) {
		_, _ = out.Write([]byte("partial"))
		select {
		case <-release:
			return "released", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func echoRunner(answer string) toolmanager.Runner {
	return func(context.Context, json.RawMessage, *toolmanager.Output) (any, error) {
		return answer, nil
	}
}

func registerToolManagerSteps(sc *godog.ScenarioContext) {
	sc.Step(`^an empty tool registry$`, stepEmptyToolRegistry)
	sc.Step(`^a tool "([^"]*)" with description "([^"]*)" is declared$`, stepDeclareTool)
	sc.Step(`^the registry lists "([^"]*)"$`, stepRegistryLists)
	sc.Step(`^a registry with tool "([^"]*)"$`, stepRegistryWithTool)
	sc.Step(`^another tool "([^"]*)" is declared$`, stepDeclareAnotherTool)
	sc.Step(`^the declaration fails$`, stepDeclarationFails)
	sc.Step(`^the registry still lists one tool$`, stepRegistryOneTool)
	sc.Step(`^a manager with a blocking tool "([^"]*)"$`, stepManagerBlockingTool)
	sc.Step(`^a manager with a 10ms tick and a blocking tool "([^"]*)"$`, stepManagerTickBlockingTool)
	sc.Step(`^a manager with tool "([^"]*)" answering "([^"]*)"$`, stepManagerEchoTool)
	sc.Step(`^a manager with a failing tool "([^"]*)"$`, stepManagerFailingTool)
	sc.Step(`^a manager with no tools$`, stepManagerNoTools)
	sc.Step(`^a manager with a tool "([^"]*)" that writes and blocks$`, stepManagerChattyTool)
	sc.Step(`^the tool "([^"]*)" is started$`, stepStartTool)
	sc.Step(`^the tool "([^"]*)" is started and writes$`, stepStartAndWrites)
	sc.Step(`^the call returns a job handle before the tool finishes$`, stepHandleBeforeFinish)
	sc.Step(`^the job reaches done with result "([^"]*)"$`, stepJobDoneWithResult)
	sc.Step(`^the job reaches failed with an error$`, stepJobFailedWithError)
	sc.Step(`^the job fails at once with reason "([^"]*)"$`, stepJobFailsAtOnce)
	sc.Step(`^peep reports state "([^"]*)" and the output so far$`, stepPeepStateOutput)
	sc.Step(`^a job\.tick event carries elapsed time$`, stepTickEventElapsed)
	sc.Step(`^a job\.completed event carries the result "([^"]*)"$`, stepCompletedEventResult)
	sc.Step(`^the running job is killed$`, stepKillRunningJob)
	sc.Step(`^the job is killed at once$`, stepJobKilledAtOnce)
	sc.Step(`^a job\.killed event is emitted$`, stepKilledEvent)
	sc.Step(`^the manager unloads$`, stepManagerUnloads)
	sc.Step(`^the job fails with "([^"]*)"$`, stepJobFailsWith)
	sc.Step(`^no job\.killed event is emitted$`, stepNoKilledEvent)
}

func newToolManagerWorld(w *world) {
	w.tmRecorder = &tmRecorder{}
	w.tmManager = toolmanager.New(toolmanager.Options{Publisher: w.tmRecorder})
}

func stepEmptyToolRegistry(ctx context.Context) error {
	w := worldFrom(ctx)
	w.tmRegistry = toolmanager.NewRegistry()
	return nil
}

func stepDeclareTool(ctx context.Context, name, description string) error {
	w := worldFrom(ctx)
	return w.tmRegistry.Declare(toolmanager.Tool{
		Name: name, Description: description, Schema: json.RawMessage(`{}`), Run: echoRunner("ok"),
	})
}

func stepRegistryLists(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	for _, t := range w.tmRegistry.Tools() {
		if t.Name == name {
			return nil
		}
	}
	return fmt.Errorf("registry does not list %q", name)
}

func stepRegistryWithTool(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	w.tmRegistry = toolmanager.NewRegistry()
	return w.tmRegistry.Declare(toolmanager.Tool{Name: name, Run: echoRunner("ok")})
}

func stepDeclareAnotherTool(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	w.tmDeclErr = w.tmRegistry.Declare(toolmanager.Tool{Name: name, Run: echoRunner("ok")})
	return nil
}

func stepDeclarationFails(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.tmDeclErr == nil {
		return errors.New("duplicate declaration was accepted")
	}
	return nil
}

func stepRegistryOneTool(ctx context.Context) error {
	w := worldFrom(ctx)
	if got := len(w.tmRegistry.Tools()); got != 1 {
		return fmt.Errorf("registry has %d tools, want 1", got)
	}
	return nil
}

func stepManagerBlockingTool(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	newToolManagerWorld(w)
	w.tmRelease = make(chan struct{})
	return w.tmManager.Registry().Declare(toolmanager.Tool{Name: name, Run: blockingRunner(w.tmRelease)})
}

func stepManagerTickBlockingTool(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	w.tmRecorder = &tmRecorder{}
	w.tmManager = toolmanager.New(toolmanager.Options{DefaultTick: 10 * time.Millisecond, Publisher: w.tmRecorder})
	w.tmRelease = make(chan struct{})
	return w.tmManager.Registry().Declare(toolmanager.Tool{Name: name, Run: blockingRunner(w.tmRelease)})
}

func stepManagerEchoTool(ctx context.Context, name, answer string) error {
	w := worldFrom(ctx)
	newToolManagerWorld(w)
	return w.tmManager.Registry().Declare(toolmanager.Tool{Name: name, Run: echoRunner(answer)})
}

func stepManagerFailingTool(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	newToolManagerWorld(w)
	return w.tmManager.Registry().Declare(toolmanager.Tool{
		Name: name,
		Run: func(context.Context, json.RawMessage, *toolmanager.Output) (any, error) {
			return nil, errors.New("boom")
		},
	})
}

func stepManagerNoTools(ctx context.Context) error {
	w := worldFrom(ctx)
	newToolManagerWorld(w)
	return nil
}

func stepManagerChattyTool(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	newToolManagerWorld(w)
	w.tmRelease = make(chan struct{})
	return w.tmManager.Registry().Declare(toolmanager.Tool{Name: name, Run: chattyRunner(w.tmRelease)})
}

func stepStartTool(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	w.tmJob = w.tmManager.Start(name, nil, 0)
	return nil
}

func stepStartAndWrites(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	w.tmJob = w.tmManager.Start(name, nil, 0)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.tmManager.Peep(w.tmJob).Output, "partial") {
			return nil
		}
		time.Sleep(2 * time.Millisecond)
	}
	return errors.New("the tool never wrote its partial output")
}

func stepHandleBeforeFinish(ctx context.Context) error {
	w := worldFrom(ctx)
	if w.tmJob == nil {
		return errors.New("no job handle")
	}
	if st := w.tmManager.Peep(w.tmJob); st.State != toolmanager.StateRunning {
		return fmt.Errorf("job state = %s, want running while the tool blocks", st.State)
	}
	return nil
}

func stepJobDoneWithResult(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	st := w.tmJob.Wait()
	if st.State != toolmanager.StateDone {
		return fmt.Errorf("job state = %s (%s), want done", st.State, st.Error)
	}
	if got := fmt.Sprint(st.Result); got != want {
		return fmt.Errorf("job result = %q, want %q", got, want)
	}
	return nil
}

func stepJobFailedWithError(ctx context.Context) error {
	w := worldFrom(ctx)
	st := w.tmJob.Wait()
	if st.State != toolmanager.StateFailed || st.Error == "" {
		return fmt.Errorf("job = {%s %q}, want failed with an error", st.State, st.Error)
	}
	return nil
}

func stepJobFailsAtOnce(ctx context.Context, reason string) error {
	w := worldFrom(ctx)
	st := w.tmJob.Wait()
	if st.State != toolmanager.StateFailed || !strings.Contains(st.Error, reason) {
		return fmt.Errorf("job = {%s %q}, want failed containing %q", st.State, st.Error, reason)
	}
	return nil
}

func stepPeepStateOutput(ctx context.Context, state string) error {
	w := worldFrom(ctx)
	st := w.tmManager.Peep(w.tmJob)
	if string(st.State) != state {
		return fmt.Errorf("peep state = %s, want %s", st.State, state)
	}
	if st.Output != "partial" {
		return fmt.Errorf("peep output = %q, want %q", st.Output, "partial")
	}
	return nil
}

func stepTickEventElapsed(ctx context.Context) error {
	w := worldFrom(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range w.tmRecorder.byTopic("job.tick") {
			if e.payload["job"] == w.tmJob.ID() {
				if _, ok := e.payload["elapsed_ms"]; ok {
					return nil
				}
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	return errors.New("no job.tick event with elapsed time")
}

func stepCompletedEventResult(ctx context.Context, want string) error {
	w := worldFrom(ctx)
	w.tmJob.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range w.tmRecorder.byTopic("job.completed") {
			if e.payload["job"] == w.tmJob.ID() {
				if got := fmt.Sprint(e.payload["result"]); got != want {
					return fmt.Errorf("completed result = %q, want %q", got, want)
				}
				return nil
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	return errors.New("no job.completed event")
}

func stepKillRunningJob(ctx context.Context) error {
	w := worldFrom(ctx)
	w.tmManager.Kill(w.tmJob)
	return nil
}

func stepJobKilledAtOnce(ctx context.Context) error {
	w := worldFrom(ctx)
	if st := w.tmManager.Peep(w.tmJob); st.State != toolmanager.StateKilled {
		return fmt.Errorf("job state = %s, want killed", st.State)
	}
	return nil
}

func stepKilledEvent(ctx context.Context) error {
	w := worldFrom(ctx)
	if events := w.tmRecorder.byTopic("job.killed"); len(events) == 0 {
		return errors.New("no job.killed event")
	}
	return nil
}

func stepManagerUnloads(ctx context.Context) error {
	w := worldFrom(ctx)
	w.tmManager.Close()
	return nil
}

func stepJobFailsWith(ctx context.Context, reason string) error {
	w := worldFrom(ctx)
	st := w.tmManager.Peep(w.tmJob)
	if st.State != toolmanager.StateFailed || !strings.Contains(st.Error, reason) {
		return fmt.Errorf("job = {%s %q}, want failed containing %q", st.State, st.Error, reason)
	}
	return nil
}

func stepNoKilledEvent(ctx context.Context) error {
	w := worldFrom(ctx)
	if events := w.tmRecorder.byTopic("job.killed"); len(events) != 0 {
		return fmt.Errorf("job.killed was emitted %d times, want none", len(events))
	}
	return nil
}
