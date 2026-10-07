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
	defaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	defaultGeminiModel   = "gemini-3.6-flash"
	geminiRequestTimeout = 30 * time.Second

	defaultGeminiThinkingBudget = 512
)

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  struct {
		MaxOutputTokens int `json:"maxOutputTokens"`
		ThinkingConfig  struct {
			ThinkingBudget int `json:"thinkingBudget"`
		} `json:"thinkingConfig"`
	} `json:"generationConfig"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type geminiProvider struct {
	client         *http.Client
	baseURL        string
	name           string
	apiKey         string
	model          string
	thinkingBudget int
	weight         int
}

func NewGeminiProvider(name, apiKey, model string, thinkingBudget *int, weight int) *geminiProvider {
	if model == "" {
		model = defaultGeminiModel
	}
	budget := defaultGeminiThinkingBudget
	if thinkingBudget != nil {
		budget = *thinkingBudget
	}
	return &geminiProvider{
		client:         &http.Client{Timeout: geminiRequestTimeout},
		baseURL:        defaultGeminiBaseURL,
		name:           name,
		apiKey:         apiKey,
		model:          model,
		thinkingBudget: budget,
		weight:         weight,
	}
}

func (p *geminiProvider) Name() string {
	return p.name
}

func (p *geminiProvider) Weight() int {
	return p.weight
}

func (p *geminiProvider) Ask(ctx context.Context, systemPrompt, question string) (string, error) {
	reqBody := geminiRequest{
		SystemInstruction: &geminiContent{Parts: []geminiPart{{Text: systemPrompt}}},
		Contents: []geminiContent{
			{Role: "user", Parts: []geminiPart{{Text: question}}},
		},
	}
	reqBody.GenerationConfig.MaxOutputTokens = maxOutputTokens
	reqBody.GenerationConfig.ThinkingConfig.ThinkingBudget = p.thinkingBudget
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", p.baseURL, p.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", err
	}

	var parsed geminiResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if parsed.Error != nil {
			return "", fmt.Errorf("gemini api: %s", parsed.Error.Message)
		}
		return "", fmt.Errorf("gemini api: unexpected status %d", resp.StatusCode)
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", errors.New("gemini api: no candidates returned")
	}

	return strings.TrimSpace(parsed.Candidates[0].Content.Parts[0].Text), nil
}
