package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type GeminiEmbedder struct {
	APIKey string
	Model  string
	client *http.Client
}

func NewGeminiEmbedder(apiKey, model string) *GeminiEmbedder {
	return &GeminiEmbedder{
		APIKey: apiKey,
		Model:  model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (e *GeminiEmbedder) ModelID() string { return e.Model }

func (e *GeminiEmbedder) Dimensions() int {
	switch e.Model {
	case "text-embedding-004", "gemini-embedding-exp-03-07":
		return 768
	default:
		return 768
	}
}

func (e *GeminiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
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

func (e *GeminiEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	type contentPart struct {
		Text string `json:"text"`
	}
	type content struct {
		Parts []contentPart `json:"parts"`
	}
	type embedReq struct {
		Model   string  `json:"model"`
		Content content `json:"content"`
	}

	requests := make([]embedReq, len(texts))
	for i, t := range texts {
		requests[i] = embedReq{
			Model:   "models/" + e.Model,
			Content: content{Parts: []contentPart{{Text: t}}},
		}
	}

	body, _ := json.Marshal(map[string]any{"requests": requests})
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:batchEmbedContents?key=%s", e.Model, e.APIKey)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini embeddings: status %d", resp.StatusCode)
	}

	var out struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	result := make([][]float32, len(out.Embeddings))
	for i, e := range out.Embeddings {
		result[i] = e.Values
	}
	return result, nil
}
