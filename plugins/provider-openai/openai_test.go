package provideropenai

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultsRegisterOpenAI(t *testing.T) {
	cfg := Defaults()
	if cfg.Name != "openai" || cfg.Endpoint != "https://api.openai.com/v1" {
		t.Fatalf("defaults = %+v, want the standard OpenAI provider", cfg)
	}
	if cfg.Credential != "env:OPENAI_API_KEY" {
		t.Fatalf("credential = %q, want a reference, never a secret", cfg.Credential)
	}
	if len(cfg.Models) != 2 || cfg.Models[0] != "gpt-4o" || cfg.Models[1] != "gpt-4o-mini" {
		t.Fatalf("models = %v, want gpt-4o and gpt-4o-mini", cfg.Models)
	}
}

func TestParseConfigEmptyIsDefaults(t *testing.T) {
	for _, payload := range [][]byte{nil, {}, []byte("  ")} {
		cfg, err := ParseConfig(payload)
		if err != nil {
			t.Fatalf("ParseConfig(%q): %v", payload, err)
		}
		if want := Defaults(); cfg.Name != want.Name || cfg.Endpoint != want.Endpoint ||
			cfg.Credential != want.Credential || len(cfg.Models) != len(want.Models) {
			t.Fatalf("ParseConfig(%q) = %+v, want defaults %+v", payload, cfg, want)
		}
	}
}

func TestParseConfigOverrides(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"name":"proxy","endpoint":"http://127.0.0.1:11434/v1","models":["llama-3.2"]}`))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	if cfg.Name != "proxy" || cfg.Endpoint != "http://127.0.0.1:11434/v1" {
		t.Fatalf("config = %+v, want the overridden name and endpoint", cfg)
	}
	if cfg.Credential != DefaultCredential {
		t.Fatalf("credential = %q, want the default kept when unset", cfg.Credential)
	}
	if len(cfg.Models) != 1 || cfg.Models[0] != "llama-3.2" {
		t.Fatalf("models = %v, want the overridden list", cfg.Models)
	}
}

func TestParseConfigInvalid(t *testing.T) {
	if _, err := ParseConfig([]byte("not json")); err == nil {
		t.Fatal("ParseConfig(invalid) = nil, want error")
	}
}

func TestRegisterRequestShape(t *testing.T) {
	req := Defaults().RegisterRequest()
	var doc struct {
		Op       string `json:"op"`
		Provider Config `json:"provider"`
	}
	if err := json.Unmarshal(req, &doc); err != nil {
		t.Fatalf("register request is not valid JSON: %v", err)
	}
	if doc.Op != "register" || doc.Provider.Name != "openai" {
		t.Fatalf("register request = %s, want op register for openai", req)
	}
	for _, want := range []string{`"provider"`, `"endpoint"`, `"credential"`, `"models"`} {
		if !strings.Contains(string(req), want) {
			t.Fatalf("register request = %s, want field %s", req, want)
		}
	}
}

func TestUnregisterRequestShape(t *testing.T) {
	req := Defaults().UnregisterRequest()
	var doc struct {
		Op   string `json:"op"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(req, &doc); err != nil {
		t.Fatalf("unregister request is not valid JSON: %v", err)
	}
	if doc.Op != "unregister" || doc.Name != "openai" {
		t.Fatalf("unregister request = %s, want op unregister for openai", req)
	}
}

func TestPresentDetectsSameNamedEntry(t *testing.T) {
	cfg := Defaults()
	if !cfg.Present([]byte(`{"providers":[{"name":"openai"},{"name":"local"}]}`)) {
		t.Fatal("Present = false with openai listed, want true")
	}
	if cfg.Present([]byte(`{"providers":[{"name":"local"}]}`)) {
		t.Fatal("Present = true without openai, want false")
	}
	if cfg.Present([]byte("not json")) {
		t.Fatal("Present = true on invalid JSON, want false")
	}
}

func TestChatCompletionPayloadSerializesToolMessageWithCallID(t *testing.T) {
	req := ChatRequest{
		Model: "gpt-4o",
		Messages: []ChatMessage{
			{Role: "user", Content: "run tool"},
			{Role: "assistant", Content: ""},
			{Role: "tool", Content: `{"result":42}`, ToolCallID: "call_123"},
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	payload := string(b)
	if !strings.Contains(payload, `"role":"tool"`) {
		t.Fatalf("payload missing role tool: %s", payload)
	}
	if !strings.Contains(payload, `"tool_call_id":"call_123"`) {
		t.Fatalf("payload missing tool_call_id call_123: %s", payload)
	}
}

func TestChatRequestSerializesParallelToolCallsFalse(t *testing.T) {
	f := false
	req := ChatRequest{
		Model:             "gpt-4o",
		Messages:          []ChatMessage{{Role: "user", Content: "test"}},
		ParallelToolCalls: &f,
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	payload := string(b)
	if !strings.Contains(payload, `"parallel_tool_calls":false`) {
		t.Fatalf("payload missing parallel_tool_calls:false: %s", payload)
	}

	// When ParallelToolCalls is nil, it must be omitted
	reqNil := ChatRequest{
		Model:    "gpt-4o",
		Messages: []ChatMessage{{Role: "user", Content: "test"}},
	}
	bNil, err := json.Marshal(reqNil)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(bNil), "parallel_tool_calls") {
		t.Fatalf("payload should omit parallel_tool_calls when nil: %s", string(bNil))
	}
}

func TestChatRequestValidationEnforcesValidNonEmptyRoles(t *testing.T) {
	req := ChatRequest{
		Model: "gpt-4o",
		Messages: []ChatMessage{
			{Role: "system", Content: "instruction"},
			{Role: "user", Content: "query"},
			{Role: "assistant", Content: "calling tool"},
			{Role: "tool", Content: `{"result":true}`, ToolCallID: "call_1"},
		},
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("Validate(valid): unexpected error %v", err)
	}

	// Empty role rejected
	badReqEmpty := ChatRequest{
		Model:    "gpt-4o",
		Messages: []ChatMessage{{Role: "", Content: "bad"}},
	}
	if err := badReqEmpty.Validate(); err == nil || !strings.Contains(err.Error(), "empty role") {
		t.Fatalf("Validate(empty): want empty role error, got %v", err)
	}

	// Invalid role rejected
	badReqInvalid := ChatRequest{
		Model:    "gpt-4o",
		Messages: []ChatMessage{{Role: "moderator", Content: "bad"}},
	}
	if err := badReqInvalid.Validate(); err == nil || !strings.Contains(err.Error(), "invalid role") {
		t.Fatalf("Validate(invalid): want invalid role error, got %v", err)
	}
}


