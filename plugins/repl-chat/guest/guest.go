//go:build wasip1

// Command guest is the repl-chat wasm guest for the memento loader.
//
// It provides the key "repl" (bound to the session nickname), injects
// "chat-history", "model-registry", and "llm-context", and hosts the REPL
// session on WASI stdin. Each chat turn runs the composed pipeline over the
// loader's invoke ABI: append the user turn, read recent history, project the
// context window, ask the model, and record the assistant reply.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"unsafe"

	replchat "github.com/DaviMGDev/core-agent/plugins/repl-chat"
)

const (
	effectID   = 1
	provideKey = "repl"

	historyKey   = "chat-history"
	contextKey   = "llm-context"
	modelKey     = "model-registry"
	conversation = "main"
	defaultModel = "fast"
	recentTurns  = 10
	responseCap  = 8192
)

var injectKeys = []string{"chat-history", "model-registry", "llm-context"}

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

// turn is one pipeline request; Op selects the operation.
type turn struct {
	Op           string    `json:"op"`
	Conversation string    `json:"conversation,omitempty"`
	Role         string    `json:"role,omitempty"`
	Text         string    `json:"text,omitempty"`
	N            int       `json:"n,omitempty"`
	Model        string    `json:"model,omitempty"`
	Messages     []message `json:"messages,omitempty"`
	Context      []message `json:"context,omitempty"`
}

// message is one conversation turn crossing the ABI.
type message struct {
	Role string `json:"role,omitempty"`
	Text string `json:"text"`
}

// historyResult reads the messages field of history and context responses.
type historyResult struct {
	Messages []message `json:"messages"`
}

// answerResult reads the text field of a model response.
type answerResult struct {
	Text string `json:"text"`
}

// invokeJSON sends one request to a key's provider and returns the raw
// response bytes.
func invokeJSON(key string, req any) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	kb := []byte(key)
	resp := make([]byte, responseCap)
	n := invokeHost(
		unsafe.Pointer(&kb[0]), uint32(len(kb)),
		unsafe.Pointer(&payload[0]), uint32(len(payload)),
		unsafe.Pointer(&resp[0]), uint32(len(resp)),
	)
	if n <= 0 {
		return nil, fmt.Errorf("invoke %s: no response", key)
	}
	return resp[:n], nil
}

// respond runs one chat turn through the composed system: append the user
// turn, read recent history, project it into the context window, ask the
// model, and record the assistant reply.
func respond(line string) (string, error) {
	if _, err := invokeJSON(historyKey, turn{Op: "append", Conversation: conversation, Role: "user", Text: line}); err != nil {
		return "", err
	}
	raw, err := invokeJSON(historyKey, turn{Op: "recent", Conversation: conversation, N: recentTurns})
	if err != nil {
		return "", err
	}
	var history historyResult
	if err := json.Unmarshal(raw, &history); err != nil {
		return "", fmt.Errorf("reading history: %w", err)
	}
	raw, err = invokeJSON(contextKey, turn{Op: "project", Messages: history.Messages})
	if err != nil {
		return "", err
	}
	var window historyResult
	if err := json.Unmarshal(raw, &window); err != nil {
		return "", fmt.Errorf("reading context: %w", err)
	}
	raw, err = invokeJSON(modelKey, turn{Op: "respond", Model: defaultModel, Text: line, Context: window.Messages})
	if err != nil {
		return "", err
	}
	var answer answerResult
	if err := json.Unmarshal(raw, &answer); err != nil {
		return "", fmt.Errorf("reading answer: %w", err)
	}
	if _, err := invokeJSON(historyKey, turn{Op: "append", Conversation: conversation, Role: "assistant", Text: answer.Text}); err != nil {
		return "", err
	}
	return answer.Text, nil
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
	if !bindProvided([]byte(session.Nick())) {
		return 2
	}
	if err := session.Run(os.Stdin, emit, respond); err != nil {
		emit("repl: input error: " + err.Error() + "\n")
		return 3
	}
	return 0
}

//go:wasmexport memento_revert_effect
func mementoRevertEffect(effectID uint32) uint32 {
	emit(replchat.LeaveMessage + "\n")
	return 0
}

func main() {}
