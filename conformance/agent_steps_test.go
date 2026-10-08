package conformance

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/DaviMGDev/core-agent/plugins/agent"
)

// agentRecorder captures what one turn did.
type agentRecorder struct {
	answer    agent.Answer
	appended  []agent.Message
	published []string
	jobs      []string
	recent    []agentRecent
	presented []agent.Tool
}

type agentRecent struct {
	conversation string
	n            int
}

func (r *agentRecorder) deps(cfg agent.Config) agent.Deps {
	return agent.Deps{
		Append: func(role, text string) error {
			r.appended = append(r.appended, agent.Message{Role: role, Text: text})
			return nil
		},
		Recent: func(n int) ([]agent.Message, error) {
			r.recent = append(r.recent, agentRecent{conversation: cfg.Conversation, n: n})
			return []agent.Message{{Role: "user", Text: "earlier"}}, nil
		},
		Project: func(msgs []agent.Message) ([]agent.Message, error) { return msgs, nil },
		Respond: func(text string, context []agent.Message, tools []agent.Tool) (agent.Answer, error) {
			r.presented = tools
			return r.answer, nil
		},
		StartJob: func(tool string, args json.RawMessage) (string, error) {
			r.jobs = append(r.jobs, tool)
			return "job-1", nil
		},
		Publish: func(text string) error {
			r.published = append(r.published, text)
			return nil
		},
	}
}

func registerAgentSteps(sc *godog.ScenarioContext) {
	sc.Step(`^an agent whose model answers "([^"]*)"$`, stepAgentAnswers)
	sc.Step(`^an agent whose model stays silent$`, stepAgentSilent)
	sc.Step(`^an agent whose model calls the tool "([^"]*)"$`, stepAgentCallsTool)
	sc.Step(`^a user line wakes the agent$`, stepAgentWake)
	sc.Step(`^the turn yields exactly one chat\.message "([^"]*)"$`, stepTurnOneMessage)
	sc.Step(`^the turn yields no message$`, stepTurnNoMessage)
	sc.Step(`^the turn starts one job for "([^"]*)"$`, stepTurnStartsJob)
	sc.Step(`^an agent with conversation "([^"]*)" and budget (\d+)$`, stepAgentConversationBudget)
	sc.Step(`^the pipeline asks for (\d+) recent turns of "([^"]*)"$`, stepPipelineAsked)
	sc.Step(`^an agent with tools "([^"]*)" and "([^"]*)"$`, stepAgentTools)
	sc.Step(`^an agent with tools "([^"]*)" and "([^"]*)", hiding "([^"]*)" and renaming "([^"]*)" to "([^"]*)"$`, stepAgentToolsProjected)
	sc.Step(`^the tools are presented$`, stepPresentTools)
	sc.Step(`^the model sees "([^"]*)" and "([^"]*)"$`, stepSeesBoth)
	sc.Step(`^the model sees "([^"]*)" only$`, stepSeesOnly)
	sc.Step(`^an agent with tool "([^"]*)" with an argument schema$`, stepAgentToolWithSchema)
	sc.Step(`^the model sees "([^"]*)" with its argument schema$`, stepSeesWithSchema)
	sc.Step(`^an agent with tool "([^"]*)" renamed to "([^"]*)"$`, stepAgentToolRenamed)
	sc.Step(`^the model calls the tool "([^"]*)"$`, stepModelCallsTool)
}

func agentWorld(ctx context.Context) (*world, *agentRecorder) {
	w := worldFrom(ctx)
	if w.agentRec == nil {
		w.agentRec = &agentRecorder{}
		w.agentCfg = agent.Config{}
	}
	return w, w.agentRec
}

func stepAgentAnswers(ctx context.Context, text string) error {
	_, rec := agentWorld(ctx)
	rec.answer = agent.Answer{Text: text}
	return nil
}

func stepAgentSilent(ctx context.Context) error {
	_, rec := agentWorld(ctx)
	rec.answer = agent.Answer{Silence: true}
	return nil
}

func stepAgentCallsTool(ctx context.Context, tool string) error {
	_, rec := agentWorld(ctx)
	rec.answer = agent.Answer{Tool: tool}
	return nil
}

func stepAgentWake(ctx context.Context) error {
	w, rec := agentWorld(ctx)
	res, err := agent.RunTurn(w.agentCfg, "hi", rec.deps(w.agentCfg))
	if err != nil {
		return err
	}
	w.agentRes = res
	return nil
}

func stepTurnOneMessage(ctx context.Context, text string) error {
	w, rec := agentWorld(ctx)
	if !w.agentRes.Spoke || w.agentRes.Text != text {
		return fmt.Errorf("result = %+v, want one message %q", w.agentRes, text)
	}
	if len(rec.published) != 1 || rec.published[0] != text {
		return fmt.Errorf("published = %v, want one %q", rec.published, text)
	}
	return nil
}

func stepTurnNoMessage(ctx context.Context) error {
	w, rec := agentWorld(ctx)
	if w.agentRes.Spoke || len(rec.published) != 0 {
		return fmt.Errorf("result = %+v, published = %v; want no message", w.agentRes, rec.published)
	}
	return nil
}

func stepTurnStartsJob(ctx context.Context, tool string) error {
	w, rec := agentWorld(ctx)
	if w.agentRes.Job == "" || len(rec.jobs) != 1 || rec.jobs[0] != tool {
		return fmt.Errorf("result = %+v, jobs = %v; want one job for %q", w.agentRes, rec.jobs, tool)
	}
	return nil
}

func stepAgentConversationBudget(ctx context.Context, conversation string, budget int) error {
	w, _ := agentWorld(ctx)
	w.agentCfg.Conversation = conversation
	w.agentCfg.Budget = budget
	return nil
}

func stepPipelineAsked(ctx context.Context, n int, conversation string) error {
	_, rec := agentWorld(ctx)
	if len(rec.recent) != 1 || rec.recent[0].conversation != conversation || rec.recent[0].n != n {
		return fmt.Errorf("recent calls = %+v, want one for %q with n=%d", rec.recent, conversation, n)
	}
	return nil
}

func stepAgentTools(ctx context.Context, first, second string) error {
	w, _ := agentWorld(ctx)
	w.agentCfg.Tools = []agent.Tool{{Name: first}, {Name: second}}
	return nil
}

func stepAgentToolsProjected(ctx context.Context, first, second, hidden, renamed, alias string) error {
	w, _ := agentWorld(ctx)
	w.agentCfg.Tools = []agent.Tool{{Name: first}, {Name: second}}
	w.agentCfg.Hidden = []string{hidden}
	w.agentCfg.Renamed = map[string]string{renamed: alias}
	return nil
}

func stepPresentTools(ctx context.Context) error {
	w, rec := agentWorld(ctx)
	rec.presented = agent.Present(w.agentCfg)
	return nil
}

func stepSeesBoth(ctx context.Context, first, second string) error {
	_, rec := agentWorld(ctx)
	if len(rec.presented) != 2 || rec.presented[0].Name != first || rec.presented[1].Name != second {
		return fmt.Errorf("presented = %+v, want %q and %q", rec.presented, first, second)
	}
	return nil
}

func stepSeesOnly(ctx context.Context, name string) error {
	_, rec := agentWorld(ctx)
	if len(rec.presented) != 1 || rec.presented[0].Name != name {
		return fmt.Errorf("presented = %+v, want %q only", rec.presented, name)
	}
	return nil
}

func stepAgentToolWithSchema(ctx context.Context, tool string) error {
	w, _ := agentWorld(ctx)
	schema := json.RawMessage(`{"type":"object","properties":{"brief":{"type":"string"}}}`)
	w.agentCfg.Tools = []agent.Tool{{Name: tool, Parameters: schema}}
	return nil
}

func stepSeesWithSchema(ctx context.Context, tool string) error {
	_, rec := agentWorld(ctx)
	if len(rec.presented) != 1 || rec.presented[0].Name != tool || len(rec.presented[0].Parameters) == 0 {
		return fmt.Errorf("presented = %+v, want %q with schema", rec.presented, tool)
	}
	return nil
}

func stepAgentToolRenamed(ctx context.Context, tool, alias string) error {
	w, _ := agentWorld(ctx)
	w.agentCfg.Tools = []agent.Tool{{Name: tool}}
	w.agentCfg.Renamed = map[string]string{tool: alias}
	return nil
}

func stepModelCallsTool(ctx context.Context, tool string) error {
	_, rec := agentWorld(ctx)
	rec.answer = agent.Answer{Tool: tool, Args: json.RawMessage(`{}`)}
	return nil
}
