// Native provider-openai plugin: a compiled memento component that injects
// the provider registry and registers its OpenAI-compatible provider at
// activation, unregistering it on unload. When the registry already carries
// the same name (for example from a config file), the plugin leaves it
// untouched and registers no cleanup — exactly the wasm guest's contract.
package provideropenai

import (
	"fmt"
	"io"

	"github.com/DaviMGDev/core-agent/internal/keys"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// Component is the native provider-openai plugin. Log receives lifecycle
// lines; nil discards them.
type Component struct {
	Log io.Writer
}

// NewComponent returns a provider-openai component logging to log.
func NewComponent(log io.Writer) *Component {
	return &Component{Log: log}
}

// Declarations returns the injected provider-registry key. The plugin
// provides nothing.
func (c *Component) Declarations() runtime.Declarations {
	return runtime.Declarations{Inject: []mcontext.AnyKey{keys.ProviderRegistryKey}}
}

// Activate parses the payload over the defaults and registers the provider,
// unless the registry already carries the same name.
func (c *Component) Activate(inst *runtime.Instance, payload any) error {
	raw, err := keys.PayloadBytes(payload)
	if err != nil {
		return fmt.Errorf("provider-openai: %w", err)
	}
	cfg, err := ParseConfig(raw)
	if err != nil {
		c.emit("provider-openai: " + err.Error() + "\n")
		return err
	}
	reg, ok := runtime.Get(inst, keys.ProviderRegistryKey)
	if !ok {
		return fmt.Errorf("provider-openai: provider-registry is not available")
	}
	// A same-named entry (for example from a config file) stays: the
	// plugin leaves it untouched rather than failing the composition.
	if _, found := reg.Get(cfg.Name); found {
		c.emit(fmt.Sprintf("provider-openai: provider %q already registered, leaving it\n", cfg.Name))
		return nil
	}
	if err := reg.Register(keys.Provider{
		Name:       cfg.Name,
		Endpoint:   cfg.Endpoint,
		Credential: cfg.Credential,
		Models:     cfg.Models,
	}); err != nil {
		c.emit(fmt.Sprintf("provider-openai: register %q failed: %v\n", cfg.Name, err))
		return err
	}
	c.emit(fmt.Sprintf("provider-openai: provider %q registered\n", cfg.Name))
	// Unload ordering deactivates dependents first, so the registry is
	// still alive when this inverse runs.
	return inst.Context().RegisterEffect(func() (func() error, error) {
		return func() error {
			if err := reg.Unregister(cfg.Name); err != nil {
				c.emit(fmt.Sprintf("provider-openai: unregister %q failed: %v\n", cfg.Name, err))
				return err
			}
			c.emit(fmt.Sprintf("provider-openai: provider %q released\n", cfg.Name))
			return nil
		}, nil
	})
}

// emit writes s to the log verbatim; call sites carry their own newline,
// matching the guest's log lines exactly.
func (c *Component) emit(s string) {
	if c.Log == nil || s == "" {
		return
	}
	fmt.Fprint(c.Log, s)
}
