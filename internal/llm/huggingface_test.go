package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func newTestHuggingFaceProvider(t *testing.T, statusCode int, responseBody string) (p *huggingFaceProvider, lastRequest func() hfRequest) {
	t.Helper()

	var mu sync.Mutex
	var captured hfRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		json.Unmarshal(body, &captured)
		mu.Unlock()
		w.WriteHeader(statusCode)
		w.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)

	p = NewHuggingFaceProvider("test", "test-token", "", 1)
	p.client = server.Client()
	p.baseURL = server.URL

	lastRequest = func() hfRequest {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
	return p, lastRequest
}

func TestHuggingFaceProvider_Ask_Success(t *testing.T) {
	p, lastRequest := newTestHuggingFaceProvider(t, http.StatusOK, `{"choices":[{"message":{"role":"assistant","content":"  42  "}}]}`)

	answer, err := p.Ask(context.Background(), testSystemPrompt, "what is the answer to everything?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if answer != "42" {
		t.Errorf("answer = %q, want %q", answer, "42")
	}

	req := lastRequest()
	if req.Model != defaultHuggingFaceModel {
		t.Errorf("model = %q, want %q", req.Model, defaultHuggingFaceModel)
	}
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
		t.Fatalf("messages = %+v, want [system, user]", req.Messages)
	}
	if req.Messages[0].Content != testSystemPrompt {
		t.Errorf("system message = %q, want %q", req.Messages[0].Content, testSystemPrompt)
	}
	if want := "what is the answer to everything?"; req.Messages[1].Content != want {
		t.Errorf("question = %q, want %q", req.Messages[1].Content, want)
	}
}

func TestHuggingFaceProvider_Ask_CustomModel(t *testing.T) {
	p, lastRequest := newTestHuggingFaceProvider(t, http.StatusOK, `{"choices":[{"message":{"content":"ok"}}]}`)
	p.model = "custom-model"

	if _, err := p.Ask(context.Background(), testSystemPrompt, "hi"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got := lastRequest().Model; got != "custom-model" {
		t.Errorf("model = %q, want %q", got, "custom-model")
	}
}

func TestHuggingFaceProvider_Ask_APIError(t *testing.T) {
	p, _ := newTestHuggingFaceProvider(t, http.StatusInternalServerError, `{"error":{"message":"boom"}}`)

	_, err := p.Ask(context.Background(), testSystemPrompt, "are you ok")
	if err == nil {
		t.Fatal("expected an error on a non-200 response")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to contain %q", err, "boom")
	}
}

func TestHuggingFaceProvider_Ask_APIError_StringShaped(t *testing.T) {
	p, _ := newTestHuggingFaceProvider(t, http.StatusInternalServerError, `{"error":"boom"}`)

	_, err := p.Ask(context.Background(), testSystemPrompt, "are you ok")
	if err == nil {
		t.Fatal("expected an error on a non-200 response")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to contain %q", err, "boom")
	}
}

func TestHuggingFaceProvider_Ask_NoChoices(t *testing.T) {
	p, _ := newTestHuggingFaceProvider(t, http.StatusOK, `{"choices":[]}`)

	_, err := p.Ask(context.Background(), testSystemPrompt, "are you ok")
	if err == nil {
		t.Fatal("expected an error when no choices are returned")
	}
}
