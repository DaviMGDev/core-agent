package notifications

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	mcontext "github.com/DaviMGDev/memento/context"
	"github.com/DaviMGDev/memento/plugins/wasm"
	"github.com/DaviMGDev/memento/runtime"
)

// hostServices adapts the bus to the loader's host-services contract for the
// guest path test. Jobs are not this package's concern.
type hostServices struct {
	bus *Bus
}

func (h *hostServices) StartJob(*runtime.Instance, []byte) ([]byte, error) {
	return nil, errors.New("no jobs in this test")
}

func (h *hostServices) PeepJob(*runtime.Instance, []byte) ([]byte, error) {
	return nil, errors.New("no jobs in this test")
}

func (h *hostServices) KillJob(*runtime.Instance, []byte) ([]byte, error) {
	return nil, errors.New("no jobs in this test")
}

func (h *hostServices) Publish(topic string, payload []byte) error {
	h.bus.Publish(topic, payload)
	return nil
}

func (h *hostServices) Cancelled(*runtime.Instance) bool { return false }

// collectWaker records every event it is woken with.
type collectWaker struct {
	mu     sync.Mutex
	events []Event
}

func (w *collectWaker) Wake(sub *Subscription) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, sub.Take()...)
}

func (w *collectWaker) got() []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Event(nil), w.events...)
}

func TestGuestPublishReachesSubscribers(t *testing.T) {
	ctx := context.Background()
	bus := New()
	sub := &collectWaker{}
	bus.Subscribe(TopicChatMessage, sub)

	engine, err := wasm.NewEngine(ctx, wasm.WithHostServices(&hostServices{bus: bus}))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer engine.Close(ctx)

	comp, err := wasm.NewComponent(ctx, engine, buildPublishGuest(TopicChatMessage, `{"text":"hi"}`))
	if err != nil {
		t.Fatalf("NewComponent: %v", err)
	}

	s := runtime.New()
	defer s.Close()
	id, err := s.Insert(comp, nil)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	waitActive(t, s, id)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if events := sub.got(); len(events) == 1 {
			if events[0].Topic != TopicChatMessage || string(events[0].Payload) != `{"text":"hi"}` {
				t.Fatalf("event = {%s %q}, want {chat.message %q}", events[0].Topic, events[0].Payload, `{"text":"hi"}`)
			}
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("subscriber received %v, want one chat.message event", sub.got())
}

func waitActive(t *testing.T, s *runtime.Scheduler, id mcontext.FiberID) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, ok := s.Inspect(id)
		if ok && info.State == runtime.StateActive {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("fiber %v did not become active", id)
}

// --- minimal wasm guest: publish one event during activation ---

func encodeLEB128U(val uint32) []byte {
	var res []byte
	for {
		b := byte(val & 0x7f)
		val >>= 7
		if val != 0 {
			res = append(res, b|0x80)
		} else {
			res = append(res, b)
			break
		}
	}
	return res
}

func encodeLEB128S(val int32) []byte {
	var res []byte
	for more := true; more; {
		b := byte(val & 0x7f)
		val >>= 7
		if (val == 0 && (b&0x40) == 0) || (val == -1 && (b&0x40) != 0) {
			more = false
		} else {
			b |= 0x80
		}
		res = append(res, b)
	}
	return res
}

func encodeVec(items [][]byte) []byte {
	var buf bytes.Buffer
	buf.Write(encodeLEB128U(uint32(len(items))))
	for _, it := range items {
		buf.Write(it)
	}
	return buf.Bytes()
}

func encodeSection(secID byte, content []byte) []byte {
	var buf bytes.Buffer
	buf.WriteByte(secID)
	buf.Write(encodeLEB128U(uint32(len(content))))
	buf.Write(content)
	return buf.Bytes()
}

func encodeString(s string) []byte {
	b := []byte(s)
	return append(encodeLEB128U(uint32(len(b))), b...)
}

func i32Const(v int32) []byte {
	return append([]byte{0x41}, encodeLEB128S(v)...)
}

func activeData(offset int32, s string) []byte {
	b := append([]byte{0x00}, i32Const(offset)...)
	b = append(b, 0x0b)
	b = append(b, encodeLEB128U(uint32(len(s)))...)
	return append(b, []byte(s)...)
}

func memImport(name string, typeIndex byte) []byte {
	return append(encodeString("memento"), append(encodeString(name), 0x00, typeIndex)...)
}

// buildPublishGuest builds a guest whose activation publishes one event:
// topic at offset 32, payload at 64.
func buildPublishGuest(topic, payload string) []byte {
	type0 := []byte{0x60, 0x02, 0x7f, 0x7f, 0x01, 0x7f}             // (i32,i32)->i32
	type3 := []byte{0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f} // (i32,i32,i32,i32)->i32
	type2 := []byte{0x60, 0x01, 0x7f, 0x01, 0x7f}                   // (i32)->i32
	type1 := []byte{0x60, 0x00, 0x01, 0x7f}                         // ()->i32
	typeSec := encodeSection(1, encodeVec([][]byte{type0, type3, type2, type1}))

	importSec := encodeSection(2, encodeVec([][]byte{
		memImport("publish", 0x01),
	}))

	// Defined funcs 1..2: activate (type1), revert (type2).
	funcSec := encodeSection(3, encodeVec([][]byte{{0x03}, {0x02}}))
	memSec := encodeSection(5, encodeVec([][]byte{{0x00, 0x01}}))
	exportSec := encodeSection(7, encodeVec([][]byte{
		append(encodeString("memory"), 0x02, 0x00),
		append(encodeString("memento_activate"), 0x00, 0x01),
		append(encodeString("memento_revert_effect"), 0x00, 0x02),
	}))

	body := []byte{0x00}
	body = append(body, i32Const(32)...)
	body = append(body, i32Const(int32(len(topic)))...)
	body = append(body, i32Const(64)...)
	body = append(body, i32Const(int32(len(payload)))...)
	body = append(body, 0x10, 0x00) // publish(32, topicLen, 64, payloadLen)
	body = append(body, 0x1a)       // drop
	body = append(body, i32Const(0)...)
	body = append(body, 0x0f, 0x0b) // return

	revert := []byte{0x00}
	revert = append(revert, i32Const(0)...)
	revert = append(revert, 0x0f, 0x0b)

	codeSec := encodeSection(10, encodeVec([][]byte{
		append(encodeLEB128U(uint32(len(body))), body...),
		append(encodeLEB128U(uint32(len(revert))), revert...),
	}))

	dataSec := encodeSection(11, encodeVec([][]byte{
		activeData(32, topic),
		activeData(64, payload),
	}))

	var wasmBuf bytes.Buffer
	wasmBuf.Write([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	wasmBuf.Write(typeSec)
	wasmBuf.Write(importSec)
	wasmBuf.Write(funcSec)
	wasmBuf.Write(memSec)
	wasmBuf.Write(exportSec)
	wasmBuf.Write(codeSec)
	wasmBuf.Write(dataSec)
	return wasmBuf.Bytes()
}
