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

type CohereReranker struct {
	APIKey  string
	Model   string
	BaseURL string
	client  *http.Client
}

func NewCohereReranker(apiKey, model string) *CohereReranker {
	return &CohereReranker{
		APIKey: apiKey,
		Model:  model,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

func (r *CohereReranker) ModelID() string { return r.Model }

func (r *CohereReranker) Rerank(ctx context.Context, query string, candidates []Candidate) ([]Candidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	docs := make([]string, len(candidates))
	for i, c := range candidates {
		docs[i] = c.Content
	}

	body, _ := json.Marshal(map[string]any{
		"model":     r.Model,
		"query":     query,
		"documents": docs,
		"top_n":     len(candidates),
	})

	base := "https://api.cohere.com"
	if r.BaseURL != "" {
		base = strings.TrimRight(r.BaseURL, "/")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v2/rerank", bytes.NewReader(body))
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
		return nil, fmt.Errorf("cohere rerank HTTP %d: %v", resp.StatusCode, errBody)
	}

	var out struct {
		Results []apiRerankResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode cohere response: %w", err)
	}

	return applyRerankResults(out.Results, candidates), nil
}
