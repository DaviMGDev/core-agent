// Package replchat implements the repl-chat session protocol and the
// terminal's wake vocabulary.
//
// The library is pure: Handle turns one input line into one Reply, delegating
// chat turns to a responder, and Run hosts the session loop over any reader.
// The host driver runs Run — stdin, the prompt, and the loop are its — while
// the guest handles one wake at a time over the memento ABI: a user line runs
// one agent turn, and the events a bus wake carries are rendered to the log.
package replchat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	// DefaultNick is used when the activation payload carries no nickname.
	DefaultNick = "agent"

	// LeaveMessage is emitted by the guest's effect inverse on unload.
	LeaveMessage = "repl: session closed"
)

// Reply is the outcome of handling one input line.
type Reply struct {
	// Text is the response line; empty when the line produced no response.
	Text string
	// Quit reports that the session must end.
	Quit bool
}

// TopicChatMessage is the layer's topic for conversation messages.
const TopicChatMessage = "chat.message"

// Wake is one host call into the terminal: a user line, or the events a bus
// wake carries.
type Wake struct {
	Line   string  `json:"line,omitempty"`
	Events []Event `json:"events,omitempty"`
}

// Event is one bus event in a wake.
type Event struct {
	Topic   string          `json:"topic"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Message is the chat.message payload: the text a renderer shows.
type Message struct {
	Text string `json:"text"`
}

// Answer is the terminal's reply to a wake: empty on success, or the failure
// for the session to report in the transcript.
type Answer struct {
	Error string `json:"error,omitempty"`
}

// Render returns the transcript lines one wake's events contribute:
// chat.message events, one line per message. A payload that carries a text
// renders the text (an empty one renders nothing); any other payload renders
// verbatim.
func Render(events []Event) []string {
	var out []string
	for _, e := range events {
		if e.Topic != TopicChatMessage {
			continue
		}
		text := string(e.Payload)
		var m struct {
			Text *string `json:"text"`
		}
		if err := json.Unmarshal(e.Payload, &m); err == nil && m.Text != nil {
			text = *m.Text
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		out = append(out, oneLine(text))
	}
	return out
}

// Session is one REPL session: a nickname and a chat-turn counter.
type Session struct {
	nick string
	turn int
}

// New starts a session. A blank nickname falls back to DefaultNick.
func New(nick string) *Session {
	if strings.TrimSpace(nick) == "" {
		nick = DefaultNick
	}
	return &Session{nick: nick}
}

// Nick returns the session nickname.
func (s *Session) Nick() string { return s.nick }

// Turn returns how many chat turns the session answered.
func (s *Session) Turn() int { return s.turn }

// JoinMessage is the line the guest announces when the session starts.
func (s *Session) JoinMessage() string {
	return fmt.Sprintf("repl: %s joined (type :help, :quit to leave)", s.nick)
}

// Handle interprets one input line. Commands and blank lines never call
// respond and never advance the counter; any other line is one chat turn whose
// text is respond's result.
func (s *Session) Handle(line string, respond func(string) (string, error)) (Reply, error) {
	line = strings.TrimSpace(line)
	switch line {
	case "":
		return Reply{}, nil
	case ":quit", ":q", ":exit":
		return Reply{Quit: true}, nil
	case ":help":
		return Reply{Text: "commands: :help, :quit"}, nil
	default:
		s.turn++
		text, err := respond(line)
		if err != nil {
			return Reply{}, err
		}
		return Reply{Text: oneLine(text)}, nil
	}
}

// Run hosts the session loop on in: it emits the join line, a prompt before
// every read, and one response per chat turn; blank lines emit nothing more
// than the next prompt. A responder error is reported in the transcript and
// the session continues. It returns when a quit command arrives or the input
// ends, and reports a read error. The host driver supplies stdin and wires
// emit to the terminal's output; a chat turn's message arrives through the
// responder's published chat.message, not its return value.
func (s *Session) Run(in io.Reader, emit func(string), respond func(string) (string, error)) error {
	emit(s.JoinMessage() + "\n")
	sc := bufio.NewScanner(in)
	for {
		emit("you> ")
		if !sc.Scan() {
			break
		}
		reply, err := s.Handle(sc.Text(), respond)
		if err != nil {
			emit("repl: " + err.Error() + "\n")
			continue
		}
		if reply.Quit {
			return nil
		}
		if reply.Text != "" {
			emit(reply.Text + "\n")
		}
	}
	return sc.Err()
}

// oneLine flattens embedded line breaks so a reply is always one line.
func oneLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
}
