package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const testSystemPrompt = "you are a test assistant"

func newTestGeminiProvider(t *testing.T, statusCode int, responseBody string) (p *geminiProvider, lastRequest func() geminiRequest, lastPath func() string) {
	t.Helper()

	var mu sync.Mutex
	var captured geminiRequest
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		json.Unmarshal(body, &captured)
		path = r.URL.Path
		mu.Unlock()
		w.WriteHeader(statusCode)
		w.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)

	p = NewGeminiProvider("test", "test-key", "", nil, 1)
	p.client = server.Client()
	p.baseURL = server.URL

	lastRequest = func() geminiRequest {
		mu.Lock()
		defer mu.Unlock()
		return captured
	}
	lastPath = func() string {
		mu.Lock()
		defer mu.Unlock()
		return path
	}
	return p, lastRequest, lastPath
}

func TestGeminiProvider_Ask_Success(t *testing.T) {
	p, lastRequest, lastPath := newTestGeminiProvider(t, http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":"  42  "}]}}]}`)

	answer, err := p.Ask(context.Background(), testSystemPrompt, "what is the answer to everything?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if answer != "42" {
		t.Errorf("answer = %q, want %q", answer, "42")
	}

	req := lastRequest()
	if len(req.Contents) != 1 || len(req.Contents[0].Parts) != 1 {
		t.Fatalf("request contents = %+v, want a single user part", req.Contents)
	}
	if want := "what is the answer to everything?"; req.Contents[0].Parts[0].Text != want {
		t.Errorf("question = %q, want %q", req.Contents[0].Parts[0].Text, want)
	}
	if req.SystemInstruction == nil || req.SystemInstruction.Parts[0].Text != testSystemPrompt {
		t.Errorf("system instruction = %+v, want %q", req.SystemInstruction, testSystemPrompt)
	}
	if want := "/models/" + defaultGeminiModel + ":generateContent"; lastPath() != want {
		t.Errorf("request path = %q, want %q", lastPath(), want)
	}
	if got := req.GenerationConfig.ThinkingConfig.ThinkingBudget; got != defaultGeminiThinkingBudget {
		t.Errorf("thinking budget = %d, want default %d", got, defaultGeminiThinkingBudget)
	}
}

func TestGeminiProvider_Ask_CustomThinkingBudget(t *testing.T) {
	p, lastRequest, _ := newTestGeminiProvider(t, http.StatusOK, `{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`)
	budget := 0
	p.thinkingBudget = budget

	if _, err := p.Ask(context.Background(), testSystemPrompt, "hi"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got := lastRequest().GenerationConfig.ThinkingConfig.ThinkingBudget; got != budget {
		t.Errorf("thinking budget = %d, want %d", got, budget)
	}
}

func TestGeminiProvider_Ask_CustomModel(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
	}))
	t.Cleanup(server.Close)

	p := NewGeminiProvider("test", "test-key", "custom-model", nil, 1)
	p.client = server.Client()
	p.baseURL = server.URL

	if _, err := p.Ask(context.Background(), testSystemPrompt, "hi"); err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if want := "/models/custom-model:generateContent"; path != want {
		t.Errorf("request path = %q, want %q", path, want)
	}
}

func TestGeminiProvider_Ask_APIError(t *testing.T) {
	p, _, _ := newTestGeminiProvider(t, http.StatusInternalServerError, `{"error":{"message":"boom"}}`)

	_, err := p.Ask(context.Background(), testSystemPrompt, "are you ok")
	if err == nil {
		t.Fatal("expected an error on a non-200 response")
	}
}

func TestGeminiProvider_Ask_NoCandidates(t *testing.T) {
	p, _, _ := newTestGeminiProvider(t, http.StatusOK, `{"candidates":[]}`)

	_, err := p.Ask(context.Background(), testSystemPrompt, "are you ok")
	if err == nil {
		t.Fatal("expected an error when no candidates are returned")
	}
}
