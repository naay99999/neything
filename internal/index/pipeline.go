package index

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/chunk"
	"github.com/naay99999/neything/internal/embed"
	"github.com/naay99999/neything/internal/loader"
	"github.com/naay99999/neything/internal/pathfilter"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
)

// maxIndexFileSize caps how large a file Index/IndexPath will read into
// memory before hashing/chunking it — matches read_document's cap
// (maxReadFileSize in cmd/ney/mcp_tools.go). Oversized files are skipped
// with a stderr warning; stdout is reserved for the MCP protocol, so
// diagnostics from any path runMCP can reach must never write there.
const maxIndexFileSize = 20 * 1024 * 1024

type Stats struct {
	FilesScanned  int
	FilesSkipped  int
	FilesRemoved  int
	FilesFailed   int
	ChunksCreated int
	VectorsPruned int
	// ChunksPendingEmbed is the number of chunks written by this run that
	// have no vector yet. Phase A (this file) never embeds — that's
	// EmbedWorker's job now — so every chunk this run created is
	// definitionally pending, no extra diff query needed.
	ChunksPendingEmbed int
	Duration           time.Duration
}

// flushEveryDocs bounds how much indexed work a crash can lose: vectors are
// held in memory between flushes (SQLite commits per document regardless).
const flushEveryDocs = 100

type Indexer struct {
	DB       *store.DB
	Vectors  vectorstore.VectorStore
	Embedder embed.Embedder
	Loaders  loader.Registry
	// Filter decides which files/dirs are excluded (dotfiles + secret-file
	// patterns + user config). nil is valid and applies the built-in rules
	// only — see pathfilter.
	Filter        *pathfilter.Filter
	ChunkResolver *chunk.Resolver
	// BatchSize is unused by indexDocument as of the prepared-statement-per-
	// document fix (chunks/FTS rows for a document are now inserted in one
	// call each, not sub-batched) — kept only so existing callers that set
	// it don't need updating.
	BatchSize  int
	OnProgress func(file string, chunks int)
}

var supportedExts = map[string]bool{
	".md":       true,
	".markdown": true,
	".txt":      true,
}

// walkIndexable walks root, applying dir/file exclusion (ix.Filter, nil-safe:
// dotfiles + built-in secret patterns always apply) and the supported-
// extension check, calling fn for each indexable file. Shared by Index and
// PruneMissing so their views of "what exists" can never diverge — a file the
// walk excludes here is also invisible to prune's seen-set, which means
// previously indexed files that later match a deny pattern get pruned
// automatically on the next run.
func (ix *Indexer) walkIndexable(root string, fn func(path string, d fs.DirEntry) error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && ix.Filter.ExcludedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if ix.Filter.ExcludedFile(d.Name()) {
			return nil
		}
		if !supportedExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		return fn(path, d)
	})
}

// Index walks rootPath and performs Phase A only: parse → chunk → write
// chunk rows + FTS rows. It never calls Embedder.Embed or Vectors.Add — that
// is EmbedWorker's job (internal/index/embedworker.go), run separately so a
// slow/unreachable embedder can't hold the single SQLite connection or block
// indexing. Embedder may be nil (FTS-only, tier 0-1 mode); when set, it is
// used only for model-consistency bookkeeping elsewhere (EmbedWorker), never
// here.
//
// Model-consistency checking (comparing the configured embedder against the
// one the existing index was built with) also moved to EmbedWorker: since
// this pipeline no longer writes vectors, chunk+FTS writing is
// embedder-neutral and has nothing to be inconsistent about.
func (ix *Indexer) Index(ctx context.Context, rootPath, workspaceName string) (_ *Stats, err error) {
	start := time.Now()

	workspaceID, err := ix.DB.UpsertWorkspace(workspaceName, rootPath)
	if err != nil {
		return nil, fmt.Errorf("upsert workspace: %w", err)
	}

	// Flush even on error so documents already committed to SQLite keep a
	// consistent vector store when a run aborts partway through. Phase A
	// only ever calls Vectors.Delete (for chunks that were re-chunked or
	// removed) — never Add — but Delete still mutates in-memory state that
	// needs persisting.
	defer func() {
		if ferr := ix.Vectors.Flush(); ferr != nil && err == nil {
			err = fmt.Errorf("flush vectors: %w", ferr)
		}
	}()

	stats := &Stats{}
	seenPaths := make(map[string]bool)
	pathToHash := make(map[string]string)
	docsSinceFlush := 0

	err = ix.walkIndexable(rootPath, func(path string, d fs.DirEntry) error {
		stats.FilesScanned++
		seenPaths[path] = true

		if info, ierr := d.Info(); ierr == nil && info.Size() > maxIndexFileSize {
			stats.FilesSkipped++
			fmt.Fprintf(os.Stderr, "warning: skip %s: file too large (%d bytes, max %d)\n", path, info.Size(), maxIndexFileSize)
			return nil
		}

		fileData, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(fileData)
		hash := hex.EncodeToString(sum[:])
		pathToHash[path] = hash

		if err := ix.indexFileIfNeeded(ctx, path, workspaceID, fileData, hash, stats); err != nil {
			if ctx.Err() != nil {
				return err
			}
			stats.FilesFailed++
			fmt.Fprintf(os.Stderr, "warning: index %s: %v\n", path, err)
			return nil
		}
		docsSinceFlush++
		if docsSinceFlush >= flushEveryDocs {
			docsSinceFlush = 0
			if err := ix.Vectors.Flush(); err != nil {
				return fmt.Errorf("flush vectors: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	hashFor := func(path string) string { return pathToHash[path] }
	if err := ix.pruneMissing(ctx, workspaceID, seenPaths, hashFor, stats); err != nil {
		return nil, err
	}

	ix.DB.SetMeta("last_indexed_at", time.Now().Format(time.RFC3339))

	stats.ChunksPendingEmbed = stats.ChunksCreated
	stats.Duration = time.Since(start)
	return stats, nil
}

// IndexPath indexes a single file (Phase A only — see Index). workspaceName
// is used only when creating a new workspace.
func (ix *Indexer) IndexPath(ctx context.Context, path string, workspaceID int64, workspaceName string) (*Stats, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if !supportedExts[ext] {
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
	// Excluded files (dotfiles, secret patterns, user config) are a silent
	// skip, not an error — the watcher fires on every save of e.g. prod.env
	// and shouldn't warn each time.
	if ix.Filter.ExcludedFile(filepath.Base(path)) {
		return &Stats{FilesScanned: 1, FilesSkipped: 1}, nil
	}

	stats := &Stats{FilesScanned: 1}
	if info, ierr := os.Stat(path); ierr == nil && info.Size() > maxIndexFileSize {
		stats.FilesSkipped++
		fmt.Fprintf(os.Stderr, "warning: skip %s: file too large (%d bytes, max %d)\n", path, info.Size(), maxIndexFileSize)
		return stats, nil
	}

	fileData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(fileData)
	hash := hex.EncodeToString(sum[:])

	if err := ix.indexFileIfNeeded(ctx, path, workspaceID, fileData, hash, stats); err != nil {
		return stats, err
	}

	if stats.FilesSkipped == 0 {
		if err := ix.Vectors.Flush(); err != nil {
			return stats, fmt.Errorf("flush vectors: %w", err)
		}
		ix.DB.SetMeta("last_indexed_at", time.Now().Format(time.RFC3339))
	}
	stats.ChunksPendingEmbed = stats.ChunksCreated
	_ = workspaceName
	return stats, nil
}

// RemovePath deletes a document and its vectors by file path.
func (ix *Indexer) RemovePath(ctx context.Context, path string) (*Stats, error) {
	stats := &Stats{}
	doc, err := ix.DB.GetDocumentByPath(path)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return stats, nil
	}
	chunkIDs, err := ix.DB.DeleteDocumentWithCleanup(doc.ID)
	if err != nil {
		return nil, err
	}
	if len(chunkIDs) > 0 {
		if err := ix.Vectors.Delete(ctx, store.Int64SliceToStrings(chunkIDs)); err != nil {
			return nil, fmt.Errorf("delete vectors: %w", err)
		}
		if err := ix.Vectors.Flush(); err != nil {
			return nil, fmt.Errorf("flush vectors: %w", err)
		}
		stats.VectorsPruned += len(chunkIDs)
	}
	stats.FilesRemoved++
	ix.DB.SetMeta("last_indexed_at", time.Now().Format(time.RFC3339))
	return stats, nil
}

// PruneMissing removes indexed documents whose files no longer exist under rootPath.
func (ix *Indexer) PruneMissing(ctx context.Context, rootPath string, workspaceID int64) (*Stats, error) {
	stats := &Stats{}
	seenPaths := make(map[string]bool)

	err := ix.walkIndexable(rootPath, func(path string, d fs.DirEntry) error {
		seenPaths[path] = true
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Hash lazily: pruneMissing only asks for rename candidates, and only
	// when documents actually went missing — the common no-op sync (e.g.
	// the watcher's periodic prune) never reads file contents at all. Also
	// respects maxIndexFileSize so a huge untracked file can't be read
	// fully into memory just to check whether it's a rename.
	hashFor := func(path string) string {
		if info, err := os.Stat(path); err != nil || info.Size() > maxIndexFileSize {
			return ""
		}
		fileData, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		sum := sha256.Sum256(fileData)
		return hex.EncodeToString(sum[:])
	}

	if err := ix.pruneMissing(ctx, workspaceID, seenPaths, hashFor, stats); err != nil {
		return nil, err
	}
	if stats.VectorsPruned > 0 {
		if err := ix.Vectors.Flush(); err != nil {
			return nil, fmt.Errorf("flush vectors: %w", err)
		}
	}
	if stats.FilesRemoved > 0 {
		ix.DB.SetMeta("last_indexed_at", time.Now().Format(time.RFC3339))
	}
	return stats, nil
}

func (ix *Indexer) indexFileIfNeeded(ctx context.Context, path string, workspaceID int64, data []byte, hash string, stats *Stats) error {
	existing, err := ix.DB.GetDocumentByPath(path)
	if err != nil {
		return fmt.Errorf("get doc: %w", err)
	}
	if existing != nil && existing.Hash == hash {
		// Only trust the hash if chunks actually exist — heals indexes
		// where an earlier failed run left a hashed but chunk-less row.
		// EXISTS-style check instead of materializing every chunk ID just
		// to test len(ids) > 0.
		exists, cErr := ix.DB.ChunkExistsForDocument(existing.ID)
		if cErr == nil && exists {
			stats.FilesSkipped++
			return nil
		}
	}

	if existing == nil {
		byHash, err := ix.DB.GetDocumentByHashInWorkspace(workspaceID, hash)
		if err != nil {
			return err
		}
		if byHash != nil && byHash.Path != path {
			if err := ix.DB.UpdateDocumentPath(byHash.ID, path); err != nil {
				return err
			}
			stats.FilesSkipped++
			return nil
		}
	}

	ld, ok := ix.Loaders.Dispatch(path)
	if !ok {
		return nil
	}
	docs, err := ld.Load(ctx, path, data, hash)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if err := ix.indexDocument(ctx, doc, workspaceID, hash, len(data), stats); err != nil {
			return err
		}
	}
	return nil
}

// pruneMissing deletes documents whose files are gone, first checking for
// renames by content hash. hashFor returns a file's content hash ("" if
// unknown); it is only called for rename candidates, and only when some
// document is actually missing.
func (ix *Indexer) pruneMissing(ctx context.Context, workspaceID int64, seenPaths map[string]bool, hashFor func(path string) string, stats *Stats) error {
	docs, err := ix.DB.GetDocumentsByWorkspace(workspaceID)
	if err != nil {
		return err
	}

	docPaths := make(map[string]bool, len(docs))
	for _, doc := range docs {
		docPaths[doc.Path] = true
	}
	var missing []*store.Document
	for _, doc := range docs {
		if !seenPaths[doc.Path] {
			missing = append(missing, doc)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	// Rename candidates are files without a document row of their own in
	// this workspace; only those need hashing. Further narrow to files
	// whose size matches some missing document's stored size_bytes — a
	// content match is impossible otherwise, so this skips hashing (and,
	// for PruneMissing's lazy hashFor, reading) most of the tree instead of
	// hashing every untracked file to find renames among a handful.
	wantedSizes := make(map[int64]bool, len(missing))
	for _, doc := range missing {
		wantedSizes[doc.SizeBytes] = true
	}

	hashToPaths := make(map[string][]string)
	for p := range seenPaths {
		if docPaths[p] {
			continue
		}
		if info, err := os.Stat(p); err != nil || !wantedSizes[info.Size()] {
			continue
		}
		if h := hashFor(p); h != "" {
			hashToPaths[h] = append(hashToPaths[h], p)
		}
	}

	// Batch-fetch document rows for every candidate path that might be
	// consulted below, instead of one GetDocumentByPath query per
	// (missing-document, candidate) pair.
	var candidatePaths []string
	for _, doc := range missing {
		candidatePaths = append(candidatePaths, hashToPaths[doc.Hash]...)
	}
	existingByPath, err := ix.DB.GetDocumentsByPaths(candidatePaths)
	if err != nil {
		return err
	}

	claimed := make(map[string]bool)
	for _, doc := range missing {
		var renamePath string
		matchCount := 0
		for _, p := range hashToPaths[doc.Hash] {
			if claimed[p] {
				continue
			}
			// A candidate may still belong to a document in another
			// workspace (paths are globally unique).
			if existing := existingByPath[p]; existing != nil && existing.ID != doc.ID {
				continue
			}
			renamePath = p
			matchCount++
		}
		if matchCount == 1 {
			if err := ix.DB.UpdateDocumentPath(doc.ID, renamePath); err != nil {
				return err
			}
			claimed[renamePath] = true
			continue
		}

		chunkIDs, err := ix.DB.DeleteDocumentWithCleanup(doc.ID)
		if err != nil {
			return err
		}
		if len(chunkIDs) > 0 {
			if err := ix.Vectors.Delete(ctx, store.Int64SliceToStrings(chunkIDs)); err != nil {
				return fmt.Errorf("delete vectors: %w", err)
			}
			stats.VectorsPruned += len(chunkIDs)
		}
		stats.FilesRemoved++
	}
	return nil
}

// indexDocument writes one loaded document's chunks. This is Phase A only:
// upsert doc row → tx { delete old chunks+FTS → insert chunks → insert FTS }
// → commit → Vectors.Delete(oldIDs) → record hash. It never calls
// Embedder.Embed or Vectors.Add — embedding is EmbedWorker's job, run
// entirely outside this transaction (and outside this pipeline) so a slow or
// unreachable embedder never holds the single SQLite connection.
func (ix *Indexer) indexDocument(ctx context.Context, doc loader.Document, workspaceID int64, hash string, sizeBytes int, stats *Stats) error {
	// The row is upserted without its content hash: the hash is only recorded
	// after chunks are committed. Otherwise a failed run would leave a
	// fresh-looking document that every later run skips as "unchanged".
	// (Upserting must stay outside the tx — the single SQLite connection is
	// already held once the tx opens.)
	sd := &store.Document{
		WorkspaceID: workspaceID,
		Path:        doc.Path,
		Type:        doc.Type,
		Hash:        "",
		SizeBytes:   int64(sizeBytes),
	}
	docID, err := ix.DB.UpsertDocument(sd)
	if err != nil {
		return fmt.Errorf("upsert doc: %w", err)
	}

	tx, err := ix.DB.Begin()
	if err != nil {
		return err
	}

	oldIDs, err := ix.DB.DeleteChunksByDocument(tx, docID)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("delete old chunks: %w", err)
	}

	rawChunks := ix.ChunkResolver.For(doc).Chunk(doc)
	if len(rawChunks) == 0 {
		tx.Rollback()
		if len(oldIDs) > 0 {
			if err := ix.Vectors.Delete(ctx, store.Int64SliceToStrings(oldIDs)); err != nil {
				return fmt.Errorf("delete old vectors: %w", err)
			}
			stats.VectorsPruned += len(oldIDs)
		}
		return ix.DB.UpdateDocumentHash(docID, hash)
	}

	storeChunks := make([]*store.Chunk, len(rawChunks))
	for i, c := range rawChunks {
		storeChunks[i] = &store.Chunk{
			DocumentID: docID,
			ChunkIndex: c.Index,
			Content:    c.Content,
			StartPos:   c.StartPos,
			EndPos:     c.EndPos,
		}
	}

	// One prepared statement each for the whole document's chunks (instead
	// of re-preparing per BatchSize-sized sub-batch): InsertChunks already
	// prepares once per call, so calling it once here — rather than in a
	// loop over sub-batches — is enough to satisfy that. UpsertChunksFTS
	// does the same for the FTS side.
	if err := ix.DB.InsertChunks(tx, storeChunks); err != nil {
		tx.Rollback()
		return fmt.Errorf("insert chunks: %w", err)
	}
	if err := ix.DB.UpsertChunksFTS(tx, storeChunks); err != nil {
		tx.Rollback()
		return fmt.Errorf("insert fts: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	if len(oldIDs) > 0 {
		if err := ix.Vectors.Delete(ctx, store.Int64SliceToStrings(oldIDs)); err != nil {
			return fmt.Errorf("delete old vectors: %w", err)
		}
		stats.VectorsPruned += len(oldIDs)
	}

	if err := ix.DB.UpdateDocumentHash(docID, hash); err != nil {
		return fmt.Errorf("record hash: %w", err)
	}

	stats.ChunksCreated += len(storeChunks)
	if ix.OnProgress != nil {
		ix.OnProgress(doc.Path, len(storeChunks))
	}
	return nil
}

func IsSupportedExt(path string) bool {
	return supportedExts[strings.ToLower(filepath.Ext(path))]
}
