// OpenAI-compatible wire shapes: how the provider request offers the
// presented tool surface as a tools array. Pure data mapping, host-testable;
// the guest owns the exchange itself (MM4).
package modelmanager

import "encoding/json"

// FunctionTool is one entry of the OpenAI-compatible tools array.
type FunctionTool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolFunction is the callable description a provider offers natively.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// OpenAITools maps the presented surface to the OpenAI-compatible tools
// array: one {"type":"function","function":{name,description,parameters}}
// entry per callable. An empty surface maps to nil, so a request field with
// omitempty is omitted — providers without tool support see the same request
// as before.
func OpenAITools(tools []Tool) []FunctionTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]FunctionTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, FunctionTool{
			Type: "function",
			Function: ToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return out
}

// ToolCall is one function invocation of a provider's tool_calls answer.
// Arguments arrive as the JSON string the OpenAI-compatible shape carries.
type ToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// DirectiveFromToolCalls normalizes a provider's tool_calls answer to the
// agent's {"tool","args"} directive text: the first call stands, the rest
// drop — one answer, one call. The arguments string embeds as a JSON object;
// an empty or invalid one is omitted. ok is false when no call is present.
func DirectiveFromToolCalls(calls []ToolCall) (text string, ok bool) {
	if len(calls) == 0 {
		return "", false
	}
	directive := struct {
		Tool string          `json:"tool"`
		Args json.RawMessage `json:"args,omitempty"`
	}{Tool: calls[0].Function.Name}
	if args := json.RawMessage(calls[0].Function.Arguments); json.Valid(args) {
		directive.Args = args
	}
	b, err := json.Marshal(directive)
	if err != nil {
		return "", false
	}
	return string(b), true
}
