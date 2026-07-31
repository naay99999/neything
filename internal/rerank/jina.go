package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/apiretry"
)

type JinaReranker struct {
	APIKey  string
	Model   string
	BaseURL string
	client  *http.Client
}

func NewJinaReranker(apiKey, model string) *JinaReranker {
	return &JinaReranker{
		APIKey: apiKey,
		Model:  model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (r *JinaReranker) ModelID() string { return r.Model }

func (r *JinaReranker) Rerank(ctx context.Context, query string, candidates []Candidate) ([]Candidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	docs := docsForRerank(candidates)

	body, _ := json.Marshal(map[string]any{
		"model":     r.Model,
		"query":     query,
		"documents": docs,
		"top_n":     len(candidates),
	})

	base := "https://api.jina.ai"
	if r.BaseURL != "" {
		base = strings.TrimRight(r.BaseURL, "/")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := apiretry.Do(ctx, r.client, req, 3)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody map[string]any
		json.NewDecoder(resp.Body).Decode(&errBody)
		return nil, fmt.Errorf("jina rerank HTTP %d: %v", resp.StatusCode, errBody)
	}

	var out struct {
		Results []apiRerankResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode jina response: %w", err)
	}

	return applyRerankResults(out.Results, candidates), nil
}
