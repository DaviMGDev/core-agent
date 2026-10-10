// Native provider-manager plugin: the registry behind the
// "provider-registry" key as a compiled memento component.
//
// Activation parses the payload ({"providers":[...]}) into a validated
// registry and binds it for the registry's consumers; every mutation is
// visible through the shared object, so no re-binding is needed. The
// registered effect only logs the release: the kernel reclaims the binding
// itself. Handle serves the same operation documents the wasm guest serves
// (register, unregister, list, provider-for), so the contract stays
// executable in both forms.
package providermanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/DaviMGDev/core-agent/internal/keys"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// Component is the native provider-manager plugin. Log receives lifecycle
// lines; nil discards them.
type Component struct {
	Log      io.Writer
	registry *Registry
}

// NewComponent returns a provider-manager component logging to log.
func NewComponent(log io.Writer) *Component {
	return &Component{Log: log, registry: NewRegistry()}
}

// Declarations returns the provided provider-registry key.
func (c *Component) Declarations() runtime.Declarations {
	return runtime.Declarations{Provide: []mcontext.AnyKey{keys.ProviderRegistryKey}}
}

// Activate parses the payload into the registry and binds it.
func (c *Component) Activate(inst *runtime.Instance, payload any) error {
	raw, err := keys.PayloadBytes(payload)
	if err != nil {
		return fmt.Errorf("provider-manager: %w", err)
	}
	providers, err := ParseConfig(raw)
	if err != nil {
		c.emit("provider-manager: " + err.Error() + "\n")
		return err
	}
	if c.registry == nil {
		c.registry = NewRegistry()
	}
	for _, p := range providers {
		if err := c.registry.Register(p); err != nil {
			c.emit("provider-manager: " + err.Error() + "\n")
			return err
		}
	}
	if err := runtime.Bind[keys.ProviderRegistry](inst, keys.ProviderRegistryKey, registryAdapter{c.registry}); err != nil {
		return fmt.Errorf("provider-manager: %w", err)
	}
	if err := inst.Context().RegisterEffect(func() (func() error, error) {
		return func() error {
			c.emit("provider-manager: providers released\n")
			return nil
		}, nil
	}); err != nil {
		return fmt.Errorf("provider-manager: %w", err)
	}
	c.emit(fmt.Sprintf("provider-manager: %d provider(s) ready\n", len(providers)))
	return nil
}

// Handle serves one registry operation document and returns the result
// document: the same contract the wasm guest's handler serves, including
// its failure shape — a failed operation logs its cause and answers empty
// bytes with no error, exactly like the host side of memento_handle.
func (c *Component) Handle(ctx context.Context, req []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.registry == nil {
		return nil, errors.New("provider-manager: not activated")
	}
	var op Op
	if err := json.Unmarshal(req, &op); err != nil {
		return []byte{}, nil
	}
	result, err := Apply(c.registry, op)
	if err != nil {
		c.emit(err.Error() + "\n")
		return []byte{}, nil
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("provider-manager: %w", err)
	}
	return out, nil
}

// emit writes s to the log verbatim; call sites carry their own newline,
// matching the guest's log lines exactly.
func (c *Component) emit(s string) {
	if c.Log == nil || s == "" {
		return
	}
	fmt.Fprint(c.Log, s)
}

// registryAdapter implements keys.ProviderRegistry over a *Registry,
// translating the shared contract shape to the library shape.
type registryAdapter struct {
	r *Registry
}

func toShared(p Provider) keys.Provider {
	return keys.Provider{
		Name:       p.Name,
		Endpoint:   p.Endpoint,
		Credential: p.Credential,
		Models:     append([]string(nil), p.Models...),
		Mock:       p.Mock,
	}
}

func fromShared(p keys.Provider) Provider {
	return Provider{
		Name:       p.Name,
		Endpoint:   p.Endpoint,
		Credential: p.Credential,
		Models:     append([]string(nil), p.Models...),
		Mock:       p.Mock,
	}
}

func (a registryAdapter) Register(p keys.Provider) error {
	return a.r.Register(fromShared(p))
}

func (a registryAdapter) Unregister(name string) error {
	return a.r.Remove(name)
}

func (a registryAdapter) Get(name string) (keys.Provider, bool) {
	p, ok := a.r.Get(name)
	if !ok {
		return keys.Provider{}, false
	}
	return toShared(p), true
}

func (a registryAdapter) List() []keys.Provider {
	providers := a.r.List()
	out := make([]keys.Provider, 0, len(providers))
	for _, p := range providers {
		out = append(out, toShared(p))
	}
	return out
}

func (a registryAdapter) ProviderFor(model string) (keys.Provider, bool) {
	p, ok := a.r.ProviderFor(model)
	if !ok {
		return keys.Provider{}, false
	}
	return toShared(p), true
}
