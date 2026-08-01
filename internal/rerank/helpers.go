package rerank

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// redactURL strips the query string and any userinfo from a request URL so
// it can be safely formatted into an error message. Credentials leak through
// both: Go's http.Client blanks the userinfo *password* in the *url.Error it
// returns, but nothing redacts a `?api-key=…` query param, and we format
// URLs into errors ourselves. Unparseable input degrades to the bare prefix
// before the first '?' rather than risking passing the raw string through.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		if i := strings.IndexByte(raw, '?'); i >= 0 {
			return raw[:i]
		}
		return raw
	}
	u.RawQuery = ""
	u.User = nil
	u.Fragment = ""
	return u.String()
}

// maxRerankContentChars caps how much of a chunk's content is sent to a
// reranker API per candidate. Relevance scoring doesn't need the full
// chunk body, and unbounded payloads cost more (latency, and some
// providers cap request size/tokens) for candidate sets that can run into
// the dozens. This only bounds the outgoing request payload — the
// Candidate returned by Rerank (via applyRerankResults, which maps back
// into the original, untruncated candidates slice) always carries the
// caller's original full content.
const maxRerankContentChars = 2000

// docsForRerank builds the "documents" request payload for a rerank API
// call, truncating each candidate's content so a handful of oversized
// chunks can't blow up the request.
func docsForRerank(candidates []Candidate) []string {
	docs := make([]string, len(candidates))
	for i, c := range candidates {
		docs[i] = truncateContent(c.Content, maxRerankContentChars)
	}
	return docs
}

// truncateContent trims s to at most maxChars bytes without splitting a
// multi-byte UTF-8 rune at the cut point.
func truncateContent(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	b := s[:maxChars]
	for len(b) > 0 {
		r, size := utf8.DecodeLastRuneInString(b)
		if r != utf8.RuneError || size != 1 {
			break
		}
		b = b[:len(b)-1]
	}
	return b
}

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
