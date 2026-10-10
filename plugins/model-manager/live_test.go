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
	"time"

	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// TestLiveResolutionServesLateRegisteredProvider composes the real guests
// the way cmd/core-agent does and proves issue #12's core criterion: a
// provider registered after model-manager activation serves the next
// response — no snapshot, one provider-for invoke per response. The late
// provider points at a scripted HTTP endpoint, so the test also proves the
// exchange uses that endpoint.
func TestLiveResolutionServesLateRegisteredProvider(t *testing.T) {
	var (
		mu   sync.Mutex
		hits int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"late answer"}}]}`)
	}))
	defer srv.Close()

	ctx := context.Background()
	var log lockedBuffer
	pmComp := providermanager.NewComponent(&log)
	mmComp := NewComponent(&log)

	sched := runtime.New()
	defer sched.Close()

	pmID, err := sched.Insert(pmComp, `{"providers":[]}`)
	if err != nil {
		t.Fatalf("insert provider-manager: %v", err)
	}
	waitActive(t, sched, pmID)
	mmID, err := sched.Insert(mmComp, `{"models":[]}`)
	if err != nil {
		t.Fatalf("insert model-manager: %v", err)
	}
	waitActive(t, sched, mmID)

	// A model no provider serves fails loudly naming the model.
	raw, err := mmComp.Handle(ctx, []byte(`{"op":"respond","model":"model-x","context":[{"role":"user","text":"hi"}]}`))
	if err == nil {
		var res Result
		if jerr := json.Unmarshal(raw, &res); jerr == nil && res.Text != "" {
			t.Fatalf("respond for unserved model = %+v, want failure", res)
		}
	}
	if got := log.String(); !strings.Contains(got, `no provider serves model "model-x"`) {
		t.Fatalf("log missing the loud failure:\n%s", got)
	}

	// Register the provider after activation, through the live operation —
	// the path a provider plugin's guest takes.
	regReq, _ := json.Marshal(map[string]any{
		"op": "register",
		"provider": map[string]any{
			"name":     "late",
			"endpoint": srv.URL,
			"models":   []string{"model-x"},
		},
	})
	raw, err = pmComp.Handle(ctx, regReq)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	var regRes providermanager.Result
	if err := json.Unmarshal(raw, &regRes); err != nil || !regRes.Ok {
		t.Fatalf("register = %s, %v; want ok", raw, err)
	}

	// The next response reaches the late provider's endpoint.
	raw, err = mmComp.Handle(ctx, []byte(`{"op":"respond","model":"model-x","context":[{"role":"user","text":"hi"}]}`))
	if err != nil {
		t.Fatalf("respond: %v\nlog:\n%s", err, log.String())
	}
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("respond decode: %v", err)
	}
	if res.Model != "model-x" || res.Text != "late answer" {
		t.Fatalf("respond = %+v, want model-x with the late answer", res)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Fatalf("provider hits = %d, want 1", hits)
	}

	// Unregistering withdraws it: the next response fails naming the model.
	// A handler failure surfaces as an empty answer with the cause logged
	// (the host Handle reports bytes, not errors).
	unReq, _ := json.Marshal(map[string]any{"op": "unregister", "name": "late"})
	if _, err := pmComp.Handle(ctx, unReq); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	raw, err = mmComp.Handle(ctx, []byte(`{"op":"respond","model":"model-x"}`))
	if err != nil {
		t.Fatalf("respond handle: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("respond after unregister = %s, want no answer", raw)
	}
	if got := log.String(); !strings.Contains(got, `no provider serves model "model-x"`) {
		t.Fatalf("log missing the post-unregister failure:\n%s", got)
	}

	for _, id := range []mcontext.FiberID{mmID, pmID} {
		if err := sched.Remove(id); err != nil {
			t.Fatalf("remove %d: %v", id, err)
		}
	}
	waitGone(t, sched, mmID, pmID)
}

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
