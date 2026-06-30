package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	case "text-embedding-004", "gemini-embedding-001":
		return 768
	case "gemini-embedding-004", "gemini-embedding-2", "gemini-embedding-2-preview":
		return 3072
	default:
		return 3072
	}
}

func (e *GeminiEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	// sequential to stay within free-tier RPM limits
	for i, text := range texts {
		vec, err := e.embedOneWithRetry(ctx, text)
		if err != nil {
			return nil, err
		}
		results[i] = vec
	}
	return results, nil
}

func (e *GeminiEmbedder) embedOneWithRetry(ctx context.Context, text string) ([]float32, error) {
	const maxRetries = 5
	delay := 5 * time.Second
	for attempt := 0; attempt < maxRetries; attempt++ {
		vec, retryAfter, err := e.embedOne(ctx, text)
		if err == nil {
			return vec, nil
		}
		if retryAfter == 0 {
			return nil, err // non-retryable error
		}
		wait := retryAfter
		if wait < delay {
			wait = delay
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		delay *= 2
	}
	return nil, fmt.Errorf("gemini embeddings: exceeded max retries")
}

// embedOne returns (vector, retryAfter, error).
// retryAfter > 0 means 429 and the caller should retry after that duration.
func (e *GeminiEmbedder) embedOne(ctx context.Context, text string) ([]float32, time.Duration, error) {
	body, _ := json.Marshal(map[string]any{
		"model": "models/" + e.Model,
		"content": map[string]any{
			"parts": []map[string]string{{"text": text}},
		},
	})

	url := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:embedContent?key=%s",
		e.Model, e.APIKey,
	)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		b, _ := io.ReadAll(resp.Body)
		// parse retryDelay from error details if present
		retryAfter := parseRetryDelay(b)
		if retryAfter == 0 {
			retryAfter = 60 * time.Second
		}
		return nil, retryAfter, fmt.Errorf("rate limited (retry in %s)", retryAfter.Round(time.Second))
	}

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("gemini embeddings: status %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}

	var out struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, 0, err
	}
	if len(out.Embedding.Values) == 0 {
		return nil, 0, fmt.Errorf("gemini embeddings: empty response")
	}
	return out.Embedding.Values, 0, nil
}

// parseRetryDelay extracts the retry delay from a Gemini 429 response body.
// The body contains {"error":{"details":[{"retryDelay":"37s"}]}}.
func parseRetryDelay(body []byte) time.Duration {
	// fast path: find "retryDelay" string value
	s := string(body)
	idx := strings.Index(s, `"retryDelay"`)
	if idx < 0 {
		return 0
	}
	rest := s[idx+len(`"retryDelay"`):]
	// skip : whitespace "
	rest = strings.TrimLeft(rest, `: "`)
	end := strings.IndexAny(rest, `"`)
	if end < 0 {
		return 0
	}
	d, err := time.ParseDuration(rest[:end])
	if err != nil {
		return 0
	}
	return d
}
