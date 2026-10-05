// Package replchat implements the repl-chat session protocol.
//
// The library is pure: Handle turns one input line into one Reply. The guest
// owns stdin, logging, and the memento ABI.
package replchat

import (
	"bufio"
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

// Handle interprets one input line. Commands and blank lines do not advance
// the turn counter; any other line is one chat turn answered by the
// first-cut local stub.
func (s *Session) Handle(line string) Reply {
	line = strings.TrimSpace(line)
	switch line {
	case "":
		return Reply{}
	case ":quit", ":q", ":exit":
		return Reply{Quit: true}
	case ":help":
		return Reply{Text: "commands: :help, :quit"}
	default:
		s.turn++
		return Reply{Text: StubResponse(s.turn, oneLine(line))}
	}
}

// oneLine flattens embedded line breaks so a reply is always one line.
func oneLine(s string) string {
	return strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(s)
}

// StubResponse renders the first-cut deterministic response for a turn: it
// names the turn and echoes the line. Transport-backed answers replace the
// stub, not this protocol.
func StubResponse(turn int, line string) string {
	return fmt.Sprintf("turn %d: %s", turn, line)
}

// Run hosts the session loop on in: it emits the join line, a prompt before
// every read, and one response per chat turn; blank lines emit nothing more
// than the next prompt. It returns when a quit command arrives or the input
// ends, and reports a read error. The guest supplies stdin and wires emit to
// the kernel log; the protocol stays here, testable on the host.
func (s *Session) Run(in io.Reader, emit func(string)) error {
	emit(s.JoinMessage() + "\n")
	sc := bufio.NewScanner(in)
	for {
		emit("you> ")
		if !sc.Scan() {
			break
		}
		reply := s.Handle(sc.Text())
		if reply.Quit {
			return nil
		}
		if reply.Text != "" {
			emit(reply.Text + "\n")
		}
	}
	return sc.Err()
}
