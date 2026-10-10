package modelmanager

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// TestRespondResolvesCredentialReference proves the reference discipline
// end to end: the provider document carries env:TEST_SECRET, the exchange
// sends the resolved secret, and the reference itself never crosses.
func TestRespondResolvesCredentialReference(t *testing.T) {
	var (
		mu   sync.Mutex
		auth string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"secret ok"}}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	var log lockedBuffer
	pmComp := providermanager.NewComponent(&log)
	mmComp := NewComponent(&log)
	mmComp.ResolveCredential = func(name string) (string, bool) {
		if name == "TEST_SECRET" {
			return "sk-test", true
		}
		return "", false
	}

	sched := runtime.New()
	defer sched.Close()

	pmPayload, _ := json.Marshal(map[string]any{"providers": []any{map[string]any{
		"name": "cred", "endpoint": srv.URL, "credential": "env:TEST_SECRET", "models": []string{"model-c"},
	}}})
	pmID, err := sched.Insert(pmComp, string(pmPayload))
	if err != nil {
		t.Fatalf("insert provider-manager: %v", err)
	}
	waitActive(t, sched, pmID)
	mmID, err := sched.Insert(mmComp, `{"models":[]}`)
	if err != nil {
		t.Fatalf("insert model-manager: %v", err)
	}
	waitActive(t, sched, mmID)

	raw, err := mmComp.Handle(ctx, []byte(`{"op":"respond","model":"model-c","context":[{"role":"user","text":"hi"}]}`))
	if err != nil {
		t.Fatalf("respond: %v\nlog:\n%s", err, log.String())
	}
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("respond decode: %v", err)
	}
	if res.Text != "secret ok" {
		t.Fatalf("respond = %+v, want the provider answer", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if auth != "Bearer sk-test" {
		t.Fatalf("authorization = %q, want the resolved secret (the reference must never cross)", auth)
	}

	for _, id := range []mcontext.FiberID{mmID, pmID} {
		if err := sched.Remove(id); err != nil {
			t.Fatalf("remove %d: %v", id, err)
		}
	}
	waitGone(t, sched, mmID, pmID)
}

// TestRespondFailsOnMissingCredential proves a missing secret fails the
// exchange loudly instead of crossing the reference literally.
func TestRespondFailsOnMissingCredential(t *testing.T) {
	ctx := context.Background()
	var log lockedBuffer
	pmComp := providermanager.NewComponent(&log)
	mmComp := NewComponent(&log)
	mmComp.ResolveCredential = func(name string) (string, bool) { return "", false }

	sched := runtime.New()
	defer sched.Close()

	pmPayload, _ := json.Marshal(map[string]any{"providers": []any{map[string]any{
		"name": "cred", "endpoint": "http://127.0.0.1:9", "credential": "env:MISSING", "models": []string{"model-c"},
	}}})
	pmID, err := sched.Insert(pmComp, string(pmPayload))
	if err != nil {
		t.Fatalf("insert provider-manager: %v", err)
	}
	waitActive(t, sched, pmID)
	mmID, err := sched.Insert(mmComp, `{"models":[]}`)
	if err != nil {
		t.Fatalf("insert model-manager: %v", err)
	}
	waitActive(t, sched, mmID)

	raw, err := mmComp.Handle(ctx, []byte(`{"op":"respond","model":"model-c","context":[{"role":"user","text":"hi"}]}`))
	if err != nil {
		t.Fatalf("respond errored: %v, want an empty answer", err)
	}
	if len(raw) != 0 {
		t.Fatalf("respond = %s, want no answer", raw)
	}
	if got := log.String(); !strings.Contains(got, `request to provider "cred" failed`) {
		t.Fatalf("log missing the loud failure:\n%s", got)
	}

	// The suite must not dial out: the endpoint is unroutable, so any
	// answer at all would prove the reference crossed literally.
	for _, id := range []mcontext.FiberID{mmID, pmID} {
		if err := sched.Remove(id); err != nil {
			t.Fatalf("remove %d: %v", id, err)
		}
	}
	waitGone(t, sched, mmID, pmID)
}
