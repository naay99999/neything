package rerank

type apiRerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

func applyRerankResults(results []apiRerankResult, candidates []Candidate) []Candidate {
	ranked := make([]Candidate, 0, len(results))
	for _, res := range results {
		if res.Index < 0 || res.Index >= len(candidates) {
			continue
		}
		c := candidates[res.Index]
		c.Score = float32(res.RelevanceScore)
		ranked = append(ranked, c)
	}
	return ranked
}
