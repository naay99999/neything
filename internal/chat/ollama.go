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

type OllamaChatModel struct {
	BaseURL string
	Model   string
	client  *http.Client
}

func NewOllamaChatModel(baseURL, model string) *OllamaChatModel {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaChatModel{
		BaseURL: baseURL,
		Model:   model,
		client:  &http.Client{Timeout: 300 * time.Second},
	}
}

func (m *OllamaChatModel) ModelID() string { return m.Model }

func (m *OllamaChatModel) Complete(ctx context.Context, prompt string, ctxChunks []chunk.Chunk) (string, error) {
	fullPrompt := buildPrompt(prompt, ctxChunks)

	body, _ := json.Marshal(map[string]any{
		"model":  m.Model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "user", "content": fullPrompt},
		},
	})

	req, err := http.NewRequestWithContext(ctx, "POST", m.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := apiretry.Do(ctx, m.client, req, 3)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama chat: status %d", resp.StatusCode)
	}

	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Message.Content == "" {
		return "", fmt.Errorf("empty response from Ollama")
	}
	return out.Message.Content, nil
}
