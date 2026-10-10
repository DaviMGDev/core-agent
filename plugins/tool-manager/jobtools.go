package tool_manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The agent's job surface as manager tools: list, observe, and kill. The
// tools reach the manager — the only job owner — and their descriptions bind
// the model to report only what a tool confirmed.
const (
	// JobsToolName lists the jobs visible to the caller.
	JobsToolName = "jobs"
	// PeepToolName observes one job: state, last tick, and output so far.
	PeepToolName = "peep"
	// KillToolName accepts a kill for one job.
	KillToolName = "kill"
)

// jobRefDoc is one job tool's argument document.
type jobRefDoc struct {
	Job string `json:"job"`
}

// JobTools returns the job surface as manager tools, in declaration order.
// Visibility is subtree-scoped: a root caller — the top-level agent — sees
// the whole launch, and a caller running inside a job (a subagent) sees only
// that job's subtree.
func JobTools(m *Manager) []Tool {
	jobRefSchema := json.RawMessage(`{"type":"object","properties":{` +
		`"job":{"type":"string","description":"the job id"}},` +
		`"required":["job"]}`)
	return []Tool{
		{
			Name: JobsToolName,
			Description: "list the jobs visible to this agent in creation order: job id, tool, state, and age. " +
				"Report only what the tool returns.",
			Schema: json.RawMessage(`{"type":"object","properties":{}}`),
			Run: func(_ context.Context, _ json.RawMessage, out *Output) (any, error) {
				return m.jobsFrom(out), nil
			},
		},
		{
			Name: PeepToolName,
			Description: "observe one job: its state, last tick, and output so far. " +
				"Report only the values the tool returns.",
			Schema: jobRefSchema,
			Run: func(_ context.Context, args json.RawMessage, out *Output) (any, error) {
				job, err := m.callerJob(args, out)
				if err != nil {
					return nil, err
				}
				return statusFrom(job.Peep()), nil
			},
		},
		{
			Name: KillToolName,
			Description: "accept a kill for one job; it is honored at the job's next safe point and the result " +
				"reports the new state. Never claim a kill the tool did not confirm.",
			Schema: jobRefSchema,
			Run: func(_ context.Context, args json.RawMessage, out *Output) (any, error) {
				job, err := m.callerJob(args, out)
				if err != nil {
					return nil, err
				}
				return statusFrom(m.Kill(job)), nil
			},
		},
	}
}

// jobRow is one job in a jobs answer: identity, state, and timing. Output
// stays with peep.
type jobRow struct {
	Job       string `json:"job"`
	Tool      string `json:"tool,omitempty"`
	State     string `json:"state,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms,omitempty"`
	LastTick  string `json:"last_tick,omitempty"`
}

// jobsFrom lists every job the caller may see, in creation order.
func (m *Manager) jobsFrom(out *Output) []jobRow {
	jobs := m.Jobs()
	rows := make([]jobRow, 0, len(jobs))
	for _, job := range jobs {
		if !m.visibleFrom(out, job) {
			continue
		}
		st := job.Peep()
		row := jobRow{
			Job:       st.ID,
			Tool:      st.Tool,
			State:     string(st.State),
			ElapsedMS: st.Elapsed.Milliseconds(),
		}
		if !st.LastTick.IsZero() {
			row.LastTick = st.LastTick.UTC().Format(time.RFC3339Nano)
		}
		rows = append(rows, row)
	}
	return rows
}

// callerJob reads a job reference and refuses an unknown or out-of-scope id
// with a factual error.
func (m *Manager) callerJob(args json.RawMessage, out *Output) (*Job, error) {
	var ref jobRefDoc
	if err := json.Unmarshal(args, &ref); err != nil {
		return nil, fmt.Errorf("job reference: %w", err)
	}
	if ref.Job == "" {
		return nil, errors.New("job reference: job is required")
	}
	job, ok := m.Job(ref.Job)
	if !ok {
		return nil, fmt.Errorf("unknown job %q", ref.Job)
	}
	if !m.visibleFrom(out, job) {
		return nil, fmt.Errorf("job %q is outside the caller's subtree", ref.Job)
	}
	return job, nil
}

// visibleFrom reports whether the caller may see job: a root call — the
// top-level agent — sees the whole launch, and a call made under a job (a
// subagent's) sees only that job's subtree.
func (m *Manager) visibleFrom(out *Output, job *Job) bool {
	if out == nil || out.job == nil {
		return false
	}
	out.job.mu.Lock()
	root := out.job.parent
	out.job.mu.Unlock()
	return root == nil || job.descendantOf(root)
}
