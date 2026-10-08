// Package provideropenai is the host-testable library behind the
// provider-openai plugin: one OpenAI-compatible provider document, its
// defaults, and the register/unregister operation documents the guest
// exchanges with the live provider registry. The documents cross as JSON,
// so this package never imports provider-manager's library (system spec:
// isolation); the registry owns validation.
package provideropenai

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Defaults of the registered provider: the public OpenAI endpoint, a
// credential reference the host substitutes, and the served models.
const (
	DefaultName       = "openai"
	DefaultEndpoint   = "https://api.openai.com/v1"
	DefaultCredential = "env:OPENAI_API_KEY"
)

var defaultModels = []string{"gpt-4o", "gpt-4o-mini"}

// Config is the provider this plugin registers. Every field is optional:
// an empty payload means the defaults.
type Config struct {
	Name       string   `json:"name,omitempty"`
	Endpoint   string   `json:"endpoint,omitempty"`
	Credential string   `json:"credential,omitempty"`
	Models     []string `json:"models,omitempty"`
}

// Defaults returns the standard OpenAI provider document.
func Defaults() Config {
	return Config{
		Name:       DefaultName,
		Endpoint:   DefaultEndpoint,
		Credential: DefaultCredential,
		Models:     append([]string(nil), defaultModels...),
	}
}

// ParseConfig reads an optional JSON payload over the defaults: set fields
// override, unset fields keep their defaults. An empty payload is the
// defaults; an invalid one is an error.
func ParseConfig(payload []byte) (Config, error) {
	cfg := Defaults()
	if len(bytes.TrimSpace(payload)) == 0 {
		return cfg, nil
	}
	var override Config
	if err := json.Unmarshal(payload, &override); err != nil {
		return Config{}, fmt.Errorf("provider-openai: parsing config: %w", err)
	}
	if override.Name != "" {
		cfg.Name = override.Name
	}
	if override.Endpoint != "" {
		cfg.Endpoint = override.Endpoint
	}
	if override.Credential != "" {
		cfg.Credential = override.Credential
	}
	if override.Models != nil {
		cfg.Models = override.Models
	}
	return cfg, nil
}

// RegisterRequest builds the registry operation that adds this provider:
// {"op":"register","provider":{...}}.
func (c Config) RegisterRequest() []byte {
	out, err := json.Marshal(struct {
		Op       string `json:"op"`
		Provider Config `json:"provider"`
	}{Op: "register", Provider: c})
	if err != nil {
		return nil
	}
	return out
}

// UnregisterRequest builds the registry operation that removes this
// provider: {"op":"unregister","name":...}.
func (c Config) UnregisterRequest() []byte {
	out, err := json.Marshal(struct {
		Op   string `json:"op"`
		Name string `json:"name"`
	}{Op: "unregister", Name: c.Name})
	if err != nil {
		return nil
	}
	return out
}

// Present reports whether a provider-registry list document already carries
// a provider of this name. The guest checks before registering, so a
// same-named entry (for example from a config file) is left untouched.
func (c Config) Present(listDoc []byte) bool {
	var list struct {
		Providers []struct {
			Name string `json:"name"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(listDoc, &list); err != nil {
		return false
	}
	for _, p := range list.Providers {
		if p.Name == c.Name {
			return true
		}
	}
	return false
}
