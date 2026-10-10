package tool_manager

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/DaviMGDev/memento/runtime"
)

// State is a job's life stage: queued, running, done, failed, or killed.
type State string

// The job states.
const (
	StateQueued  State = "queued"
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
	StateKilled  State = "killed"
)

// Job is one tool call. A handle is returned at once; the call runs in its
// own goroutine.
type Job struct {
	mu        sync.Mutex
	id        string
	tool      string
	args      json.RawMessage
	tick      time.Duration
	state     State
	started   time.Time
	lastTick  time.Time
	result    any
	err       string
	output    outputBuffer
	cancel    context.CancelFunc
	caller    *runtime.Instance
	parent    *Job
	children  []*Job
	done      chan struct{}
	reclaimed bool
}

// Caller returns the instance the job was started from, when it came through
// the guest job import; host-started jobs have none.
func (j *Job) Caller() *runtime.Instance {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.caller
}

// descendantOf reports whether the job is root or one of root's descendants.
func (j *Job) descendantOf(root *Job) bool {
	cur := j
	for cur != nil {
		if cur == root {
			return true
		}
		cur.mu.Lock()
		parent := cur.parent
		cur.mu.Unlock()
		cur = parent
	}
	return false
}

// Depth returns the job's depth in the tree: a root job is 1, and a child is
// one deeper than its parent.
func (j *Job) Depth() int {
	depth := 1
	cur := j
	for {
		cur.mu.Lock()
		parent := cur.parent
		cur.mu.Unlock()
		if parent == nil {
			return depth
		}
		depth++
		cur = parent
	}
}

// ID returns the job's identity.
func (j *Job) ID() string { return j.id }

// Parent returns the job's parent in the job tree, or nil for a root job.
func (j *Job) Parent() *Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.parent
}

// Tool returns the tool the job calls.
func (j *Job) Tool() string { return j.tool }

// State returns the job's current state.
func (j *Job) State() State {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

// Status is a job's observable state: what peep returns.
type Status struct {
	ID       string
	Tool     string
	State    State
	Elapsed  time.Duration
	LastTick time.Time
	Output   string
	Result   any
	Error    string
}

// Peep returns the job's status: state, last tick, and output so far.
func (j *Job) Peep() Status {
	j.mu.Lock()
	defer j.mu.Unlock()
	st := Status{
		ID:       j.id,
		Tool:     j.tool,
		State:    j.state,
		LastTick: j.lastTick,
		Output:   j.output.string(),
		Result:   j.result,
		Error:    j.err,
	}
	if !j.started.IsZero() {
		st.Elapsed = time.Since(j.started)
	}
	return st
}

// Wait blocks until the job reaches a terminal state and returns its status.
func (j *Job) Wait() Status {
	<-j.done
	return j.Peep()
}

func (j *Job) appendOutput(p []byte) { j.output.write(p) }

// setRunning marks the job running and starts its clock.
func (j *Job) setRunning() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state == StateQueued {
		j.state = StateRunning
	}
	j.started = time.Now()
}

// finish records the runner's outcome unless a kill already decided it. It
// reports the resulting state and whether the caller should emit an event.
func (j *Job) finish(result any, err error) (State, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state == StateKilled || j.reclaimed {
		return j.state, false
	}
	if err != nil {
		j.state = StateFailed
		j.err = err.Error()
		return j.state, true
	}
	j.state = StateDone
	j.result = result
	return j.state, true
}

// markKilled marks the job killed at once and cancels its runner. It reports
// whether this call did the killing.
func (j *Job) markKilled(reason string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.state != StateQueued && j.state != StateRunning {
		return false
	}
	j.state = StateKilled
	j.err = reason
	if j.cancel != nil {
		j.cancel()
	}
	return true
}

// adopt records a parent-child edge for the job tree. Each job's fields are
// written under that job's own lock.
func (j *Job) adopt(child *Job) {
	child.mu.Lock()
	child.parent = j
	child.mu.Unlock()
	j.mu.Lock()
	j.children = append(j.children, child)
	j.mu.Unlock()
}
