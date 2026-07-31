package search

import (
	"context"
	"fmt"
	"strconv"

	"github.com/naay99999/neything/internal/embed"
	"github.com/naay99999/neything/internal/rerank"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
)

type EnrichedResult struct {
	ChunkID   int64
	DocPath   string
	DocType   string
	Content   string
	StartPos  int
	EndPos    int
	Score     float32
	Workspace string
}

// Retrieval modes accepted by RetrieveOptions.Mode. An empty Mode is
// treated as ModeAuto.
const (
	ModeAuto     = "auto"
	ModeSemantic = "semantic"
	ModeKeyword  = "keyword"
	ModeHybrid   = "hybrid"
)

type RetrieveOptions struct {
	TopK       int
	FetchK     int
	Workspace  string
	PathPrefix string
	// Mode selects which signal(s) to use: auto (default, both when
	// available, degrades gracefully), semantic (error if unavailable),
	// keyword (FTS only), hybrid (force both, error if semantic
	// unavailable). Empty behaves like auto.
	Mode   string
	Rerank bool
}

// SearchMeta reports what actually happened during a Search call, since
// auto mode can silently fall back to a subset of signals instead of
// failing outright.
type SearchMeta struct {
	SemanticUsed  bool    `json:"semantic_used"`
	KeywordUsed   bool    `json:"keyword_used"`
	EmbedCoverage float64 `json:"embed_coverage"`
	// Degraded is a human-readable note when a requested signal couldn't be
	// used (e.g. embedder down, no vectors yet). Empty when nothing was
	// degraded.
	Degraded string `json:"degraded,omitempty"`
}

// chunkStore is the subset of *store.DB the retriever needs: FTS lookup,
// chunk content, and document metadata. Declared as an interface (rather
// than depending on the concrete *store.DB) so tests can substitute a
// call-counting stub — see TestRetriever_HydratesOnceForOverlappingIDs in
// retriever_test.go, which pins down the fix-1 restructuring below (hydrate
// the fused/deduped ID set exactly once, instead of once per search leg).
// *store.DB satisfies this interface with its existing method set, so
// callers elsewhere in the codebase that build a Retriever with a
// *store.DB value need no changes.
type chunkStore interface {
	SearchFTS(query string, limit int) ([]store.FTSResult, error)
	GetChunksByIDs(ids []int64) ([]*store.Chunk, error)
	GetDocumentsByChunkIDs(chunkIDs []int64) (map[int64]*store.DocWithWorkspace, error)
	CountChunks() (int, error)
}

type Retriever struct {
	DB       chunkStore
	Vectors  vectorstore.VectorStore
	Embedder embed.Embedder
	Reranker rerank.Reranker
}

func (r *Retriever) Search(ctx context.Context, query string, opts RetrieveOptions) ([]EnrichedResult, SearchMeta, error) {
	topK := opts.TopK
	if topK <= 0 {
		topK = 8
	}
	fetchK := opts.FetchK
	if fetchK <= 0 {
		fetchK = topK * 3
	}
	if fetchK < 10 {
		fetchK = 10
	}
	// Workspace/path filters are applied after hydration; overfetch so
	// filtered-out hits don't starve the final result set.
	if opts.Workspace != "" || opts.PathPrefix != "" {
		fetchK *= 4
	}

	mode := opts.Mode
	if mode == "" {
		mode = ModeAuto
	}

	meta := SearchMeta{EmbedCoverage: r.embedCoverage()}

	wantKeyword := mode == ModeAuto || mode == ModeKeyword || mode == ModeHybrid
	wantSemantic := mode == ModeAuto || mode == ModeSemantic || mode == ModeHybrid

	// keywordSearch/semanticSearch return raw (chunk ID, per-leg score)
	// pairs only — content and document metadata are NOT fetched here.
	// Hydration is deferred until after fusion (see hydrate below) so a
	// chunk ID that both legs agree on, or one that gets truncated away by
	// fetchK, is never pulled from SQLite more than once.
	var keyword []EnrichedResult
	if wantKeyword {
		kw, err := r.keywordSearch(ctx, query, fetchK)
		if err != nil {
			// FTS errors never fail the request — record and move on.
			meta.Degraded = appendDegraded(meta.Degraded, fmt.Sprintf("keyword search failed: %v", err))
		} else if len(kw) > 0 {
			keyword = kw
			meta.KeywordUsed = true
		}
	}

	var semantic []EnrichedResult
	if wantSemantic {
		if reason := r.semanticUnavailableReason(); reason != "" {
			if mode == ModeSemantic || mode == ModeHybrid {
				return nil, meta, fmt.Errorf("semantic search unavailable: %s", reason)
			}
			meta.Degraded = appendDegraded(meta.Degraded, reason)
		} else {
			sem, err := r.semanticSearch(ctx, query, fetchK)
			if err != nil {
				if mode == ModeSemantic || mode == ModeHybrid {
					return nil, meta, fmt.Errorf("semantic search failed: %w", err)
				}
				meta.Degraded = appendDegraded(meta.Degraded, fmt.Sprintf("semantic search failed, degraded to keyword-only: %v", err))
			} else if len(sem) > 0 {
				semantic = sem
				meta.SemanticUsed = true
			}
		}
	}

	var fused []EnrichedResult
	switch {
	case meta.SemanticUsed && meta.KeywordUsed:
		fused = ReciprocalRankFusion(semantic, keyword, defaultRRFK)
	case meta.SemanticUsed:
		fused = semantic
	case meta.KeywordUsed:
		fused = keyword
	}

	// Truncate to the candidate budget before hydrating: fetchK already
	// covers what a downstream rerank pass needs (callers size it via
	// config.FetchK, which factors in RerankTopK) and is inflated above
	// when workspace/path filters are active, to absorb filter attrition.
	if len(fused) > fetchK {
		fused = fused[:fetchK]
	}

	// Hydrate content + document metadata ONCE for the deduped, ranked ID
	// set — two SQLite round-trips total for the whole search, regardless
	// of how many legs ran or how much their candidate sets overlapped.
	results, err := r.hydrate(fused, opts.Workspace, opts.PathPrefix)
	if err != nil {
		return nil, meta, fmt.Errorf("hydrate results: %w", err)
	}

	if opts.Rerank && r.Reranker != nil && len(results) > 0 {
		// results is already bounded by fetchK (which itself accounts for
		// RerankTopK via config.FetchK), so no further candidate cap is
		// needed here — content truncation for the wire payload happens
		// inside the reranker implementations (internal/rerank).
		byID := make(map[int64]EnrichedResult, len(results))
		for _, res := range results {
			byID[res.ChunkID] = res
		}

		candidates := make([]rerank.Candidate, len(results))
		for i, res := range results {
			candidates[i] = rerank.Candidate{
				ChunkID: res.ChunkID,
				Content: res.Content,
				Score:   res.Score,
			}
		}
		ranked, err := r.Reranker.Rerank(ctx, query, candidates)
		if err != nil {
			return nil, meta, fmt.Errorf("rerank: %w", err)
		}
		results = make([]EnrichedResult, len(ranked))
		for i, c := range ranked {
			if orig, ok := byID[c.ChunkID]; ok {
				orig.Score = c.Score
				results[i] = orig
			} else {
				results[i] = EnrichedResult{
					ChunkID: c.ChunkID,
					Content: c.Content,
					Score:   c.Score,
				}
			}
		}
	}

	if len(results) > topK {
		results = results[:topK]
	}
	return results, meta, nil
}

// semanticUnavailableReason reports why semantic search can't run right
// now, or "" if it can. It never issues a query while the DB is mid-read
// (Count() below only touches the in-memory vector store).
func (r *Retriever) semanticUnavailableReason() string {
	if r.Embedder == nil {
		return "no embedder configured"
	}
	if r.Vectors == nil || r.Vectors.Count() == 0 {
		return "no vectors indexed yet"
	}
	return ""
}

// embedCoverage returns Vectors.Count() / total chunk count, or 0 when
// either isn't available. Best-effort: DB errors are swallowed since
// coverage is informational, not load-bearing for the search itself.
func (r *Retriever) embedCoverage() float64 {
	if r.Vectors == nil || r.DB == nil {
		return 0
	}
	vecCount := r.Vectors.Count()
	if vecCount == 0 {
		return 0
	}
	chunkCount, err := r.DB.CountChunks()
	if err != nil || chunkCount == 0 {
		return 0
	}
	coverage := float64(vecCount) / float64(chunkCount)
	if coverage > 1 {
		coverage = 1
	}
	return coverage
}

func appendDegraded(existing, msg string) string {
	if existing == "" {
		return msg
	}
	return existing + "; " + msg
}

// semanticSearch runs the vector-store leg and returns raw (chunk ID,
// score) pairs — no content or document metadata is fetched here (see
// hydrate).
func (r *Retriever) semanticSearch(ctx context.Context, query string, fetchK int) ([]EnrichedResult, error) {
	vecs, err := r.Embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	rawResults, err := r.Vectors.Search(ctx, vecs[0], fetchK)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	out := make([]EnrichedResult, 0, len(rawResults))
	for _, res := range rawResults {
		id, err := strconv.ParseInt(res.ID, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, EnrichedResult{ChunkID: id, Score: res.Score})
	}
	return out, nil
}

// keywordSearch runs the FTS leg and returns raw (chunk ID, score) pairs —
// no content or document metadata is fetched here (see hydrate).
func (r *Retriever) keywordSearch(_ context.Context, query string, fetchK int) ([]EnrichedResult, error) {
	ftsResults, err := r.DB.SearchFTS(query, fetchK)
	if err != nil {
		return nil, fmt.Errorf("fts search: %w", err)
	}
	if len(ftsResults) == 0 {
		return nil, nil
	}

	out := make([]EnrichedResult, len(ftsResults))
	for i, fr := range ftsResults {
		out[i] = EnrichedResult{ChunkID: fr.ChunkID, Score: fr.Score}
	}
	return out, nil
}

// hydrate fetches chunk content and document metadata for scored — in
// exactly two SQLite round-trips no matter how many search legs
// contributed IDs or how much they overlapped — then applies
// workspace/path filtering. The input order (typically post-fusion rank)
// is preserved in the output, minus anything filtered out or missing.
func (r *Retriever) hydrate(scored []EnrichedResult, workspaceName, pathPrefix string) ([]EnrichedResult, error) {
	if len(scored) == 0 {
		return nil, nil
	}

	chunkIDs := make([]int64, len(scored))
	for i, s := range scored {
		chunkIDs[i] = s.ChunkID
	}

	docMap, err := r.DB.GetDocumentsByChunkIDs(chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch docs: %w", err)
	}

	chunks, err := r.DB.GetChunksByIDs(chunkIDs)
	if err != nil {
		return nil, fmt.Errorf("fetch chunks: %w", err)
	}

	chunkByID := make(map[int64]*store.Chunk, len(chunks))
	for _, c := range chunks {
		chunkByID[c.ID] = c
	}

	var results []EnrichedResult
	for _, s := range scored {
		c, ok := chunkByID[s.ChunkID]
		if !ok {
			continue
		}
		dw, ok := docMap[s.ChunkID]
		if !ok {
			continue
		}
		if workspaceName != "" && dw.WorkspaceName != workspaceName {
			continue
		}
		if pathPrefix != "" && !hasPathPrefix(dw.Path, pathPrefix) {
			continue
		}
		results = append(results, EnrichedResult{
			ChunkID:   c.ID,
			DocPath:   dw.Path,
			DocType:   dw.Type,
			Content:   c.Content,
			StartPos:  c.StartPos,
			EndPos:    c.EndPos,
			Score:     s.Score,
			Workspace: dw.WorkspaceName,
		})
	}

	return results, nil
}

func hasPathPrefix(path, prefix string) bool {
	return len(path) >= len(prefix) && path[:len(prefix)] == prefix
}
