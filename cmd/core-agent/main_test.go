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
	"github.com/DaviMGDev/core-agent/plugins/agent"
	"github.com/DaviMGDev/core-agent/plugins/bash"
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
		`mock(gemma4:cloud): hello there (context:2)`,
		`repl: session closed`,
		`provider-manager: providers released`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q:\n%s", want, got)
		}
	}
}

// TestRunProviderOpenAIRegistersInComposition proves the provider plugin
// joins the default composition: with no openai entry in the provider
// document, the session transcript shows the plugin registering it at
// activation, and unloading releases it — while the session itself answers
// through the scripted local provider, untouched by the registration.
func TestRunProviderOpenAIRegistersInComposition(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"local answer"}}]}`)
	}))
	defer srv.Close()

	cfg := sessionFrom(t, testResolved(t), "tester")
	cfg.providers = providerDoc(testProvider{Name: "local", Endpoint: srv.URL, Models: []string{"gemma4:cloud"}})

	var out transcript
	in := strings.NewReader("hello there\n:quit\n")
	if err := runConfig(context.Background(), in, &out, cfg); err != nil {
		t.Fatalf("runConfig: %v", err)
	}
	got := out.String()

	for _, want := range []string{
		`provider-manager: 1 provider(s) ready`,
		`provider-openai: provider "openai" registered`,
		`local answer`,
		`provider-openai: provider "openai" released`,
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

// TestAgentPayloadListsJobTools proves the composition's declarations reach
// the agent natively: jobs, peep, and kill travel with their schemas in the
// top-level agent payload.
func TestAgentPayloadListsJobTools(t *testing.T) {
	manager := toolmanager.New(toolmanager.Options{})
	for _, tool := range toolmanager.JobTools(manager) {
		if err := manager.Registry().Declare(tool); err != nil {
			t.Fatalf("Declare(%s): %v", tool.Name, err)
		}
	}
	raw, err := agentPayload(`{"conversation":"main","model":"fast"}`, manager.Registry())
	if err != nil {
		t.Fatalf("agentPayload: %v", err)
	}
	var cfg agent.Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("Unmarshal agent payload: %v", err)
	}
	for _, tool := range cfg.Tools {
		if tool.Name == toolmanager.PeepToolName || tool.Name == toolmanager.KillToolName {
			if len(tool.Parameters) == 0 || !strings.Contains(string(tool.Parameters), "job") {
				t.Errorf("tool %s schema = %s, want a job parameter", tool.Name, tool.Parameters)
			}
		}
	}
	names := make(map[string]bool)
	for _, tool := range cfg.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{toolmanager.JobsToolName, toolmanager.PeepToolName, toolmanager.KillToolName} {
		if !names[want] {
			t.Errorf("agent payload missing tool %q: %+v", want, cfg.Tools)
		}
	}
}

// TestCompositionDeclaresBashTool proves the default composition declares the
// bash tool and the top-level agent payload presents it with its schema.
func TestCompositionDeclaresBashTool(t *testing.T) {
	sessCfg := sessionFrom(t, testResolved(t), "tester")
	var manager *toolmanager.Manager
	sessCfg.onComposed = func(m *toolmanager.Manager) { manager = m }
	sess, err := composeSession(context.Background(), sessCfg, io.Discard)
	if err != nil {
		t.Fatalf("composeSession: %v", err)
	}
	defer sess.Close()

	if manager == nil {
		t.Fatal("composition never reported the manager")
	}
	tool, ok := manager.Registry().Lookup(bash.ToolName)
	if !ok {
		t.Fatalf("registry does not hold %q", bash.ToolName)
	}
	if !strings.Contains(string(tool.Schema), "command") {
		t.Fatalf("bash schema = %s, want the command parameter", tool.Schema)
	}

	raw, err := agentPayload(`{"conversation":"main","model":"fast"}`, manager.Registry())
	if err != nil {
		t.Fatalf("agentPayload: %v", err)
	}
	var cfg agent.Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("Unmarshal agent payload: %v", err)
	}
	for _, tool := range cfg.Tools {
		if tool.Name != bash.ToolName {
			continue
		}
		if !strings.Contains(string(tool.Parameters), "remind_ms") {
			t.Fatalf("agent payload bash schema = %s, want remind_ms", tool.Parameters)
		}
		return
	}
	t.Fatalf("agent payload missing %q: %+v", bash.ToolName, cfg.Tools)
}

// TestComposedAgentConfigListsSubagentTool proves the entry injects the
// registry's tools, schemas included, into the top-level agent payload so the
// provider request carries the subagent tool natively.
func TestComposedAgentConfigListsSubagentTool(t *testing.T) {
	// Unit check: agentPayload merges the registry's tools and schemas.
	reg := toolmanager.New(toolmanager.Options{}).Registry()
	if err := reg.Declare(subagent.Tool(nil, nil, subagent.Options{})); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	raw, err := agentPayload(`{"conversation":"main","model":"fast"}`, reg)
	if err != nil {
		t.Fatalf("agentPayload: %v", err)
	}
	var cfg agent.Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("Unmarshal agent payload: %v", err)
	}
	if len(cfg.Tools) != 1 || cfg.Tools[0].Name != subagent.ToolName {
		t.Fatalf("tools = %+v, want subagent tool present in agent payload", cfg.Tools)
	}
	if len(cfg.Tools[0].Parameters) == 0 || !strings.Contains(string(cfg.Tools[0].Parameters), "brief") {
		t.Fatalf("subagent schema = %s, want brief parameter schema", cfg.Tools[0].Parameters)
	}

	// Host-composed check: running the composition carries the tool to the provider.
	var (
		mu   sync.Mutex
		body string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		body = string(b)
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"understood"}}]}`)
	}))
	defer srv.Close()

	sessCfg := sessionFrom(t, testResolved(t), "tester")
	sessCfg.providers = providerDoc(testProvider{Name: "local", Endpoint: srv.URL, Models: []string{"gemma4:cloud"}})

	var out transcript
	in := strings.NewReader("check tools\n:quit\n")
	if err := runConfig(context.Background(), in, &out, sessCfg); err != nil {
		t.Fatalf("runConfig: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{`"type":"function"`, `"name":"subagent"`, `"brief"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("provider request body missing %q:\n%s", want, body)
		}
	}
}

// TestRunNativeToolCallsSessionSubagent proves end to end that a provider
// returning OpenAI-compatible tool_calls drives a subagent delegation to
// completion, and the completion wake produces the delegated speech.
func TestRunNativeToolCallsSessionSubagent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		switch line := lastUserMessage(raw); {
		case strings.HasPrefix(line, "delegate natively"):
			_, _ = io.WriteString(w, `{
				"choices": [{
					"message": {
						"role": "assistant",
						"tool_calls": [{
							"id": "call_sub_1",
							"type": "function",
							"function": {
								"name": "subagent",
								"arguments": "{\"brief\":\"count the items\"}"
							}
						}]
					}
				}]
			}`)
		case strings.HasPrefix(line, "count the items"):
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"counted 42 items"}}]}`)
		case strings.HasPrefix(line, "job.completed"):
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"delegation finished: 42 items"}}]}`)
		default:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"fallback"}}]}`)
		}
	}))
	defer srv.Close()

	sessCfg := sessionFrom(t, testResolved(t), "tester")
	sessCfg.providers = providerDoc(testProvider{Name: "local", Endpoint: srv.URL, Models: []string{"gemma4:cloud"}})

	var out transcript
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runConfig(context.Background(), pr, &out, sessCfg) }()

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

	if _, err := pw.Write([]byte("delegate natively\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitTranscript("delegation finished: 42 items")

	if _, err := pw.Write([]byte(":quit\n")); err != nil {
		t.Fatalf("write :quit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("runConfig: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "\ncounted 42 items\n") {
		t.Fatalf("child's private message leaked to transcript:\n%s", got)
	}
}

// TestRealGemmaDelegation exercises the real gemma4:cloud model (when auth is
// present) and verifies that asking it to delegate calls the subagent tool and
// returns the reply.
func TestRealGemmaDelegation(t *testing.T) {
	cfg, err := loadSession(false, "tester")
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}
	managerReady := make(chan *toolmanager.Manager, 1)
	cfg.onComposed = func(m *toolmanager.Manager) { managerReady <- m }

	var out transcript
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- runConfig(context.Background(), pr, &out, cfg) }()

	manager := <-managerReady
	waitTranscript := func(marker string, timeout time.Duration) {
		t.Helper()
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if strings.Contains(out.String(), marker) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("transcript missing %q within %v:\n%s", marker, timeout, out.String())
	}

	// Wait for the session to be ready
	waitTranscript("repl: tester joined", 10*time.Second)

	// Instruct the model to call the subagent tool
	prompt := "Call the subagent tool with brief: what is 7 plus 5?\n"
	if _, err := pw.Write([]byte(prompt)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Wait for the delegated reply (12)
	waitTranscript("12", 45*time.Second)

	// Verify a subagent job was started and finished
	var foundSubagentJob bool
	for _, j := range manager.Jobs() {
		if j.Tool() == subagent.ToolName {
			foundSubagentJob = true
			if st := j.State(); st != toolmanager.StateDone {
				t.Fatalf("subagent job state = %s, want done", st)
			}
		}
	}
	if !foundSubagentJob {
		t.Fatalf("no subagent job was started; jobs = %+v", manager.Jobs())
	}

	if _, err := pw.Write([]byte(":quit\n")); err != nil {
		t.Fatalf("write :quit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("runConfig: %v", err)
	}
}
