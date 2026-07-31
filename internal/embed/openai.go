package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/naay99999/neything/internal/apiretry"
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

// openAIMaxBatchSize is OpenAI's documented /v1/embeddings request-size
// ceiling — see MaxBatchSize, consumed by EmbedWorker's provider-aware
// batching (internal/index/embedworker.go) so it actually hands Embed
// batches this large instead of the old flat default of 32. The chunking
// loop in Embed below stays as a defensive fallback for any other caller
// that hands it more than openAIMaxBatchSize texts at once.
const openAIMaxBatchSize = 100

// MaxBatchSize reports OpenAI's /v1/embeddings request-size ceiling — see
// the batchSizer capability EmbedWorker looks for.
func (e *OpenAIEmbedder) MaxBatchSize() int { return openAIMaxBatchSize }

func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	var all [][]float32
	for i := 0; i < len(texts); i += openAIMaxBatchSize {
		end := i + openAIMaxBatchSize
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

	resp, err := apiretry.Do(ctx, e.client, req, 3)
	if err != nil {
		return nil, fmt.Errorf("openai embeddings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embeddings: status %d%s", resp.StatusCode, errBodySnippet(resp.Body))
	}

	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	result := make([][]float32, 0, len(out.Data))
	for _, d := range out.Data {
		result = append(result, d.Embedding)
	}
	return result, nil
}
