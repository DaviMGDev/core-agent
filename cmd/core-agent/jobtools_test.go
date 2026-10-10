package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// TestRunJobToolsSession proves the model drives the job surface end to end:
// it lists a running job, peeps it mid-run, kills it — the tool reports the
// new state — and an unknown id comes back as a factual tool error, never a
// model story.
func TestRunJobToolsSession(t *testing.T) {
	var (
		mu      sync.Mutex
		bodies  []string
		target  *toolmanager.Job
		manager *toolmanager.Manager
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		line := lastUserMessage(raw)
		switch {
		case strings.HasPrefix(line, "list the jobs"):
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_jobs","type":"function","function":{"name":"jobs","arguments":"{}"}}]}}]}`)
		case strings.HasPrefix(line, "["), strings.Contains(line, `"result":[`):
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_peep","type":"function","function":{"name":"peep","arguments":"{\"job\":\"job-1\"}"}}]}}]}`)
		case strings.Contains(line, `"state":"killed"`):
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_unknown","type":"function","function":{"name":"peep","arguments":"{\"job\":\"job-99\"}"}}]}}]}`)
		case strings.Contains(line, `"output":"working`):
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_kill","type":"function","function":{"name":"kill","arguments":"{\"job\":\"job-1\"}"}}]}}]}`)
		case strings.Contains(line, "unknown job"):
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"listed, peeped, killed; unknown refused"}}]}`)
		default:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"fallback"}}]}`)
		}
	}))
	defer srv.Close()

	sessCfg := sessionFrom(t, testResolved(t), "tester")
	sessCfg.providers = providerDoc(testProvider{Name: "local", Endpoint: srv.URL, Models: []string{"gemma4:cloud"}})
	sessCfg.onComposed = func(m *toolmanager.Manager) {
		manager = m
		_ = m.Registry().Declare(toolmanager.Tool{Name: "slow", Run: func(ctx context.Context, _ json.RawMessage, out *toolmanager.Output) (any, error) {
			_, _ = out.Write([]byte("working\n"))
			<-ctx.Done()
			return nil, ctx.Err()
		}})
		target = m.Start("slow", nil, 0)
	}

	var out transcript
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runConfig(context.Background(), pr, &out, sessCfg) }()

	waitTranscript := func(marker string) {
		t.Helper()
		// Generous: under -race the composition and the wake chain are both
		// slow on this hardware (issue #14), so the budget covers the chain,
		// not the machine's speed.
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(out.String(), marker) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("transcript missing %q:\n%s", marker, out.String())
	}

	if _, err := pw.Write([]byte("list the jobs\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitTranscript("listed, peeped, killed; unknown refused")

	if _, err := pw.Write([]byte(":quit\n")); err != nil {
		t.Fatalf("write :quit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("runConfig: %v", err)
	}

	mu.Lock()
	joined := strings.Join(bodies, "\n---\n")
	mu.Unlock()
	for _, want := range []string{
		`"name":"jobs"`,
		`"name":"peep"`,
		`"name":"kill"`,
		`\"tool\":\"slow\"`,
		`\"state\":\"running\"`,
		`\"output\":\"working`,
		`\"state\":\"killed\"`,
		"unknown job",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("provider requests missing %q", want)
		}
	}

	if target == nil {
		t.Fatal("the scripted job never started")
	}
	if len(manager.Jobs()) != 1 {
		t.Errorf("len(manager.Jobs()) = %d, want exactly 1 (jobs and peep must not spawn jobs)", len(manager.Jobs()))
	}
	if state := target.State(); state != toolmanager.StateKilled {
		t.Errorf("job state after the session = %q, want killed", state)
	}
}
