// Command mock-llm serves an OpenAI-compatible chat completions endpoint with
// a deterministic reply, so the composed system runs without a real model
// provider.
//
//	go run ./cmd/mock-llm
//	CORE_AGENT_LOCAL_KEY=mock go run ./cmd/core-agent
//
// The shipped "local" provider points at http://127.0.0.1:11434/v1, which is
// the mock's default address. The provider's credential is a reference
// (`env:CORE_AGENT_LOCAL_KEY`) the host resolves before the request leaves;
// the mock accepts any value, so any non-empty environment value works.
//
// The reply echoes the model and the last user message unless -reply fixes it,
// which keeps a session readable while showing what actually reached the
// endpoint. Only `POST .../chat/completions` is needed; `GET .../models`
// is served for manual checks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// DefaultAddr matches the endpoint of the shipped "local" provider.
const DefaultAddr = "127.0.0.1:11434"

// chatRequest is the subset of an OpenAI chat completions request the mock reads.
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
}

// message is one conversation turn on the wire.
type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponse is the OpenAI-shaped reply.
type chatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
}

// choice is one completion candidate.
type choice struct {
	Index        int     `json:"index"`
	Message      message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// mock is the HTTP handler behind the command.
type mock struct {
	reply  string
	logger *log.Logger
	now    func() int64
}

// newMock builds the handler: a fixed reply when reply is non-empty, otherwise
// an echo of the model and the last user message.
func newMock(reply string, logger *log.Logger) *mock {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &mock{reply: reply, logger: logger, now: func() int64 { return time.Now().Unix() }}
}

func (m *mock) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
		m.complete(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
		m.models(w)
	default:
		http.Error(w, "mock-llm: not found", http.StatusNotFound)
	}
}

func (m *mock) complete(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "mock-llm: invalid JSON", http.StatusBadRequest)
		return
	}
	content := m.reply
	if content == "" {
		content = fmt.Sprintf("mock(%s): %s", req.Model, lastUserText(req.Messages))
	}
	m.logger.Printf("chat/completions model=%s messages=%d -> %q", req.Model, len(req.Messages), content)
	writeJSON(w, chatResponse{
		ID:      "chatcmpl-mock",
		Object:  "chat.completion",
		Created: m.now(),
		Model:   req.Model,
		Choices: []choice{{
			Index:        0,
			Message:      message{Role: "assistant", Content: content},
			FinishReason: "stop",
		}},
	})
}

func (m *mock) models(w http.ResponseWriter) {
	writeJSON(w, map[string]any{
		"object": "list",
		"data":   []map[string]string{{"id": "mock", "object": "model"}},
	})
}

// lastUserText returns the content of the last user turn, falling back to the
// last message of any role.
func lastUserText(messages []message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	if len(messages) > 0 {
		return messages[len(messages)-1].Content
	}
	return ""
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	addr := flag.String("addr", DefaultAddr, "address to listen on")
	reply := flag.String("reply", "", "fixed reply text; empty echoes the model and last user message")
	quiet := flag.Bool("quiet", false, "suppress per-request logging")
	flag.Parse()

	logger := log.New(os.Stderr, "mock-llm: ", 0)
	if *quiet {
		logger = log.New(io.Discard, "", 0)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           newMock(*reply, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	logger.Printf("listening on http://%s (chat completions at /v1/chat/completions)", *addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal(err)
		}
	case <-stop:
		logger.Print("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
