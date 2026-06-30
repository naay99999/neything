package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/naay99999/neything/internal/chunk"
)

type OpenAIChatModel struct {
	APIKey  string
	Model   string
	BaseURL string // empty = use OpenAI default
	client  *http.Client
}

func NewOpenAIChatModel(apiKey, model string) *OpenAIChatModel {
	return &OpenAIChatModel{
		APIKey: apiKey,
		Model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func NewOpenAICompatibleChatModel(baseURL, model string) *OpenAIChatModel {
	return &OpenAIChatModel{
		BaseURL: baseURL,
		Model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
}

func (m *OpenAIChatModel) ModelID() string { return m.Model }

func (m *OpenAIChatModel) Complete(ctx context.Context, prompt string, ctxChunks []chunk.Chunk) (string, error) {
	fullPrompt := buildPrompt(prompt, ctxChunks)

	body, _ := json.Marshal(map[string]any{
		"model": m.Model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helpful assistant that answers questions based on provided context."},
			{"role": "user", "content": fullPrompt},
		},
	})

	baseURL := m.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	if m.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.APIKey)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai chat: status %d", resp.StatusCode)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("no choices in OpenAI response")
	}
	return out.Choices[0].Message.Content, nil
}
