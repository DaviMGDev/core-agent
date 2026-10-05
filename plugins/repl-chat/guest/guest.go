//go:build wasip1

// Command guest is the repl-chat wasm guest for the memento loader.
//
// It provides the key "repl", injects "chat-history", "model-registry", and
// "llm-context", registers one effect, and hosts the REPL session on WASI
// stdin. Output goes through the memento log import; the effect inverse
// announces the session end on unload.
package main

import (
	"os"
	"unsafe"

	replchat "github.com/DaviMGDev/core-agent/plugins/repl-chat"
)

const (
	effectID   = 1
	provideKey = "repl"
)

var injectKeys = []string{"chat-history", "model-registry", "llm-context"}

//go:wasmimport memento declare_inject
func declareInject(ptr unsafe.Pointer, n uint32) int32

//go:wasmimport memento declare_provide
func declareProvide(ptr unsafe.Pointer, n uint32) int32

//go:wasmimport memento get_payload_len
func getPayloadLen() int32

//go:wasmimport memento get_payload
func getPayload(ptr unsafe.Pointer, max uint32) int32

//go:wasmimport memento register_effect
func registerEffect(id uint32) int32

//go:wasmimport memento log
func logMsg(ptr unsafe.Pointer, n uint32) int32

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

//go:wasmexport memento_activate
func mementoActivate() uint32 {
	if registerEffect(effectID) != 0 {
		return 1
	}

	session := replchat.New(payload())
	if err := session.Run(os.Stdin, emit); err != nil {
		emit("repl: input error: " + err.Error() + "\n")
		return 2
	}
	return 0
}

//go:wasmexport memento_revert_effect
func mementoRevertEffect(effectID uint32) uint32 {
	emit(replchat.LeaveMessage + "\n")
	return 0
}

func main() {}
