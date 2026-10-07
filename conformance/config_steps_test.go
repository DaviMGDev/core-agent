package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/DaviMGDev/core-agent/internal/config"
)

type configKey struct{}

func configFrom(ctx context.Context) *configWorld {
	return ctx.Value(configKey{}).(*configWorld)
}

// configWorld is the state one .core configuration scenario observes.
type configWorld struct {
	home, work, prepared string
	env                  map[string]string

	loaded     *config.Config
	loadErr    error
	payloads   config.Payloads
	splitErr   error
	auth       map[string]string
	reference  string
	resolved   string
	resolvedOK bool
}

func registerConfigSteps(sc *godog.ScenarioContext) {
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w := &configWorld{env: map[string]string{}, auth: map[string]string{}}
		var err error
		if w.home, err = os.MkdirTemp("", "core-config-home-"); err != nil {
			return ctx, err
		}
		if w.work, err = os.MkdirTemp("", "core-config-work-"); err != nil {
			return ctx, err
		}
		return context.WithValue(ctx, configKey{}, w), nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w := configFrom(ctx)
		_ = os.RemoveAll(w.home)
		_ = os.RemoveAll(w.work)
		if w.prepared != "" {
			_ = os.RemoveAll(w.prepared)
		}
		return ctx, nil
	})

	// Discovery and precedence.
	sc.Step(`^a user config directory with the shipped defaults$`, stepConfigUserDefaults)
	sc.Step(`^a prepared config directory outside the home directory$`, stepConfigPreparedDir)
	sc.Step(`^the environment sets CORE_DIR to it$`, stepConfigEnvCoreDir)
	sc.Step(`^the environment sets CORE_NICK to "([^"]*)"$`, stepConfigEnvCoreNick)
	sc.Step(`^the loaded configuration comes from that directory$`, stepConfigFromPrepared)
	sc.Step(`^a user config with nick "([^"]*)" and context budget (\d+)$`, stepConfigUserNickBudget)
	sc.Step(`^a project config with nick "([^"]*)"$`, stepConfigProjectNick)
	sc.Step(`^a user config with nick "([^"]*)"$`, stepConfigUserNick)
	sc.Step(`^the configuration is loaded$`, stepConfigLoad)
	sc.Step(`^the loaded nick is "([^"]*)"$`, stepConfigLoadedNick)
	sc.Step(`^the loaded providers name "([^"]*)"$`, stepConfigLoadedProvider)
	sc.Step(`^the loaded context budget is (\d+)$`, stepConfigLoadedBudget)

	// Validation.
	sc.Step(`^a user config whose settings.json is malformed$`, stepConfigMalformedSettings)
	sc.Step(`^loading fails with an error naming "([^"]*)"$`, stepConfigLoadFailsNaming)
	sc.Step(`^a user config carrying an unknown notes.txt$`, stepConfigUnknownFile)
	sc.Step(`^a settings.json with an unknown key "([^"]*)"$`, stepConfigUnknownKey)
	sc.Step(`^loading succeeds and the unknown key is ignored$`, stepConfigLoadSucceeds)
	sc.Step(`^an empty user config directory$`, stepConfigEmptyDir)
	sc.Step(`^the loaded configuration is the shipped defaults$`, stepConfigShippedDefaults)

	// Payloads.
	sc.Step(`^a complete shipped configuration$`, stepConfigUserDefaults)
	sc.Step(`^the configuration is split into payloads$`, stepConfigSplit)
	sc.Step(`^the providers payload lists "([^"]*)"$`, stepConfigPayloadProvider)
	sc.Step(`^the models payload resolves "([^"]*)" to "([^"]*)"$`, stepConfigPayloadAlias)
	sc.Step(`^the agent payload names conversation "([^"]*)" and model "([^"]*)"$`, stepConfigPayloadAgent)
	sc.Step(`^the context payload budgets (\d+)$`, stepConfigPayloadBudget)
	sc.Step(`^the history payload uses conversation "([^"]*)"$`, stepConfigPayloadHistory)
	sc.Step(`^the repl payload carries nick "([^"]*)"$`, stepConfigPayloadNick)

	// First run.
	sc.Step(`^the configuration layer prepares it$`, stepConfigPrepare)
	sc.Step(`^settings.json, providers.json, and models.json hold the shipped defaults$`, stepConfigSeeded)
	sc.Step(`^auth.json exists with mode 0600 and no credentials$`, stepConfigAuthEmpty)
	sc.Step(`^a user config whose settings.json customizes the nick to "([^"]*)"$`, stepConfigCustomNick)
	sc.Step(`^settings.json still sets the nick to "([^"]*)"$`, stepConfigStillNick)

	// Credentials.
	sc.Step(`^an auth store holding "([^"]*)" under "([^"]*)"$`, stepConfigAuthStore)
	sc.Step(`^a provider referencing "([^"]*)"$`, stepConfigProviderReference)
	sc.Step(`^the host resolves the reference$`, stepConfigResolveDefault)
	sc.Step(`^the host resolves the reference "([^"]*)"$`, stepConfigResolveNamed)
	sc.Step(`^the resolved secret is "([^"]*)"$`, stepConfigResolvedSecret)
	sc.Step(`^the environment defines "([^"]*)" as "([^"]*)"$`, stepConfigEnvDefine)
	sc.Step(`^the provider document is handed to the guest$`, stepConfigHandToGuest)
	sc.Step(`^the document carries "([^"]*)" and no secret$`, stepConfigDocReference)
}

// --- helpers ---

func (w *configWorld) userDir() string    { return filepath.Join(w.home, ".core") }
func (w *configWorld) projectDir() string { return filepath.Join(w.work, ".core") }

func (w *configWorld) options() config.Options {
	return config.Options{HomeDir: w.home, WorkDir: w.work, LookupEnv: w.lookup}
}

func (w *configWorld) lookup(name string) (string, bool) {
	v, ok := w.env[name]
	return v, ok
}

func (w *configWorld) write(dir, name, content string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
}

// load reads the layer unless a scenario already did; fresh scenarios start
// without a load, so the cached result is never stale.
func (w *configWorld) load() error {
	if w.loaded == nil && w.loadErr == nil {
		w.loaded, w.loadErr = config.Load(w.options())
	}
	return w.loadErr
}

// payloadsOf splits the loaded layer, caching the result.
func (w *configWorld) payloadsOf() (config.Payloads, error) {
	if w.payloads.Providers == "" {
		if err := w.load(); err != nil {
			return config.Payloads{}, err
		}
		w.payloads, w.splitErr = w.loaded.Payloads()
	}
	return w.payloads, w.splitErr
}

// providerCredential is the wire reference the first provider carries.
func (w *configWorld) providerCredential() (string, error) {
	payloads, err := w.payloadsOf()
	if err != nil {
		return "", err
	}
	var doc struct {
		Providers []struct {
			Credential string `json:"credential"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(payloads.Providers), &doc); err != nil {
		return "", err
	}
	if len(doc.Providers) == 0 {
		return "", errors.New("the provider payload is empty")
	}
	return doc.Providers[0].Credential, nil
}

// --- discovery and precedence ---

func stepConfigUserDefaults(ctx context.Context) error {
	return config.EnsureDefaults(configFrom(ctx).userDir())
}

func stepConfigPreparedDir(ctx context.Context) error {
	w := configFrom(ctx)
	dir, err := os.MkdirTemp("", "core-config-prepared-")
	if err != nil {
		return err
	}
	w.prepared = dir
	return w.write(dir, config.SettingsFile, `{"nick":"prepared"}`)
}

func stepConfigEnvCoreDir(ctx context.Context) error {
	w := configFrom(ctx)
	if w.prepared == "" {
		return errors.New("no prepared directory to point CORE_DIR at")
	}
	w.env["CORE_DIR"] = w.prepared
	return nil
}

func stepConfigEnvCoreNick(ctx context.Context, nick string) error {
	configFrom(ctx).env["CORE_NICK"] = nick
	return nil
}

func stepConfigFromPrepared(ctx context.Context) error {
	w := configFrom(ctx)
	if err := w.load(); err != nil {
		return err
	}
	if w.loaded.Settings.Nick != "prepared" {
		return fmt.Errorf("loaded nick %q, want the CORE_DIR directory's prepared", w.loaded.Settings.Nick)
	}
	return nil
}

func stepConfigUserNickBudget(ctx context.Context, nick string, budget int) error {
	w := configFrom(ctx)
	doc, err := json.Marshal(map[string]any{
		"nick":    nick,
		"context": map[string]int{"budget": budget},
	})
	if err != nil {
		return err
	}
	return w.write(w.userDir(), config.SettingsFile, string(doc))
}

func stepConfigProjectNick(ctx context.Context, nick string) error {
	w := configFrom(ctx)
	doc, err := json.Marshal(map[string]string{"nick": nick})
	if err != nil {
		return err
	}
	return w.write(w.projectDir(), config.SettingsFile, string(doc))
}

func stepConfigUserNick(ctx context.Context, nick string) error {
	w := configFrom(ctx)
	doc, err := json.Marshal(map[string]string{"nick": nick})
	if err != nil {
		return err
	}
	return w.write(w.userDir(), config.SettingsFile, string(doc))
}

func stepConfigLoad(ctx context.Context) error {
	w := configFrom(ctx)
	w.loaded, w.loadErr = config.Load(w.options())
	return nil
}

func stepConfigLoadedNick(ctx context.Context, nick string) error {
	w := configFrom(ctx)
	if err := w.load(); err != nil {
		return err
	}
	if w.loaded.Settings.Nick != nick {
		return fmt.Errorf("loaded nick %q, want %q", w.loaded.Settings.Nick, nick)
	}
	return nil
}

func stepConfigLoadedProvider(ctx context.Context, name string) error {
	w := configFrom(ctx)
	if err := w.load(); err != nil {
		return err
	}
	var doc struct {
		Providers []struct {
			Name string `json:"name"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.loaded.Providers, &doc); err != nil {
		return err
	}
	for _, provider := range doc.Providers {
		if provider.Name == name {
			return nil
		}
	}
	return fmt.Errorf("no loaded provider named %q", name)
}

func stepConfigLoadedBudget(ctx context.Context, budget int) error {
	w := configFrom(ctx)
	if err := w.load(); err != nil {
		return err
	}
	if w.loaded.Settings.Context.Budget != budget {
		return fmt.Errorf("loaded budget %d, want %d", w.loaded.Settings.Context.Budget, budget)
	}
	return nil
}

// --- validation ---

func stepConfigMalformedSettings(ctx context.Context) error {
	w := configFrom(ctx)
	return w.write(w.userDir(), config.SettingsFile, `{"nick":`)
}

func stepConfigLoadFailsNaming(ctx context.Context, file string) error {
	w := configFrom(ctx)
	if w.loaded == nil && w.loadErr == nil {
		w.loaded, w.loadErr = config.Load(w.options())
	}
	if w.loadErr == nil {
		return fmt.Errorf("loading succeeded, want an error naming %q", file)
	}
	if !strings.Contains(w.loadErr.Error(), file) {
		return fmt.Errorf("error %q does not name %q", w.loadErr, file)
	}
	return nil
}

func stepConfigUnknownFile(ctx context.Context) error {
	w := configFrom(ctx)
	return w.write(w.userDir(), "notes.txt", "not configuration")
}

func stepConfigUnknownKey(ctx context.Context, key string) error {
	w := configFrom(ctx)
	doc, err := json.Marshal(map[string]any{"nick": "agent", key: "ignored"})
	if err != nil {
		return err
	}
	return w.write(w.userDir(), config.SettingsFile, string(doc))
}

func stepConfigLoadSucceeds(ctx context.Context) error {
	return configFrom(ctx).load()
}

func stepConfigEmptyDir(ctx context.Context) error {
	return os.MkdirAll(configFrom(ctx).userDir(), 0o755)
}

func stepConfigShippedDefaults(ctx context.Context) error {
	w := configFrom(ctx)
	if err := w.load(); err != nil {
		return err
	}
	s := w.loaded.Settings
	if s.Nick != config.DefaultNick || s.Conversation != "main" ||
		s.Context.Budget != 4096 || s.Agent.Model != "fast" {
		return fmt.Errorf("loaded %+v, want the shipped defaults", s)
	}
	return nil
}

// --- payloads ---

func stepConfigSplit(ctx context.Context) error {
	_, err := configFrom(ctx).payloadsOf()
	return err
}

func stepConfigPayloadProvider(ctx context.Context, name string) error {
	w := configFrom(ctx)
	payloads, err := w.payloadsOf()
	if err != nil {
		return err
	}
	var doc struct {
		Providers []struct {
			Name string `json:"name"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(payloads.Providers), &doc); err != nil {
		return err
	}
	for _, provider := range doc.Providers {
		if provider.Name == name {
			return nil
		}
	}
	return fmt.Errorf("the providers payload lists no %q", name)
}

func stepConfigPayloadAlias(ctx context.Context, view, model string) error {
	w := configFrom(ctx)
	payloads, err := w.payloadsOf()
	if err != nil {
		return err
	}
	var doc struct {
		Models []struct {
			Name  string `json:"name"`
			Alias string `json:"alias"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(payloads.Models), &doc); err != nil {
		return err
	}
	for _, v := range doc.Models {
		if v.Name == view {
			if v.Alias != model {
				return fmt.Errorf("%q resolves to %q, want %q", view, v.Alias, model)
			}
			return nil
		}
	}
	return fmt.Errorf("the models payload has no view %q", view)
}

func stepConfigPayloadAgent(ctx context.Context, conversation, model string) error {
	w := configFrom(ctx)
	payloads, err := w.payloadsOf()
	if err != nil {
		return err
	}
	var doc struct {
		Conversation string `json:"conversation"`
		Model        string `json:"model"`
	}
	if err := json.Unmarshal([]byte(payloads.Agent), &doc); err != nil {
		return err
	}
	if doc.Conversation != conversation || doc.Model != model {
		return fmt.Errorf("agent payload is %s, want conversation %q and model %q", payloads.Agent, conversation, model)
	}
	return nil
}

func stepConfigPayloadBudget(ctx context.Context, budget int) error {
	w := configFrom(ctx)
	payloads, err := w.payloadsOf()
	if err != nil {
		return err
	}
	var doc struct {
		Budget int `json:"budget"`
	}
	if err := json.Unmarshal([]byte(payloads.Context), &doc); err != nil {
		return err
	}
	if doc.Budget != budget {
		return fmt.Errorf("context payload budgets %d, want %d", doc.Budget, budget)
	}
	return nil
}

func stepConfigPayloadHistory(ctx context.Context, conversation string) error {
	w := configFrom(ctx)
	payloads, err := w.payloadsOf()
	if err != nil {
		return err
	}
	if payloads.History != conversation {
		return fmt.Errorf("history payload %q, want %q", payloads.History, conversation)
	}
	return nil
}

func stepConfigPayloadNick(ctx context.Context, nick string) error {
	w := configFrom(ctx)
	payloads, err := w.payloadsOf()
	if err != nil {
		return err
	}
	if payloads.Nick != nick {
		return fmt.Errorf("repl payload %q, want %q", payloads.Nick, nick)
	}
	return nil
}

// --- first run ---

func stepConfigPrepare(ctx context.Context) error {
	return config.EnsureDefaults(configFrom(ctx).userDir())
}

func stepConfigSeeded(ctx context.Context) error {
	w := configFrom(ctx)
	for name, want := range map[string]string{
		config.SettingsFile:  config.DefaultSettings,
		config.ProvidersFile: config.DefaultProviders,
		config.ModelsFile:    config.DefaultModels,
	} {
		b, err := os.ReadFile(filepath.Join(w.userDir(), name))
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(b)) != want {
			return fmt.Errorf("%s = %s, want the shipped default", name, b)
		}
	}
	return nil
}

func stepConfigAuthEmpty(ctx context.Context) error {
	path := filepath.Join(configFrom(ctx).userDir(), config.AuthFile)
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		return fmt.Errorf("auth.json mode = %o, want 600", perm)
	}
	var store map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &store); err != nil {
		return err
	}
	if len(store) != 0 {
		return fmt.Errorf("auth.json holds %d credentials, want none", len(store))
	}
	return nil
}

func stepConfigCustomNick(ctx context.Context, nick string) error {
	w := configFrom(ctx)
	if err := config.EnsureDefaults(w.userDir()); err != nil {
		return err
	}
	doc, err := json.Marshal(map[string]string{"nick": nick})
	if err != nil {
		return err
	}
	return w.write(w.userDir(), config.SettingsFile, string(doc))
}

func stepConfigStillNick(ctx context.Context, nick string) error {
	w := configFrom(ctx)
	loaded, err := config.Load(w.options())
	if err != nil {
		return err
	}
	if loaded.Settings.Nick != nick {
		return fmt.Errorf("nick = %q, want the customized %q to survive", loaded.Settings.Nick, nick)
	}
	return nil
}

// --- credentials ---

func stepConfigAuthStore(ctx context.Context, secret, name string) error {
	w := configFrom(ctx)
	w.auth[name] = secret
	store := map[string]any{}
	for entry, key := range w.auth {
		store[entry] = map[string]string{"type": "api_key", "key": key}
	}
	doc, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return w.write(w.userDir(), config.AuthFile, string(doc))
}

func stepConfigProviderReference(ctx context.Context, reference string) error {
	w := configFrom(ctx)
	w.reference = reference
	doc, err := json.Marshal(map[string]any{
		"providers": []map[string]any{{
			"name":       "ollama",
			"endpoint":   "https://ollama.example/v1",
			"credential": reference,
			"models":     []string{"gemma4:cloud"},
		}},
	})
	if err != nil {
		return err
	}
	return w.write(w.userDir(), config.ProvidersFile, string(doc))
}

func stepConfigResolveDefault(ctx context.Context) error {
	return resolveConfigReference(ctx)
}

func stepConfigResolveNamed(ctx context.Context, reference string) error {
	configFrom(ctx).reference = reference
	return resolveConfigReference(ctx)
}

// resolveConfigReference resolves the first provider's wired credential the
// way the host transport does: the bare name after env: is stripped.
func resolveConfigReference(ctx context.Context) error {
	w := configFrom(ctx)
	if w.loaded == nil || w.loadErr != nil {
		w.loaded, w.loadErr = config.Load(w.options())
	}
	if w.loadErr != nil {
		return w.loadErr
	}
	credential, err := w.providerCredential()
	if err != nil {
		return err
	}
	w.resolved, w.resolvedOK = w.loaded.Resolve(strings.TrimPrefix(credential, "env:"), w.lookup)
	return nil
}

func stepConfigResolvedSecret(ctx context.Context, secret string) error {
	w := configFrom(ctx)
	if !w.resolvedOK {
		return fmt.Errorf("the reference did not resolve, want %q", secret)
	}
	if w.resolved != secret {
		return fmt.Errorf("resolved %q, want %q", w.resolved, secret)
	}
	return nil
}

func stepConfigEnvDefine(ctx context.Context, name, value string) error {
	configFrom(ctx).env[name] = value
	return nil
}

func stepConfigHandToGuest(ctx context.Context) error {
	w := configFrom(ctx)
	w.loaded, w.loadErr = config.Load(w.options())
	if w.loadErr != nil {
		return w.loadErr
	}
	w.payloads, w.splitErr = w.loaded.Payloads()
	return w.splitErr
}

func stepConfigDocReference(ctx context.Context, reference string) error {
	w := configFrom(ctx)
	payloads, err := w.payloadsOf()
	if err != nil {
		return err
	}
	credential, err := w.providerCredential()
	if err != nil {
		return err
	}
	want := "env:" + strings.Replace(reference, ":", "_", 1)
	if credential != want {
		return fmt.Errorf("the guest holds %q, want the wired reference %q", credential, want)
	}
	for _, secret := range w.auth {
		if secret != "" && strings.Contains(payloads.Providers, secret) {
			return fmt.Errorf("the guest payload carries the secret %q", secret)
		}
	}
	return nil
}
