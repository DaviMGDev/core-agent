//go:build wasip1

// Command guest is the repl-chat wasm guest for the memento loader: the
// terminal. It provides the key "repl" (bound to the session nickname),
// injects "agent-loop", hosts the REPL session on WASI stdin, and hands every
// user line to the agent. The agent owns the turn pipeline; the terminal is a
// view over the transcript.
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

// wake is the agent's request document for one terminal line.
type wake struct {
	Line string `json:"line"`
}

// reply is the agent's answer: the message it spoke, if any.
type reply struct {
	Text string `json:"text"`
}

// respond hands one user line to the agent and returns the message it spoke.
// A turn that starts a job or stays silent returns no text, so the terminal
// renders nothing — speech is the agent's call.
func respond(line string) (string, error) {
	payload, err := json.Marshal(wake{Line: line})
	if err != nil {
		return "", err
	}
	kb := []byte(agentKey)
	resp := make([]byte, responseCap)
	n := invokeHost(
		unsafe.Pointer(&kb[0]), uint32(len(kb)),
		unsafe.Pointer(&payload[0]), uint32(len(payload)),
		unsafe.Pointer(&resp[0]), uint32(len(resp)),
	)
	if n <= 0 {
		return "", fmt.Errorf("invoke %s: no response", agentKey)
	}
	var out reply
	if err := json.Unmarshal(resp[:n], &out); err != nil {
		return "", fmt.Errorf("reading agent reply: %w", err)
	}
	return out.Text, nil
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
