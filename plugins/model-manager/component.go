// Native model-manager plugin: the model views behind the "model-registry"
// key as a compiled memento component.
//
// Activation parses the payload ({"models":[...]}) into a registry whose
// every view resolves without cycles — a cyclic or invalid configuration
// fails the load instead of a later lookup — and binds it for the agent.
// Every response resolves its provider through the live provider registry,
// so late registrations serve and withdrawals fail loudly naming the model;
// no provider snapshot is kept. Handle serves the same operation documents
// the wasm guest serves (resolve, respond), including its failure shape:
// a failed operation logs its cause and answers empty bytes, no error.
package modelmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/DaviMGDev/core-agent/internal/keys"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// Component is the native model-manager plugin. Log receives lifecycle
// lines; nil discards them. ResolveCredential resolves a bare credential
// name (the env: scheme already stripped) to the host's secret, mirroring
// the host transport's credential hook; nil sends references literally.
// HTTPClient performs the provider exchange; nil uses a client with the
// transport's default timeout.
type Component struct {
	Log               io.Writer
	ResolveCredential func(name string) (string, bool)
	HTTPClient        *http.Client

	registry  *Registry
	providers keys.ProviderRegistry
	caller    Caller
}

// NewComponent returns a model-manager component logging to log.
func NewComponent(log io.Writer) *Component {
	return &Component{Log: log, registry: NewRegistry()}
}

// Declarations returns the provided model-registry key and the injected
// provider-registry key.
func (c *Component) Declarations() runtime.Declarations {
	return runtime.Declarations{
		Provide: []mcontext.AnyKey{keys.ModelRegistryKey},
		Inject:  []mcontext.AnyKey{keys.ProviderRegistryKey},
	}
}

// Activate parses the payload into the view registry, resolves every view
// eagerly, and binds the model service.
func (c *Component) Activate(inst *runtime.Instance, payload any) error {
	raw, err := keys.PayloadBytes(payload)
	if err != nil {
		return fmt.Errorf("model-manager: %w", err)
	}
	views, err := ParseConfig(raw)
	if err != nil {
		c.emit("model-manager: " + err.Error() + "\n")
		return err
	}
	if c.registry == nil {
		c.registry = NewRegistry()
	}
	for _, v := range views {
		if err := c.registry.Register(v); err != nil {
			c.emit("model-manager: " + err.Error() + "\n")
			return err
		}
	}
	// Resolve every view eagerly: a cyclic or invalid configuration fails
	// the load instead of a later lookup.
	for _, v := range views {
		if _, err := c.registry.Resolve(v.Name); err != nil {
			c.emit("model-manager: " + err.Error() + "\n")
			return err
		}
	}
	// No provider snapshot: every response resolves its provider through
	// the live registry, so late registrations serve and withdrawals fail
	// loudly naming the model.
	providers, ok := runtime.Get(inst, keys.ProviderRegistryKey)
	if !ok {
		return errors.New("model-manager: provider-registry is not available")
	}
	c.providers = providers
	c.caller = caller{
		providers: providers,
		resolve:   c.ResolveCredential,
		client:    c.HTTPClient,
	}
	if err := runtime.Bind[keys.ModelRegistry](inst, keys.ModelRegistryKey, modelAdapter{
		registry: c.registry,
		caller:   c.caller,
	}); err != nil {
		return fmt.Errorf("model-manager: %w", err)
	}
	if err := inst.Context().RegisterEffect(func() (func() error, error) {
		return func() error {
			c.emit("model-manager: model views released\n")
			return nil
		}, nil
	}); err != nil {
		return fmt.Errorf("model-manager: %w", err)
	}
	c.emit(fmt.Sprintf("model-manager: %d model view(s) ready\n", len(views)))
	return nil
}

// Handle serves one model operation document and returns the result
// document: the same contract the wasm guest's handler serves.
func (c *Component) Handle(ctx context.Context, req []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.registry == nil {
		return nil, errors.New("model-manager: not activated")
	}
	var op Op
	if err := json.Unmarshal(req, &op); err != nil {
		return []byte{}, nil
	}
	result, err := Apply(c.registry, op, c.callerOrDefault())
	if err != nil {
		c.emit(err.Error() + "\n")
		return []byte{}, nil
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("model-manager: %w", err)
	}
	return out, nil
}

// callerOrDefault returns the activation-wired caller, or a detached one
// when Handle runs without activation (tests that drive the handler
// directly still resolve against the live registry when bound).
func (c *Component) callerOrDefault() Caller {
	if c.caller != nil {
		return c.caller
	}
	return caller{providers: c.providers, resolve: c.ResolveCredential, client: c.HTTPClient}
}

// emit writes s to the log verbatim; call sites carry their own newline,
// matching the guest's log lines exactly.
func (c *Component) emit(s string) {
	if c.Log == nil || s == "" {
		return
	}
	fmt.Fprint(c.Log, s)
}

// modelAdapter implements keys.ModelRegistry over a view registry and its
// caller, translating the shared contract shape to the library shape.
type modelAdapter struct {
	registry *Registry
	caller   Caller
}

func (a modelAdapter) Resolve(name string) (keys.ModelResolution, error) {
	res, err := a.registry.Resolve(name)
	if err != nil {
		return keys.ModelResolution{}, err
	}
	return keys.ModelResolution{Mode: string(res.Mode), Models: res.Models}, nil
}

func (a modelAdapter) Respond(model string, messages []keys.ChatMessage, tools []keys.PresentedTool) (string, error) {
	ctx := make([]ContextMessage, 0, len(messages))
	for _, m := range messages {
		ctx = append(ctx, ContextMessage{Role: m.Role, Text: m.Text})
	}
	surface := make([]Tool, 0, len(tools))
	for _, t := range tools {
		surface = append(surface, Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	res, err := Apply(a.registry, Op{Kind: "respond", Model: model, Context: ctx, Tools: surface}, a.caller)
	if err != nil {
		return "", err
	}
	return res.Text, nil
}
