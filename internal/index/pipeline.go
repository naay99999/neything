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

	"github.com/naay/ney/internal/chunk"
	"github.com/naay/ney/internal/embed"
	"github.com/naay/ney/internal/loader"
	"github.com/naay/ney/internal/store"
	"github.com/naay/ney/internal/vectorstore"
)

type Stats struct {
	FilesScanned  int
	FilesSkipped  int
	ChunksCreated int
	Duration      time.Duration
}

type Indexer struct {
	DB         *store.DB
	Vectors    vectorstore.VectorStore
	Embedder   embed.Embedder
	Loaders    loader.Registry
	Chunker    chunk.ChunkStrategy
	BatchSize  int
	OnProgress func(file string, chunks int)
}

var supportedExts = map[string]bool{
	".md":       true,
	".markdown": true,
	".pdf":      true,
	".docx":     true,
}

func (ix *Indexer) Index(ctx context.Context, rootPath, workspaceName string) (*Stats, error) {
	start := time.Now()

	// validate embedder consistency before touching data
	if err := ix.checkEmbedderConsistency(); err != nil {
		return nil, err
	}

	workspaceID, err := ix.DB.UpsertWorkspace(workspaceName, rootPath)
	if err != nil {
		return nil, fmt.Errorf("upsert workspace: %w", err)
	}

	stats := &Stats{}

	err = filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
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

		// hash-based skip
		fileData, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		hash := fmt.Sprintf("%x", sha256.Sum256(fileData))

		existing, err := ix.DB.GetDocumentByPath(path)
		if err != nil {
			return fmt.Errorf("get doc: %w", err)
		}
		if existing != nil && existing.Hash == hash {
			stats.FilesSkipped++
			return nil
		}

		// load document
		ld, ok := ix.Loaders.Dispatch(path)
		if !ok {
			return nil
		}
		docs, err := ld.Load(ctx, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skip %s: %v\n", path, err)
			return nil
		}

		for _, doc := range docs {
			if err := ix.indexDocument(ctx, doc, workspaceID, hash, len(fileData), stats); err != nil {
				fmt.Fprintf(os.Stderr, "warning: index %s: %v\n", path, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// record active embedder
	ix.DB.SetActiveEmbedder(ix.Embedder.ModelID(), ix.Embedder.ModelID(), ix.Embedder.Dimensions())
	ix.DB.SetMeta("last_indexed_at", time.Now().Format(time.RFC3339))

	stats.Duration = time.Since(start)
	return stats, nil
}

func (ix *Indexer) indexDocument(ctx context.Context, doc loader.Document, workspaceID int64, hash string, sizeBytes int, stats *Stats) error {
	batchSize := ix.BatchSize
	if batchSize <= 0 {
		batchSize = 32
	}

	// upsert document record
	sd := &store.Document{
		WorkspaceID: workspaceID,
		Path:        doc.Path,
		Type:        doc.Type,
		Hash:        hash,
		SizeBytes:   int64(sizeBytes),
	}
	tx, err := ix.DB.Begin()
	if err != nil {
		return err
	}

	docID, err := ix.DB.UpsertDocument(sd)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("upsert doc: %w", err)
	}

	// clear stale chunks
	if err := ix.DB.DeleteChunksByDocument(tx, docID); err != nil {
		tx.Rollback()
		return fmt.Errorf("delete old chunks: %w", err)
	}

	// chunk the document
	rawChunks := ix.Chunker.Chunk(doc)
	if len(rawChunks) == 0 {
		tx.Rollback()
		return nil
	}

	// embed in batches
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
			if j < len(vecs) {
				// will set vector item after insert to get ID
				_ = vecs[j]
			}
		}

		// insert chunks to get their IDs
		batchStoreChunks := storeChunks[len(storeChunks)-len(batch):]
		if err := ix.DB.InsertChunks(tx, batchStoreChunks); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert chunks: %w", err)
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

	// add to vector store (outside SQL tx)
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
		return nil // no prior embedder recorded
	}
	// parse stored value: {"name":"x","model":"y","dimensions":N}
	stored := active.Name
	if stored == "" {
		return nil
	}
	// extract model from stored JSON-like string
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
