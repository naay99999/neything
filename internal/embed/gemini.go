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

// geminiMaxBatchSize is the largest number of texts sent to Gemini's
// batchEmbedContents endpoint in one HTTP call. Gemini's documented limit
// for this endpoint is 100 requests/call — see MaxBatchSize, consumed by
// EmbedWorker's provider-aware batching (internal/index/embedworker.go).
const geminiMaxBatchSize = 100

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
	case "text-embedding-004", "gemini-embedding-001":
		return 768
	case "gemini-embedding-004", "gemini-embedding-2", "gemini-embedding-2-preview":
		return 3072
	default:
		return 3072
	}
}

// MaxBatchSize reports Gemini's batchEmbedContents request-size ceiling —
// see the batchSizer capability EmbedWorker looks for.
func (e *GeminiEmbedder) MaxBatchSize() int { return geminiMaxBatchSize }

// Embed embeds texts via Gemini's batchEmbedContents endpoint — one HTTP
// request per up-to-geminiMaxBatchSize texts, instead of the old one
// request per text (embedContent, sequentially). texts longer than
// geminiMaxBatchSize are split into multiple batchEmbedContents calls;
// EmbedWorker's provider-aware batch sizing (batchSize(), which consults
// MaxBatchSize above) means that split essentially never triggers in
// practice, but Embed stays correct for any caller regardless of what batch
// size they hand it.
func (e *GeminiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	all := make([][]float32, 0, len(texts))
	for i := 0; i < len(texts); i += geminiMaxBatchSize {
		end := i + geminiMaxBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := e.embedBatch(ctx, texts[i:end])
		if err != nil {
			return nil, err
		}
		all = append(all, vecs...)
	}
	return all, nil
}

// embedBatch calls batchEmbedContents for one batch of up to
// geminiMaxBatchSize texts. Retries (on 429/503, and now on transient
// network errors too) are handled by apiretry.Do, same as the OpenAI
// embedder — Gemini's 429 responses report their retry delay in the
// response body rather than a Retry-After header, which apiretry.retryAfter
// parses as a fallback (see internal/apiretry/retry.go).
func (e *GeminiEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	requests := make([]map[string]any, len(texts))
	for i, text := range texts {
		requests[i] = map[string]any{
			"model": "models/" + e.Model,
			"content": map[string]any{
				"parts": []map[string]string{{"text": text}},
			},
		}
	}
	body, _ := json.Marshal(map[string]any{"requests": requests})

	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:batchEmbedContents?key=%s",
		e.Model, e.APIKey,
	)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := apiretry.Do(ctx, e.client, req, 5)
	if err != nil {
		return nil, fmt.Errorf("gemini embeddings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini embeddings: status %d%s", resp.StatusCode, errBodySnippet(resp.Body))
	}

	var out struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("gemini embeddings: expected %d embeddings, got %d", len(texts), len(out.Embeddings))
	}

	result := make([][]float32, len(out.Embeddings))
	for i, emb := range out.Embeddings {
		result[i] = emb.Values
	}
	return result, nil
}
