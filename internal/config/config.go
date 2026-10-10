// Package config resolves core-agent's .core/ configuration layer: a user
// scope (default ~/.core, relocated by CORE_DIR) and an optional project
// scope (./.core). Files layer per key over embedded defaults and split into
// the entries' payloads; credentials stay host-side, so a guest holds a
// reference and never a secret (system spec "Configuration", D17).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// File names of the layer.
const (
	SettingsFile  = "settings.json"
	ProvidersFile = "providers.json"
	ModelsFile    = "models.json"
	AuthFile      = "auth.json"
)

// DefaultNick is the REPL nickname when settings name none.
const DefaultNick = "agent"

// The embedded defaults: the shipped configuration, the layer's bottom.
const (
	DefaultSettings  = `{"nick":"agent","conversation":"main","context":{"budget":4096},"agent":{"model":"fast"},"clock":{"period_ms":500}}`
	DefaultProviders = `{"providers":[{"name":"ollama","endpoint":"https://ollama.com/v1","credential":"auth:ollama","models":["gemma4:cloud"]}]}`
	DefaultModels    = `{"models":[{"name":"fast","alias":"gemma4:cloud"},{"name":"reliable","fallback":["gemma4:cloud"]}]}`
)

// Settings are the entry knobs carried by settings.json.
type Settings struct {
	Nick         string          `json:"nick"`
	Conversation string          `json:"conversation"`
	Context      ContextSettings `json:"context"`
	Agent        AgentSettings   `json:"agent"`
	Clock        ClockSettings   `json:"clock"`
}

// ContextSettings bounds the projected context window.
type ContextSettings struct {
	Budget int `json:"budget"`
}

// AgentSettings selects the model view the agent resolves.
type AgentSettings struct {
	Model string `json:"model"`
}

// ClockSettings configures the notification clock: queued job events flush
// together every PeriodMS, and a boundary with nothing queued wakes no one.
// Zero delivers every event immediately.
type ClockSettings struct {
	PeriodMS int `json:"period_ms"`
}

// Options locates the layer. Every field is injectable for tests; an empty
// field falls back to the process state.
type Options struct {
	Dir        string                      // CORE_DIR: relocates the user scope
	ProjectDir string                      // explicit project scope; empty probes <WorkDir>/.core
	HomeDir    string                      // empty: os.UserHomeDir
	WorkDir    string                      // empty: os.Getwd
	LookupEnv  func(string) (string, bool) // empty: os.LookupEnv
}

// Config is the resolved layer.
type Config struct {
	Settings  Settings
	Providers []byte            // merged provider document
	Models    []byte            // merged model-view document
	Auth      map[string]string // credential name -> secret (user scope only)
}

// Payloads are the six per-entry payloads handed to the composition.
type Payloads struct {
	Providers string // provider-manager
	Models    string // model-manager
	History   string // chat-history: the conversation id
	Context   string // context-manager: {"budget":N}
	Agent     string // agent: {"conversation":...,"model":...}
	Nick      string // repl-chat
}

// Dirs resolves the user and project scopes. The user scope is Options.Dir
// when set, else the CORE_DIR environment variable, else ~/.core. The
// project scope is empty when it does not exist; when both scopes point at
// the same directory, the overlay is dropped and the directory acts as the
// user scope once.
func Dirs(opts Options) (user, project string, err error) {
	lookup := lookupFor(opts)
	user = opts.Dir
	if user == "" {
		if v, ok := lookup("CORE_DIR"); ok && strings.TrimSpace(v) != "" {
			user = strings.TrimSpace(v)
		}
	}
	if user == "" {
		home := opts.HomeDir
		if home == "" {
			if home, err = os.UserHomeDir(); err != nil {
				return "", "", fmt.Errorf("config: home directory: %w", err)
			}
		}
		user = filepath.Join(home, ".core")
	}
	if opts.ProjectDir != "" {
		project = opts.ProjectDir
	} else {
		wd := opts.WorkDir
		if wd == "" {
			if wd, err = os.Getwd(); err != nil {
				return "", "", fmt.Errorf("config: working directory: %w", err)
			}
		}
		candidate := filepath.Join(wd, ".core")
		if st, statErr := os.Stat(candidate); statErr == nil && st.IsDir() {
			project = candidate
		}
	}
	if project == user {
		project = ""
	}
	return user, project, nil
}

// Load resolves the layer: embedded defaults, then the user files, then the
// project files, per key; then the environment. A known file that exists
// must parse, or the load fails naming the file. Unknown files and keys are
// ignored. auth.json is read from the user scope only.
func Load(opts Options) (*Config, error) {
	userDir, projectDir, err := Dirs(opts)
	if err != nil {
		return nil, err
	}
	lookup := lookupFor(opts)

	settingsDoc := []byte(DefaultSettings)
	providersDoc := []byte(DefaultProviders)
	modelsDoc := []byte(DefaultModels)

	for _, dir := range []string{userDir, projectDir} {
		if dir == "" {
			continue
		}
		settingsDoc, err = overlayJSON(settingsDoc, filepath.Join(dir, SettingsFile))
		if err != nil {
			return nil, err
		}
		providersDoc, err = overlayNamed(providersDoc, filepath.Join(dir, ProvidersFile), "providers")
		if err != nil {
			return nil, err
		}
		modelsDoc, err = overlayNamed(modelsDoc, filepath.Join(dir, ModelsFile), "models")
		if err != nil {
			return nil, err
		}
	}

	var cfg Config
	if err := json.Unmarshal(settingsDoc, &cfg.Settings); err != nil {
		return nil, fmt.Errorf("config: settings: %w", err)
	}
	if v, ok := lookup("CORE_NICK"); ok {
		cfg.Settings.Nick = v
	}
	if v, ok := lookup("CORE_MODEL"); ok {
		cfg.Settings.Agent.Model = v
	}
	if v, ok := lookup("CORE_CONTEXT_BUDGET"); ok {
		budget, convErr := strconv.Atoi(strings.TrimSpace(v))
		if convErr != nil {
			return nil, fmt.Errorf("config: CORE_CONTEXT_BUDGET: %w", convErr)
		}
		cfg.Settings.Context.Budget = budget
	}
	if cfg.Settings.Nick == "" {
		cfg.Settings.Nick = DefaultNick
	}
	cfg.Providers = providersDoc
	cfg.Models = modelsDoc

	cfg.Auth, err = loadAuth(userDir)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Payloads splits the resolved configuration into the entries' payloads.
func (c *Config) Payloads() (Payloads, error) {
	providersDoc, err := wireProviders(c.Providers)
	if err != nil {
		return Payloads{}, err
	}
	contextDoc, err := json.Marshal(ContextSettings{Budget: c.Settings.Context.Budget})
	if err != nil {
		return Payloads{}, fmt.Errorf("config: context payload: %w", err)
	}
	agentDoc, err := json.Marshal(struct {
		Conversation string `json:"conversation"`
		Model        string `json:"model"`
	}{Conversation: c.Settings.Conversation, Model: c.Settings.Agent.Model})
	if err != nil {
		return Payloads{}, fmt.Errorf("config: agent payload: %w", err)
	}
	return Payloads{
		Providers: string(providersDoc),
		Models:    string(c.Models),
		History:   c.Settings.Conversation,
		Context:   string(contextDoc),
		Agent:     string(agentDoc),
		Nick:      c.Settings.Nick,
	}, nil
}

// MockProviders returns the -mock provider document: one in-process mock
// serving every concrete model the resolved views name.
func (c *Config) MockProviders() ([]byte, error) {
	models, err := concreteModels(c.Models)
	if err != nil {
		return nil, err
	}
	doc := struct {
		Providers []struct {
			Name   string   `json:"name"`
			Mock   bool     `json:"mock"`
			Models []string `json:"models"`
		} `json:"providers"`
	}{}
	mock := struct {
		Name   string   `json:"name"`
		Mock   bool     `json:"mock"`
		Models []string `json:"models"`
	}{Name: "mock", Mock: true, Models: models}
	doc.Providers = append(doc.Providers, mock)
	return json.Marshal(doc)
}

// authWirePrefix reserves the env: namespace memento substitutes for
// auth-store references: the loader rewrites auth:NAME to env:auth_NAME,
// because the host transport substitutes only env:NAME references.
const authWirePrefix = "auth_"

// authWireName bounds an auth-store entry name to what an env:NAME reference
// can carry.
var authWireName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// wireCredential rewrites one credential reference into its wire form:
// auth:NAME becomes env:auth_NAME; env:NAME and the empty reference pass
// through; any other scheme is an error, so a typo fails the load instead of
// reaching the provider literally.
func wireCredential(reference string) (string, error) {
	if reference == "" {
		return "", nil
	}
	scheme, name, ok := strings.Cut(reference, ":")
	if !ok {
		return "", fmt.Errorf("config: credential %q has no scheme", reference)
	}
	switch scheme {
	case "env":
		return reference, nil
	case "auth":
		if !authWireName.MatchString(name) {
			return "", fmt.Errorf("config: credential %q: name must match %s", reference, authWireName)
		}
		return "env:" + authWirePrefix + name, nil
	default:
		return "", fmt.Errorf("config: credential %q: unknown scheme %q", reference, scheme)
	}
}

// wireProviders rewrites every provider credential into its wire form.
func wireProviders(doc []byte) ([]byte, error) {
	entries, err := namedEntries(doc, "providers")
	if err != nil {
		return nil, fmt.Errorf("config: provider document: %w", err)
	}
	for _, entry := range entries {
		reference, ok := entry["credential"].(string)
		if !ok {
			continue
		}
		wired, err := wireCredential(reference)
		if err != nil {
			if name, ok := entry["name"].(string); ok {
				return nil, fmt.Errorf("config: provider %q: %w", name, err)
			}
			return nil, err
		}
		entry["credential"] = wired
	}
	return json.Marshal(map[string]any{"providers": entries})
}

// Resolve returns the secret for a credential name as the host transport
// passes it: the bare name after the env: scheme is stripped. A name in the
// auth_ namespace reads the named credential — the host environment shadows
// the auth store. Any other name reads the host environment only.
func (c *Config) Resolve(name string, lookup func(string) (string, bool)) (string, bool) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if name == "" {
		return "", false
	}
	if !strings.HasPrefix(name, authWirePrefix) {
		return lookup(name)
	}
	entry := strings.TrimPrefix(name, authWirePrefix)
	if v, ok := lookup(entry); ok {
		return v, true
	}
	v, ok := c.Auth[entry]
	return v, ok
}

// EnsureDefaults creates the user scope when missing: the three documents
// from the embedded defaults and an empty auth.json with mode 0600. An
// existing file is never overwritten.
func EnsureDefaults(userDir string) error {
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		return fmt.Errorf("config: %s: %w", userDir, err)
	}
	files := []struct {
		name    string
		content string
		mode    os.FileMode
	}{
		{SettingsFile, DefaultSettings + "\n", 0o644},
		{ProvidersFile, DefaultProviders + "\n", 0o644},
		{ModelsFile, DefaultModels + "\n", 0o644},
		{AuthFile, "{}\n", 0o600},
	}
	for _, f := range files {
		path := filepath.Join(userDir, f.name)
		if _, err := os.Stat(path); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("config: %s: %w", path, err)
		}
		if err := os.WriteFile(path, []byte(f.content), f.mode); err != nil {
			return fmt.Errorf("config: %s: %w", path, err)
		}
	}
	return nil
}

// overlayJSON merges the optional file at path over base, per key: nested
// objects merge, scalars and arrays are replaced. A missing file leaves base
// untouched; a malformed file fails naming the file.
func overlayJSON(base []byte, path string) ([]byte, error) {
	overlay, err := readOptional(path)
	if err != nil || overlay == nil {
		return base, err
	}
	var b, o any
	if err := json.Unmarshal(base, &b); err != nil {
		return nil, fmt.Errorf("config: embedded defaults: %w", err)
	}
	if err := json.Unmarshal(overlay, &o); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	merged, err := json.Marshal(mergeValue(b, o))
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return merged, nil
}

// overlayNamed merges a document of the form {"<field>":[{name:...},...]}
// over base by entry name: a project entry replaces a same-named user entry,
// new entries append, order is preserved.
func overlayNamed(base []byte, path, field string) ([]byte, error) {
	overlay, err := readOptional(path)
	if err != nil || overlay == nil {
		return base, err
	}
	baseEntries, err := namedEntries(base, field)
	if err != nil {
		return nil, fmt.Errorf("config: embedded defaults: %w", err)
	}
	overlayEntries, err := namedEntries(overlay, field)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	index := make(map[string]int, len(baseEntries))
	for i, e := range baseEntries {
		if name, ok := e["name"].(string); ok {
			index[name] = i
		}
	}
	for _, e := range overlayEntries {
		name, _ := e["name"].(string)
		if i, ok := index[name]; ok && name != "" {
			baseEntries[i] = e
			continue
		}
		index[name] = len(baseEntries)
		baseEntries = append(baseEntries, e)
	}
	merged, err := json.Marshal(map[string]any{field: baseEntries})
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return merged, nil
}

// namedEntries decodes {"<field>":[{...}]} into its entries, keeping every
// field of each entry (unknown keys included).
func namedEntries(doc []byte, field string) ([]map[string]any, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(doc, &top); err != nil {
		return nil, err
	}
	raw, ok := top[field]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var entries []map[string]any
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// loadAuth reads the user-scope auth store: {"name":{"key":"..."}}.
func loadAuth(userDir string) (map[string]string, error) {
	auth := map[string]string{}
	if userDir == "" {
		return auth, nil
	}
	raw, err := readOptional(filepath.Join(userDir, AuthFile))
	if err != nil || raw == nil {
		return auth, err
	}
	path := filepath.Join(userDir, AuthFile)
	var store map[string]json.RawMessage
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	for name, entry := range store {
		var cred struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(entry, &cred); err != nil {
			return nil, fmt.Errorf("config: %s: %w", path, err)
		}
		if cred.Key != "" {
			auth[name] = cred.Key
		}
	}
	return auth, nil
}

// mergeValue merges overlay over base: objects merge per key, everything
// else is replaced.
func mergeValue(base, overlay any) any {
	bo, bok := base.(map[string]any)
	oo, ook := overlay.(map[string]any)
	if !bok || !ook {
		return overlay
	}
	out := make(map[string]any, len(bo)+len(oo))
	for k, v := range bo {
		out[k] = v
	}
	for k, v := range oo {
		if existing, ok := out[k]; ok {
			out[k] = mergeValue(existing, v)
			continue
		}
		out[k] = v
	}
	return out
}

// concreteModels collects every concrete model the view document names, in
// order, deduplicated: alias targets, fallback chains, discuss participants.
func concreteModels(modelsDoc []byte) ([]string, error) {
	var doc struct {
		Models []struct {
			Alias    string   `json:"alias"`
			Fallback []string `json:"fallback"`
			Discuss  []string `json:"discuss"`
		} `json:"models"`
	}
	if err := json.Unmarshal(modelsDoc, &doc); err != nil {
		return nil, fmt.Errorf("config: model views: %w", err)
	}
	seen := map[string]bool{}
	var out []string
	add := func(m string) {
		if m == "" || seen[m] {
			return
		}
		seen[m] = true
		out = append(out, m)
	}
	for _, view := range doc.Models {
		add(view.Alias)
		for _, m := range view.Fallback {
			add(m)
		}
		for _, m := range view.Discuss {
			add(m)
		}
	}
	return out, nil
}

// lookupFor returns the environment lookup, defaulting to the process one.
func lookupFor(opts Options) func(string) (string, bool) {
	if opts.LookupEnv != nil {
		return opts.LookupEnv
	}
	return os.LookupEnv
}

// readOptional returns a file's bytes, nil when the file does not exist.
func readOptional(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", path, err)
	}
	return b, nil
}
