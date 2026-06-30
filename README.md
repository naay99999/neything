# Ney

Local-first AI knowledge engine. Point it at a folder, search by meaning, ask questions — data stays on your machine.

```
ney index ~/docs
ney search "how does billing work"
ney ask "what are the retry policies for failed payments?"
```

---

## Install

```bash
# Build from source (requires Go 1.22+)
go install github.com/naay/ney/cmd/ney@latest

# Or clone and build
git clone https://github.com/naay99999/neything
cd neything
go build -o ney ./cmd/ney
```

Single binary, no runtime dependencies.

---

## Quick start

**1. Set up a provider**

Ney needs an embedder (to create vectors) and a chat model (to answer questions). The fastest local setup uses Ollama for both:

```bash
ollama pull bge-m3          # embedder
ollama pull llama3.2        # chat
```

Or use cloud providers — set API keys as environment variables:

```bash
export ANTHROPIC_API_KEY=sk-...   # for Claude chat
export OPENAI_API_KEY=sk-...      # for OpenAI embed + chat
export GEMINI_API_KEY=...         # for Gemini embed + chat
```

**2. Configure**

On first run, Ney creates `~/.ney/config.yaml` with defaults. Edit it to match your setup:

```yaml
embedder:
  provider: ollama      # openai | gemini | ollama  (never claude)
  model: bge-m3

chat:
  provider: claude      # claude | openai | gemini | ollama
  model: claude-sonnet-4-6
```

**3. Check everything is ready**

```bash
ney doctor
```

**4. Index and search**

```bash
ney index ~/my-notes
ney search "database migration strategy"
ney ask "how do I roll back a failed deploy?"
```

---

## Commands

| Command | Description |
|---|---|
| `ney index <path>` | Index files recursively (`.md`, `.pdf`, `.docx`) |
| `ney search "<query>"` | Semantic search — returns ranked chunks with snippets |
| `ney ask "<question>"` | RAG: retrieve → LLM → answer with source citations |
| `ney status` | Index stats: files, chunks, DB size, last indexed |
| `ney config show` | Print current config |
| `ney config edit` | Open config in `$EDITOR` |
| `ney doctor` | Check config, API keys, Ollama, SQLite, index health |
| `ney models` | List configured providers and Ollama installed models |
| `ney reset` | Clear the index (add `--workspace <name>` for partial reset) |
| `ney version` | Print version |

### Flags (all commands)

| Flag | Description |
|---|---|
| `--workspace <name>` | Target a specific workspace |
| `--top-k <n>` | Number of chunks to retrieve (default: 8) |
| `--json` | Machine-readable JSON output |

### Workspaces

Each `ney index` creates a named workspace (defaults to the folder name). Use `--workspace` to scope searches:

```bash
ney index ~/work/code   --workspace code
ney index ~/docs/notes  --workspace notes

ney search "auth flow" --workspace code
ney ask "sprint goals" --workspace notes
```

---

## Providers

| Provider | Embed | Chat | Notes |
|---|:---:|:---:|---|
| **Ollama** | ✓ | ✓ | Local, offline, no API key |
| **OpenAI** | ✓ | ✓ | `text-embedding-3-small/large`, `gpt-4o` |
| **Gemini** | ✓ | ✓ | `text-embedding-004`, `gemini-2.0-flash` |
| **Claude** | ✗ | ✓ | Chat only — Anthropic has no embedding API |

Claude cannot be used as an embedder. `ney doctor` will catch this misconfiguration.

**Mixing providers** (e.g. Ollama embed + Claude chat) is supported and recommended for privacy — only questions reach the cloud, not your indexed content.

> **Note:** Changing the embedding model invalidates the existing index. `ney doctor` detects the mismatch; run `ney reset && ney index <path>` to rebuild.

---

## Configuration reference

`~/.ney/config.yaml`

```yaml
embedder:
  provider: ollama          # openai | gemini | ollama
  model: bge-m3
  endpoint: http://localhost:11434   # Ollama only, optional

chat:
  provider: claude
  model: claude-sonnet-4-6

retrieval:
  top_k: 8                  # chunks retrieved per query
  max_context_chars: 12000  # context window budget for LLM
  rerank: false             # reranker (future)

chunking:
  strategy: markdown        # character | sentence | paragraph | markdown
  target_chars: 1200
  overlap_chars: 150

telemetry: false            # always off
```

API keys are read from environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`). They can also be set directly in the config file, but env vars are recommended.

---

## How it works

```
Index:  Files → Loader → Chunker → Embedder → VectorStore + SQLite
Search: Query → Embed → VectorStore.Search → ranked chunks
Ask:    Query → Search → trim to context budget → LLM → answer + Sources
```

- **Loaders** extract text from `.md`, `.pdf`, `.docx`
- **Chunkers** split text into ~1200-char pieces with position tracking (line/page/paragraph)
- **Embedder** converts chunks to float32 vectors; stored in `~/.ney/vectors.bin`
- **SQLite** (`~/.ney/index.db`) stores metadata, chunk content, and workspace info
- **Hash-based skip** — unchanged files are not re-embedded on re-index
- **Offline capable** — use Ollama for both embedder and chat to run fully air-gapped

---

## Data & privacy

- All data lives in `~/.ney/` — nothing is sent anywhere unless you configure a cloud provider
- `telemetry: false` is the default and cannot be flipped remotely
- Cloud providers only receive: (a) file chunks during `ney index`, (b) your question + retrieved chunks during `ney ask`

---

## Roadmap

- Reranker support (Jina, Cohere, BGE)
- Hybrid search (BM25 + semantic)
- File watcher for automatic re-indexing
- Web UI + REST API + MCP server
- Additional loaders: Git, Notion, Obsidian, HTML
- VS Code extension
