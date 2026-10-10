package chathistory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/runtime"
)

// TestComponentOpensPayloadConversation activates the native component over
// a named conversation and proves append/recent round-trip through it.
func TestComponentOpensPayloadConversation(t *testing.T) {
	ctx := context.Background()
	var log transcript
	comp := NewComponent(&log)

	sched := runtime.New()
	defer sched.Close()
	id, err := sched.Insert(comp, "side")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	waitActive(t, sched, id)

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

	if res := handle(`{"op":"append","conversation":"side","role":"user","text":"hello"}`); res.Turn != 1 {
		t.Fatalf("append = %+v, want turn 1", res)
	}
	if res := handle(`{"op":"append","conversation":"side","role":"assistant","text":"hi"}`); res.Turn != 2 {
		t.Fatalf("append = %+v, want turn 2", res)
	}
	if res := handle(`{"op":"recent","conversation":"side","n":1}`); len(res.Messages) != 1 || res.Messages[0].Text != "hi" {
		t.Fatalf("recent = %+v, want the last turn", res)
	}
	if got := log.String(); !strings.Contains(got, `chat-history: conversation "side" open`) {
		t.Fatalf("log missing the open line:\n%s", got)
	}

	if err := sched.Remove(id); err != nil {
		t.Fatalf("remove: %v", err)
	}
	waitGone(t, sched, id)
	if got := log.String(); !strings.Contains(got, "chat-history: conversation closed") {
		t.Fatalf("log missing the release line:\n%s", got)
	}
}

// TestComponentDefaultsConversation proves an empty payload opens the
// default conversation, and a failed operation answers empty bytes.
func TestComponentDefaultsConversation(t *testing.T) {
	ctx := context.Background()
	var log transcript
	comp := NewComponent(&log)

	sched := runtime.New()
	defer sched.Close()
	id, err := sched.Insert(comp, "")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	waitActive(t, sched, id)
	defer func() {
		_ = sched.Remove(id)
	}()

	if got := log.String(); !strings.Contains(got, `chat-history: conversation "default" open`) {
		t.Fatalf("log missing the default open line:\n%s", got)
	}
	raw, err := comp.Handle(ctx, []byte(`{"op":"append","conversation":"default","role":"robot","text":"beep"}`))
	if err != nil {
		t.Fatalf("bad role errored: %v, want an empty answer", err)
	}
	if len(raw) != 0 {
		t.Fatalf("bad role = %s, want no answer", raw)
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
