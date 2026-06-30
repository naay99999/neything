package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type OpenAIEmbedder struct {
	APIKey  string
	Model   string
	BaseURL string // empty = use OpenAI default
	client  *http.Client
}

func NewOpenAIEmbedder(apiKey, model string) *OpenAIEmbedder {
	return &OpenAIEmbedder{
		APIKey: apiKey,
		Model:  model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func NewOpenAICompatibleEmbedder(baseURL, model string) *OpenAIEmbedder {
	return &OpenAIEmbedder{
		BaseURL: baseURL,
		Model:   model,
		client:  &http.Client{Timeout: 60 * time.Second},
	}
}

func (e *OpenAIEmbedder) ModelID() string { return e.Model }

func (e *OpenAIEmbedder) Dimensions() int {
	switch e.Model {
	case "text-embedding-3-large":
		return 3072
	case "text-embedding-ada-002":
		return 1536
	case "text-embedding-nomic-embed-text-v1.5", "nomic-embed-text":
		return 768
	default:
		return 1536
	}
}

func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	const batchSize = 100
	var all [][]float32
	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch, err := e.embedBatch(ctx, texts[i:end])
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
	}
	return all, nil
}

func (e *OpenAIEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{
		"model": e.Model,
		"input": texts,
	})

	var result [][]float32
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		baseURL := e.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		if e.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+e.APIKey)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Content-Type", "application/json")

		resp, err := e.client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			time.Sleep(time.Duration(1<<attempt) * time.Second)
			lastErr = fmt.Errorf("openai rate limited")
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("openai embeddings: status %d", resp.StatusCode)
		}

		var out struct {
			Data []struct {
				Embedding []float32 `json:"embedding"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()

		for _, d := range out.Data {
			result = append(result, d.Embedding)
		}
		return result, nil
	}
	return nil, lastErr
}
