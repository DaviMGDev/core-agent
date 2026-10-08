//go:build wasip1

// Command guest is the provider-openai wasm guest for the memento loader.
//
// It injects the key "provider-registry" and provides nothing: at
// activation it registers its OpenAI-compatible provider through the live
// registry operation, and its registered effect unregisters the provider on
// unload. When the registry already carries the same name (for example from
// a config file), the guest leaves it untouched and registers no cleanup.
package main

import (
	"encoding/json"
	"unsafe"

	provideropenai "github.com/DaviMGDev/core-agent/plugins/provider-openai"
)

const (
	effectID    = 1
	registryKey = "provider-registry"
	responseCap = 4096
)

//go:wasmimport memento declare_inject
func declareInject(ptr unsafe.Pointer, n uint32) int32

//go:wasmimport memento invoke
func invokeHost(keyPtr unsafe.Pointer, keyLen uint32, reqPtr unsafe.Pointer, reqLen uint32, respPtr unsafe.Pointer, respMax uint32) int32

//go:wasmimport memento get_len
func getLen(keyPtr unsafe.Pointer, keyLen uint32) int32

//go:wasmimport memento get
func getHost(keyPtr unsafe.Pointer, keyLen uint32, bufPtr unsafe.Pointer, bufMax uint32) int32

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

// injected returns the bytes bound at key in the committed view.
func injected(key string) []byte {
	kb := []byte(key)
	n := getLen(unsafe.Pointer(&kb[0]), uint32(len(kb)))
	if n <= 0 {
		return nil
	}
	buf := make([]byte, n)
	got := getHost(unsafe.Pointer(&kb[0]), uint32(len(kb)), unsafe.Pointer(&buf[0]), uint32(len(buf)))
	if got <= 0 {
		return nil
	}
	return buf[:got]
}

// invoke sends one request document to the key's provider and reports
// whether the provider answered. The request is already marshaled; the
// response bytes are returned for the caller to decode.
func invoke(key string, req []byte) ([]byte, bool) {
	if len(req) == 0 {
		return nil, false
	}
	kb := []byte(key)
	resp := make([]byte, responseCap)
	n := invokeHost(
		unsafe.Pointer(&kb[0]), uint32(len(kb)),
		unsafe.Pointer(&req[0]), uint32(len(req)),
		unsafe.Pointer(&resp[0]), uint32(len(resp)),
	)
	if n <= 0 {
		return nil, false
	}
	return resp[:n], true
}

// config is the provider this instance registered; the effect inverse
// unregisters exactly this name.
var config provideropenai.Config

// owned reports whether this instance registered the provider: a
// pre-existing same-named entry is left untouched, and there is nothing to
// clean up on unload.
var owned bool

//go:wasmexport memento_declare
func mementoDeclare() uint32 {
	b := []byte(registryKey)
	if declareInject(unsafe.Pointer(&b[0]), uint32(len(b))) != 0 {
		return 1
	}
	return 0
}

//go:wasmexport memento_activate
func mementoActivate() uint32 {
	cfg, err := provideropenai.ParseConfig(payload())
	if err != nil {
		emit("provider-openai: " + err.Error() + "\n")
		return 1
	}
	config = cfg
	// A same-named entry (for example from a config file) stays: the
	// plugin leaves it untouched rather than failing the composition.
	if config.Present(injected(registryKey)) {
		emit("provider-openai: provider \"" + config.Name + "\" already registered, leaving it\n")
		return 0
	}
	raw, ok := invoke(registryKey, config.RegisterRequest())
	if !ok {
		emit("provider-openai: register \"" + config.Name + "\" got no answer\n")
		return 2
	}
	var res struct {
		Ok    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || !res.Ok {
		emit("provider-openai: register \"" + config.Name + "\" failed\n")
		return 2
	}
	owned = true
	if registerEffect(effectID) != 0 {
		return 1
	}
	emit("provider-openai: provider \"" + config.Name + "\" registered\n")
	return 0
}

//go:wasmexport memento_revert_effect
func mementoRevertEffect(effectID uint32) uint32 {
	if !owned {
		return 0
	}
	// Unload ordering deactivates dependents first, so the registry is
	// still alive here; its inverse runs after this one.
	if _, ok := invoke(registryKey, config.UnregisterRequest()); !ok {
		emit("provider-openai: unregister \"" + config.Name + "\" got no answer\n")
		return 1
	}
	emit("provider-openai: provider \"" + config.Name + "\" released\n")
	return 0
}

func main() {}
