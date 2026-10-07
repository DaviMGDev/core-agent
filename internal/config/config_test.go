package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandbox is an isolated home and work directory pair.
type sandbox struct {
	home string
	work string
}

func newSandbox(t *testing.T) sandbox {
	t.Helper()
	return sandbox{home: t.TempDir(), work: t.TempDir()}
}

func (s sandbox) opts() Options {
	return Options{HomeDir: s.home, WorkDir: s.work, LookupEnv: noEnv}
}

// noEnv is a hermetic environment: nothing set.
func noEnv(string) (string, bool) { return "", false }

func (s sandbox) userFile(t *testing.T, name, content string) {
	t.Helper()
	writeAt(t, filepath.Join(s.home, ".core"), name, content)
}

func (s sandbox) projectFile(t *testing.T, name, content string) {
	t.Helper()
	writeAt(t, filepath.Join(s.work, ".core"), name, content)
}

func writeAt(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type providerEntry struct {
	Name       string   `json:"name"`
	Endpoint   string   `json:"endpoint"`
	Credential string   `json:"credential"`
	Models     []string `json:"models"`
	Mock       bool     `json:"mock"`
}

func providersOf(t *testing.T, doc []byte) []providerEntry {
	t.Helper()
	var parsed struct {
		Providers []providerEntry `json:"providers"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("providers document: %v", err)
	}
	return parsed.Providers
}

// --- Precedence (leaf 2.1) ---

func TestUserOverridesDefaults(t *testing.T) {
	s := newSandbox(t)
	s.userFile(t, SettingsFile, `{"nick":"user","context":{"budget":128}}`)

	cfg, err := Load(s.opts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.Nick != "user" {
		t.Errorf("nick = %q, want user", cfg.Settings.Nick)
	}
	if cfg.Settings.Context.Budget != 128 {
		t.Errorf("budget = %d, want 128", cfg.Settings.Context.Budget)
	}
	if cfg.Settings.Conversation != "main" {
		t.Errorf("conversation = %q, want the default main", cfg.Settings.Conversation)
	}
}

func TestProjectOverridesUserPerKey(t *testing.T) {
	s := newSandbox(t)
	s.userFile(t, SettingsFile, `{"nick":"user","context":{"budget":4096}}`)
	s.projectFile(t, SettingsFile, `{"nick":"project"}`)

	cfg, err := Load(s.opts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.Nick != "project" {
		t.Errorf("nick = %q, want project", cfg.Settings.Nick)
	}
	if cfg.Settings.Context.Budget != 4096 {
		t.Errorf("budget = %d, want the user's 4096 to survive", cfg.Settings.Context.Budget)
	}
}

func TestNestedSettingsMergePerKey(t *testing.T) {
	s := newSandbox(t)
	s.userFile(t, SettingsFile, `{"context":{"budget":4096,"window":8}}`)
	s.projectFile(t, SettingsFile, `{"context":{"budget":64}}`)

	merged, err := overlayJSON([]byte(DefaultSettings), filepath.Join(s.home, ".core", SettingsFile))
	if err != nil {
		t.Fatalf("user overlay: %v", err)
	}
	merged, err = overlayJSON(merged, filepath.Join(s.work, ".core", SettingsFile))
	if err != nil {
		t.Fatalf("project overlay: %v", err)
	}
	var doc struct {
		Context map[string]int `json:"context"`
	}
	if err := json.Unmarshal(merged, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Context["budget"] != 64 {
		t.Errorf("budget = %d, want the project's 64", doc.Context["budget"])
	}
	if doc.Context["window"] != 8 {
		t.Errorf("window = %d, want the user's 8 to survive the nested merge", doc.Context["window"])
	}
}

func TestProjectProviderOverridesUserByName(t *testing.T) {
	s := newSandbox(t)
	s.userFile(t, ProvidersFile, `{"providers":[`+
		`{"name":"ollama","endpoint":"https://user.example/v1","models":["gemma4:cloud"]},`+
		`{"name":"local","endpoint":"http://127.0.0.1:11434/v1","models":["llama-3.2"]}]}`)
	s.projectFile(t, ProvidersFile, `{"providers":[`+
		`{"name":"ollama","endpoint":"https://project.example/v1","models":["gemma4:cloud"]},`+
		`{"name":"extra","endpoint":"https://extra.example/v1","models":["x"]}]}`)

	cfg, err := Load(s.opts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	providers := providersOf(t, cfg.Providers)
	if len(providers) != 3 {
		t.Fatalf("providers = %d, want 3 (replaced, kept, appended)", len(providers))
	}
	if providers[0].Name != "ollama" || providers[0].Endpoint != "https://project.example/v1" {
		t.Errorf("ollama = %+v, want the project's endpoint in place", providers[0])
	}
	if providers[1].Name != "local" {
		t.Errorf("providers[1] = %q, want the user's local to survive", providers[1].Name)
	}
	if providers[2].Name != "extra" {
		t.Errorf("providers[2] = %q, want the project's new entry appended", providers[2].Name)
	}
}

// --- Validation (leaf 2.2) ---

func TestMalformedKnownFileFailsNamingIt(t *testing.T) {
	s := newSandbox(t)
	s.userFile(t, SettingsFile, `{"nick":`)

	_, err := Load(s.opts())
	if err == nil {
		t.Fatal("Load succeeded on a malformed settings.json")
	}
	if !strings.Contains(err.Error(), "settings.json") {
		t.Errorf("error %q does not name settings.json", err)
	}
}

func TestMalformedProviderDocumentFailsNamingIt(t *testing.T) {
	s := newSandbox(t)
	s.projectFile(t, ProvidersFile, `{"providers": [}`)

	_, err := Load(s.opts())
	if err == nil {
		t.Fatal("Load succeeded on a malformed providers.json")
	}
	if !strings.Contains(err.Error(), "providers.json") {
		t.Errorf("error %q does not name providers.json", err)
	}
}

func TestUnknownFilesAndKeysAreIgnored(t *testing.T) {
	s := newSandbox(t)
	s.userFile(t, "notes.txt", "not configuration")
	s.userFile(t, SettingsFile, `{"nick":"user","notes":"ignored"}`)

	cfg, err := Load(s.opts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.Nick != "user" {
		t.Errorf("nick = %q, want user", cfg.Settings.Nick)
	}
}

// --- Payload split (leaf 2.3) ---

func TestShippedDefaultsSplitIntoSixPayloads(t *testing.T) {
	s := newSandbox(t)
	cfg, err := Load(s.opts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	payloads, err := cfg.Payloads()
	if err != nil {
		t.Fatalf("Payloads: %v", err)
	}

	providers := providersOf(t, []byte(payloads.Providers))
	if len(providers) != 1 || providers[0].Name != "ollama" {
		t.Fatalf("providers = %+v, want the ollama default", providers)
	}
	if providers[0].Endpoint != "https://ollama.com/v1" || providers[0].Credential != "env:auth_ollama" {
		t.Errorf("ollama = %+v, want the cloud endpoint and the wired auth reference", providers[0])
	}
	if len(providers[0].Models) != 1 || providers[0].Models[0] != "gemma4:cloud" {
		t.Errorf("ollama models = %v, want gemma4:cloud", providers[0].Models)
	}

	var models struct {
		Models []struct {
			Name  string `json:"name"`
			Alias string `json:"alias"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(payloads.Models), &models); err != nil {
		t.Fatalf("models payload: %v", err)
	}
	resolved := ""
	for _, view := range models.Models {
		if view.Name == "fast" {
			resolved = view.Alias
		}
	}
	if resolved != "gemma4:cloud" {
		t.Errorf("fast resolves to %q, want gemma4:cloud", resolved)
	}

	if payloads.History != "main" {
		t.Errorf("history = %q, want main", payloads.History)
	}
	var ctx struct {
		Budget int `json:"budget"`
	}
	if err := json.Unmarshal([]byte(payloads.Context), &ctx); err != nil || ctx.Budget != 4096 {
		t.Errorf("context payload = %q (err %v), want budget 4096", payloads.Context, err)
	}
	var agent struct {
		Conversation string `json:"conversation"`
		Model        string `json:"model"`
	}
	if err := json.Unmarshal([]byte(payloads.Agent), &agent); err != nil ||
		agent.Conversation != "main" || agent.Model != "fast" {
		t.Errorf("agent payload = %q (err %v), want main/fast", payloads.Agent, err)
	}
	if payloads.Nick != "agent" {
		t.Errorf("nick = %q, want agent", payloads.Nick)
	}
}

// TestShippedDefaultsResolveFastToOllama follows the whole chain: the agent's
// "fast" view resolves to gemma4:cloud, and gemma4:cloud is served by ollama
// at the cloud endpoint.
func TestShippedDefaultsResolveFastToOllama(t *testing.T) {
	s := newSandbox(t)
	cfg, err := Load(s.opts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	payloads, err := cfg.Payloads()
	if err != nil {
		t.Fatalf("Payloads: %v", err)
	}

	var views struct {
		Models []struct {
			Name  string `json:"name"`
			Alias string `json:"alias"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(payloads.Models), &views); err != nil {
		t.Fatalf("models payload: %v", err)
	}
	concrete := ""
	for _, view := range views.Models {
		if view.Name == "fast" {
			concrete = view.Alias
		}
	}
	if concrete != "gemma4:cloud" {
		t.Fatalf("fast resolves to %q, want gemma4:cloud", concrete)
	}

	for _, provider := range providersOf(t, []byte(payloads.Providers)) {
		for _, model := range provider.Models {
			if model == concrete {
				if provider.Name != "ollama" || provider.Endpoint != "https://ollama.com/v1" {
					t.Errorf("serving provider = %s at %s, want ollama at the cloud endpoint", provider.Name, provider.Endpoint)
				}
				return
			}
		}
	}
	t.Errorf("no provider serves %q", concrete)
}

func TestMockProviderServesTheResolvedModels(t *testing.T) {
	s := newSandbox(t)
	cfg, err := Load(s.opts())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	mockDoc, err := cfg.MockProviders()
	if err != nil {
		t.Fatalf("MockProviders: %v", err)
	}
	providers := providersOf(t, mockDoc)
	if len(providers) != 1 || !providers[0].Mock || providers[0].Name != "mock" {
		t.Fatalf("mock providers = %+v, want one in-process mock", providers)
	}
	if len(providers[0].Models) != 1 || providers[0].Models[0] != "gemma4:cloud" {
		t.Errorf("mock models = %v, want gemma4:cloud", providers[0].Models)
	}
}

// --- First run (leaf 3.1) ---

func TestFirstRunSeedsTheDefaults(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".core")
	if err := EnsureDefaults(dir); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	for _, name := range []string{SettingsFile, ProvidersFile, ModelsFile, AuthFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	info, err := os.Stat(filepath.Join(dir, AuthFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("auth.json mode = %o, want 600", perm)
	}

	cfg, err := Load(Options{Dir: dir, WorkDir: t.TempDir(), LookupEnv: noEnv})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.Nick != "agent" {
		t.Errorf("nick = %q, want the seeded agent", cfg.Settings.Nick)
	}
	if len(cfg.Auth) != 0 {
		t.Errorf("auth = %v, want empty", cfg.Auth)
	}
}

func TestSeedingNeverOverwrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".core")
	if err := EnsureDefaults(dir); err != nil {
		t.Fatalf("EnsureDefaults: %v", err)
	}
	custom := `{"nick":"kept"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, SettingsFile), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefaults(dir); err != nil {
		t.Fatalf("second EnsureDefaults: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, SettingsFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != custom {
		t.Errorf("settings.json = %q, want the customized content to survive", b)
	}
}

func TestCoreDirRelocatesTheUserScope(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, SettingsFile, `{"nick":"moved"}`)
	lookup := func(name string) (string, bool) {
		if name == "CORE_DIR" {
			return dir, true
		}
		return "", false
	}
	cfg, err := Load(Options{HomeDir: t.TempDir(), WorkDir: t.TempDir(), LookupEnv: lookup})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Settings.Nick != "moved" {
		t.Errorf("nick = %q, want moved (from CORE_DIR)", cfg.Settings.Nick)
	}
}

// --- Credentials (leaf 4.1) ---

func TestWireCredential(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"auth:ollama", "env:auth_ollama", false},
		{"env:OPENAI_API_KEY", "env:OPENAI_API_KEY", false},
		{"", "", false},
		{"auth:bad-name", "", true},
		{"bearer:x", "", true},
		{"noscheme", "", true},
	} {
		got, err := wireCredential(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("wireCredential(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if got != tc.want {
			t.Errorf("wireCredential(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveCredentials(t *testing.T) {
	cfg := &Config{Auth: map[string]string{"ollama": "sk-auth", "SOME_KEY": "store-secret"}}

	if got, ok := cfg.Resolve("auth_ollama", noEnv); !ok || got != "sk-auth" {
		t.Errorf("auth reference = %q, %v; want sk-auth from the store", got, ok)
	}
	shadow := func(name string) (string, bool) {
		if name == "ollama" {
			return "sk-env", true
		}
		return "", false
	}
	if got, ok := cfg.Resolve("auth_ollama", shadow); !ok || got != "sk-env" {
		t.Errorf("shadowed auth reference = %q, %v; want the environment to win", got, ok)
	}

	envOnly := func(name string) (string, bool) {
		if name == "SOME_KEY" {
			return "env-secret", true
		}
		return "", false
	}
	if got, ok := cfg.Resolve("SOME_KEY", envOnly); !ok || got != "env-secret" {
		t.Errorf("env reference = %q, %v; want the environment value", got, ok)
	}
	if got, ok := cfg.Resolve("SOME_KEY", noEnv); ok {
		t.Errorf("env reference = %q, %v; the store must not satisfy an env-only name", got, ok)
	}
	if _, ok := cfg.Resolve("auth:ollama", noEnv); ok {
		t.Error("a raw auth: reference must not resolve before wiring")
	}
}
