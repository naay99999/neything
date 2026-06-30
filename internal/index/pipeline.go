package index

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/chunk"
	"github.com/naay99999/neything/internal/embed"
	"github.com/naay99999/neything/internal/loader"
	"github.com/naay99999/neything/internal/store"
	"github.com/naay99999/neything/internal/vectorstore"
)

type Stats struct {
	FilesScanned  int
	FilesSkipped  int
	FilesRemoved  int
	ChunksCreated int
	VectorsPruned int
	Duration      time.Duration
}

type Indexer struct {
	DB            *store.DB
	Vectors       vectorstore.VectorStore
	Embedder      embed.Embedder
	Loaders       loader.Registry
	GitHistory    *loader.GitHistoryLoader
	ChunkResolver *chunk.Resolver
	BatchSize     int
	OnProgress    func(file string, chunks int)
}

var supportedExts = map[string]bool{
	".md":       true,
	".markdown": true,
	".pdf":      true,
	".docx":     true,
	".html":     true,
	".htm":      true,
	".json":     true,
	".xml":      true,
}

func (ix *Indexer) Index(ctx context.Context, rootPath, workspaceName string) (*Stats, error) {
	start := time.Now()

	if err := ix.checkEmbedderConsistency(); err != nil {
		return nil, err
	}

	workspaceID, err := ix.DB.UpsertWorkspace(workspaceName, rootPath)
	if err != nil {
		return nil, fmt.Errorf("upsert workspace: %w", err)
	}

	stats := &Stats{}
	seenPaths := make(map[string]bool)
	pathToHash := make(map[string]string)

	err = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !supportedExts[ext] {
			return nil
		}
		stats.FilesScanned++
		seenPaths[path] = true

		fileData, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(fileData))
		pathToHash[path] = hash

		if err := ix.indexFileIfNeeded(ctx, path, workspaceID, hash, len(fileData), stats); err != nil {
			fmt.Fprintf(os.Stderr, "warning: index %s: %v\n", path, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := ix.indexGitHistory(ctx, rootPath, workspaceID, seenPaths, pathToHash, stats); err != nil {
		fmt.Fprintf(os.Stderr, "warning: git history: %v\n", err)
	}

	if err := ix.pruneMissing(ctx, workspaceID, seenPaths, pathToHash, stats); err != nil {
		return nil, err
	}

	ix.DB.SetActiveEmbedder(ix.Embedder.ModelID(), ix.Embedder.ModelID(), ix.Embedder.Dimensions())
	ix.DB.SetMeta("last_indexed_at", time.Now().Format(time.RFC3339))

	stats.Duration = time.Since(start)
	return stats, nil
}

// IndexPath indexes a single file. workspaceName is used only when creating a new workspace.
func (ix *Indexer) IndexPath(ctx context.Context, path string, workspaceID int64, workspaceName string) (*Stats, error) {
	if err := ix.checkEmbedderConsistency(); err != nil {
		return nil, err
	}

	ext := strings.ToLower(filepath.Ext(path))
	if !supportedExts[ext] {
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}

	fileData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(fileData))

	stats := &Stats{FilesScanned: 1}
	if err := ix.indexFileIfNeeded(ctx, path, workspaceID, hash, len(fileData), stats); err != nil {
		return stats, err
	}

	ix.DB.SetActiveEmbedder(ix.Embedder.ModelID(), ix.Embedder.ModelID(), ix.Embedder.Dimensions())
	ix.DB.SetMeta("last_indexed_at", time.Now().Format(time.RFC3339))
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
	pathToHash := make(map[string]string)

	err := filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !supportedExts[ext] {
			return nil
		}
		seenPaths[path] = true
		fileData, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		pathToHash[path] = fmt.Sprintf("%x", sha256.Sum256(fileData))
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := ix.pruneMissing(ctx, workspaceID, seenPaths, pathToHash, stats); err != nil {
		return nil, err
	}
	if stats.FilesRemoved > 0 {
		ix.DB.SetMeta("last_indexed_at", time.Now().Format(time.RFC3339))
	}
	return stats, nil
}

func (ix *Indexer) indexGitHistory(ctx context.Context, rootPath string, workspaceID int64, seenPaths map[string]bool, pathToHash map[string]string, stats *Stats) error {
	if ix.GitHistory == nil || ix.GitHistory.RecentCommits <= 0 {
		return nil
	}
	docs, err := ix.GitHistory.LoadRepo(ctx, rootPath)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		seenPaths[doc.Path] = true
		pathToHash[doc.Path] = doc.Hash

		existing, err := ix.DB.GetDocumentByPath(doc.Path)
		if err != nil {
			return err
		}
		if existing != nil && existing.Hash == doc.Hash {
			stats.FilesSkipped++
			continue
		}
		if err := ix.indexDocument(ctx, doc, workspaceID, doc.Hash, len(doc.Content), stats); err != nil {
			return err
		}
	}
	return nil
}

func (ix *Indexer) indexFileIfNeeded(ctx context.Context, path string, workspaceID int64, hash string, sizeBytes int, stats *Stats) error {
	existing, err := ix.DB.GetDocumentByPath(path)
	if err != nil {
		return fmt.Errorf("get doc: %w", err)
	}
	if existing != nil && existing.Hash == hash {
		stats.FilesSkipped++
		return nil
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
	docs, err := ld.Load(ctx, path)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if err := ix.indexDocument(ctx, doc, workspaceID, hash, sizeBytes, stats); err != nil {
			return err
		}
	}
	return nil
}

func (ix *Indexer) pruneMissing(ctx context.Context, workspaceID int64, seenPaths map[string]bool, pathToHash map[string]string, stats *Stats) error {
	docs, err := ix.DB.GetDocumentsByWorkspace(workspaceID)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if seenPaths[doc.Path] {
			continue
		}

		var renamePath string
		matchCount := 0
		for p, h := range pathToHash {
			if h != doc.Hash {
				continue
			}
			existing, err := ix.DB.GetDocumentByPath(p)
			if err != nil {
				return err
			}
			if existing != nil && existing.ID != doc.ID {
				continue
			}
			renamePath = p
			matchCount++
		}
		if matchCount == 1 {
			if err := ix.DB.UpdateDocumentPath(doc.ID, renamePath); err != nil {
				return err
			}
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

func (ix *Indexer) indexDocument(ctx context.Context, doc loader.Document, workspaceID int64, hash string, sizeBytes int, stats *Stats) error {
	batchSize := ix.BatchSize
	if batchSize <= 0 {
		batchSize = 32
	}

	sd := &store.Document{
		WorkspaceID: workspaceID,
		Path:        doc.Path,
		Type:        doc.Type,
		Hash:        hash,
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
		return nil
	}

	var storeChunks []*store.Chunk
	var vectorItems []vectorstore.VectorItem

	for i := 0; i < len(rawChunks); i += batchSize {
		end := i + batchSize
		if end > len(rawChunks) {
			end = len(rawChunks)
		}
		batch := rawChunks[i:end]

		texts := make([]string, len(batch))
		for j, c := range batch {
			texts[j] = c.Content
		}

		vecs, err := ix.Embedder.Embed(ctx, texts)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("embed batch: %w", err)
		}

		for j, c := range batch {
			sc := &store.Chunk{
				DocumentID: docID,
				ChunkIndex: c.Index,
				Content:    c.Content,
				StartPos:   c.StartPos,
				EndPos:     c.EndPos,
			}
			storeChunks = append(storeChunks, sc)
			_ = vecs[j]
		}

		batchStoreChunks := storeChunks[len(storeChunks)-len(batch):]
		if err := ix.DB.InsertChunks(tx, batchStoreChunks); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert chunks: %w", err)
		}

		for _, sc := range batchStoreChunks {
			if err := ix.DB.UpsertChunkFTS(tx, sc.ID, sc.Content); err != nil {
				tx.Rollback()
				return fmt.Errorf("insert fts: %w", err)
			}
		}

		for j, sc := range batchStoreChunks {
			if j < len(vecs) {
				vectorItems = append(vectorItems, vectorstore.VectorItem{
					ID:     strconv.FormatInt(sc.ID, 10),
					Vector: vecs[j],
				})
			}
		}
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

	if len(vectorItems) > 0 {
		if err := ix.Vectors.Add(ctx, vectorItems); err != nil {
			return fmt.Errorf("add vectors: %w", err)
		}
	}

	stats.ChunksCreated += len(storeChunks)
	if ix.OnProgress != nil {
		ix.OnProgress(doc.Path, len(storeChunks))
	}
	return nil
}

func (ix *Indexer) checkEmbedderConsistency() error {
	active, err := ix.DB.GetActiveEmbedder()
	if err != nil || active == nil {
		return nil
	}
	stored := active.Name
	if stored == "" {
		return nil
	}
	var storedModel string
	if i := strings.Index(stored, `"model":"`); i >= 0 {
		rest := stored[i+9:]
		if j := strings.Index(rest, `"`); j >= 0 {
			storedModel = rest[:j]
		}
	}
	if storedModel == "" {
		return nil
	}
	if storedModel != ix.Embedder.ModelID() {
		return fmt.Errorf(
			"embedder mismatch: index was built with model %q, current config uses %q\n"+
				"Run: ney reset && ney index <path>",
			storedModel, ix.Embedder.ModelID(),
		)
	}
	return nil
}

func IsSupportedExt(path string) bool {
	return supportedExts[strings.ToLower(filepath.Ext(path))]
}
