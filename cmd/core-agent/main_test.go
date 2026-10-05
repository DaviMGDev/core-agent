package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
