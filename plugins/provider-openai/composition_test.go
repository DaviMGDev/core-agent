package provideropenai

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// TestActivationRegistersAndUnloadUnregisters composes the native components
// the way cmd/core-agent does — provider-manager first, provider-openai
// after — and proves the lifecycle: activation registers openai into the
// live registry, and unloading the plugin removes it again.
func TestActivationRegistersAndUnloadUnregisters(t *testing.T) {
	ctx := context.Background()
	var log lockedBuffer
	pmComp := providermanager.NewComponent(&log)
	poComp := NewComponent(&log)

	sched := runtime.New()
	defer sched.Close()

	// Inserts are strictly sequenced with activation between them: an
	// activating worker reads the scheduler's fiber table through
	// runtime.Get, which races with the loop's own table writes while it
	// still has inserts to process.
	pmID, err := sched.Insert(pmComp, `{"providers":[]}`)
	if err != nil {
		t.Fatalf("insert provider-manager: %v", err)
	}
	waitActive(t, sched, pmID)
	poID, err := sched.Insert(poComp, "")
	if err != nil {
		t.Fatalf("insert provider-openai: %v", err)
	}
	waitActive(t, sched, poID)

	listed := func() []string {
		raw, err := pmComp.Handle(ctx, []byte(`{"op":"list"}`))
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var res providermanager.Result
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatalf("list decode: %v", err)
		}
		var names []string
		for _, p := range res.Providers {
			names = append(names, p.Name)
		}
		return names
	}

	if names := listed(); len(names) != 1 || names[0] != "openai" {
		t.Fatalf("registry after activation = %v, want [openai]; log:\n%s", names, log.String())
	}
	if got := log.String(); !strings.Contains(got, `provider-openai: provider "openai" registered`) {
		t.Fatalf("log missing the registration line:\n%s", got)
	}

	// Unload the plugin: its effect inverse unregisters the provider while
	// the registry is still alive (dependents deactivate first).
	if err := sched.Remove(poID); err != nil {
		t.Fatalf("remove provider-openai: %v", err)
	}
	waitGone(t, sched, poID)

	if names := listed(); len(names) != 0 {
		t.Fatalf("registry after unload = %v, want empty; log:\n%s", names, log.String())
	}
	if got := log.String(); !strings.Contains(got, `provider-openai: provider "openai" released`) {
		t.Fatalf("log missing the release line:\n%s", got)
	}

	// Unload the registry last, like the entry does.
	if err := sched.Remove(pmID); err != nil {
		t.Fatalf("remove provider-manager: %v", err)
	}
	waitGone(t, sched, pmID)
}

// TestActivationLeavesExistingEntry composes provider-openai over a registry
// that already carries openai (a config-file entry): the plugin leaves it
// untouched, and unloading the plugin keeps the entry.
func TestActivationLeavesExistingEntry(t *testing.T) {
	ctx := context.Background()
	var log lockedBuffer
	pmComp := providermanager.NewComponent(&log)
	poComp := NewComponent(&log)

	sched := runtime.New()
	defer sched.Close()

	pmID, err := sched.Insert(pmComp, `{"providers":[{"name":"openai","endpoint":"https://api.openai.com/v1","credential":"env:OPENAI_API_KEY","models":["gpt-4o"]}]}`)
	if err != nil {
		t.Fatalf("insert provider-manager: %v", err)
	}
	waitActive(t, sched, pmID)
	poID, err := sched.Insert(poComp, "")
	if err != nil {
		t.Fatalf("insert provider-openai: %v", err)
	}
	waitActive(t, sched, poID)

	if got := log.String(); !strings.Contains(got, "already registered, leaving it") {
		t.Fatalf("log missing the leave-untouched line:\n%s", got)
	}

	if err := sched.Remove(poID); err != nil {
		t.Fatalf("remove provider-openai: %v", err)
	}
	waitGone(t, sched, poID)

	raw, err := pmComp.Handle(ctx, []byte(`{"op":"list"}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var res providermanager.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("list decode: %v", err)
	}
	if len(res.Providers) != 1 || res.Providers[0].Name != "openai" {
		t.Fatalf("registry after plugin unload = %+v, want the config entry kept", res.Providers)
	}

	if err := sched.Remove(pmID); err != nil {
		t.Fatalf("remove provider-manager: %v", err)
	}
	waitGone(t, sched, pmID)
}

// TestActivationRegistersCustomPayload proves the override half: a custom
// JSON payload registers exactly that provider instead of the defaults.
func TestActivationRegistersCustomPayload(t *testing.T) {
	ctx := context.Background()
	var log lockedBuffer
	pmComp := providermanager.NewComponent(&log)
	poComp := NewComponent(&log)

	sched := runtime.New()
	defer sched.Close()

	pmID, err := sched.Insert(pmComp, `{"providers":[]}`)
	if err != nil {
		t.Fatalf("insert provider-manager: %v", err)
	}
	waitActive(t, sched, pmID)
	poID, err := sched.Insert(poComp, `{"name":"proxy","endpoint":"http://127.0.0.1:11434/v1","models":["llama-3.2"]}`)
	if err != nil {
		t.Fatalf("insert provider-openai: %v", err)
	}
	waitActive(t, sched, poID)

	raw, err := pmComp.Handle(ctx, []byte(`{"op":"list"}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var res providermanager.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("list decode: %v", err)
	}
	if len(res.Providers) != 1 || res.Providers[0].Name != "proxy" {
		t.Fatalf("registry = %+v, want exactly the custom proxy entry", res.Providers)
	}
	if res.Providers[0].Endpoint != "http://127.0.0.1:11434/v1" {
		t.Fatalf("endpoint = %q, want the override", res.Providers[0].Endpoint)
	}

	if err := sched.Remove(poID); err != nil {
		t.Fatalf("remove provider-openai: %v", err)
	}
	waitGone(t, sched, poID)
	if err := sched.Remove(pmID); err != nil {
		t.Fatalf("remove provider-manager: %v", err)
	}
	waitGone(t, sched, pmID)
}

// lockedBuffer is a concurrency-safe transcript sink: activating workers
// log concurrently, and the shared log must not race.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.WriteString(string(p))
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitActive(t *testing.T, sched *runtime.Scheduler, ids ...mcontext.FiberID) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		active := 0
		for _, id := range ids {
			info, ok := sched.Inspect(id)
			if !ok {
				t.Fatalf("fiber %d disappeared during startup", id)
			}
			if info.State == runtime.StateFailed {
				t.Fatalf("fiber %d failed: %v", id, info.Err)
			}
			if info.State == runtime.StateActive {
				active++
			}
		}
		if active == len(ids) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for activation")
}

func waitGone(t *testing.T, sched *runtime.Scheduler, ids ...mcontext.FiberID) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		gone := true
		for _, id := range ids {
			if _, ok := sched.Inspect(id); ok {
				gone = false
				break
			}
		}
		if gone {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for unload")
}
