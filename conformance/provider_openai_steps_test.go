package conformance

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cucumber/godog"

	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	provideropenai "github.com/DaviMGDev/core-agent/plugins/provider-openai"
)

// provider-openai lifecycle steps. Plugin conformance exercises the
// host-testable libraries: these steps drive the same lifecycle the guest
// performs — build the register document at activation, apply it to the
// registry, and apply the unregister document on unload — so the scenarios
// prove the documents and the registry operations compose. The real-guest
// path (invoke at activation, effect-guarded unregister) is proven by the
// plugin's composition tests beside its code.
func registerProviderOpenAISteps(sc *godog.ScenarioContext) {
	sc.Step(`^a composition with provider-manager$`, stepCompositionWithProviderManager)
	sc.Step(`^provider-openai activates$`, stepProviderOpenAIActivates)
	sc.Step(`^the provider registry contains "([^"]*)"$`, stepProviderRegistryContains)
	sc.Step(`^an active composition with provider-openai$`, stepActiveCompositionWithProviderOpenAI)
	sc.Step(`^provider-openai is unloaded$`, stepProviderOpenAIUnloaded)
	sc.Step(`^the provider registry does not contain "([^"]*)"$`, stepProviderRegistryNotContains)
}

func stepCompositionWithProviderManager(ctx context.Context) error {
	w := worldFrom(ctx)
	w.providers = providermanager.NewRegistry()
	w.openai = provideropenai.Defaults()
	return nil
}

func stepProviderOpenAIActivates(ctx context.Context) error {
	w := worldFrom(ctx)
	raw := w.openai.RegisterRequest()
	var op providermanager.Op
	if err := json.Unmarshal(raw, &op); err != nil {
		return fmt.Errorf("register document is not a registry op: %w", err)
	}
	res, err := providermanager.Apply(w.providers, op)
	if err != nil || !res.Ok {
		return fmt.Errorf("activation register failed: %v (%+v)", err, res)
	}
	return nil
}

func stepProviderRegistryContains(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	if _, ok := w.providers.Get(name); !ok {
		return fmt.Errorf("registry does not contain %q", name)
	}
	return nil
}

func stepActiveCompositionWithProviderOpenAI(ctx context.Context) error {
	if err := stepCompositionWithProviderManager(ctx); err != nil {
		return err
	}
	return stepProviderOpenAIActivates(ctx)
}

func stepProviderOpenAIUnloaded(ctx context.Context) error {
	w := worldFrom(ctx)
	raw := w.openai.UnregisterRequest()
	var op providermanager.Op
	if err := json.Unmarshal(raw, &op); err != nil {
		return fmt.Errorf("unregister document is not a registry op: %w", err)
	}
	res, err := providermanager.Apply(w.providers, op)
	if err != nil || !res.Ok {
		return fmt.Errorf("unload unregister failed: %v (%+v)", err, res)
	}
	return nil
}

func stepProviderRegistryNotContains(ctx context.Context, name string) error {
	w := worldFrom(ctx)
	if _, ok := w.providers.Get(name); ok {
		return fmt.Errorf("registry still contains %q", name)
	}
	return nil
}
