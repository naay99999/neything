# Ney

**Point your AI at your files.** Ney is a local-first MCP server + CLI that gives AI clients (Claude Code, Claude Desktop, Cursor) search and read access to your local documents — markdown, PDF, DOCX, HTML, and more. Your files never leave your machine, and secret files are never indexed or served.

```bash
claude mcp add ney -- ney mcp --root ~/docs
```

That's it — your AI can now search and read everything under `~/docs`. Works out of the box with **zero configuration**: no model server, no API key. Keyword search is available the moment the server starts; add a local embedder later (`ney init`) for semantic search.

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
brew install naay99999/tap/ney
```
(Linux: use the curl installer or `go install` below — the brew package is macOS-only.)

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

## MCP — connect your AI

`ney mcp` starts answering the moment it starts up, and layers in better results as indexing catches up in the background:

- **Tier 0 (live scan)** — instantly, before anything is indexed: filename + content grep straight off disk.
- **Tier 1 (keyword/FTS)** — seconds after startup, once the initial parse → chunk → SQLite FTS5 pass finishes for a root. No embedder required.
- **Tier 2 (semantic)** — fills in progressively once you've run `ney init` and configured an embedder; `index_status` reports embedding progress so a client can tell partial results from final ones.

**Claude Code:**
```bash
claude mcp add ney -- ney mcp --root ~/docs
```

**Claude Desktop** (`claude_desktop_config.json`):
```json
{
  "mcpServers": {
    "ney": {
      "command": "ney",
      "args": ["mcp", "--root", "/Users/you/docs"]
    }
  }
}
```

**Cursor** (`.cursor/mcp.json`):
```json
{
  "mcpServers": {
    "ney": {
      "command": "ney",
      "args": ["mcp", "--root", "/Users/you/docs"]
    }
  }
}
```

`ney mcp` serves five tools over stdio: `search_documents`, `search_folder`, `read_document`, `list_workspaces`, `index_status`. `search_folder` is the whole-machine fallback: when the index has no match, the AI asks you where the file might be (Downloads? Desktop?) and live-scans that folder on demand — bounded, home-directory-only, and secret-blind like everything else; files it surfaces become readable via `read_document` for the session. Omit `--root` to serve whatever workspaces are already in `~/.ney/index.db` from prior `ney index` runs; pass one or more `--root <path>` to index+serve fresh folders.

### Security

Ney is built to hand an AI *your documents* — not your secrets, and not the rest of your disk:

- **Root containment** — `read_document` only serves files inside the roots you passed, plus files a user-directed `search_folder` call surfaced this session. Absolute paths, `..` traversal, and symlinks pointing outside a root are all rejected (paths are symlink-resolved on both sides before comparison).
- **Secret files are never indexed or served** — dotfiles/dot-directories (`.env`, `.ssh/`, `.git/`) plus a built-in deny list of secret-looking names (`*secret*`, `*credential*`, `*password*`, `*.key`, `*.pem`, `id_rsa*`, `*.env`, keystores, ...) are excluded everywhere: the indexer, the live scan, and `read_document`. This is always on and cannot be disabled.
- **Your own excludes** — add glob patterns under `index.exclude` in the config (see below). Files that were indexed before a pattern matched them are pruned automatically on the next index pass.
- **Local-first** — with no cloud provider configured, nothing ever leaves your machine. With a cloud embedder, only file chunks are sent for embedding — configure a local embedder (Ollama / LM Studio) to keep even that on-device.

### Concurrent access

Only one writer process (`index`, `watch`, `mcp`, `reset`) holds `~/.ney/writer.lock` at a time. If a second `ney mcp` starts while one is already running — say Claude Desktop and Claude Code both spawn one — the second **serves read-only** instead of failing: search and read work off the existing index (as of its startup), while indexing/embedding/watching stay with the first process. `index_status` reports `mode: "read-only"` so clients can tell. `ney index`/`ney reset` from another terminal still fail fast with the holder's pid rather than racing a vector-file write. Read-only commands (`search`, `status`) never need the lock.

---

## CLI

The same engine is usable directly from the terminal:

```bash
ney index ~/my-notes
ney search "order-1233"
```

With no providers configured, indexing writes the chunk + keyword (FTS5) index only — fast, CPU-only, fully offline. Search runs in keyword mode and tells you so. This is already enough for exact lookups (order numbers, names, error codes).

**Add semantic search (optional):**

```bash
ney init
```

The wizard detects local model servers (Ollama, LM Studio), lists the models they expose, lets you pick an embedding model, and writes `~/.ney/config.yaml` for you. It also works with a remote server — just enter its URL. The fastest local setup:

```bash
ollama pull bge-m3          # embedder → enables semantic search
```

Or use a cloud embedder — set the API key as an environment variable (`OPENAI_API_KEY` / `GEMINI_API_KEY`). After configuring an embedder, the next `ney index` (or the background worker in `ney mcp`/`ney watch`) embeds the already-indexed chunks — no full re-index needed.

**Check everything is ready:**

```bash
ney doctor
```

### Commands

| Command | Description |
|---|---|
| `ney mcp` | Serve `search_documents`/`read_document`/`list_workspaces`/`index_status` over MCP (stdio) — see [MCP](#mcp--connect-your-ai) above |
| `ney init` | Interactive setup — detects Ollama/LM Studio, picks an embedding model, writes the config |
| `ney index <path>` | Index files recursively (`.md`, `.pdf`, `.docx`, ...); prunes missing files and orphan vectors; `--no-embed` writes chunks + keyword index only |
| `ney watch <path>` | Watch directory and re-index on changes (debounced; Ctrl+C to stop) |
| `ney search "<query>"` | Search — semantic + keyword combined (`retrieval.mode: auto`), grouped by file with snippets; live-scans folders that aren't indexed yet |
| `ney status` | Index stats: files, chunks, DB size, last indexed |
| `ney config` | Print current config (`config show` / `config edit` also work) |
| `ney doctor` | Check config, API keys, Ollama, SQLite, index health |
| `ney models` | List configured providers and locally available models |
| `ney reset` | Clear the index (add `--workspace <name>` for partial reset) |
| `ney version` | Print version |

### Flags (all commands)

| Flag | Description |
|---|---|
| `--workspace <name>` | Target a specific workspace |
| `--top-k <n>` | Number of chunks to retrieve (default: 8) |
| `--provider <name>` | Override the embedder provider |
| `--path <dir>` | Limit results to files under this directory |
| `--json` | Machine-readable JSON output |

### Workspaces

Each `ney index` creates a named workspace (defaults to the folder name). Use `--workspace` to scope searches:

```bash
ney index ~/work/code   --workspace code
ney index ~/docs/notes  --workspace notes

ney search "auth flow" --workspace code
```

`ney search` defaults to the workspace containing your current directory; `--all` forces a global search.

---

## Providers

Embedding providers (for semantic search — entirely optional):

| Provider | Notes |
|---|---|
| **Ollama** | Local, offline, no API key |
| **LM Studio** (`lmstudio`) | Any OpenAI-compatible server (LM Studio, vLLM, llama.cpp); set `endpoint`, no API key |
| **OpenAI** | `text-embedding-3-small/large` |
| **Gemini** | `text-embedding-004` |

Claude cannot be used as an embedder (Anthropic has no embedding API); `ney doctor` will catch this misconfiguration.

> **Note:** Changing the embedding model invalidates the existing index. `ney doctor` detects the mismatch; run `ney reset && ney index <path>` to rebuild.

---

## Configuration reference

`~/.ney/config.yaml`

```yaml
embedder:
  provider: ollama          # none | openai | gemini | ollama | lmstudio (default: none — keyword-only)
  model: bge-m3
  endpoint: http://localhost:11434   # ollama/lmstudio only (LM Studio default: http://localhost:1234)

retrieval:
  top_k: 8                  # chunks retrieved per query
  rerank: false             # set true to rerank retrieved results
  rerank_top_k: 24          # candidates fetched before rerank
  mode: auto                # auto | semantic | keyword | hybrid — auto uses what's available
                            # (legacy key `hybrid: true/false` still accepted)

# indexing — extra exclude patterns (globs matched case-insensitively against
# file/dir names), on top of the built-in always-on excludes (dotfiles +
# secret-file names — see Security above)
index:
  exclude: []
  # exclude: ["*.bak", "drafts-*", "node_modules"]

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
  ocr:
    enabled: false          # requires: brew install tesseract poppler
    lang: eng
    min_chars: 32

telemetry: false            # always off
```

API keys are read from environment variables (`OPENAI_API_KEY`, `GEMINI_API_KEY`, `COHERE_API_KEY`, `JINA_API_KEY`). They can also be set directly in the config file, but env vars are recommended.

---

## How it works

```
Index:   Files → Loader → Chunker → SQLite + FTS   (Phase A — instant, CPU-only)
Embed:   pending chunks → Embedder → VectorStore    (Phase B — background worker)
Search:  Query → FTS + (Embed → VectorStore) → RRF → ranked chunks   (auto mode: uses whatever's ready)
```

- **Loaders** extract text from `.md`, `.pdf`, `.docx`, `.html`, `.json`, `.xml`, plus Obsidian/Notion markdown and Confluence XML; optional OCR for scanned PDFs
- **Path filter** (always on) excludes dotfiles and secret-file patterns from every surface — indexing, live scan, and MCP reads
- **Chunkers** split text with format-aware defaults (`chunking.strategy: auto`) — markdown by heading, PDF by page, docx/html by paragraph
- **Embedder** converts chunks to float32 vectors; stored in `~/.ney/vectors.bin` (brute) or `~/.ney/vectors.hnsw` (HNSW backend)
- **SQLite** (`~/.ney/index.db`) stores metadata, chunk content, and workspace info
- **Hash-based skip** — unchanged files are not re-embedded on re-index
- **Incremental sync** — deleted files and stale vectors are removed on re-index; renames detected by content hash
- **Vector store** — `brute` (default) or `hnsw` via `vector_store.backend` in config; migrate with `ney index --migrate-vectors`
- **Offline capable** — use Ollama as the embedder to run fully air-gapped
- **Tiered search** — `ney mcp` (and `ney search` on a not-yet-indexed folder) never returns nothing: live filesystem scan → keyword/FTS → semantic, whichever tiers are ready

---

## Data & privacy

- All data lives in `~/.ney/` — nothing is sent anywhere unless you configure a cloud provider
- `telemetry: false` is the default and cannot be flipped remotely
- A cloud embedder (if configured) only receives file chunks during indexing — use a local embedder to keep everything on-device
- Secret files (dotfiles, keys, credentials — see [Security](#security)) are never indexed, never searchable, and never served over MCP

---

## Roadmap

Ney is focused on being the best way to connect an AI client to your local documents. Next up:

- Better MCP ergonomics: resource templates, per-root tool scoping
- Smarter incremental embedding and index compaction
- VS Code extension

See [docs/roadmap.md](docs/roadmap.md) for history and details.
