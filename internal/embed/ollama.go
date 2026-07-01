package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type OllamaEmbedder struct {
	BaseURL    string
	Model      string
	client     *http.Client
	dimensions int
	dims       sync.Once
	legacyMode bool
	legacyOnce sync.Once
}

func NewOllamaEmbedder(baseURL, model string) (*OllamaEmbedder, error) {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	e := &OllamaEmbedder{
		BaseURL: baseURL,
		Model:   model,
		client:  &http.Client{Timeout: 120 * time.Second},
	}
	// probe to get dimensions
	vecs, err := e.Embed(context.Background(), []string{"probe"})
	if err != nil {
		return nil, fmt.Errorf("ollama probe embed: %w", err)
	}
	if len(vecs) > 0 {
		e.dimensions = len(vecs[0])
	}
	return e, nil
}

func (e *OllamaEmbedder) ModelID() string  { return e.Model }
func (e *OllamaEmbedder) Dimensions() int  { return e.dimensions }

func (e *OllamaEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	// detect legacy mode on first call
	var legacyErr error
	e.legacyOnce.Do(func() {
		body, _ := json.Marshal(map[string]any{"model": e.Model, "input": texts[:1]})
		req, _ := http.NewRequestWithContext(ctx, "POST", e.BaseURL+"/api/embed", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := e.client.Do(req)
		if err != nil {
			legacyErr = err
			return
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			e.legacyMode = true
		}
	})
	if legacyErr != nil {
		return nil, legacyErr
	}

	if e.legacyMode {
		return e.embedLegacy(ctx, texts)
	}
	return e.embedBatch(ctx, texts)
}

func (e *OllamaEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	body, _ := json.Marshal(map[string]any{"model": e.Model, "input": texts})
	req, err := http.NewRequestWithContext(ctx, "POST", e.BaseURL+"/api/embed", bytes.NewReader(body))
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
		return nil, fmt.Errorf("ollama embed: status %d%s", resp.StatusCode, errBodySnippet(resp.Body))
	}

	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Embeddings, nil
}

func (e *OllamaEmbedder) embedLegacy(ctx context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, 0, len(texts))
	for _, text := range texts {
		body, _ := json.Marshal(map[string]any{"model": e.Model, "prompt": text})
		req, err := http.NewRequestWithContext(ctx, "POST", e.BaseURL+"/api/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := e.client.Do(req)
		if err != nil {
			return nil, err
		}

		var out struct {
			Embedding []float32 `json:"embedding"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			resp.Body.Close()
			return nil, err
		}
		resp.Body.Close()
		result = append(result, out.Embedding)
	}
	return result, nil
}
