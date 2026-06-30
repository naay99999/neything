# Ney

Local-first AI knowledge engine. Point it at a folder, search by meaning, ask questions — data stays on your machine.

```
ney index ~/docs
ney search "how does billing work"
ney ask "what are the retry policies for failed payments?"
```

Or just run `ney` with no arguments for an interactive prompt — type a question directly, no command syntax to remember.

---

## Install

**macOS / Linux — curl:**
```bash
curl -sSL https://raw.githubusercontent.com/naay99999/neything/main/scripts/install.sh | sh
```

**macOS — Homebrew:**
```bash
brew trust naay99999/tap   # required once for new taps
brew tap naay99999/tap
brew install ney
```

**Go users:**
```bash
go install github.com/naay99999/neything/cmd/ney@latest
```
> Make sure `$(go env GOPATH)/bin` is in your PATH — add `export PATH="$HOME/go/bin:$PATH"` to `~/.zshrc` (or `~/.bashrc`) if `ney` isn't found after install.

**Build from source:**
```bash
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
| `ney index <path>` | Index files recursively (`.md`, `.pdf`, `.docx`); prunes missing files and orphan vectors |
| `ney watch <path>` | Watch directory and re-index on changes (debounced; Ctrl+C to stop) |
| `ney search "<query>"` | Semantic search — returns chunks grouped by file with snippets |
| `ney ask "<question>"` | RAG: retrieve → LLM → answer with source citations |
| `ney status` | Index stats: files, chunks, DB size, last indexed |
| `ney config` | Print current config (`config show` / `config edit` also work) |
| `ney doctor` | Check config, API keys, Ollama, SQLite, index health |
| `ney models` | List configured providers and Ollama installed models |
| `ney reset` | Clear the index (add `--workspace <name>` for partial reset) |
| `ney version` | Print version |

### Flags (all commands)

| Flag | Description |
|---|---|
| `--workspace <name>` | Target a specific workspace |
| `--top-k <n>` | Number of chunks to retrieve (default: 8) |
| `--provider <name>` | Override embedder on `index`/`search`; override chat on `ask` |
| `--path <dir>` | Limit results to files under this directory |
| `--json` | Machine-readable JSON output |

### Interactive mode

Run `ney` with no arguments to drop into a prompt:

```
$ ney
ney> status
ney> search auth flow
ney> what are the retry policies for failed payments?
```

- A line starting with a known command word (`ask`, `search`, `index`, `watch`, `status`, `config`, `doctor`, `models`, `reset`, `version`, `help`) dispatches that command — no quoting needed around `ask`/`search` queries.
- Anything else is treated as a question and sent straight to `ask`.
- Meta-commands: `:help`, `:clear`, `:quit` / `:exit`.
- Line history persists across sessions in `~/.ney/history` (arrow keys to recall).
- Each line runs statelessly, same as a one-shot CLI call — no conversation memory between lines yet.

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
  rerank: false             # set true to rerank before LLM (ask only)
  rerank_top_k: 24          # candidates fetched before rerank
  hybrid: false             # combine semantic + BM25 keyword search

reranker:                   # used when retrieval.rerank is true
  provider: cohere          # cohere | jina | ollama
  model: rerank-v3.5
  # endpoint: http://localhost:11434   # ollama/local only

chunking:
  strategy: markdown        # auto | character | sentence | paragraph | markdown | tokenizer | page
  target_chars: 1200
  overlap_chars: 150
  target_tokens: 300        # tokenizer strategy (~4 chars/token)
  overlap_tokens: 50
  # by_format:              # used when strategy: auto
  #   md: markdown
  #   pdf: page
  #   docx: paragraph

loaders:
  git:
    recent_commits: 0       # index recent git commits (0 = disabled)
  ocr:
    enabled: false          # requires: brew install tesseract poppler
    lang: eng
    min_chars: 32

telemetry: false            # always off
```

API keys are read from environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `COHERE_API_KEY`, `JINA_API_KEY`). They can also be set directly in the config file, but env vars are recommended.

---

## How it works

```
Index:  Files → Loader → Chunker → Embedder → VectorStore + SQLite + FTS
Search: Query → Embed → VectorStore + optional FTS → RRF → ranked chunks
Ask:    Query → Search → optional Rerank → trim to context budget → LLM → answer + Sources
```

- **Loaders** extract text from `.md`, `.pdf`, `.docx`, `.html`, `.json`, `.xml`, plus Obsidian/Notion markdown and Confluence XML; optional git commit history and OCR for scanned PDFs
- **Chunkers** split text with format-aware defaults (`chunking.strategy: auto`) — markdown by heading, PDF by page, docx/html by paragraph
- **Embedder** converts chunks to float32 vectors; stored in `~/.ney/vectors.bin` (brute) or `~/.ney/vectors.hnsw` (HNSW backend)
- **SQLite** (`~/.ney/index.db`) stores metadata, chunk content, and workspace info
- **Hash-based skip** — unchanged files are not re-embedded on re-index
- **Incremental sync** — deleted files and stale vectors are removed on re-index; renames detected by content hash
- **Vector store** — `brute` (default) or `hnsw` via `vector_store.backend` in config; migrate with `ney index --migrate-vectors`
- **Offline capable** — use Ollama for both embedder and chat to run fully air-gapped

---

## Data & privacy

- All data lives in `~/.ney/` — nothing is sent anywhere unless you configure a cloud provider
- `telemetry: false` is the default and cannot be flipped remotely
- Cloud providers only receive: (a) file chunks during `ney index`, (b) your question + retrieved chunks during `ney ask`

---

## Roadmap

Phase 3 (v0.4) is complete — see [docs/roadmap.md](docs/roadmap.md) for details.

Next up (Phase 4):

- REST API (`ney serve`)
- MCP server (`ney mcp`)
- Web UI dashboard
- VS Code extension
