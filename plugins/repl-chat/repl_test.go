package replchat

import (
	"strings"
	"testing"
)

func TestJoinMessage(t *testing.T) {
	cases := []struct {
		nick string
		want string
	}{
		{"agent", "repl: agent joined (type :help, :quit to leave)"},
		{"", "repl: agent joined (type :help, :quit to leave)"},
		{"  ", "repl: agent joined (type :help, :quit to leave)"},
		{"alice", "repl: alice joined (type :help, :quit to leave)"},
	}
	for _, c := range cases {
		if got := New(c.nick).JoinMessage(); got != c.want {
			t.Errorf("New(%q).JoinMessage() = %q, want %q", c.nick, got, c.want)
		}
	}
}

func TestHandleCommandsDoNotAdvanceTurns(t *testing.T) {
	s := New("agent")

	help := s.Handle(":help")
	if help.Quit {
		t.Fatal(":help ended the session")
	}
	for _, want := range []string{":help", ":quit"} {
		if !strings.Contains(help.Text, want) {
			t.Errorf(":help reply %q does not name %q", help.Text, want)
		}
	}

	if r := s.Handle("   "); r.Text != "" || r.Quit {
		t.Errorf("blank line reply = %+v, want empty", r)
	}

	if s.Turn() != 0 {
		t.Fatalf("Turn() = %d after commands and blank lines, want 0", s.Turn())
	}
}

func TestHandleQuitAliases(t *testing.T) {
	for _, alias := range []string{":quit", ":q", ":exit"} {
		s := New("agent")
		if r := s.Handle(alias); !r.Quit {
			t.Errorf("%s: Quit = false, want true", alias)
		}
	}
}

func TestHandleChatTurns(t *testing.T) {
	s := New("agent")

	first := s.Handle("hello")
	if first.Quit {
		t.Fatal("chat turn ended the session")
	}
	if first.Text != "turn 1: hello" {
		t.Errorf("first reply = %q, want %q", first.Text, "turn 1: hello")
	}
	if s.Turn() != 1 {
		t.Fatalf("Turn() = %d after one chat turn, want 1", s.Turn())
	}

	second := s.Handle("  again  ")
	if second.Text != "turn 2: again" {
		t.Errorf("second reply = %q, want %q", second.Text, "turn 2: again")
	}

	// A command between turns must not consume a turn.
	s.Handle(":help")
	if s.Turn() != 2 {
		t.Fatalf("Turn() = %d after a command between turns, want 2", s.Turn())
	}
}

func TestHandleSingleLineResponses(t *testing.T) {
	s := New("agent")
	for _, line := range []string{"hello", "two words", "with\nnewline"} {
		reply := s.Handle(line)
		if reply.Text == "" {
			t.Errorf("line %q produced no response", line)
		}
		if c := strings.Count(strings.TrimRight(reply.Text, "\n"), "\n"); c != 0 {
			t.Errorf("line %q produced %d embedded newlines: %q", line, c, reply.Text)
		}
	}
}

func TestRunScriptedSession(t *testing.T) {
	s := New("tester")
	var events []string
	err := s.Run(strings.NewReader("hello there\n\n:help\n:quit\nignored\n"), func(e string) {
		events = append(events, e)
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(events, "")
	for _, want := range []string{
		"repl: tester joined (type :help, :quit to leave)\n",
		"you> ",
		"turn 1: hello there\n",
		"commands: :help, :quit\n",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("session transcript missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "ignored") {
		t.Errorf("lines after :quit were processed:\n%s", joined)
	}
}

func TestRunEndsOnEOF(t *testing.T) {
	s := New("")
	var events []string
	if err := s.Run(strings.NewReader("hi\n"), func(e string) { events = append(events, e) }); err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(events, "")
	if !strings.Contains(joined, "repl: agent joined") {
		t.Errorf("blank nickname did not default to agent:\n%s", joined)
	}
	if !strings.Contains(joined, "turn 1: hi") || !strings.Contains(joined, "you> ") {
		t.Errorf("EOF session transcript incomplete:\n%s", joined)
	}
}
