# Ney

Local-first AI knowledge engine. Point it at a folder, search by meaning, ask questions — data stays on your machine.

```
ney index ~/docs
ney search "how does billing work"
ney ask "what are the retry policies for failed payments?"
```

Works out of the box with **zero configuration** — no model server, no API key: keyword search and the [MCP server](#mcp) run immediately after install. Configure an embedder later (`ney init`) to add semantic search, and a chat model to enable `ney ask`.

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

**1. Index and search — no setup needed**

```bash
ney index ~/my-notes
ney search "order-1233"
```

With no providers configured, indexing writes the chunk + keyword (FTS5) index only — fast, CPU-only, fully offline. Search runs in keyword mode and tells you so. This is already enough for exact lookups (order numbers, names, error codes) and for serving AI clients over [MCP](#mcp).

**2. Add semantic search + ask (optional)**

```bash
ney init
```

The wizard detects local model servers (Ollama, LM Studio), lists the models they expose, lets you pick an embedder and a chat model, and writes `~/.ney/config.yaml` for you. It also works with a remote server — just enter its URL (e.g. `http://192.168.1.150:1234`).

The fastest local setup uses Ollama for both:

```bash
ollama pull bge-m3          # embedder → enables semantic search
ollama pull llama3.2        # chat     → enables ney ask
```

LM Studio works too: load a chat model plus an embedding model (e.g. `nomic-embed-text`) and enable its local server. Or use cloud providers — set API keys as environment variables:

```bash
export ANTHROPIC_API_KEY=sk-...   # for Claude chat
export OPENAI_API_KEY=sk-...      # for OpenAI embed + chat
export GEMINI_API_KEY=...         # for Gemini embed + chat
```

You can also configure by hand — the default `~/.ney/config.yaml` (created on first run with both providers set to `none`) has commented examples:

```yaml
embedder:
  provider: ollama      # none | openai | gemini | ollama | lmstudio  (never claude)
  model: bge-m3

chat:
  provider: claude      # none | claude | openai | gemini | ollama | lmstudio
  model: claude-sonnet-4-6
```

After configuring an embedder, the next `ney index` (or the background worker in `ney mcp`/`ney watch`) embeds the already-indexed chunks — no full re-index needed.

**3. Check everything is ready**

```bash
ney doctor
```

**4. Ask questions**

```bash
ney ask "how do I roll back a failed deploy?"
```

---

## MCP

Point an AI client at ney and it can search your files immediately — no waiting for indexing to finish, no embedder, no API key. `ney mcp` starts answering the moment it starts up, and layers in better results as indexing catches up in the background:

- **Tier 0 (live scan)** — instantly, before anything is indexed: filename + content grep straight off disk.
- **Tier 1 (keyword/FTS)** — seconds after startup, once Phase A (parse → chunk → SQLite FTS5) finishes for a root. No embedder required.
- **Tier 2 (semantic)** — fills in progressively once you've run `ney init` and configured an embedder; `search_documents`'s `index_status` reports embedding progress so a client can tell partial results from final ones.

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

`ney mcp` serves four tools over stdio: `search_documents`, `read_document`, `list_workspaces`, `index_status`. Omit `--root` to serve whatever workspaces are already in `~/.ney/index.db` from prior `ney index` runs; pass one or more `--root <path>` to index+serve fresh folders.

Only one writer process (`index`, `watch`, `mcp`, `reset`) can hold `~/.ney/writer.lock` at a time. If `ney mcp` is running and you try `ney index` or `ney reset` from another terminal, that command fails fast with the pid/command currently holding the lock instead of racing a vector-file write. Read-only commands (`search`, `ask`, `status`) never need the lock.

`ney index --no-embed` writes chunks + keyword index only (Phase A) and defers embedding for later — handy for a fast first pass over a big corpus, and exactly what `ney mcp` does under the hood before its background embed worker starts. `retrieval.mode` (`auto` | `semantic` | `keyword` | `hybrid`, default `auto`) controls which signals `search`/`ask`/`search_documents` combine — `auto` uses whatever's available and degrades gracefully (embedder unreachable, no vectors yet) instead of failing the request outright.

---

## Commands

| Command | Description |
|---|---|
| `ney init` | Interactive setup — detects Ollama/LM Studio, picks models, writes the config |
| `ney index <path>` | Index files recursively (`.md`, `.pdf`, `.docx`); prunes missing files and orphan vectors; `--no-embed` writes chunks + keyword index only |
| `ney watch <path>` | Watch directory and re-index on changes (debounced; Ctrl+C to stop) |
| `ney search "<query>"` | Search — semantic + keyword combined (`retrieval.mode: auto`), grouped by file with snippets; live-scans folders that aren't indexed yet |
| `ney ask "<question>"` | RAG: retrieve → LLM → answer with source citations |
| `ney status` | Index stats: files, chunks, DB size, last indexed |
| `ney config` | Print current config (`config show` / `config edit` also work) |
| `ney doctor` | Check config, API keys, Ollama, SQLite, index health |
| `ney models` | List configured providers and Ollama installed models |
| `ney reset` | Clear the index (add `--workspace <name>` for partial reset) |
| `ney mcp` | Serve `search_documents`/`read_document`/`list_workspaces`/`index_status` over MCP (stdio) — see [MCP](#mcp) above |
| `ney version` | Print version |

### Flags (all commands)

| Flag | Description |
|---|---|
| `--workspace <name>` | Target a specific workspace |
| `--top-k <n>` | Number of chunks to retrieve (default: 8) |
| `--provider <name>` | Override embedder on `index`/`search`/`watch`; override chat on `ask` |
| `--path <dir>` | Limit results to files under this directory |
| `--json` | Machine-readable JSON output |

### Interactive mode

Run `ney` with no arguments to drop into a prompt. It opens with a startup screen showing your configured models, index size, and data directory, then:

```
$ ney
ney> status
ney> search auth flow
ney> what are the retry policies for failed payments?
```

If nothing is indexed yet, ney offers a short getting-started menu (set up providers / index a folder / skip) instead of a silent prompt. Type `?` at any time for help.

- A line starting with a known command word (`init`, `ask`, `search`, `index`, `watch`, `status`, `config`, `doctor`, `models`, `reset`, `version`, `help`) dispatches that command — no quoting needed around `ask`/`search` queries. Commands can optionally be prefixed with `/` (e.g. `/config`).
- Any *multi-word* line that isn't a command is treated as a question and sent straight to `ask`. A single unknown word is assumed to be a typo — ney suggests the nearest command instead of calling the LLM.
- Type a bare `config`, `reset`, or `index` with no arguments and ney asks what you want instead of erroring — e.g. `config` prompts "Show or edit config? [s/e]", `reset` prompts for full vs. one workspace, `index` prompts for a path. Giving the full command (`config edit`, `reset --workspace foo`, `index ~/docs`) skips the prompt as before.
- Leave with `exit`, `quit`, or `q` (`:quit` / `:exit` still work). Other meta-commands: `:help`, `:clear`.
- Line history persists across sessions in `~/.ney/history` (arrow keys to recall).
- Each line runs statelessly, same as a one-shot CLI call — no conversation memory between lines yet.
- `ask`/`search` show a spinner while waiting on the embedder/LLM, and `ask` answers type out instead of appearing all at once. Output is colored on an interactive terminal; set `NO_COLOR=1` to disable, or pipe/redirect output to fall back to plain text automatically.

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
| **LM Studio** (`lmstudio`) | ✓ | ✓ | Any OpenAI-compatible server (LM Studio, vLLM, llama.cpp); set `endpoint`, no API key |
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
  provider: ollama          # none | openai | gemini | ollama | lmstudio (default: none — keyword-only)
  model: bge-m3
  endpoint: http://localhost:11434   # ollama/lmstudio only (LM Studio default: http://localhost:1234)

chat:
  provider: claude          # none | claude | openai | gemini | ollama | lmstudio (default: none — ask disabled)
  model: claude-sonnet-4-6
  # endpoint: http://localhost:1234  # ollama/lmstudio only

retrieval:
  top_k: 8                  # chunks retrieved per query
  max_context_chars: 12000  # context window budget for LLM
  rerank: false             # set true to rerank before LLM (ask only)
  rerank_top_k: 24          # candidates fetched before rerank
  mode: auto                # auto | semantic | keyword | hybrid — auto uses what's available
                            # (legacy key `hybrid: true/false` still accepted)

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
Index:   Files → Loader → Chunker → SQLite + FTS   (Phase A — instant, CPU-only)
Embed:   pending chunks → Embedder → VectorStore    (Phase B — background worker)
Search:  Query → FTS + (Embed → VectorStore) → RRF → ranked chunks   (auto mode: uses whatever's ready)
Ask:     Query → Search → optional Rerank → trim to context budget → LLM → answer + Sources
```

- **Loaders** extract text from `.md`, `.pdf`, `.docx`, `.html`, `.json`, `.xml`, plus Obsidian/Notion markdown and Confluence XML; optional git commit history and OCR for scanned PDFs
- **Chunkers** split text with format-aware defaults (`chunking.strategy: auto`) — markdown by heading, PDF by page, docx/html by paragraph
- **Embedder** converts chunks to float32 vectors; stored in `~/.ney/vectors.bin` (brute) or `~/.ney/vectors.hnsw` (HNSW backend)
- **SQLite** (`~/.ney/index.db`) stores metadata, chunk content, and workspace info
- **Hash-based skip** — unchanged files are not re-embedded on re-index
- **Incremental sync** — deleted files and stale vectors are removed on re-index; renames detected by content hash
- **Vector store** — `brute` (default) or `hnsw` via `vector_store.backend` in config; migrate with `ney index --migrate-vectors`
- **Offline capable** — use Ollama for both embedder and chat to run fully air-gapped
- **Tiered search** — `ney mcp` (and `ney search` on a not-yet-indexed folder) never returns nothing: live filesystem scan → keyword/FTS → semantic, whichever tiers are ready, see [MCP](#mcp)

---

## Data & privacy

- All data lives in `~/.ney/` — nothing is sent anywhere unless you configure a cloud provider
- `telemetry: false` is the default and cannot be flipped remotely
- Cloud providers only receive: (a) file chunks during `ney index`, (b) your question + retrieved chunks during `ney ask`

---

## Roadmap

Phase 3 (v0.4) is complete, and Phase 4.2 (MCP server + tiered search) is complete — see [MCP](#mcp) above and [docs/roadmap.md](docs/roadmap.md) for details.

Next up (rest of Phase 4):

- REST API (`ney serve`)
- Web UI dashboard
- VS Code extension
