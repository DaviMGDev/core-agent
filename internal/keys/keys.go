// Package keys holds the shared typed contracts of the native default
// composition: one package-level key per provided capability, with the value
// an interface the provider implements and consumers use. Memento keys are
// identity values, so sharing a contract means sharing these variables —
// not agreeing on names (the names match the historical provide/inject key
// strings for diagnostics). Plugins import this package for keys and
// contract types only, never one another's libraries; the wasm guests keep
// their JSON wire ABI.
package keys

import (
	"context"
	"encoding/json"
	"fmt"

	mcontext "github.com/DaviMGDev/memento/context"
)

// Provider is one AI provider endpoint. Credential is a non-secret
// reference such as "env:OPENAI_API_KEY", never a literal secret; it may be
// empty for an endpoint that needs no authentication. Models lists the
// concrete model names the provider serves. Mock providers answer
// in-process and need neither an endpoint nor a credential.
type Provider struct {
	Name       string
	Endpoint   string
	Credential string
	Models     []string
	Mock       bool
}

// ProviderRegistry is the live provider registry behind the
// "provider-registry" key: provider plugins register at activation and
// unregister on unload; model-manager resolves through it per response.
type ProviderRegistry interface {
	Register(p Provider) error
	Unregister(name string) error
	Get(name string) (Provider, bool)
	List() []Provider
	ProviderFor(model string) (Provider, bool)
}

// ProviderRegistryKey is the provider-registry capability: the native
// provider-manager binds it, provider-openai and model-manager inject it.
var ProviderRegistryKey = mcontext.NewKey[ProviderRegistry]("provider-registry")

// ChatMessage is one conversation turn supplied as context to a response.
type ChatMessage struct {
	Role string
	Text string
}

// PresentedTool is one callable of the agent's presented surface: the name
// the model sees, its description, and its argument schema.
type PresentedTool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ModelResolution is the flattened resolution of a model view name: how it
// resolves and the concrete models it reaches.
type ModelResolution struct {
	Mode   string
	Models []string
}

// ModelRegistry is the model service behind the "model-registry" key: the
// native model-manager binds it, the agent injects it. Respond answers
// through the resolved provider; the tools travel as a native tools array
// when the surface is non-empty.
type ModelRegistry interface {
	Resolve(name string) (ModelResolution, error)
	Respond(model string, messages []ChatMessage, tools []PresentedTool) (string, error)
}

// ModelRegistryKey is the model-registry capability: the native
// model-manager binds it, the agent injects it.
var ModelRegistryKey = mcontext.NewKey[ModelRegistry]("model-registry")

// AgentLoop is the agent turn loop behind the "agent-loop" key: the
// terminal, the host waker, and subagent runners reach it through Handle
// with wake documents.
type AgentLoop interface {
	Handle(ctx context.Context, req []byte) ([]byte, error)
}

// AgentLoopKey is the agent-loop capability: the native agent binds its
// loop, the terminal injects it.
var AgentLoopKey = mcontext.NewKey[AgentLoop]("agent-loop")

// ReplKey is the repl capability the native terminal binds to its session
// nickname. Nothing injects it; the declaration documents the terminal.
var ReplKey = mcontext.NewKey[string]("repl")

// History is the conversation record behind the "chat-history" key:
// append turns and serve recent windows. Append reports the conversation
// length after the append, like the operation document's turn field.
type History interface {
	Append(conversation, role, text string) (turn int, err error)
	Recent(conversation string, n int) []ChatMessage
}

// HistoryKey is the chat-history capability: the native chat-history
// binds it, the agent injects it.
var HistoryKey = mcontext.NewKey[History]("chat-history")

// Context is the projection service behind the "llm-context" key: it
// projects recorded turns into the bounded window a model call receives.
// Project returns the kept turns in append order and the dropped count.
type Context interface {
	Project(messages []ChatMessage) (kept []ChatMessage, dropped int)
}

// ContextKey is the llm-context capability: the native context-manager
// binds it, the agent injects it.
var ContextKey = mcontext.NewKey[Context]("llm-context")

// PayloadBytes normalizes an activation payload to bytes: the entry hands
// payloads over as strings, tests as strings or byte slices, and an absent
// payload arrives as nil.
func PayloadBytes(payload any) ([]byte, error) {
	switch p := payload.(type) {
	case nil:
		return nil, nil
	case string:
		return []byte(p), nil
	case []byte:
		return p, nil
	default:
		return nil, fmt.Errorf("keys: unsupported payload type %T", payload)
	}
}
