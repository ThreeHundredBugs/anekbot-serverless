package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultHuggingFaceBaseURL = "https://router.huggingface.co/v1"
	defaultHuggingFaceModel   = "deepseek-ai/DeepSeek-V4.1-Flash"
	huggingFaceRequestTimeout = 30 * time.Second
)

type hfMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type hfRequest struct {
	Model     string      `json:"model"`
	Messages  []hfMessage `json:"messages"`
	MaxTokens int         `json:"max_tokens"`
}

// hfError accepts both error shapes the Hugging Face router uses: a plain string, or {"message": "..."}.
type hfError struct {
	Message string
}

func (e *hfError) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &e.Message); err == nil {
		return nil
	}

	var asObject struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &asObject); err != nil {
		return err
	}
	e.Message = asObject.Message
	return nil
}

type hfResponse struct {
	Choices []struct {
		Message hfMessage `json:"message"`
	} `json:"choices"`
	Error *hfError `json:"error"`
}

type huggingFaceProvider struct {
	client  *http.Client
	baseURL string
	name    string
	apiKey  string
	model   string
	weight  int
}

func NewHuggingFaceProvider(name, apiKey, model string, weight int) *huggingFaceProvider {
	if model == "" {
		model = defaultHuggingFaceModel
	}
	return &huggingFaceProvider{
		client:  &http.Client{Timeout: huggingFaceRequestTimeout},
		baseURL: defaultHuggingFaceBaseURL,
		name:    name,
		apiKey:  apiKey,
		model:   model,
		weight:  weight,
	}
}

func (p *huggingFaceProvider) Name() string {
	return p.name
}

func (p *huggingFaceProvider) Weight() int {
	return p.weight
}

func (p *huggingFaceProvider) Ask(ctx context.Context, systemPrompt, question string) (string, error) {
	reqBody := hfRequest{
		Model:     p.model,
		MaxTokens: maxOutputTokens,
		Messages: []hfMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: question},
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", err
	}

	var parsed hfResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil {
			return "", fmt.Errorf("huggingface api: %s", parsed.Error.Message)
		}
		return "", fmt.Errorf("huggingface api: unexpected status %d", resp.StatusCode)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("huggingface api: no choices returned")
	}

	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}
