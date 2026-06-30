# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & run

```bash
go build -o ney ./cmd/ney   # build binary
go vet ./...                 # lint
go test ./...                # run all tests
```

No external runtime dependencies — SQLite is bundled via `modernc.org/sqlite` (pure Go, no cgo).

## Architecture

Ney is a local-first CLI RAG engine. Data lives in `~/.ney/`: `config.yaml`, `index.db` (SQLite), and `vectors.bin` (flat binary vector store).

**Request flows:**

```
Index:  Files → Loader → ChunkStrategy → Embedder → VectorStore + SQLite
Search: Query → Embed → VectorStore.Search → fetch chunks from SQLite
Ask:    Search → trim to MaxContextChars → ChatModel → answer + citations
```

**Package map:**

| Package | Role |
|---|---|
| `cmd/ney/` | Cobra commands; thin wrappers that wire internal packages |
| `internal/config/` | Config loading (viper), validation, and **provider factory functions** (`NewEmbedder`, `NewChatModel`) |
| `internal/loader/` | File loaders for `.md`, `.pdf`, `.docx` via `Registry.Dispatch` |
| `internal/chunk/` | `ChunkStrategy` implementations: `character`, `sentence`, `paragraph`, `markdown` |
| `internal/embed/` | `Embedder` interface + OpenAI, Gemini, Ollama, OpenAI-compatible implementations |
| `internal/chat/` | `ChatModel` interface + Claude, OpenAI, Gemini, Ollama implementations |
| `internal/index/` | `Indexer` — hash-based skip, batch embed, SQLite+vector upsert pipeline |
| `internal/search/` | `Retriever` — embeds query, calls VectorStore, hydrates chunks from SQLite |
| `internal/store/` | SQLite wrapper; schema in `schema.go` |
| `internal/vectorstore/` | `BruteForceStore` — cosine similarity over in-memory float32 vectors, persisted as a flat binary file |
| `internal/rerank/` | Interface only; not yet implemented |

**Key constraints:**

- SQLite runs with `SetMaxOpenConns(1)`. Because of this, `indexDocument` in `pipeline.go` upserts the document row *before* opening the transaction — doing it inside the tx would deadlock since the single connection is already held.
- Vector IDs are SQLite chunk row IDs serialized as strings. This is the join key between `vectors.bin` and `index.db`.
- Changing the embedding model invalidates the whole index. The indexer checks model consistency at the start of every `Index` call and refuses to proceed on mismatch.
- Claude cannot be used as an embedder (Anthropic has no embedding API). `config.Validate` enforces this.
- Provider factory functions live in `internal/config/config.go`, not in the provider packages themselves.

**Adding a new provider:** implement `embed.Embedder` or `chat.ChatModel`, add a case to `config.NewEmbedder` / `config.NewChatModel`, update `config.Validate`'s allow-lists, and update `ney doctor` / `ney models` output.

**Adding a new loader:** implement `loader.Loader`, register it in `loader/registry.go`, add the extension to `supportedExts` in `internal/index/pipeline.go`.
