// Package providermanager manages the provider side of the system: the
// registry behind the "provider-registry" key. Transport (HTTP) is deferred
// to the upstream ABI proposal; this package is management only.
package providermanager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrDuplicate is returned when a provider name is already registered.
var ErrDuplicate = errors.New("provider-manager: duplicate provider name")

// Provider identifies one AI provider endpoint. Credential is a non-secret
// reference such as "env:OPENAI_API_KEY", never a literal secret.
type Provider struct {
	Name       string `json:"name"`
	Endpoint   string `json:"endpoint"`
	Credential string `json:"credential"`
}

// Validate checks a provider definition.
func Validate(p Provider) error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("provider-manager: empty provider name")
	}
	if strings.TrimSpace(p.Credential) == "" {
		return fmt.Errorf("provider-manager: provider %q has no credential reference", p.Name)
	}
	u, err := url.Parse(p.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("provider-manager: provider %q endpoint %q is not an absolute http(s) URL", p.Name, p.Endpoint)
	}
	return nil
}

// Registry stores providers in insertion order.
type Registry struct {
	order     []string
	providers map[string]Provider
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

// Register validates and appends a provider.
func (r *Registry) Register(p Provider) error {
	if err := Validate(p); err != nil {
		return err
	}
	if _, ok := r.providers[p.Name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicate, p.Name)
	}
	r.providers[p.Name] = p
	r.order = append(r.order, p.Name)
	return nil
}

// Get returns the provider named name.
func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[name]
	return p, ok
}

// Len returns the number of registered providers.
func (r *Registry) Len() int { return len(r.order) }

// List returns the providers in insertion order.
func (r *Registry) List() []Provider {
	out := make([]Provider, 0, len(r.order))
	for _, name := range r.order {
		out = append(out, r.providers[name])
	}
	return out
}

// Remove deletes the provider named name.
func (r *Registry) Remove(name string) error {
	if _, ok := r.providers[name]; !ok {
		return fmt.Errorf("provider-manager: unknown provider %q", name)
	}
	delete(r.providers, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

type config struct {
	Providers []Provider `json:"providers"`
}

// ParseConfig reads a {"providers":[...]} payload. An empty payload is an
// empty configuration.
func ParseConfig(payload []byte) ([]Provider, error) {
	if len(bytes.TrimSpace(payload)) == 0 {
		return nil, nil
	}
	var c config
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("provider-manager: parsing config: %w", err)
	}
	return c.Providers, nil
}
