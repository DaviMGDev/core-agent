//go:build wasip1

// Command guest is the agent wasm guest: it owns the turn loop split out of
// repl-chat (system D14) and provides the key "agent-loop". The terminal
// invokes it for user lines; the host waker invokes the same handler with
// queued job events. One implementation serves the top-level agent and every
// subagent.
package main

import (
	"encoding/json"
	"fmt"
	"unsafe"

	"github.com/DaviMGDev/core-agent/plugins/agent"
)

const (
	effectID   = 1
	provideKey = "agent-loop"

	historyKey  = "chat-history"
	contextKey  = "llm-context"
	modelKey    = "model-registry"
	responseCap = 16384
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

//go:wasmimport memento job_start
func jobStart(reqPtr unsafe.Pointer, reqLen uint32) int32

//go:wasmimport memento job_result_len
func jobResultLen() int32

//go:wasmimport memento job_result
func jobResult(ptr unsafe.Pointer, max uint32) int32

//go:wasmimport memento publish
func publishHost(topicPtr unsafe.Pointer, topicLen uint32, payloadPtr unsafe.Pointer, payloadLen uint32) int32

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

// config holds the agent configuration parsed at activation.
var config = agent.Config{}

// turn is one pipeline request; Op selects the operation.
type turn struct {
	Op           string          `json:"op"`
	Conversation string          `json:"conversation,omitempty"`
	Role         string          `json:"role,omitempty"`
	Text         string          `json:"text,omitempty"`
	N            int             `json:"n,omitempty"`
	Model        string          `json:"model,omitempty"`
	Messages     []agent.Message `json:"messages,omitempty"`
	Context      []agent.Message `json:"context,omitempty"`
	Tools        []agent.Tool    `json:"tools,omitempty"`
}

// historyResult reads the messages field of history and context responses.
type historyResult struct {
	Messages []agent.Message `json:"messages"`
}

// answerResult reads the text field of a model response.
type answerResult struct {
	Text string `json:"text"`
}

// wakeResponse is the handler's answer: the message it spoke, the job it
// started, and — when asked — the conversation the turn ran on.
type wakeResponse struct {
	Text         string          `json:"text,omitempty"`
	Job          string          `json:"job,omitempty"`
	Conversation []agent.Message `json:"conversation,omitempty"`
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

// deps builds the turn's operations over the composed system: the pipeline
// over invoke, jobs over the job imports, messages over publish. A private
// turn — a subagent's — keeps its speech in the child's conversation.
func deps(cfg agent.Config, publish bool) agent.Deps {
	return agent.Deps{
		Append: func(role, text string) error {
			_, err := invokeJSON(historyKey, turn{Op: "append", Conversation: cfg.Conversation, Role: role, Text: text})
			return err
		},
		Recent: func(n int) ([]agent.Message, error) {
			raw, err := invokeJSON(historyKey, turn{Op: "recent", Conversation: cfg.Conversation, N: n})
			if err != nil {
				return nil, err
			}
			var res historyResult
			if err := json.Unmarshal(raw, &res); err != nil {
				return nil, fmt.Errorf("reading history: %w", err)
			}
			return res.Messages, nil
		},
		Project: func(msgs []agent.Message) ([]agent.Message, error) {
			raw, err := invokeJSON(contextKey, turn{Op: "project", Messages: msgs})
			if err != nil {
				return nil, err
			}
			var res historyResult
			if err := json.Unmarshal(raw, &res); err != nil {
				return nil, fmt.Errorf("reading context: %w", err)
			}
			return res.Messages, nil
		},
		Respond: func(text string, context []agent.Message, tools []agent.Tool) (agent.Answer, error) {
			raw, err := invokeJSON(modelKey, turn{Op: "respond", Model: cfg.Model, Text: text, Context: context, Tools: tools})
			if err != nil {
				return agent.Answer{}, err
			}
			var res answerResult
			if err := json.Unmarshal(raw, &res); err != nil {
				return agent.Answer{}, fmt.Errorf("reading answer: %w", err)
			}
			return agent.ParseAnswer(res.Text), nil
		},
		StartJob: func(tool string, args json.RawMessage) (string, error) {
			req := map[string]any{"tool": tool}
			if len(args) > 0 {
				req["args"] = args
			}
			b, err := json.Marshal(req)
			if err != nil {
				return "", err
			}
			if code := jobStart(unsafe.Pointer(&b[0]), uint32(len(b))); code != 0 {
				return "", fmt.Errorf("job_start: code %d", code)
			}
			n := jobResultLen()
			if n <= 0 {
				return "", fmt.Errorf("job_start: no handle")
			}
			buf := make([]byte, n)
			got := jobResult(unsafe.Pointer(&buf[0]), uint32(len(buf)))
			if got <= 0 {
				return "", fmt.Errorf("job_start: handle read failed")
			}
			var handle struct {
				Job string `json:"job"`
			}
			if err := json.Unmarshal(buf[:got], &handle); err != nil {
				return "", fmt.Errorf("job_start: %w", err)
			}
			return handle.Job, nil
		},
		Publish: func(text string) error {
			if !publish {
				return nil
			}
			topic := []byte("chat.message")
			payload, err := json.Marshal(map[string]string{"text": text})
			if err != nil {
				return err
			}
			if code := publishHost(unsafe.Pointer(&topic[0]), uint32(len(topic)), unsafe.Pointer(&payload[0]), uint32(len(payload))); code != 0 {
				return fmt.Errorf("publish: code %d", code)
			}
			return nil
		},
	}
}

// allTurns asks chat-history for the whole conversation; recent clamps to the
// length, so a bound above any real conversation returns everything.
const allTurns = 1 << 20

// dumpConversation reads a conversation back from chat-history.
func dumpConversation(conversation string) ([]agent.Message, error) {
	raw, err := invokeJSON(historyKey, turn{Op: "recent", Conversation: conversation, N: allTurns})
	if err != nil {
		return nil, err
	}
	var res historyResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("reading history: %w", err)
	}
	return res.Messages, nil
}

// describeEvents flattens a bus wake into the line the model sees.
func describeEvents(events []agent.Event) string {
	line := ""
	for _, e := range events {
		if line != "" {
			line += "; "
		}
		line += e.Topic
		if len(e.Payload) > 0 {
			line += " " + string(e.Payload)
		}
	}
	return line
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
	if raw := payload(); raw != "" {
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			emit("agent: bad config: " + err.Error() + "\n")
			return 2
		}
	}
	config = config.Normalized()
	if !bindProvided([]byte(config.Conversation)) {
		return 3
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

//go:wasmexport memento_handle
func mementoHandle(reqPtr, reqLen, respPtr, respMax uint32) uint32 {
	defer func() { arena = arena[:0] }()
	var wake agent.Wake
	if err := json.Unmarshal(byteSlice(reqPtr, reqLen), &wake); err != nil {
		return 0
	}
	cfg := config
	if wake.Config != nil {
		cfg = wake.Config.Normalized()
	}
	line := wake.Line
	if line == "" {
		line = describeEvents(wake.Events)
	}
	if line == "" {
		return 0
	}
	result, err := agent.RunTurn(cfg, line, deps(cfg, !wake.Private))
	if err != nil {
		emit("agent: " + err.Error() + "\n")
		return 0
	}
	out := wakeResponse{Text: result.Text, Job: result.Job}
	if wake.Dump {
		if msgs, err := dumpConversation(cfg.Conversation); err == nil {
			out.Conversation = msgs
		}
	}
	b, err := json.Marshal(out)
	if err != nil || uint32(len(b)) > respMax {
		return 0
	}
	copy(byteSlice(respPtr, respMax), b)
	return uint32(len(b))
}

//go:wasmexport memento_revert_effect
func mementoRevertEffect(effectID uint32) uint32 {
	emit("agent: loop closed\n")
	return 0
}

func main() {}
