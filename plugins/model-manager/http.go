// Native provider exchange: how the model-manager component performs a
// completion over plain HTTP, resolved to a provider endpoint. It mirrors
// the wasm guest's exchange exactly — same request shape, same credential
// substitution, same completion decoding — with the host transport's role
// folded in: credential references resolve through the resolver, and the
// exchange runs on the component's HTTP client.
package modelmanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/DaviMGDev/core-agent/internal/keys"
)

// defaultHTTPTimeout bounds one provider exchange, matching the host
// transport's default for the guest path.
const defaultHTTPTimeout = 30 * time.Second

// credentialRef matches an `env:NAME` reference inside a header value — the
// same shape the host transport substitutes on the guest path.
var credentialRef = regexp.MustCompile(`env:[A-Za-z_][A-Za-z0-9_]*`)

// substituteRef replaces `env:NAME` references in value with the host's
// secret. A nil resolver sends references literally, like a transport with
// no credential hook. An unresolvable reference is an error naming it, so a
// missing secret fails the exchange instead of crossing the wire literally.
func substituteRef(value string, resolve func(name string) (string, bool)) (string, error) {
	if resolve == nil {
		return value, nil
	}
	for _, ref := range credentialRef.FindAllString(value, -1) {
		if _, ok := resolve(strings.TrimPrefix(ref, "env:")); !ok {
			return "", fmt.Errorf("credential %q is not available", ref)
		}
	}
	return credentialRef.ReplaceAllStringFunc(value, func(ref string) string {
		secret, _ := resolve(strings.TrimPrefix(ref, "env:"))
		return secret
	}), nil
}

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	Tools    []FunctionTool `json:"tools,omitempty"`
}

type chatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

// caller performs one model completion over HTTP, resolved to a provider
// endpoint. The credential crosses as a reference the host substitutes, so
// a secret is held only for the duration of one exchange.
type caller struct {
	providers keys.ProviderRegistry
	resolve   func(name string) (string, bool)
	client    *http.Client
}

func (c caller) Complete(model string, messages []ContextMessage, tools []Tool) (string, error) {
	p, ok := c.providers.ProviderFor(model)
	if !ok {
		return "", fmt.Errorf("model-manager: no provider serves model %q", model)
	}
	if p.Mock {
		return MockCaller{}.Complete(model, messages, tools)
	}
	msgs := make([]chatMessage, 0, len(messages))
	for _, m := range messages {
		msgs = append(msgs, chatMessage{Role: m.Role, Content: m.Text})
	}
	req := chatRequest{Model: model, Messages: msgs, Tools: OpenAITools(tools)}
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	headers := map[string]string{"content-type": "application/json"}
	if p.Credential != "" {
		secret, err := substituteRef("Bearer "+p.Credential, c.resolve)
		if err != nil {
			return "", fmt.Errorf("model-manager: request to provider %q failed", p.Name)
		}
		headers["authorization"] = secret
	}
	httpReq, err := http.NewRequest("POST", strings.TrimRight(p.Endpoint, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("model-manager: request to provider %q failed", p.Name)
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}
	client := c.client
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("model-manager: request to provider %q failed", p.Name)
	}
	defer httpResp.Body.Close()
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("model-manager: request to provider %q failed", p.Name)
	}
	return decodeCompletion(p.Name, httpResp.StatusCode, respBody)
}

// decodeCompletion extracts the assistant text from one provider response:
// a tool_calls answer normalizes to the agent's directive, any other answer
// is the message content.
func decodeCompletion(providerName string, status int, body []byte) (string, error) {
	if status < 200 || status >= 300 {
		detail := strings.TrimSpace(string(body))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		if detail == "" {
			return "", fmt.Errorf("model-manager: provider %q returned status %d", providerName, status)
		}
		return "", fmt.Errorf("model-manager: provider %q returned status %d: %s", providerName, status, detail)
	}
	var completion chatResponse
	if err := json.Unmarshal(body, &completion); err != nil {
		return "", fmt.Errorf("model-manager: decoding completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", errors.New("model-manager: completion had no choices")
	}
	message := completion.Choices[0].Message
	if directive, ok := DirectiveFromToolCalls(message.ToolCalls); ok {
		return directive, nil
	}
	return message.Content, nil
}
