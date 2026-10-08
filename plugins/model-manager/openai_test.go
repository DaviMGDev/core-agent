package modelmanager

import (
	"encoding/json"
	"strings"
	"testing"
)

// requestDoc mirrors the provider request shape the guest marshals: the tools
// field is omitempty, so an empty surface leaves no trace in the body.
type requestDoc struct {
	Model string         `json:"model"`
	Tools []FunctionTool `json:"tools,omitempty"`
}

func TestProviderRequestCarriesTheToolsArray(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"brief":{"type":"string"}},"required":["brief"]}`)
	surface := []Tool{
		{Name: "subagent", Description: "call an agent", Parameters: schema},
		{Name: "read"},
	}
	tools := OpenAITools(surface)
	if len(tools) != 2 {
		t.Fatalf("OpenAITools = %+v, want one entry per callable", tools)
	}
	if tools[0].Type != "function" || tools[0].Function.Name != "subagent" ||
		tools[0].Function.Description != "call an agent" {
		t.Fatalf("entry = %+v, want an OpenAI function entry for subagent", tools[0])
	}
	if string(tools[0].Function.Parameters) != string(schema) {
		t.Fatalf("parameters = %s, want the argument schema byte-identical", tools[0].Function.Parameters)
	}

	body, err := json.Marshal(requestDoc{Model: "gpt", Tools: tools})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string          `json:"name"`
				Parameters json.RawMessage `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Tools) != 2 || decoded.Tools[0].Type != "function" ||
		decoded.Tools[0].Function.Name != "subagent" || decoded.Tools[1].Function.Name != "read" {
		t.Fatalf("request tools = %s, want the OpenAI-compatible array built from the surface", body)
	}
	if string(decoded.Tools[0].Function.Parameters) != string(schema) {
		t.Fatalf("request parameters = %s, want the schema intact on the wire", decoded.Tools[0].Function.Parameters)
	}
}

func TestToolCallsNormalizeToTheDirective(t *testing.T) {
	call := ToolCall{ID: "call_1", Type: "function"}
	call.Function.Name = "subagent"
	call.Function.Arguments = `{"brief":"sum"}`

	text, ok := DirectiveFromToolCalls([]ToolCall{call})
	if !ok {
		t.Fatal("DirectiveFromToolCalls: no call found")
	}
	if want := `{"tool":"subagent","args":{"brief":"sum"}}`; text != want {
		t.Fatalf("directive = %q, want %q", text, want)
	}
	var directive struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal([]byte(text), &directive); err != nil {
		t.Fatalf("the directive must stay valid JSON: %v", err)
	}
	if directive.Tool != "subagent" || string(directive.Args) != `{"brief":"sum"}` {
		t.Fatalf("directive = %+v, want tool subagent with its arguments as an object", directive)
	}
}

func TestToolCallsTakeTheFirstAndTolerateEmptyArguments(t *testing.T) {
	first := ToolCall{ID: "call_1", Type: "function"}
	first.Function.Name = "read"
	first.Function.Arguments = `{"path":"/x"}`
	second := ToolCall{ID: "call_2", Type: "function"}
	second.Function.Name = "write"
	second.Function.Arguments = `{"path":"/y"}`

	text, ok := DirectiveFromToolCalls([]ToolCall{first, second})
	if !ok {
		t.Fatal("DirectiveFromToolCalls: no call found")
	}
	if want := `{"tool":"read","args":{"path":"/x"}}`; text != want {
		t.Fatalf("directive = %q, want the first call only: %q", text, want)
	}

	bare := ToolCall{}
	bare.Function.Name = "read"
	text, ok = DirectiveFromToolCalls([]ToolCall{bare})
	if !ok {
		t.Fatal("DirectiveFromToolCalls: no call found")
	}
	if want := `{"tool":"read"}`; text != want {
		t.Fatalf("directive = %q, want the args omitted when empty: %q", text, want)
	}

	invalid := ToolCall{}
	invalid.Function.Name = "read"
	invalid.Function.Arguments = "not json"
	text, ok = DirectiveFromToolCalls([]ToolCall{invalid})
	if !ok {
		t.Fatal("DirectiveFromToolCalls: no call found")
	}
	if want := `{"tool":"read"}`; text != want {
		t.Fatalf("directive = %q, want invalid arguments omitted: %q", text, want)
	}

	if _, ok := DirectiveFromToolCalls(nil); ok {
		t.Fatal("DirectiveFromToolCalls(nil) = ok, want no call")
	}
}

func TestProviderRequestOmitsToolsWhenEmpty(t *testing.T) {
	for _, surface := range [][]Tool{nil, {}} {
		if got := OpenAITools(surface); got != nil {
			t.Fatalf("OpenAITools(%v) = %+v, want nil so the field is omitted", surface, got)
		}
		body, err := json.Marshal(requestDoc{Model: "gpt", Tools: OpenAITools(surface)})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(body), "tools") {
			t.Fatalf("request body = %s, want no tools field for an empty surface", body)
		}
	}
}
