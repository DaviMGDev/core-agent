//go:build wasip1

// Command guest is the model-manager wasm guest for the memento loader.
//
// It provides the key "model-registry", injects "provider-registry",
// registers one effect, and turns the activation payload ({"models":[...]})
// into a registry whose every view resolves without cycles.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

//go:wasmimport memento get_len
func getLen(keyPtr unsafe.Pointer, keyLen uint32) int32

//go:wasmimport memento get
func getHost(keyPtr unsafe.Pointer, keyLen uint32, bufPtr unsafe.Pointer, bufMax uint32) int32

//go:wasmimport memento get_payload_len
func getPayloadLen() int32

//go:wasmimport memento get_payload
func getPayload(ptr unsafe.Pointer, max uint32) int32

//go:wasmimport memento http_request
func httpRequest(ptr unsafe.Pointer, n uint32) int32

//go:wasmimport memento http_response_len
func httpResponseLen() int32

//go:wasmimport memento http_response
func httpResponse(ptr unsafe.Pointer, max uint32) int32

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
	providers = nil
	if raw := injected("provider-registry"); len(raw) > 0 {
		var reg providerRegistry
		if err := json.Unmarshal(raw, &reg); err != nil {
			emit("model-manager: " + err.Error() + "\n")
			return 1
		}
		providers = reg.Providers
	}
	emit("model-manager: " + strconv.Itoa(len(views)) + " model view(s) ready\n")
	return 0
}

// provider is one entry of the injected provider registry document. The
// document crosses as JSON, so this package keeps its own shape and stays
// independent of provider-manager's library (system spec: isolation).
type provider struct {
	Name       string   `json:"name"`
	Endpoint   string   `json:"endpoint"`
	Credential string   `json:"credential"`
	Models     []string `json:"models"`
}

type providerRegistry struct {
	Providers []provider `json:"providers"`
}

// providers caches the injected registry for the instance.
var providers []provider

// providerFor returns the first provider, in config order, that serves model.
func providerFor(model string) (provider, bool) {
	for _, p := range providers {
		for _, m := range p.Models {
			if m == model {
				return p, true
			}
		}
	}
	return provider{}, false
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}

type httpRequestDoc struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

type httpResponseDoc struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// caller performs one model completion over the loader's HTTP transport,
// resolved to a provider endpoint. The credential crosses as a reference the
// host substitutes, so the guest never holds a secret.
type caller struct{}

func (caller) Complete(model string, messages []modelmanager.ContextMessage) (string, error) {
	p, ok := providerFor(model)
	if !ok {
		return "", fmt.Errorf("model-manager: no provider serves model %q", model)
	}
	msgs := make([]chatMessage, 0, len(messages))
	for _, m := range messages {
		msgs = append(msgs, chatMessage{Role: m.Role, Content: m.Text})
	}
	body, err := json.Marshal(chatRequest{Model: model, Messages: msgs})
	if err != nil {
		return "", err
	}
	doc, err := json.Marshal(httpRequestDoc{
		Method: "POST",
		URL:    strings.TrimRight(p.Endpoint, "/") + "/chat/completions",
		Headers: map[string]string{
			"content-type":  "application/json",
			"authorization": "Bearer " + p.Credential,
		},
		Body: string(body),
	})
	if err != nil {
		return "", err
	}
	if httpRequest(unsafe.Pointer(&doc[0]), uint32(len(doc))) != 0 {
		return "", fmt.Errorf("model-manager: request to provider %q failed", p.Name)
	}
	n := httpResponseLen()
	if n <= 0 {
		return "", errors.New("model-manager: provider returned no response document")
	}
	buf := make([]byte, n)
	if got := httpResponse(unsafe.Pointer(&buf[0]), uint32(len(buf))); got <= 0 {
		return "", errors.New("model-manager: provider response unavailable")
	}
	return decodeCompletion(p.Name, buf)
}

// decodeCompletion reads the response document and extracts the assistant text.
func decodeCompletion(providerName string, buf []byte) (string, error) {
	var resp httpResponseDoc
	if err := json.Unmarshal(buf, &resp); err != nil {
		return "", fmt.Errorf("model-manager: decoding response document: %w", err)
	}
	if resp.Status < 200 || resp.Status >= 300 {
		return "", fmt.Errorf("model-manager: provider %q returned status %d", providerName, resp.Status)
	}
	var completion chatResponse
	if err := json.Unmarshal([]byte(resp.Body), &completion); err != nil {
		return "", fmt.Errorf("model-manager: decoding completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", errors.New("model-manager: completion had no choices")
	}
	return completion.Choices[0].Message.Content, nil
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
	result, err := modelmanager.Apply(registry, op, caller{})
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
