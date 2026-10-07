//go:build wasip1

// Command guest is the repl-chat wasm guest for the memento loader: the
// terminal. It provides the key "repl" (bound to the session nickname),
// injects "agent-loop", and serves the host's wake handler: a user line runs
// one agent turn, and the chat.message events a bus wake carries are rendered
// to the log. The host driver owns stdin, the prompt, and the session loop —
// reading stdin and being woken cannot both live in a serialized guest.
package main

import (
	"encoding/json"
	"fmt"
	"unsafe"

	replchat "github.com/DaviMGDev/core-agent/plugins/repl-chat"
)

const (
	effectID   = 1
	provideKey = "repl"

	agentKey    = "agent-loop"
	responseCap = 8192
)

var injectKeys = []string{"agent-loop"}

//go:wasmimport memento declare_inject
func declareInject(ptr unsafe.Pointer, n uint32) int32

//go:wasmimport memento declare_provide
func declareProvide(ptr unsafe.Pointer, n uint32) int32

//go:wasmimport memento bind
func bindHost(keyPtr unsafe.Pointer, keyLen uint32, valPtr unsafe.Pointer, valLen uint32) int32

//go:wasmimport memento invoke
func invokeHost(keyPtr unsafe.Pointer, keyLen uint32, reqPtr unsafe.Pointer, reqLen uint32, respPtr unsafe.Pointer, respMax uint32) int32

//go:wasmimport memento get_payload_len
func getPayloadLen() int32

//go:wasmimport memento get_payload
func getPayload(ptr unsafe.Pointer, max uint32) int32

//go:wasmimport memento register_effect
func registerEffect(id uint32) int32

//go:wasmimport memento log
func logMsg(ptr unsafe.Pointer, n uint32) int32

// byteSlice views n bytes of guest memory at ptr.
func byteSlice(ptr, n uint32) []byte {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), n)
}

// emit writes s to the host log through the memento ABI.
func emit(s string) {
	b := []byte(s)
	if len(b) == 0 {
		return
	}
	logMsg(unsafe.Pointer(&b[0]), uint32(len(b)))
}

// payload returns the activation payload handed to the guest.
func payload() string {
	n := getPayloadLen()
	if n <= 0 {
		return ""
	}
	buf := make([]byte, n)
	got := getPayload(unsafe.Pointer(&buf[0]), uint32(len(buf)))
	if got <= 0 {
		return ""
	}
	return string(buf[:got])
}

// bindProvided registers the provided key's value through the host ABI; a
// declared provide must be bound during activation.
func bindProvided(value []byte) bool {
	kb := []byte(provideKey)
	if len(value) == 0 {
		return bindHost(unsafe.Pointer(&kb[0]), uint32(len(kb)), nil, 0) == 0
	}
	return bindHost(unsafe.Pointer(&kb[0]), uint32(len(kb)), unsafe.Pointer(&value[0]), uint32(len(value))) == 0
}

// agentWake is the agent's request document for one terminal line.
type agentWake struct {
	Line string `json:"line"`
}

// runLine hands one user line to the agent and waits for the turn to finish.
// The message it speaks is published as chat.message, so the terminal renders
// it from the wake — this answer carries no text.
func runLine(line string) error {
	req, err := json.Marshal(agentWake{Line: line})
	if err != nil {
		return err
	}
	kb := []byte(agentKey)
	resp := make([]byte, responseCap)
	n := invokeHost(
		unsafe.Pointer(&kb[0]), uint32(len(kb)),
		unsafe.Pointer(&req[0]), uint32(len(req)),
		unsafe.Pointer(&resp[0]), uint32(len(resp)),
	)
	if n <= 0 {
		return fmt.Errorf("invoke %s: no response", agentKey)
	}
	return nil
}

//go:wasmexport memento_declare
func mementoDeclare() uint32 {
	for _, k := range injectKeys {
		b := []byte(k)
		if declareInject(unsafe.Pointer(&b[0]), uint32(len(b))) != 0 {
			return 1
		}
	}
	b := []byte(provideKey)
	if declareProvide(unsafe.Pointer(&b[0]), uint32(len(b))) != 0 {
		return 1
	}
	return 0
}

// memento_activate registers the terminal and binds its key. The host drives
// the session: the guest returns, freeing the module lock for wakes.
//
//go:wasmexport memento_activate
func mementoActivate() uint32 {
	if registerEffect(effectID) != 0 {
		return 1
	}
	if !bindProvided([]byte(replchat.New(payload()).Nick())) {
		return 2
	}
	return 0
}

// arena keeps handler buffers alive for the duration of one exchange.
var arena [][]byte

//go:wasmexport memento_alloc
func mementoAlloc(size uint32) uint32 {
	if size == 0 {
		return 0
	}
	b := make([]byte, size)
	arena = append(arena, b)
	return uint32(uintptr(unsafe.Pointer(&b[0])))
}

// memento_handle serves one wake: a user line runs one agent turn; the events
// a bus wake carries are rendered to the log. Failures ride the answer, so
// the session reports them and continues.
//
//go:wasmexport memento_handle
func mementoHandle(reqPtr, reqLen, respPtr, respMax uint32) uint32 {
	defer func() { arena = arena[:0] }()
	var wake replchat.Wake
	if err := json.Unmarshal(byteSlice(reqPtr, reqLen), &wake); err != nil {
		return 0
	}
	answer := replchat.Answer{}
	switch {
	case wake.Line != "":
		if err := runLine(wake.Line); err != nil {
			answer.Error = err.Error()
		}
	case len(wake.Events) > 0:
		for _, line := range replchat.Render(wake.Events) {
			emit(line + "\n")
		}
	default:
		return 0
	}
	out, err := json.Marshal(answer)
	if err != nil || uint32(len(out)) > respMax {
		return 0
	}
	copy(byteSlice(respPtr, respMax), out)
	return uint32(len(out))
}

//go:wasmexport memento_revert_effect
func mementoRevertEffect(effectID uint32) uint32 {
	emit(replchat.LeaveMessage + "\n")
	return 0
}

func main() {}
