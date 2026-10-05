//go:build wasip1

// Command guest is the context-manager wasm guest for the memento loader.
//
// It provides the key "llm-context", injects "chat-history", registers one
// effect, and opens a window with the budget from the activation payload
// ({"budget":<runes>}; default 4096).
package main

import (
	"strconv"
	"unsafe"

	contextmanager "github.com/DaviMGDev/core-agent/plugins/context-manager"
)

const (
	effectID   = 1
	provideKey = "llm-context"
)

var injectKeys = []string{"chat-history"}

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
func payload() []byte {
	n := getPayloadLen()
	if n <= 0 {
		return nil
	}
	buf := make([]byte, n)
	got := getPayload(unsafe.Pointer(&buf[0]), uint32(len(buf)))
	if got <= 0 {
		return nil
	}
	return buf[:got]
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

	cfg, err := contextmanager.ParseConfig(payload())
	if err != nil {
		emit("context-manager: " + err.Error() + "\n")
		return 1
	}
	budget := cfg.Budget
	if budget == 0 {
		budget = contextmanager.DefaultBudget
	}
	emit("context-manager: window ready (budget " + strconv.Itoa(budget) + ")\n")
	return 0
}

//go:wasmexport memento_revert_effect
func mementoRevertEffect(effectID uint32) uint32 {
	emit("context-manager: window released\n")
	return 0
}

func main() {}
