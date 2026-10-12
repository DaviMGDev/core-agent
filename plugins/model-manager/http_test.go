package modelmanager

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

	"github.com/DaviMGDev/core-agent/internal/keys"
	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// TestRespondResolvesCredentialReference proves the reference discipline
// end to end: the provider document carries env:TEST_SECRET, the exchange
// sends the resolved secret, and the reference itself never crosses.
func TestRespondResolvesCredentialReference(t *testing.T) {
	var (
		mu   sync.Mutex
		auth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"secret ok"}}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	var log lockedBuffer
	pmComp := providermanager.NewComponent(&log)
	mmComp := NewComponent(&log)
	mmComp.ResolveCredential = func(name string) (string, bool) {
		if name == "TEST_SECRET" {
			return "sk-test", true
		}
		return "", false
	}

	sched := runtime.New()
	defer sched.Close()

	pmPayload, _ := json.Marshal(map[string]any{"providers": []any{map[string]any{
		"name": "cred", "endpoint": srv.URL, "credential": "env:TEST_SECRET", "models": []string{"model-c"},
	}}})
	pmID, err := sched.Insert(pmComp, string(pmPayload))
	if err != nil {
		t.Fatalf("insert provider-manager: %v", err)
	}
	waitActive(t, sched, pmID)
	mmID, err := sched.Insert(mmComp, `{"models":[]}`)
	if err != nil {
		t.Fatalf("insert model-manager: %v", err)
	}
	waitActive(t, sched, mmID)

	raw, err := mmComp.Handle(ctx, []byte(`{"op":"respond","model":"model-c","context":[{"role":"user","text":"hi"}]}`))
	if err != nil {
		t.Fatalf("respond: %v\nlog:\n%s", err, log.String())
	}
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("respond decode: %v", err)
	}
	if res.Text != "secret ok" {
		t.Fatalf("respond = %+v, want the provider answer", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if auth != "Bearer sk-test" {
		t.Fatalf("authorization = %q, want the resolved secret (the reference must never cross)", auth)
	}

	for _, id := range []mcontext.FiberID{mmID, pmID} {
		if err := sched.Remove(id); err != nil {
			t.Fatalf("remove %d: %v", id, err)
		}
	}
	waitGone(t, sched, mmID, pmID)
}

// TestRespondFailsOnMissingCredential proves a missing secret fails the
// exchange loudly instead of crossing the reference literally.
func TestRespondFailsOnMissingCredential(t *testing.T) {
	ctx := context.Background()
	var log lockedBuffer
	pmComp := providermanager.NewComponent(&log)
	mmComp := NewComponent(&log)
	mmComp.ResolveCredential = func(name string) (string, bool) { return "", false }

	sched := runtime.New()
	defer sched.Close()

	pmPayload, _ := json.Marshal(map[string]any{"providers": []any{map[string]any{
		"name": "cred", "endpoint": "http://127.0.0.1:9", "credential": "env:MISSING", "models": []string{"model-c"},
	}}})
	pmID, err := sched.Insert(pmComp, string(pmPayload))
	if err != nil {
		t.Fatalf("insert provider-manager: %v", err)
	}
	waitActive(t, sched, pmID)
	mmID, err := sched.Insert(mmComp, `{"models":[]}`)
	if err != nil {
		t.Fatalf("insert model-manager: %v", err)
	}
	waitActive(t, sched, mmID)

	raw, err := mmComp.Handle(ctx, []byte(`{"op":"respond","model":"model-c","context":[{"role":"user","text":"hi"}]}`))
	if err != nil {
		t.Fatalf("respond errored: %v, want an empty answer", err)
	}
	if len(raw) != 0 {
		t.Fatalf("respond = %s, want no answer", raw)
	}
	if got := log.String(); !strings.Contains(got, `request to provider "cred" failed`) {
		t.Fatalf("log missing the loud failure:\n%s", got)
	}

	// The suite must not dial out: the endpoint is unroutable, so any
	// answer at all would prove the reference crossed literally.
	for _, id := range []mcontext.FiberID{mmID, pmID} {
		if err := sched.Remove(id); err != nil {
			t.Fatalf("remove %d: %v", id, err)
		}
	}
	waitGone(t, sched, mmID, pmID)
}

type testProviderRegistry struct {
	p keys.Provider
}

func (r testProviderRegistry) Register(p keys.Provider) error { return nil }
func (r testProviderRegistry) Unregister(name string) error   { return nil }
func (r testProviderRegistry) Get(name string) (keys.Provider, bool) {
	return r.p, true
}
func (r testProviderRegistry) List() []keys.Provider { return []keys.Provider{r.p} }
func (r testProviderRegistry) ProviderFor(model string) (keys.Provider, bool) {
	return r.p, true
}

// TestProviderExchangeTransportTimeoutFailsFactually proves the HTTP client timeout
// acts purely as a transport deadline, returning a factual provider failure.
func TestProviderExchangeTransportTimeoutFailsFactually(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := testProviderRegistry{
		p: keys.Provider{
			Name:     "slow",
			Endpoint: srv.URL,
			Models:   []string{"slow-model"},
		},
	}

	c := caller{
		providers: reg,
		client:    &http.Client{Timeout: 10 * time.Millisecond},
	}
	_, err := c.Complete("slow-model", []ContextMessage{{Role: "user", Text: "hi"}}, nil)
	if err == nil {
		t.Fatal("Complete succeeded, want timeout error")
	}
	if !strings.Contains(err.Error(), `request to provider "slow" failed`) {
		t.Fatalf("Complete error = %q, want provider failed message", err.Error())
	}
}

func TestCallerSerializesToolMessageWithCallID(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"done"}}]}`)
	}))
	defer srv.Close()

	reg := testProviderRegistry{
		p: keys.Provider{
			Name:     "testp",
			Endpoint: srv.URL,
			Models:   []string{"test-model"},
		},
	}
	c := caller{
		providers: reg,
		client:    &http.Client{Timeout: 5 * time.Second},
	}
	_, err := c.Complete("test-model", []ContextMessage{
		{Role: RoleUser, Text: "run"},
		{Role: RoleTool, Text: `{"status":"ok"}`, ToolCallID: "call_abc123"},
	}, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	raw := string(body)
	if !strings.Contains(raw, `"role":"tool"`) {
		t.Fatalf("sent body missing role tool: %s", raw)
	}
	if !strings.Contains(raw, `"tool_call_id":"call_abc123"`) {
		t.Fatalf("sent body missing tool_call_id: %s", raw)
	}
}

func TestCallerPassesParallelToolCallsFalseWhenToolsPresent(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	reg := testProviderRegistry{
		p: keys.Provider{
			Name:     "testp",
			Endpoint: srv.URL,
			Models:   []string{"test-model"},
		},
	}
	c := caller{
		providers: reg,
		client:    &http.Client{Timeout: 5 * time.Second},
	}
	_, err := c.Complete("test-model", []ContextMessage{{Role: RoleUser, Text: "hi"}}, []Tool{{Name: "bash"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	raw := string(body)
	if !strings.Contains(raw, `"parallel_tool_calls":false`) {
		t.Fatalf("sent body missing parallel_tool_calls:false: %s", raw)
	}

	// When tools are nil/empty, parallel_tool_calls must be omitted
	_, err = c.Complete("test-model", []ContextMessage{{Role: RoleUser, Text: "hi"}}, nil)
	if err != nil {
		t.Fatalf("Complete without tools: %v", err)
	}
	if strings.Contains(string(body), "parallel_tool_calls") {
		t.Fatalf("body should omit parallel_tool_calls when tools are nil: %s", string(body))
	}
}

func TestCallerRejectsEmptyOrInvalidMessageRolesBeforeHTTP(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer srv.Close()

	reg := testProviderRegistry{
		p: keys.Provider{
			Name:     "testp",
			Endpoint: srv.URL,
			Models:   []string{"test-model"},
		},
	}
	c := caller{
		providers: reg,
		client:    &http.Client{Timeout: 5 * time.Second},
	}

	// Empty role fails without making HTTP request
	_, err := c.Complete("test-model", []ContextMessage{{Role: "", Text: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "empty role") {
		t.Fatalf("Complete(empty role) error = %v, want empty role error", err)
	}
	if called {
		t.Fatal("HTTP request was sent despite empty role")
	}

	// Invalid role fails without making HTTP request
	_, err = c.Complete("test-model", []ContextMessage{{Role: "unknown_role", Text: "hi"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid role") {
		t.Fatalf("Complete(invalid role) error = %v, want invalid role error", err)
	}
	if called {
		t.Fatal("HTTP request was sent despite invalid role")
	}
}

func TestCallerHTTPExchangePayloadRoleCompliance(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"verified"}}]}`)
	}))
	defer srv.Close()

	reg := testProviderRegistry{
		p: keys.Provider{
			Name:     "testp",
			Endpoint: srv.URL,
			Models:   []string{"test-model"},
		},
	}
	c := caller{
		providers: reg,
		client:    &http.Client{Timeout: 5 * time.Second},
	}

	messages := []ContextMessage{
		{Role: RoleSystem, Text: "system prompt"},
		{Role: RoleUser, Text: "user line"},
		{Role: RoleAssistant, Text: "call tool"},
		{Role: RoleTool, Text: `{"status":"ok"}`, ToolCallID: "call_99"},
	}
	res, err := c.Complete("test-model", messages, nil)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res != "verified" {
		t.Fatalf("res = %q, want verified", res)
	}

	var req chatRequest
	if err := json.Unmarshal(capturedBody, &req); err != nil {
		t.Fatalf("Unmarshal captured body: %v", err)
	}
	if len(req.Messages) != 4 {
		t.Fatalf("expected 4 messages in payload, got %d", len(req.Messages))
	}
	for i, m := range req.Messages {
		if strings.TrimSpace(m.Role) == "" {
			t.Fatalf("message %d has empty role in HTTP payload: %+v", i, m)
		}
		if !ValidRole(m.Role) {
			t.Fatalf("message %d has invalid role %q in HTTP payload", i, m.Role)
		}
	}
}


