package providermanager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// TestComponentBindsParsedProviders activates the native component over a
// two-provider payload and proves the key carries the parsed registry.
func TestComponentBindsParsedProviders(t *testing.T) {
	ctx := context.Background()
	var log transcript
	comp := NewComponent(&log)

	sched := runtime.New()
	defer sched.Close()
	id, err := sched.Insert(comp, `{"providers":[{"name":"openai","endpoint":"https://api.openai.com/v1","credential":"env:OPENAI_API_KEY","models":["gpt-4o"]},{"name":"local","endpoint":"http://127.0.0.1:11434/v1","models":["llama"],"mock":true}]}`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	waitActive(t, sched, id)

	raw, err := comp.Handle(ctx, []byte(`{"op":"list"}`))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("list decode: %v", err)
	}
	if len(res.Providers) != 2 || res.Providers[0].Name != "openai" || res.Providers[1].Name != "local" {
		t.Fatalf("registry = %+v, want [openai local] in order", res.Providers)
	}
	if !res.Providers[1].Mock {
		t.Fatalf("local provider lost its mock flag: %+v", res.Providers[1])
	}
	if got := log.String(); !strings.Contains(got, "provider-manager: 2 provider(s) ready") {
		t.Fatalf("log missing the ready line:\n%s", got)
	}

	if err := sched.Remove(id); err != nil {
		t.Fatalf("remove: %v", err)
	}
	waitGone(t, sched, id)
	if got := log.String(); !strings.Contains(got, "provider-manager: providers released") {
		t.Fatalf("log missing the release line:\n%s", got)
	}
}

// TestComponentRejectsBadPayload proves an invalid payload fails the
// activation instead of binding an empty registry.
func TestComponentRejectsBadPayload(t *testing.T) {
	comp := NewComponent(nil)

	sched := runtime.New()
	defer sched.Close()
	id, err := sched.Insert(comp, `{"providers":[{broken`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	waitFailed(t, sched, id)
}

// TestComponentHandlerRoundTrip proves the native handler serves the same
// operation documents as the guest handler: register, provider-for,
// unregister, and unknown operations refused.
func TestComponentHandlerRoundTrip(t *testing.T) {
	ctx := context.Background()
	comp := NewComponent(nil)

	sched := runtime.New()
	defer sched.Close()
	id, err := sched.Insert(comp, `{"providers":[]}`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	waitActive(t, sched, id)
	defer func() {
		_ = sched.Remove(id)
	}()

	handle := func(req string) Result {
		t.Helper()
		raw, err := comp.Handle(ctx, []byte(req))
		if err != nil {
			t.Fatalf("handle %s: %v", req, err)
		}
		var res Result
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatalf("handle %s decode: %v", req, err)
		}
		return res
	}

	if res := handle(`{"op":"register","provider":{"name":"proxy","endpoint":"http://127.0.0.1:11434/v1","models":["llama-3.2"]}}`); !res.Ok {
		t.Fatalf("register = %+v, want ok", res)
	}
	if res := handle(`{"op":"provider-for","model":"llama-3.2"}`); !res.Found || res.Provider.Name != "proxy" {
		t.Fatalf("provider-for = %+v, want the proxy entry", res)
	}
	// A duplicate register is refused the way the guest handler refuses
	// it: the cause is logged and the answer is empty bytes, no error —
	// the host side of memento_handle reports bytes, not errors.
	raw, err := comp.Handle(ctx, []byte(`{"op":"register","provider":{"name":"proxy","endpoint":"http://127.0.0.1:11434/v1","models":["other"]}}`))
	if err != nil {
		t.Fatalf("duplicate register errored: %v, want an empty answer", err)
	}
	if len(raw) != 0 {
		t.Fatalf("duplicate register = %s, want no answer", raw)
	}
	if res := handle(`{"op":"unregister","name":"proxy"}`); !res.Ok {
		t.Fatalf("unregister = %+v, want ok", res)
	}
	if res := handle(`{"op":"list"}`); !res.Ok || len(res.Providers) != 0 {
		t.Fatalf("list after unregister = %+v, want empty", res)
	}
	raw, err = comp.Handle(ctx, []byte(`{"op":"frobnicate"}`))
	if err != nil {
		t.Fatalf("unknown operation errored: %v, want an empty answer", err)
	}
	if len(raw) != 0 {
		t.Fatalf("unknown operation = %s, want no answer", raw)
	}
}

type transcript struct {
	buf strings.Builder
}

func (t *transcript) Write(p []byte) (int, error) { return t.buf.Write(p) }
func (t *transcript) String() string              { return t.buf.String() }

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

func waitFailed(t *testing.T, sched *runtime.Scheduler, ids ...mcontext.FiberID) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		failed := 0
		for _, id := range ids {
			info, ok := sched.Inspect(id)
			if !ok {
				t.Fatalf("fiber %d disappeared during startup", id)
			}
			if info.State == runtime.StateFailed {
				failed++
			}
		}
		if failed == len(ids) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for activation failure")
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
