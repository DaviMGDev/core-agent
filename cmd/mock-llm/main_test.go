package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestMock(reply string) *mock {
	m := newMock(reply, log.New(io.Discard, "", 0))
	m.now = func() int64 { return 1700000000 }
	return m
}

func postCompletions(t *testing.T, handler http.Handler, path, body string) (*http.Response, chatResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	resp := rec.Result()
	t.Cleanup(func() { _ = resp.Body.Close() })

	var decoded chatResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
			t.Fatalf("decoding response: %v", err)
		}
	}
	return resp, decoded
}

func TestEchoesModelAndLastUserMessage(t *testing.T) {
	body := `{"model":"llama-3.2","messages":[{"role":"system","content":"be brief"},{"role":"user","content":"hello"},{"role":"assistant","content":"hi"},{"role":"user","content":"what is 2+2?"}],"stream":false}`
	resp, got := postCompletions(t, newTestMock(""), "/v1/chat/completions", body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got.Model != "llama-3.2" {
		t.Errorf("model = %q, want llama-3.2", got.Model)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(got.Choices))
	}
	if want := "mock(llama-3.2): what is 2+2?"; got.Choices[0].Message.Content != want {
		t.Errorf("content = %q, want %q", got.Choices[0].Message.Content, want)
	}
	if got.Choices[0].Message.Role != "assistant" || got.Choices[0].FinishReason != "stop" {
		t.Errorf("choice = %+v, want an assistant stop", got.Choices[0])
	}
	if got.Object != "chat.completion" || got.ID == "" || got.Created == 0 {
		t.Errorf("envelope = %+v, want an OpenAI-shaped completion", got)
	}
}

func TestFixedReplyOverridesEcho(t *testing.T) {
	_, got := postCompletions(t, newTestMock("fixed answer"), "/v1/chat/completions",
		`{"model":"m","messages":[{"role":"user","content":"anything"}]}`)
	if got.Choices[0].Message.Content != "fixed answer" {
		t.Errorf("content = %q, want the fixed reply", got.Choices[0].Message.Content)
	}
}

func TestAcceptsPathWithoutVersionPrefix(t *testing.T) {
	resp, _ := postCompletions(t, newTestMock(""), "/chat/completions",
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for /chat/completions", resp.StatusCode)
	}
}

func TestInvalidJSONIsRefused(t *testing.T) {
	resp, _ := postCompletions(t, newTestMock(""), "/v1/chat/completions", `not json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestModelsAreListed(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestMock("").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"object":"list"`) {
		t.Fatalf("body = %s, want a model list", rec.Body.String())
	}
}

func TestUnknownPathAndMethodAreNotFound(t *testing.T) {
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/v1/chat/completions"},
		{http.MethodPost, "/v1/embeddings"},
	} {
		rec := httptest.NewRecorder()
		newTestMock("").ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}
