package contextmanager

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DaviMGDev/core-agent/internal/keys"
	chathistory "github.com/DaviMGDev/core-agent/plugins/chat-history"
	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// TestComponentProjectsAgainstPayloadBudget activates the native component
// with a small budget and proves project keeps the newest fitting turns.
func TestComponentProjectsAgainstPayloadBudget(t *testing.T) {
	ctx := context.Background()
	var log lockedBuffer
	comp := NewComponent(&log)

	sched := runtime.New()
	defer sched.Close()
	hID, err := sched.Insert(chathistory.NewComponent(&log), "test")
	if err != nil {
		t.Fatalf("insert chat-history: %v", err)
	}
	waitActive(t, sched, hID)
	id, err := sched.Insert(comp, `{"budget":10}`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	waitActive(t, sched, id)

	raw, err := comp.Handle(ctx, []byte(`{"op":"project","messages":[{"role":"user","text":"aaaa"},{"role":"assistant","text":"bbbb"},{"role":"user","text":"cc"}]}`))
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	var res Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("project decode: %v", err)
	}
	// Budget 10 runes, newest-first accounting: "cc" (2, total 2), "bbbb"
	// (4, total 6), "aaaa" (4, total 10 — 6+4 is not over 10), so all
	// three fit exactly with nothing dropped.
	if len(res.Messages) != 3 || res.Dropped != 0 {
		t.Fatalf("project = %+v, want all three turns kept", res)
	}
	if res.Messages[0].Text != "aaaa" || res.Messages[2].Text != "cc" {
		t.Fatalf("project = %+v, want append order kept", res)
	}

	// Over budget the oldest drops: a budget-9 window over the same turns
	// keeps "bbbb" and "cc" (2+4=6 fit) and omits "aaaa" (6+4=10 is
	// over), reporting one dropped turn. The kernel allows one provider
	// per key, so the narrower window is asserted on the adapter
	// directly rather than a second fiber.
	kept, dropped := contextAdapter{budget: 9}.Project([]keys.ChatMessage{
		{Role: "user", Text: "aaaa"},
		{Role: "assistant", Text: "bbbb"},
		{Role: "user", Text: "cc"},
	})
	if len(kept) != 2 || kept[0].Text != "bbbb" || kept[1].Text != "cc" || dropped != 1 {
		t.Fatalf("narrow project = %+v (dropped %d), want [bbbb cc] with one dropped", kept, dropped)
	}
	if got := log.String(); !strings.Contains(got, "context-manager: window ready (budget 10)") {
		t.Fatalf("log missing the ready line:\n%s", got)
	}

	if err := sched.Remove(id); err != nil {
		t.Fatalf("remove: %v", err)
	}
	waitGone(t, sched, id)
	if got := log.String(); !strings.Contains(got, "context-manager: window released") {
		t.Fatalf("log missing the release line:\n%s", got)
	}
}

// TestComponentDefaultsBudget proves an empty payload selects DefaultBudget
// and a failed operation answers empty bytes.
func TestComponentDefaultsBudget(t *testing.T) {
	ctx := context.Background()
	var log lockedBuffer
	comp := NewComponent(&log)

	sched := runtime.New()
	defer sched.Close()
	hID, err := sched.Insert(chathistory.NewComponent(&log), "test")
	if err != nil {
		t.Fatalf("insert chat-history: %v", err)
	}
	waitActive(t, sched, hID)
	id, err := sched.Insert(comp, "")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	waitActive(t, sched, id)
	defer func() {
		_ = sched.Remove(id)
	}()

	if got := log.String(); !strings.Contains(got, "context-manager: window ready (budget 4096)") {
		t.Fatalf("log missing the default ready line:\n%s", got)
	}
	raw, err := comp.Handle(ctx, []byte(`{"op":"frobnicate"}`))
	if err != nil {
		t.Fatalf("unknown operation errored: %v, want an empty answer", err)
	}
	if len(raw) != 0 {
		t.Fatalf("unknown operation = %s, want no answer", raw)
	}
}

// TestComponentRejectsBadPayload proves an invalid payload fails the
// activation instead of binding a zero budget.
func TestComponentRejectsBadPayload(t *testing.T) {
	var log lockedBuffer
	comp := NewComponent(nil)

	sched := runtime.New()
	defer sched.Close()
	hID, err := sched.Insert(chathistory.NewComponent(&log), "test")
	if err != nil {
		t.Fatalf("insert chat-history: %v", err)
	}
	waitActive(t, sched, hID)
	id, err := sched.Insert(comp, `{"budget":`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	waitFailed(t, sched, id)
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
