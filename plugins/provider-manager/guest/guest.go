//go:build wasip1

// Command guest is the provider-manager wasm guest for the memento loader.
//
// It provides the key "provider-registry", registers one effect, and turns
// the activation payload ({"providers":[...]}) into a validated registry.
// Its handler serves the live registry operations — register, unregister,
// list, provider-for — so provider plugins can register at runtime; every
// mutation re-binds the provided value, so readers of the key see the
// current registry (the owner never changes, so no dependent reactivates).
package main

import (
	"encoding/json"
	"strconv"
	"unsafe"

	providermanager "github.com/DaviMGDev/core-agent/plugins/provider-manager"
)

const (
	effectID   = 1
	provideKey = "provider-registry"
)

//go:wasmimport memento declare_inject
func declareInject(ptr unsafe.Pointer, n uint32) int32

//go:wasmimport memento declare_provide
func declareProvide(ptr unsafe.Pointer, n uint32) int32

//go:wasmimport memento bind
func bindHost(keyPtr unsafe.Pointer, keyLen uint32, valPtr unsafe.Pointer, valLen uint32) int32

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

// bindProvided registers the provided key's value through the host ABI; a
// declared provide must be bound during activation.
func bindProvided(value []byte) bool {
	kb := []byte(provideKey)
	if len(value) == 0 {
		return bindHost(unsafe.Pointer(&kb[0]), uint32(len(kb)), nil, 0) == 0
	}
	return bindHost(unsafe.Pointer(&kb[0]), uint32(len(kb)), unsafe.Pointer(&value[0]), uint32(len(value))) == 0
}

//go:wasmexport memento_declare
func mementoDeclare() uint32 {
	b := []byte(provideKey)
	if declareProvide(unsafe.Pointer(&b[0]), uint32(len(b))) != 0 {
		return 1
	}
	return 0
}

// registry is the module instance's provider registry, seeded at
// activation and mutated by the live operations the handler serves.
var registry *providermanager.Registry

//go:wasmexport memento_activate
func mementoActivate() uint32 {
	if registerEffect(effectID) != 0 {
		return 1
	}

	p := payload()
	if !bindProvided(p) {
		return 2
	}
	providers, err := providermanager.ParseConfig(p)
	if err != nil {
		emit("provider-manager: " + err.Error() + "\n")
		return 1
	}
	registry = providermanager.NewRegistry()
	for _, p := range providers {
		if err := registry.Register(p); err != nil {
			emit("provider-manager: " + err.Error() + "\n")
			return 1
		}
	}
	emit("provider-manager: " + strconv.Itoa(len(providers)) + " provider(s) ready\n")
	return 0
}

// arena keeps handler buffers alive for the duration of one exchange.
var arena [][]byte

func byteSlice(ptr, n uint32) []byte {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), n)
}

// providersDoc marshals the registry in the activation payload's shape:
// {"providers":[...]}, so readers of the key always see a parseable list.
func providersDoc() []byte {
	out, err := json.Marshal(struct {
		Providers []providermanager.Provider `json:"providers"`
	}{Providers: registry.List()})
	if err != nil {
		return nil
	}
	return out
}

//go:wasmexport memento_alloc
func mementoAlloc(size uint32) uint32 {
	if size == 0 {
		return 0
	}
	b := make([]byte, size)
	arena = append(arena, b)
	return uint32(uintptr(unsafe.Pointer(&b[0])))
}

//go:wasmexport memento_handle
func mementoHandle(reqPtr, reqLen, respPtr, respMax uint32) uint32 {
	defer func() { arena = arena[:0] }()
	var op providermanager.Op
	if err := json.Unmarshal(byteSlice(reqPtr, reqLen), &op); err != nil {
		return 0
	}
	result, err := providermanager.Apply(registry, op)
	if err != nil {
		emit(err.Error() + "\n")
		return 0
	}
	// A mutation re-binds the provided value, so later readers of the key
	// see the current registry. Re-binding keeps the same owner, so the
	// scheduler reports no provider change and no dependent reactivates.
	if op.Kind == "register" || op.Kind == "unregister" {
		if !bindProvided(providersDoc()) {
			emit("provider-manager: re-bind failed\n")
			return 0
		}
	}
	out, err := json.Marshal(result)
	if err != nil || uint32(len(out)) > respMax {
		return 0
	}
	copy(byteSlice(respPtr, respMax), out)
	return uint32(len(out))
}

//go:wasmexport memento_revert_effect
func mementoRevertEffect(effectID uint32) uint32 {
	emit("provider-manager: providers released\n")
	return 0
}

func main() {}
