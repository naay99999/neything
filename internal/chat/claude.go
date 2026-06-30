package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/naay99999/neything/internal/apiretry"
	"github.com/naay99999/neything/internal/chunk"
)

type ClaudeChatModel struct {
	APIKey string
	Model  string
	client *http.Client
}

func NewClaudeChatModel(apiKey, model string) *ClaudeChatModel {
	return &ClaudeChatModel{
		APIKey: apiKey,
		Model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

func (m *ClaudeChatModel) ModelID() string { return m.Model }

func (m *ClaudeChatModel) Complete(ctx context.Context, prompt string, ctxChunks []chunk.Chunk) (string, error) {
	fullPrompt := buildPrompt(prompt, ctxChunks)

	body, _ := json.Marshal(map[string]any{
		"model":      m.Model,
		"max_tokens": 4096,
		"messages": []map[string]string{
			{"role": "user", "content": fullPrompt},
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", m.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	resp, err := apiretry.Do(ctx, m.client, req, 3)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody map[string]any
		json.NewDecoder(resp.Body).Decode(&errBody)
		return "", fmt.Errorf("claude API: status %d: %v", resp.StatusCode, errBody)
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	for _, c := range out.Content {
		if c.Type == "text" {
			return c.Text, nil
		}
	}
	return "", fmt.Errorf("no text content in Claude response")
}
