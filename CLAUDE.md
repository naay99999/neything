# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & run

```bash
go build -o ney ./cmd/ney   # build binary
go vet ./...                 # lint
go test ./...                # run all tests
```

No external runtime dependencies for core indexing — SQLite is bundled via `modernc.org/sqlite` (pure Go, no cgo). Optional OCR uses external `pdftoppm` + `tesseract` when `loaders.ocr.enabled: true`.

## Architecture

Ney is a local-first CLI RAG engine. Data lives in `~/.ney/`: `config.yaml`, `index.db` (SQLite), and vector files (`vectors.bin` for brute-force, `vectors.hnsw` for HNSW backend).

**Request flows:**

```
Index:  Files → Loader → ChunkResolver → Embedder → VectorStore + SQLite + FTS
        (+ optional GitHistoryLoader, OCR fallback in PDFLoader)
Search: Query → Embed → VectorStore + optional FTS → RRF → optional rerank → chunks
Ask:    Search → trim to MaxContextChars → ChatModel → answer + citations
Watch:  fsnotify → debounced IndexPath / RemovePath / PruneMissing
```

**Package map:**

| Package | Role |
|---|---|
| `cmd/ney/` | Cobra commands; thin wrappers that wire internal packages |
| `internal/config/` | Config loading (viper), validation, and **provider factory functions** (`NewEmbedder`, `NewChatModel`, `NewReranker`, `NewVectorStore`) |
| `internal/loader/` | File loaders: `.md`/Obsidian/Notion, `.pdf` (+ OCR), `.docx`, `.html`, `.json`, Confluence `.xml`, Git history |
| `internal/chunk/` | `ChunkStrategy` + `Resolver` (auto per doc type): `character`, `sentence`, `paragraph`, `markdown`, `tokenizer`, `page` |
| `internal/embed/` | `Embedder` interface + OpenAI, Gemini, Ollama, OpenAI-compatible implementations |
| `internal/chat/` | `ChatModel` interface + Claude, OpenAI, Gemini, Ollama implementations |
| `internal/index/` | `Indexer` — hash-based skip, prune missing files, orphan vector cleanup, rename detection |
| `internal/watch/` | File watcher with debounce for `ney watch` |
| `internal/search/` | `Retriever` — hybrid search, RRF fusion, optional rerank |
| `internal/store/` | SQLite wrapper; schema in `schema.go`; FTS5 in `fts.go` |
| `internal/vectorstore/` | `VectorStore` interface; `BruteForceStore` + `HNSWStore` (lazy graph rebuild) |
| `internal/rerank/` | Reranker interface + Cohere, Jina, Ollama/local implementations |

**Key constraints:**

- SQLite runs with `SetMaxOpenConns(1)`. Because of this, `indexDocument` in `pipeline.go` upserts the document row *before* opening the transaction — doing it inside the tx would deadlock since the single connection is already held.
- Do **not** rely on `LastInsertId()` after unrelated statements on the single SQLite connection — always `SELECT` the row id after upsert (see `UpsertWorkspace`, `UpsertDocument`).
- Vector IDs are SQLite chunk row IDs serialized as strings. This is the join key between vector files and `index.db`.
- Re-indexing must call `Vectors.Delete` for old chunk IDs before adding new ones; `Index()` also prunes documents missing from the filesystem.
- Changing the embedding model invalidates the whole index. The indexer checks model consistency at the start of every `Index` call and refuses to proceed on mismatch.
- Claude cannot be used as an embedder (Anthropic has no embedding API). `config.Validate` enforces this.
- Provider factory functions live in `internal/config/config.go`, not in the provider packages themselves.

**Adding a new provider:** implement `embed.Embedder` or `chat.ChatModel`, add a case to `config.NewEmbedder` / `config.NewChatModel`, update `config.Validate`'s allow-lists, and update `ney doctor` / `ney models` output.

**Adding a new loader:** implement `loader.Loader`, register it in `cmd/ney/indexer.go` (order matters for `.md` sniffing), add the extension to `supportedExts` in `internal/index/pipeline.go` if file-based.
