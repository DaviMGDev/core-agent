// Package link implements the TUI link: the JSON-lines protocol a screen
// (Neovim) speaks to the core over stdio.
//
// One JSON object per line. Requests (deliver, load) go in; agent messages,
// load answers, job transitions, and errors come out. The codec is pure: the
// entry owns the streams, the driver owns the loop.
package link

import (
	"encoding/json"
	"fmt"
)

// Kind is a link line's discriminator.
type Kind string

// The link vocabulary: four requests in, six messages out.
const (
	KindDeliver Kind = "deliver"
	KindLoad    Kind = "load"
	KindJobs    Kind = "jobs"
	KindPeek    Kind = "peek"
	KindMessage Kind = "message"
	KindLoaded  Kind = "loaded"
	KindJob     Kind = "job"
	KindError   Kind = "error"
)

// Request is one line from the screen.
type Request struct {
	Kind Kind   `json:"kind"`
	Chat string `json:"chat,omitempty"`
	Text string `json:"text,omitempty"`
	Job  string `json:"job,omitempty"`
}

// Turn is one recorded conversation turn.
type Turn struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// Message is one agent speech line (a chat.message), attributed to its chat.
type Message struct {
	Kind Kind   `json:"kind"`
	Chat string `json:"chat"`
	Role string `json:"role"`
	Text string `json:"text"`
}

// Loaded is the answer to a load request: a chat's turns in append order.
type Loaded struct {
	Kind     Kind   `json:"kind"`
	Chat     string `json:"chat"`
	Messages []Turn `json:"messages"`
}

// Job is one job state transition. Ticks never cross the link.
type Job struct {
	Kind   Kind   `json:"kind"`
	Event  string `json:"event"`
	Job    string `json:"job"`
	Tool   string `json:"tool,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// JobInfo is one job in a jobs answer: identity, state, tree link, and
// timing. Output stays with Peek.
type JobInfo struct {
	Job      string `json:"job"`
	Tool     string `json:"tool,omitempty"`
	State    string `json:"state,omitempty"`
	Parent   string `json:"parent,omitempty"`
	AgeMS    int64  `json:"age_ms,omitempty"`
	IdleMS   int64  `json:"idle_ms,omitempty"`
	LastTick string `json:"last_tick,omitempty"`
}

// Jobs is the answer to a jobs request: the launch's jobs in creation order,
// with each job's parent naming its place in the tree.
type Jobs struct {
	Kind Kind      `json:"kind"`
	Jobs []JobInfo `json:"jobs"`
}

// Peek is the answer to a peek request: one job's state, last tick, and
// output so far, with the result and error once it is terminal.
type Peek struct {
	Kind     Kind            `json:"kind"`
	Job      string          `json:"job"`
	Tool     string          `json:"tool,omitempty"`
	State    string          `json:"state,omitempty"`
	Parent   string          `json:"parent,omitempty"`
	AgeMS    int64           `json:"age_ms,omitempty"`
	IdleMS   int64           `json:"idle_ms,omitempty"`
	LastTick string          `json:"last_tick,omitempty"`
	Output   string          `json:"output,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    string          `json:"error,omitempty"`
}

// Failure is a refused request or a failed turn; the loop continues.
type Failure struct {
	Kind  Kind   `json:"kind"`
	Error string `json:"error"`
}

// DecodeRequest parses one inbound line. Every line must be a known request
// with the fields that request needs; anything else is malformed and the
// caller answers a Failure.
func DecodeRequest(line []byte) (Request, error) {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return Request{}, fmt.Errorf("link: %w", err)
	}
	switch req.Kind {
	case KindDeliver:
		if req.Chat == "" {
			return Request{}, fmt.Errorf("link: deliver: empty chat")
		}
		if req.Text == "" {
			return Request{}, fmt.Errorf("link: deliver: empty text")
		}
	case KindLoad:
		if req.Chat == "" {
			return Request{}, fmt.Errorf("link: load: empty chat")
		}
	case KindJobs:
	case KindPeek:
		if req.Job == "" {
			return Request{}, fmt.Errorf("link: peek: empty job")
		}
	default:
		return Request{}, fmt.Errorf("link: unknown kind %q", req.Kind)
	}
	return req, nil
}

// EncodeLine marshals one outbound message as one line, newline included.
// JSON escapes embedded newlines, so the result is always exactly one line.
func EncodeLine(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("link: %w", err)
	}
	return append(b, '\n'), nil
}
