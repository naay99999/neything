# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & run

```bash
go build -o ney ./cmd/ney   # build binary
go vet ./...                 # lint
go test ./...                # run all tests
go test ./internal/store -run TestName   # run a single test
```

No external runtime dependencies — SQLite is bundled via `modernc.org/sqlite` (pure Go, no cgo).

## Architecture

Ney is a local-first **personal context server for AI**, MCP-first: the primary surface is `ney mcp` (AI clients get layered context — profile, active projects, and document/memory search), with a CLI (`index`/`search`/`watch`) alongside. There is no chat/ask/REPL surface — that was removed in the 2026-07 MCP-first refocus (see docs/roadmap.md). Data lives in `~/.ney/`: `config.yaml`, `index.db` (SQLite), vector files (`vectors.bin` for brute-force, `vectors.hnsw` for HNSW backend), `writer.lock` (single-writer coordination), `profile.md` (user-owned, AI-editable via `update_profile`), and `memory/` (one markdown file per `remember` call, indexed + watched like any workspace).

The embedder is **optional**: `provider: none`/unset in config runs ney in keyword-only (FTS) mode with no API key or local model needed — `ney init` is the upgrade path to semantic search. `config.HasEmbedder()` checks this; `NewEmbedder` returns `(nil, nil)` when unset (not an error).

**Request flows:**

```
Index (Phase A):  Files → pathfilter (deny dotfiles/secrets) → Loader → ChunkResolver
                   → SQLite chunks + FTS (no embedding)
Index (Phase B):  EmbedWorker computes pending = chunk IDs − VectorStore IDs, embeds
                   in batches outside any SQL transaction, sweeps orphan vectors
Search:  Query → FTS (always, if rows exist) + semantic (if embedder configured and
         vectors exist) → RRF fusion → optional rerank → chunks + SearchMeta
         (degrades to keyword-only, never fails, if the embedder is down/unset)
Watch:   fsnotify → debounced IndexPath / RemovePath / PruneMissing → Phase A only,
         then EmbedWorker.Notify()
Context: get_context/list_projects → context.ScanRepos(dev_roots) live git scan
         (union with indexed workspaces) + LoadProfile(profile.md) → Render — no DB
         table, stateless, never fails. remember/update_profile write plain md files
         directly (memoryDir()/profile.md) — read-only-safe, no DB/vector writes.
MCP:     ney mcp → writer lock → Phase A per --root (background) → EmbedWorker loop
         → watcher per root → serves get_context/list_projects/search_documents/
         search_folder/read_document/remember/update_profile/index_folder/index_status
         immediately, before indexing finishes (tier 0 live scan fills the gap — see
         internal/scan). If the lock is held by another process, it serves READ-ONLY
         instead of failing: no indexer/worker/watchers, no workspace upserts,
         index_status reports mode:"read-only", and unindexed --roots are permanently
         marked Phase-A-running so live scan covers them. The memory workspace
         (~/.ney/memory) is registered as a served root in BOTH modes; only write mode
         indexes and watches it (see cmd_mcp.go's runMCP)
```

Indexing is split into two phases specifically so a slow/unreachable embedder can never hold the single SQLite connection: Phase A (`internal/index/pipeline.go`) never calls `Embedder.Embed` or `Vectors.Add`; embedding is entirely `EmbedWorker`'s job (`internal/index/embedworker.go`), run outside any transaction. There is no "embedded?" column — pending/orphan state is computed by diffing SQLite chunk IDs against `VectorStore.IDs()` on each worker pass (relies on chunk IDs being `AUTOINCREMENT`, never reused).

**Package map:**

| Package | Role |
|---|---|
| `cmd/ney/` | Cobra commands; thin wrappers that wire internal packages. `cmd_mcp.go` + `mcp_tools.go` implement `ney mcp` |
| `internal/config/` | Config loading (viper), validation, and **provider factory functions** (`NewEmbedder`, `NewReranker`, `NewVectorStore`) |
| `internal/loader/` | File loaders: `.md`/`.markdown` (+ Obsidian wikilink metadata, + Notion-export quirk stripping — both are `.md` variants sniffed by content, not separate formats), plain `.txt`. md-only since the 2026-07-31 layered-context refocus — PDF/OCR/DOCX/HTML/JSON/Confluence (the genuinely separate formats) were deleted |
| `internal/chunk/` | `ChunkStrategy` + `Resolver` (auto per doc type): `character`, `sentence`, `paragraph`, `markdown`, `tokenizer` |
| `internal/embed/` | `Embedder` interface + OpenAI, Gemini, Ollama, OpenAI-compatible implementations |
| `internal/context/` | Layered-context core (`neycontext`): `ScanRepos` (live git scan of `context.dev_roots`), `LoadProfile`/`UpdateProfile` (`profile.md`), `Render` (L1 markdown blob), `WriteMemory` (`remember`'s md file writer). Pure/stateless — no DB table |
| `internal/discover/` | Setup-wizard repo scanner: thin wrapper over `context.ScanRepos` returning `Candidate{Path, Name, LastCommit, DocCount}` for the wizard's repo picker |
| `internal/pathfilter/` | Shared file/dir exclusion: built-in always-on deny (dotfiles + secret-name globs) + user `index.exclude` globs; nil `*Filter` is valid (built-ins only). Applied by the indexer walk, live scan, and `read_document` |
| `internal/index/` | `Indexer` (Phase A: chunk+FTS, hash-based skip, prune, rename detection) + `EmbedWorker` (Phase B: progressive embed, orphan cleanup, backoff, model-consistency check) |
| `internal/watch/` | File watcher with debounce for `ney watch`; accepts an external ctx + optional `Serialize`/prune-disable so `ney mcp` can run several under one shared mutex |
| `internal/search/` | `Retriever` — auto/semantic/keyword/hybrid modes, RRF fusion, optional rerank, `SearchMeta` (what actually ran, degradation reason) |
| `internal/scan/` | Tier-0 "live scan": stateless filename + content-grep search of the raw filesystem, used before/without an index (`ney mcp` while Phase A runs; `ney search` on an unindexed folder) |
| `internal/store/` | SQLite wrapper; schema in `schema.go`; FTS5 in `fts.go` |
| `internal/vectorstore/` | `VectorStore` interface (includes `IDs()` for the Phase B diff); `BruteForceStore` + `HNSWStore` (lazy graph rebuild, full rebuild on any `Delete`) |
| `internal/rerank/` | Reranker interface + Cohere, Jina, Ollama/local implementations |
| `internal/lockfile/` | Cross-process writer lock (`~/.ney/writer.lock`, pid + staleness check) held by every command that writes chunks/vectors |
| `internal/apiretry/` | Shared HTTP retry helper (`apiretry.Do`) used by provider clients; honors `Retry-After` |
| `internal/citation/` | Formats chunk locations for display (`lines`/`pages`/`paragraphs` by doc type) |

**Key constraints:**

- SQLite runs with `SetMaxOpenConns(1)`. Because of this, `indexDocument` in `pipeline.go` upserts the document row *before* opening the transaction — doing it inside the tx would deadlock since the single connection is already held. The same rule applies to any new store method: **never issue a query while a prior query's `sql.Rows` is still open** — drain/close it first, or it hangs.
- Do **not** rely on `LastInsertId()` after unrelated statements on the single SQLite connection — always `SELECT` the row id after upsert (see `UpsertWorkspace`, `UpsertDocument`).
- Vector IDs are SQLite chunk row IDs serialized as strings. This is the join key between vector files and `index.db`.
- Re-indexing must call `Vectors.Delete` for old chunk IDs before adding new ones; `Index()` also prunes documents missing from the filesystem. `EmbedWorker` owns writing `active_embedder` (`SetActiveEmbedder`, on first successful embed batch) and the model-consistency check — Phase A no longer does either, since it never touches vectors.
- Changing the embedding model invalidates the whole index. `EmbedWorker` checks model consistency (via a substring scan of the `active_embedder` meta blob — `GetActiveEmbedder`'s `Sscanf`-based parse is broken, don't use it) at the start of every drain and refuses to proceed on mismatch (`blocked_mismatch`, sticky until `ney reset`).
- Claude cannot be used as an embedder (Anthropic has no embedding API). `config.Validate` enforces this.
- Provider factory functions live in `internal/config/config.go`, not in the provider packages themselves.
- Any command that writes chunks or vectors (`index`, `watch`, `mcp`, `reset`) must acquire `internal/lockfile.Acquire(config.NeyDir())` before opening the DB/VectorStore, and release it on exit — two writers flushing the same vector file concurrently silently clobber each other. Read-only commands (`search`, `status`) don't need it. `ney mcp` is the one writer that degrades instead of failing: on `lockfile.ErrLocked` it serves read-only (see `acquireWriterLock` in `cmd_mcp.go`).
- Lock-free reads are safe because VectorStore `Flush` no-ops unless something mutated (`unsaved` flag) — a reader that never Add/Deletes can `Close()` without clobbering the writer's file.
- Security invariant: everything served to an MCP client must pass BOTH containment AND `pathfilter.ExcludedPath` (secret/dotfile deny). Containment is: inside a served root (`resolveAllowedPath`, symlink-resolved on both sides), OR in the session's `serverState.discovered` set — paths surfaced by a user-directed `search_folder` call (itself bounded to $HOME and secret-blind). The same filter must gate the indexer walk and `internal/scan` — never add a file-discovery path that bypasses `pathfilter`.
- `ney mcp`'s stdout is reserved for the MCP protocol, full stop — every diagnostic goes to stderr. Never call `fmt.Println`/`PrintJSON`/the spinner helpers (which write to stdout) from any code path `runMCP` can reach.
- The memory workspace (`~/.ney/memory`, target of `remember`) is registered internally by `runMCP` in BOTH read-write and read-only mode — it's always a served root so `read_document`/`search_documents` can reach it, added directly to the `rootSet` rather than through `index_folder`'s home-directory validation. Only write mode indexes and watches it (Phase A + a watcher through the same per-root pipeline as any other root); in read-only mode it's served but not indexed by this process — a live scan or the lock-holder's watcher covers freshness.
- `get_context`/`list_projects` are stateless: `context.ScanRepos` shells out to `git` live on every call (no DB table, no caching) and any per-repo git error just skips that repo — `get_context` must never return an MCP error. `remember` and `update_profile` (`internal/context`) only ever write plain markdown files (`memoryDir()`, `profile.md`) — no DB/vector writes — so both work correctly when `ney mcp` is running read-only.

**Setup wizard (`cmd/ney/cmd_init.go`, `mcpclients.go`):** bare `ney` on a machine without DB meta `setup_completed` (and zero workspaces) offers the wizard; `ney init` reruns it. Steps: `[1/4]` scan `context.dev_roots` for git repos (prompt for a root if none configured and no `~/workspace`) → pick + index → `[2/4]` bootstrap `~/.ney/profile.md` from the embedded template, asking 2–3 short questions (skipped if a profile already exists — it's user-owned from then on) → `[3/4]` register AI clients (Claude Desktop JSON merge / `claude mcp add` / Codex TOML section — all take the config path as a parameter, back up `.bak` first, and refuse to overwrite unparseable files) → `[4/4]` optional embedder. Clients are registered args-less (`ney mcp`): the workspaces table is the single source of truth for what's served.

**Dynamic roots:** `ney mcp`'s served roots live in a `rootSet` (cmd_mcp.go) that `index_folder` appends to mid-session — read_document containment and live-scan targeting must always consult `rs.snapshot()`, never a captured slice.

**Workspace scoping (`cmd/ney/workspace.go`, `sync.go`):** `search` defaults to the workspace whose `root_path` contains the cwd (longest match wins); `--workspace` overrides, `--all` forces global. Before searching, `syncWorkspaceIfKnown` silently re-indexes the cwd workspace (reusing the already-open DB/Vectors/Embedder — never open a second writer) and never fails the caller's request. If a scoped search returns nothing, commands fall back to a global search and label results with their workspace. When the scope isn't backed by an indexed workspace yet (or has zero documents), `ney search` additionally runs a tier-0 `internal/scan` pass and appends results labeled `source: "live-scan"`.

**Adding a new provider:** implement `embed.Embedder`, add a case to `config.NewEmbedder`, update `config.Validate`'s allow-lists, and update `ney doctor` / `ney models` output.

**Adding a new loader:** implement `loader.Loader`, register it in `cmd/ney/indexer.go` (order matters for `.md` sniffing), add the extension to `supportedExts` in `internal/index/pipeline.go` if file-based.
