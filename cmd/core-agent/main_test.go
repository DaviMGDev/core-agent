package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DaviMGDev/core-agent/internal/config"
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

// testProvider is one scripted provider-document entry.
type testProvider struct {
	Name       string   `json:"name"`
	Endpoint   string   `json:"endpoint,omitempty"`
	Credential string   `json:"credential,omitempty"`
	Models     []string `json:"models"`
}

// providerDoc marshals a scripted provider document.
func providerDoc(providers ...testProvider) string {
	b, err := json.Marshal(struct {
		Providers []testProvider `json:"providers"`
	}{Providers: providers})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// testResolved loads the embedded defaults with isolated directories and an
// empty environment.
func testResolved(t *testing.T) *config.Config {
	t.Helper()
	resolved, err := config.Load(config.Options{
		HomeDir:   t.TempDir(),
		WorkDir:   t.TempDir(),
		LookupEnv: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return resolved
}

// sessionFrom builds a session configuration from a resolved layer.
func sessionFrom(t *testing.T, resolved *config.Config, nick string) sessionConfig {
	t.Helper()
	payloads, err := resolved.Payloads()
	if err != nil {
		t.Fatalf("config.Payloads: %v", err)
	}
	if nick != "" {
		payloads.Nick = nick
	}
	return sessionConfig{
		nick:      payloads.Nick,
		providers: payloads.Providers,
		models:    payloads.Models,
		history:   payloads.History,
		context:   payloads.Context,
		agent:     payloads.Agent,
		credentials: func(name string) (string, bool) {
			return resolved.Resolve(name, nil)
		},
	}
}

// TestLoadSessionPrecedence proves the entry's order: files lose to the
// environment, and -nick beats both.
func TestLoadSessionPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CORE_DIR", dir)
	if err := config.EnsureDefaults(dir); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	custom, err := json.Marshal(map[string]any{
		"nick":  "file-nick",
		"agent": map[string]string{"model": "file-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.SettingsFile), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CORE_NICK", "env-nick")
	t.Setenv("CORE_MODEL", "env-model")

	fromEnv, err := loadSession(false, "")
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if fromEnv.nick != "env-nick" {
		t.Errorf("nick = %q, want the environment to beat the file", fromEnv.nick)
	}
	if !strings.Contains(fromEnv.agent, `"model":"env-model"`) {
		t.Errorf("agent payload = %s, want the environment model", fromEnv.agent)
	}

	fromFlag, err := loadSession(false, "flag-nick")
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	if fromFlag.nick != "flag-nick" {
		t.Errorf("nick = %q, want the flag to beat the environment", fromFlag.nick)
	}
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

	cfg := sessionFrom(t, testResolved(t), "tester")
	cfg.providers = providerDoc(
		testProvider{Name: "local", Endpoint: srv.URL, Credential: "env:CORE_AGENT_TEST_KEY", Models: []string{"gemma4:cloud"}},
		testProvider{Name: "openai", Endpoint: "https://api.openai.com/v1", Credential: "env:OPENAI_API_KEY", Models: []string{"gpt-4o-mini"}},
	)

	var out transcript
	in := strings.NewReader("hello there\n:help\n:quit\n")
	if err := runConfig(context.Background(), in, &out, cfg); err != nil {
		t.Fatalf("runConfig: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		`provider-manager: 2 provider(s) ready`,
		`model-manager: 2 model view(s) ready`,
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
	for _, want := range []string{`"model":"gemma4:cloud"`, `"role":"user"`, `"content":"hello there"`} {
		if !strings.Contains(req.body, want) {
			t.Errorf("provider body missing %s: %s", want, req.body)
		}
	}
}

// TestRunMockLLMAnswersWithoutProvider runs a session with the in-process mock
// provider: no server, no network, and the same pipeline — resolved entirely
// from a fresh .core/ directory the layer seeds itself.
func TestRunMockLLMAnswersWithoutProvider(t *testing.T) {
	t.Setenv("CORE_DIR", t.TempDir())
	cfg, err := loadSession(true, "tester")
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}

	var out transcript
	in := strings.NewReader("hello there\n:quit\n")
	if err := runConfig(context.Background(), in, &out, cfg); err != nil {
		t.Fatalf("runConfig: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		`provider-manager: 1 provider(s) ready`,
		`mock(gemma4:cloud): hello there (context:1)`,
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

	cfg := sessionFrom(t, testResolved(t), "tester")
	cfg.providers = providerDoc(testProvider{Name: "local", Endpoint: srv.URL, Models: []string{"gemma4:cloud"}})
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

// TestRunResolvesAuthStoreCredential proves an auth: reference resolves
// host-side from auth.json — the guest only ever sends the reference — and a
// same-named environment variable shadows the stored secret.
func TestRunResolvesAuthStoreCredential(t *testing.T) {
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
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"auth ok"}}]}`)
	}))
	defer srv.Close()

	dir := t.TempDir()
	t.Setenv("CORE_DIR", dir)
	if err := config.EnsureDefaults(dir); err != nil {
		t.Fatal(err)
	}
	providers := providerDoc(testProvider{Name: "ollama", Endpoint: srv.URL, Credential: "auth:ollama", Models: []string{"gemma4:cloud"}})
	if err := os.WriteFile(filepath.Join(dir, config.ProvidersFile), []byte(providers), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := json.Marshal(map[string]any{"ollama": map[string]string{"type": "api_key", "key": "sk-auth"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.AuthFile), store, 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T) (string, bool, string) {
		t.Helper()
		cfg, err := loadSession(false, "tester")
		if err != nil {
			t.Fatalf("loadSession: %v", err)
		}
		var out transcript
		if err := runConfig(context.Background(), strings.NewReader("hello\n:quit\n"), &out, cfg); err != nil {
			t.Fatalf("runConfig: %v", err)
		}
		mu.Lock()
		defer mu.Unlock()
		return auth, seen, out.String()
	}

	if got, ok, log := run(t); got != "Bearer sk-auth" {
		t.Fatalf("authorization = %q (seen=%v), want the auth.json secret:\n%s", got, ok, log)
	}

	t.Setenv("ollama", "sk-env")
	if got, ok, log := run(t); got != "Bearer sk-env" {
		t.Fatalf("authorization = %q (seen=%v), want the environment to shadow the store:\n%s", got, ok, log)
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

	cfg := sessionFrom(t, testResolved(t), "tester")
	cfg.providers = providerDoc(testProvider{Name: "local", Endpoint: srv.URL, Models: []string{"gemma4:cloud"}})

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
