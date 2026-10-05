package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
)

// transcript is a concurrency-safe log sink.
type transcript struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (t *transcript) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.Write(p)
}

func (t *transcript) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}

func TestRunHostsAScriptedSession(t *testing.T) {
	var out transcript
	in := strings.NewReader("hello there\nsecond question\n:help\n:quit\n")
	if err := run(context.Background(), in, &out, "tester"); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		`provider-manager: 2 provider(s) ready`,
		`model-manager: 3 model view(s) ready`,
		`chat-history: conversation "main" open`,
		`context-manager: window ready (budget 4096)`,
		`repl: tester joined`,
		`you> `,
		`[llama-3.2] hello there (context:1)`,
		`[llama-3.2] second question (context:3)`,
		`commands: :help, :quit`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("startup/session transcript missing %q:\n%s", want, got)
		}
	}

	for _, want := range []string{
		`repl: session closed`,
		`context-manager: window released`,
		`chat-history: conversation closed`,
		`model-manager: model views released`,
		`provider-manager: providers released`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unload transcript missing %q:\n%s", want, got)
		}
	}
}
