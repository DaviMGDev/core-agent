//go:build wasip1

// Command guest is the model-manager wasm guest for the memento loader.
//
// It provides the key "model-registry", injects "provider-registry",
// registers one effect, and turns the activation payload ({"models":[...]})
// into a registry whose every view resolves without cycles.
package main

import (
	"encoding/json"
	"strconv"
	"unsafe"

	modelmanager "github.com/DaviMGDev/core-agent/plugins/model-manager"
)

const (
	effectID   = 1
	provideKey = "model-registry"
)

var injectKeys = []string{"provider-registry"}

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

	p := payload()
	if !bindProvided(p) {
		return 2
	}
	views, err := modelmanager.ParseConfig(p)
	if err != nil {
		emit("model-manager: " + err.Error() + "\n")
		return 1
	}
	registry = modelmanager.NewRegistry()
	for _, v := range views {
		if err := registry.Register(v); err != nil {
			emit("model-manager: " + err.Error() + "\n")
			return 1
		}
	}
	// Resolve every view eagerly: a cyclic or invalid configuration fails
	// the load instead of a later lookup.
	for _, v := range views {
		if _, err := registry.Resolve(v.Name); err != nil {
			emit("model-manager: " + err.Error() + "\n")
			return 1
		}
	}
	emit("model-manager: " + strconv.Itoa(len(views)) + " model view(s) ready\n")
	return 0
}

// registry is the module instance's view registry, serving handlers.
var registry *modelmanager.Registry

// arena keeps handler buffers alive for the duration of one exchange.
var arena [][]byte

func byteSlice(ptr, n uint32) []byte {
	if n == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), n)
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
	var op modelmanager.Op
	if err := json.Unmarshal(byteSlice(reqPtr, reqLen), &op); err != nil {
		return 0
	}
	result, err := modelmanager.Apply(registry, op)
	if err != nil {
		return 0
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
	emit("model-manager: model views released\n")
	return 0
}

func main() {}
