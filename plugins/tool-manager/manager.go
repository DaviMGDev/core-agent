package tool_manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/DaviMGDev/core-agent/plugins/notifications"
	"github.com/DaviMGDev/memento/runtime"
)

// Publisher is the event sink for job events; the notification bus satisfies
// it. A nil publisher drops events.
type Publisher interface {
	Publish(topic string, payload []byte)
}

// Event is one job event delivered to a parent's listener.
type Event struct {
	Topic   string
	Payload []byte
}

// jobListener is one parent's event channel.
type jobListener struct {
	events chan Event
	done   chan struct{}
}

// send delivers one event, or gives up when the listener is released. A full
// buffer blocks the emitter: the parent is expected to drain its children's
// events.
func (l *jobListener) send(ev Event) {
	select {
	case l.events <- ev:
	case <-l.done:
	}
}

// Options configure a Manager.
type Options struct {
	// DefaultTick is the tick interval for jobs that do not name one.
	// Zero keeps 30s.
	DefaultTick time.Duration
	// Publisher receives job events.
	Publisher Publisher
}

// Manager owns the tool registry and the jobs. It is host-side (system D14)
// and implements the loader's host-services contract: guests reach it through
// the job imports.
type Manager struct {
	mu          sync.Mutex
	registry    *Registry
	jobs        map[string]*Job
	seq         uint64
	defaultTick time.Duration
	publisher   Publisher
	callers     map[*runtime.Instance][]*Job
	listeners   map[*Job]*jobListener
	closed      bool
	wg          sync.WaitGroup
}

// New returns a manager with an empty registry.
func New(opts Options) *Manager {
	tick := opts.DefaultTick
	if tick <= 0 {
		tick = 30 * time.Second
	}
	return &Manager{
		registry:    NewRegistry(),
		jobs:        make(map[string]*Job),
		defaultTick: tick,
		publisher:   opts.Publisher,
		callers:     make(map[*runtime.Instance][]*Job),
		listeners:   make(map[*Job]*jobListener),
	}
}

// Registry returns the manager's tool registry.
func (m *Manager) Registry() *Registry { return m.registry }

// Start begins a tool call and returns its job handle at once. An unknown
// tool is refused as a job that fails immediately with the reason; the call
// itself always returns a job.
func (m *Manager) Start(tool string, args json.RawMessage, tick time.Duration) *Job {
	return m.start(tool, args, tick, nil, nil)
}

// start creates one job, adopts it under parent when given, and launches its
// runner. Adoption happens before the runner starts, so the job's first
// event already routes to its parent.
func (m *Manager) start(tool string, args json.RawMessage, tick time.Duration, caller *runtime.Instance, parent *Job) *Job {
	m.mu.Lock()
	m.seq++
	job := &Job{
		id:     fmt.Sprintf("job-%d", m.seq),
		tool:   tool,
		args:   args,
		state:  StateQueued,
		caller: caller,
		done:   make(chan struct{}),
	}
	if tick > 0 {
		job.tick = tick
	} else {
		job.tick = m.defaultTick
	}
	m.jobs[job.id] = job
	closed := m.closed
	m.mu.Unlock()

	if parent != nil && parent != job {
		parent.adopt(job)
	}
	if closed {
		m.refuse(job, "tool manager closed")
		return job
	}
	t, ok := m.registry.Lookup(tool)
	if !ok {
		m.refuse(job, fmt.Sprintf("unknown tool %q", tool))
		return job
	}

	m.emitJob(job, notifications.TopicJobStarted, map[string]any{"job": job.id, "tool": job.tool})
	job.setRunning()
	m.wg.Add(1)
	go m.run(job, t)
	return job
}

// refuse fails a job at once, with the reason. A refused start is a job too:
// it emits started and failed like any other.
func (m *Manager) refuse(job *Job, reason string) {
	job.mu.Lock()
	job.state = StateFailed
	job.err = reason
	job.mu.Unlock()
	close(job.done)
	m.emitJob(job, notifications.TopicJobStarted, map[string]any{"job": job.id, "tool": job.tool})
	m.emitJob(job, notifications.TopicJobFailed, map[string]any{"job": job.id, "error": reason})
}

// run executes one job: it starts the tick loop, calls the runner, and
// publishes the terminal event.
func (m *Manager) run(job *Job, t Tool) {
	defer m.wg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	job.mu.Lock()
	job.cancel = cancel
	// A job killed or reclaimed before its runner started must not run: the
	// kill decided, or the manager closed, while cancel was still nil.
	stop := job.state != StateQueued && job.state != StateRunning
	job.mu.Unlock()
	if stop {
		cancel()
	}

	tickDone := make(chan struct{})
	go m.tickLoop(ctx, job, tickDone)

	result, err := t.Run(ctx, job.args, &Output{job: job})
	close(tickDone)
	cancel()

	state, emit := job.finish(result, err)
	if !emit {
		close(job.done)
		return
	}
	switch state {
	case StateDone:
		m.emitJob(job, notifications.TopicJobCompleted, map[string]any{"job": job.id, "result": result})
	case StateFailed:
		m.emitJob(job, notifications.TopicJobFailed, map[string]any{"job": job.id, "error": err.Error()})
	}
	close(job.done)
}

// tickLoop emits job.tick on the job's interval while it runs. A tick is a
// clock nudge — time passed, peep me — never a health claim.
func (m *Manager) tickLoop(ctx context.Context, job *Job, done <-chan struct{}) {
	ticker := time.NewTicker(job.tick)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			job.mu.Lock()
			job.lastTick = now
			elapsed := now.Sub(job.started)
			job.mu.Unlock()
			m.emitJob(job, notifications.TopicJobTick, map[string]any{"job": job.id, "elapsed_ms": elapsed.Milliseconds()})
		}
	}
}

// Kill is accepted at any point: the job is marked killed at once, its runner
// unwinds at the next safe point, and killing a job kills its descendants.
func (m *Manager) Kill(job *Job) Status {
	if job.markKilled("killed") {
		m.emitJob(job, notifications.TopicJobKilled, map[string]any{"job": job.id})
		m.killDescendants(job)
	}
	return job.Peep()
}

func (m *Manager) killDescendants(job *Job) {
	job.mu.Lock()
	children := append([]*Job(nil), job.children...)
	job.mu.Unlock()
	for _, child := range children {
		m.Kill(child)
	}
}

// Peep returns the job's status.
func (m *Manager) Peep(job *Job) Status { return job.Peep() }

// Job looks a job up by id.
func (m *Manager) Job(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	return j, ok
}

// Attribute records that a job is executing on a caller instance, so the
// caller's cancellation poll and host imports answer for it. Attributions
// stack: one guest instance can run nested turns, and the innermost job is
// the one in flight.
func (m *Manager) Attribute(caller *runtime.Instance, job *Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callers[caller] = append(m.callers[caller], job)
}

// Release drops one caller's job attribution, leaving any nested ones in
// place.
func (m *Manager) Release(caller *runtime.Instance, job *Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stack := m.callers[caller]
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == job {
			stack = append(stack[:i], stack[i+1:]...)
			break
		}
	}
	if len(stack) == 0 {
		delete(m.callers, caller)
		return
	}
	m.callers[caller] = stack
}

// attributed returns the job currently executing on the caller's instance.
func (m *Manager) attributed(caller *runtime.Instance) *Job {
	stack := m.callers[caller]
	if len(stack) == 0 {
		return nil
	}
	return stack[len(stack)-1]
}

// Close reclaims every job: runners are cancelled and their jobs land failed
// with "tool manager closed". Reclamation is not a kill — no job.killed is
// emitted, and a runner returning afterwards does not resurrect the job.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	jobs := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		jobs = append(jobs, j)
	}
	m.mu.Unlock()

	for _, j := range jobs {
		j.mu.Lock()
		if j.state == StateQueued || j.state == StateRunning {
			j.state = StateFailed
			j.err = "tool manager closed"
			j.reclaimed = true
			if j.cancel != nil {
				j.cancel()
			}
		}
		j.mu.Unlock()
	}
	m.wg.Wait()
}

// emitJob publishes one job event. A job with a parent delivers to the
// parent's listener — the parent is the sole listener to its child's subtree
// events — while a root job publishes on the bus. An event with a parent but
// no listener is dropped: nobody else may see it.
func (m *Manager) emitJob(job *Job, topic string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	job.mu.Lock()
	parent := job.parent
	job.mu.Unlock()
	m.mu.Lock()
	listener := m.listeners[parent]
	m.mu.Unlock()
	if listener != nil {
		listener.send(Event{Topic: topic, Payload: b})
		return
	}
	if parent != nil || m.publisher == nil {
		return
	}
	m.publisher.Publish(topic, b)
}

// Listen returns a channel receiving the events of job's direct children:
// ticks, completions, failures, and kills. Unlisten releases it; a send that
// races the release is dropped.
func (m *Manager) Listen(job *Job) <-chan Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.listeners[job]; ok {
		return l.events
	}
	l := &jobListener{events: make(chan Event, 64), done: make(chan struct{})}
	m.listeners[job] = l
	return l.events
}

// Unlisten releases a listener registered with Listen.
func (m *Manager) Unlisten(job *Job) {
	m.mu.Lock()
	l := m.listeners[job]
	delete(m.listeners, job)
	m.mu.Unlock()
	if l != nil {
		close(l.done)
	}
}

// --- the loader's host-services contract ---

// startRequest is the JSON document of a job_start call.
type startRequest struct {
	Tool   string          `json:"tool"`
	Args   json.RawMessage `json:"args,omitempty"`
	TickMS int64           `json:"tick_ms,omitempty"`
}

// jobRef is the JSON document naming one job.
type jobRef struct {
	Job string `json:"job"`
}

// statusDoc is the JSON document peep and kill return.
type statusDoc struct {
	Job       string `json:"job"`
	Tool      string `json:"tool,omitempty"`
	State     string `json:"state,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms,omitempty"`
	LastTick  string `json:"last_tick,omitempty"`
	Output    string `json:"output,omitempty"`
	Result    any    `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

// StartJob implements the loader's job-start import: one request document in,
// one handle document stashed. It returns as soon as the job has a handle and
// never waits for the call to finish. A job started while the caller's
// instance is attributed to a parent job is adopted into that job's subtree.
func (m *Manager) StartJob(caller *runtime.Instance, req []byte) ([]byte, error) {
	var r startRequest
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, fmt.Errorf("job start: %w", err)
	}
	if r.Tool == "" {
		return nil, errors.New("job start: tool is required")
	}
	m.mu.Lock()
	parent := m.attributed(caller)
	m.mu.Unlock()
	job := m.start(r.Tool, r.Args, time.Duration(r.TickMS)*time.Millisecond, caller, parent)
	return json.Marshal(jobRef{Job: job.ID()})
}

// PeepJob implements the loader's job-peep import.
func (m *Manager) PeepJob(caller *runtime.Instance, req []byte) ([]byte, error) {
	job, err := m.jobFromDoc(req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(statusFrom(job.Peep()))
}

// KillJob implements the loader's job-kill import.
func (m *Manager) KillJob(caller *runtime.Instance, req []byte) ([]byte, error) {
	job, err := m.jobFromDoc(req)
	if err != nil {
		return nil, err
	}
	return json.Marshal(statusFrom(m.Kill(job)))
}

// Publish implements the loader's publish import: guest publishes reach the
// bus like any other publisher.
func (m *Manager) Publish(topic string, payload []byte) error {
	if m.publisher == nil {
		return errors.New("no bus configured")
	}
	m.publisher.Publish(topic, payload)
	return nil
}

// Cancelled implements the loader's cancellation contract: the host answers
// for the calling job only.
func (m *Manager) Cancelled(caller *runtime.Instance) bool {
	m.mu.Lock()
	job := m.attributed(caller)
	m.mu.Unlock()
	if job == nil {
		return false
	}
	return job.State() == StateKilled
}

func (m *Manager) jobFromDoc(req []byte) (*Job, error) {
	var r jobRef
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, fmt.Errorf("job reference: %w", err)
	}
	if r.Job == "" {
		return nil, errors.New("job reference: job is required")
	}
	job, ok := m.Job(r.Job)
	if !ok {
		return nil, fmt.Errorf("unknown job %q", r.Job)
	}
	return job, nil
}

func statusFrom(st Status) statusDoc {
	doc := statusDoc{
		Job:       st.ID,
		Tool:      st.Tool,
		State:     string(st.State),
		ElapsedMS: st.Elapsed.Milliseconds(),
		Output:    st.Output,
		Result:    st.Result,
		Error:     st.Error,
	}
	if !st.LastTick.IsZero() {
		doc.LastTick = st.LastTick.UTC().Format(time.RFC3339Nano)
	}
	return doc
}
