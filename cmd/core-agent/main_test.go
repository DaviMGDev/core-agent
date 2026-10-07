package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DaviMGDev/core-agent/plugins/subagent"
	toolmanager "github.com/DaviMGDev/core-agent/plugins/tool-manager"
)

// lastUserMessage returns the last user message of a chat-completions body,
// so a scripted provider can answer the turn instead of the whole transcript.
func lastUserMessage(body []byte) string {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return ""
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return req.Messages[i].Content
		}
	}
	return ""
}

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

// providerRequest is what the fake provider observed.
type providerRequest struct {
	auth string
	path string
	body string
}

// TestRunCallsTheProviderOverHTTP scripts a session against a local provider
// and asserts the whole path: the REPL pipeline reaches model-manager, whose
// guest performs the exchange over the loader's HTTP transport, with the
// credential reference resolved host-side.
func TestRunCallsTheProviderOverHTTP(t *testing.T) {
	t.Setenv("CORE_AGENT_TEST_KEY", "s3cret")

	var (
		mu   sync.Mutex
		seen []providerRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, providerRequest{auth: r.Header.Get("Authorization"), path: r.URL.Path, body: string(body)})
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"live answer from the provider"}}]}`)
	}))
	defer srv.Close()

	cfg := sessionConfig{
		nick: "tester",
		providers: fmt.Sprintf(`{"providers":[`+
			`{"name":"local","endpoint":%q,"credential":"env:CORE_AGENT_TEST_KEY","models":["llama-3.2"]},`+
			`{"name":"openai","endpoint":"https://api.openai.com/v1","credential":"env:OPENAI_API_KEY","models":["gpt-4o-mini"]}]}`, srv.URL),
		models:  modelConfig,
		history: historyConversation,
		context: contextConfig,
	}

	var out transcript
	in := strings.NewReader("hello there\n:help\n:quit\n")
	if err := runConfig(context.Background(), in, &out, cfg); err != nil {
		t.Fatalf("runConfig: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		`provider-manager: 2 provider(s) ready`,
		`model-manager: 3 model view(s) ready`,
		`chat-history: conversation "main" open`,
		`context-manager: window ready (budget 4096)`,
		`repl: tester joined`,
		`you> `,
		`live answer from the provider`,
		`commands: :help, :quit`,
		`repl: session closed`,
		`context-manager: window released`,
		`chat-history: conversation closed`,
		`model-manager: model views released`,
		`provider-manager: providers released`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q:\n%s", want, got)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("provider calls = %d, want 1:\n%s", len(seen), got)
	}
	req := seen[0]
	if req.path != "/chat/completions" {
		t.Errorf("provider path = %q, want /chat/completions", req.path)
	}
	if req.auth != "Bearer s3cret" {
		t.Errorf("authorization = %q, want the host-resolved secret", req.auth)
	}
	for _, want := range []string{`"model":"llama-3.2"`, `"role":"user"`, `"content":"hello there"`} {
		if !strings.Contains(req.body, want) {
			t.Errorf("provider body missing %s: %s", want, req.body)
		}
	}
}

// TestRunMockLLMAnswersWithoutProvider runs a session with the in-process mock
// provider: no server, no network, and the same pipeline.
func TestRunMockLLMAnswersWithoutProvider(t *testing.T) {
	cfg := defaultConfig("tester")
	cfg.providers = mockProviderConfig

	var out transcript
	in := strings.NewReader("hello there\n:quit\n")
	if err := runConfig(context.Background(), in, &out, cfg); err != nil {
		t.Fatalf("runConfig: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		`provider-manager: 1 provider(s) ready`,
		`mock(llama-3.2): hello there (context:1)`,
		`repl: session closed`,
		`provider-manager: providers released`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q:\n%s", want, got)
		}
	}
}

// TestRunMockSessionSubagentRunAndKill scripts the whole agent layer: the
// model delegates to a subagent (a job), hears the completion wake, speaks
// unprompted, then delegates a slow call the test kills.
func TestRunMockSessionSubagentRunAndKill(t *testing.T) {
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		answer := "ok"
		switch line := lastUserMessage(raw); {
		case strings.HasPrefix(line, "delegate the count"):
			answer = `{"tool":"subagent","args":{"brief":"count the files"}}`
		case strings.HasPrefix(line, "count the files"):
			answer = "the count is 3"
		case strings.HasPrefix(line, "job.completed"):
			answer = "delegated: the count is 3"
		case strings.HasPrefix(line, "stop the slow one"):
			answer = `{"tool":"subagent","args":{"brief":"hold the line"}}`
		case strings.HasPrefix(line, "hold the line"):
			<-hold
			answer = "held"
		case strings.HasPrefix(line, "job.killed"):
			answer = "stopped the delegation"
		}
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, answer)
	}))
	defer srv.Close()

	cfg := sessionConfig{
		nick:      "tester",
		providers: fmt.Sprintf(`{"providers":[{"name":"local","endpoint":%q,"models":["llama-3.2"]}]}`, srv.URL),
		models:    modelConfig,
		history:   historyConversation,
		context:   contextConfig,
	}
	managerReady := make(chan *toolmanager.Manager, 1)
	cfg.onComposed = func(m *toolmanager.Manager) { managerReady <- m }

	var out transcript
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runConfig(context.Background(), pr, &out, cfg) }()

	manager := <-managerReady
	waitTranscript := func(marker string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(out.String(), marker) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("transcript missing %q:\n%s", marker, out.String())
	}

	// A delegation is a job; its completion wakes the agent, which speaks
	// unprompted and the terminal renders the message.
	if _, err := pw.Write([]byte("delegate the count\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitTranscript("delegated: the count is 3")

	// A slow delegation is killed; the kill wakes the agent, which speaks.
	if _, err := pw.Write([]byte("stop the slow one\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	var target *toolmanager.Job
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && target == nil {
		for _, job := range manager.Jobs() {
			if job.Tool() == subagent.ToolName && job.State() == toolmanager.StateRunning {
				target = job
			}
		}
		if target == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if target == nil {
		t.Fatalf("no running subagent job:\n%s", out.String())
	}
	manager.Kill(target)
	close(hold)
	waitTranscript("stopped the delegation")
	if st := target.State(); st != toolmanager.StateKilled {
		t.Fatalf("killed subagent job = %s, want killed", st)
	}
	// The child's own turns are private: only the parent's messages render.
	if strings.Contains(out.String(), "\nthe count is 3\n") {
		t.Fatalf("the child's private message rendered:\n%s", out.String())
	}

	if _, err := pw.Write([]byte(":quit\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = pw.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runConfig: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("session did not end:\n%s", out.String())
	}
}

// TestRunWithoutCredentialSendsNoAuthorization covers a keyless provider such
// as a local runtime: the request goes out with no Authorization header.
func TestRunWithoutCredentialSendsNoAuthorization(t *testing.T) {
	var (
		mu   sync.Mutex
		auth string
		seen bool
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth, seen = r.Header.Get("Authorization"), true
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"keyless ok"}}]}`)
	}))
	defer srv.Close()

	cfg := defaultConfig("tester")
	cfg.providers = fmt.Sprintf(`{"providers":[{"name":"local","endpoint":%q,"models":["llama-3.2"]}]}`, srv.URL)

	var out transcript
	in := strings.NewReader("hello\n:quit\n")
	if err := runConfig(context.Background(), in, &out, cfg); err != nil {
		t.Fatalf("runConfig: %v", err)
	}
	if !strings.Contains(out.String(), "keyless ok") {
		t.Fatalf("transcript missing the provider answer:\n%s", out.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if !seen {
		t.Fatal("provider was never called")
	}
	if auth != "" {
		t.Fatalf("authorization = %q, want no header for a keyless provider", auth)
	}
}
