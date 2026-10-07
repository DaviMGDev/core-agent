package replchat

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func echoResponder(line string) (string, error) {
	return "echo: " + line, nil
}

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
	noRespond := func(string) (string, error) {
		t.Fatal("respond called for a command or blank line")
		return "", nil
	}

	help, err := s.Handle(":help", noRespond)
	if err != nil || help.Quit {
		t.Fatalf(":help = %+v, %v; want a non-quit reply", help, err)
	}
	for _, want := range []string{":help", ":quit"} {
		if !strings.Contains(help.Text, want) {
			t.Errorf(":help reply %q does not name %q", help.Text, want)
		}
	}

	if r, err := s.Handle("   ", noRespond); err != nil || r.Text != "" || r.Quit {
		t.Errorf("blank line reply = %+v, %v; want empty", r, err)
	}

	if s.Turn() != 0 {
		t.Fatalf("Turn() = %d after commands and blank lines, want 0", s.Turn())
	}
}

func TestHandleQuitAliases(t *testing.T) {
	for _, alias := range []string{":quit", ":q", ":exit"} {
		s := New("agent")
		if r, err := s.Handle(alias, echoResponder); err != nil || !r.Quit {
			t.Errorf("%s: reply = %+v, %v; want Quit", alias, r, err)
		}
	}
}

func TestHandleChatTurnsDelegate(t *testing.T) {
	s := New("agent")

	first, err := s.Handle("hello", echoResponder)
	if err != nil || first.Quit {
		t.Fatalf("first turn = %+v, %v", first, err)
	}
	if first.Text != "echo: hello" {
		t.Errorf("first reply = %q, want %q", first.Text, "echo: hello")
	}
	if s.Turn() != 1 {
		t.Fatalf("Turn() = %d after one chat turn, want 1", s.Turn())
	}

	second, err := s.Handle("  again  ", echoResponder)
	if err != nil || second.Text != "echo: again" {
		t.Errorf("second reply = %+v, %v; want echo: again", second, err)
	}

	// A command between turns must not consume a turn.
	if _, err := s.Handle(":help", echoResponder); err != nil {
		t.Fatalf(":help: %v", err)
	}
	if s.Turn() != 2 {
		t.Fatalf("Turn() = %d after a command between turns, want 2", s.Turn())
	}
}

func TestHandleResponderError(t *testing.T) {
	s := New("agent")
	want := errors.New("pipeline down")
	if _, err := s.Handle("hello", func(string) (string, error) { return "", want }); !errors.Is(err, want) {
		t.Fatalf("Handle error = %v, want %v", err, want)
	}
}

func TestHandleSingleLineReplies(t *testing.T) {
	s := New("agent")
	respond := func(string) (string, error) { return "two\nlines", nil }
	r, err := s.Handle("hello", respond)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if strings.Contains(r.Text, "\n") {
		t.Fatalf("reply has embedded newline: %q", r.Text)
	}
}

func TestRunScriptedSession(t *testing.T) {
	s := New("tester")
	var events []string
	err := s.Run(strings.NewReader("hello there\n\n:help\n:quit\nignored\n"), func(e string) {
		events = append(events, e)
	}, echoResponder)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(events, "")
	for _, want := range []string{
		"repl: tester joined (type :help, :quit to leave)\n",
		"you> ",
		"echo: hello there\n",
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
	if err := s.Run(strings.NewReader("hi\n"), func(e string) { events = append(events, e) }, echoResponder); err != nil {
		t.Fatalf("Run: %v", err)
	}
	joined := strings.Join(events, "")
	if !strings.Contains(joined, "repl: agent joined") {
		t.Errorf("blank nickname did not default to agent:\n%s", joined)
	}
	if !strings.Contains(joined, "echo: hi") || !strings.Contains(joined, "you> ") {
		t.Errorf("EOF session transcript incomplete:\n%s", joined)
	}
}

func TestRenderChatMessages(t *testing.T) {
	events := []Event{
		{Topic: TopicChatMessage, Payload: []byte(`{"text":"hello there"}`)},
		{Topic: "job.tick", Payload: []byte(`{"elapsed":"1s"}`)},
		{Topic: TopicChatMessage, Payload: []byte(`plain text`)},
		{Topic: TopicChatMessage, Payload: []byte(`{"text":""}`)},
		{Topic: TopicChatMessage, Payload: []byte(`{"text":"two\nlines"}`)},
	}
	got := Render(events)
	want := []string{"hello there", "plain text", "two lines"}
	if len(got) != len(want) {
		t.Fatalf("Render = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Render[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestWakeRoundTrip(t *testing.T) {
	raw := []byte(`{"events":[{"topic":"chat.message","payload":{"text":"hi"}}]}`)
	var w Wake
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("unmarshal wake: %v", err)
	}
	if w.Line != "" || len(w.Events) != 1 || w.Events[0].Topic != TopicChatMessage {
		t.Fatalf("wake = %+v, want one chat.message event", w)
	}
	if got := Render(w.Events); len(got) != 1 || got[0] != "hi" {
		t.Fatalf("Render(wake) = %q, want [hi]", got)
	}
}

func TestRunContinuesAfterResponderError(t *testing.T) {
	s := New("agent")
	var events []string
	calls := 0
	respond := func(line string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("pipeline down")
		}
		return "echo: " + line, nil
	}
	if err := s.Run(strings.NewReader("bad\n:quit\n"), func(e string) { events = append(events, e) }, respond); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if joined := strings.Join(events, ""); !strings.Contains(joined, "repl: pipeline down") {
		t.Errorf("responder error not reported:\n%s", joined)
	}
}
