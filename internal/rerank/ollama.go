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

type OllamaReranker struct {
	Endpoint string
	Model    string
	client   *http.Client
}

func NewOllamaReranker(endpoint, model string) *OllamaReranker {
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	endpoint = strings.TrimRight(endpoint, "/")
	return &OllamaReranker{
		Endpoint: endpoint,
		Model:    model,
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (r *OllamaReranker) ModelID() string { return r.Model }

func (r *OllamaReranker) Rerank(ctx context.Context, query string, candidates []Candidate) ([]Candidate, error) {
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

	url := r.Endpoint + "/v1/rerank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := apiretry.Do(ctx, r.client, req, 3)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody map[string]any
		json.NewDecoder(resp.Body).Decode(&errBody)
		// redactURL, not url: the endpoint is user-configured and some local
		// gateways/proxies carry credentials in it (`?api-key=…`, userinfo).
		// This error reaches stderr and MCP client logs, so echo only the
		// scheme/host/path needed to identify which endpoint failed.
		return nil, fmt.Errorf("local rerank HTTP %d at %s: %v", resp.StatusCode, redactURL(url), errBody)
	}

	var out struct {
		Results []apiRerankResult `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode local rerank response: %w", err)
	}

	return applyRerankResults(out.Results, candidates), nil
}
