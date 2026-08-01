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

Ney is a local-first **personal context server for AI**, MCP-first: the primary surface is `ney mcp` (AI clients get layered context — profile, active projects, and document/memory search), with a CLI (`index`/`search`/`watch`) alongside. There is no chat/ask/REPL surface — that was removed in the 2026-07 MCP-first refocus (see docs/roadmap.md). Data lives in `~/.ney/`: `config.yaml`, `index.db` (SQLite), `writer.lock` (single-writer coordination), `profile.md` (user-owned, AI-editable via `update_profile`), and `memory/` (one markdown file per `remember` call, indexed + watched like any workspace).

Search is **keyword-only** (SQLite FTS5). There are no embeddings, vectors, or rerank, and there will not be — the connected AI client is the intelligence layer; ney's job is retrieval + layered context. Zero configuration: no API key, no model server, nothing to set up (removed in the 2026-08 keyword-only cut — see docs/roadmap.md).

**Request flows:**

```
Index:   Files → pathfilter (deny non-regular files, dotfiles, secrets)
                   → Loader → ChunkResolver → SQLite chunks + FTS
Search:  Query → FTS5 → chunks + SearchMeta. An FTS error is recorded in
         SearchMeta.Degraded and returns empty results, never an error, so an
         MCP caller's live-scan supplement can still answer.
Watch:   fsnotify → debounced IndexPath / RemovePath / PruneMissing
Context: get_context/list_projects → context.ScanRepos(dev_roots) live git scan
         (union with indexed workspaces) + LoadProfile(profile.md) → Render — no DB
         table, stateless, never fails. remember/update_profile write plain md files
         directly (memoryDir()/profile.md) — read-only-safe, no DB writes.
MCP:     ney mcp → writer lock → indexing per --root (background)
         → watcher per root → serves get_context/list_projects/search_documents/
         search_folder/read_document/remember/update_profile/index_folder/index_status
         immediately, before indexing finishes (tier 0 live scan fills the gap — see
         internal/scan). If the lock is held by another process, it serves READ-ONLY
         instead of failing: no indexer/watchers, no workspace upserts,
         index_status reports mode:"read-only", and unindexed --roots are permanently
         marked indexing-in-progress so live scan covers them. The memory workspace
         (~/.ney/memory) is registered as a served root in BOTH modes; only write mode
         indexes and watches it (see cmd_mcp.go's runMCP)
```

**Package map:**

| Package | Role |
|---|---|
| `cmd/ney/` | Cobra commands; thin wrappers that wire internal packages. `cmd_mcp.go` + `mcp_tools.go` implement `ney mcp` |
| `internal/config/` | Config loading (viper) + validation (`config.go`), and the single config writer `SetDevRoots` (`save.go`) |
| `internal/loader/` | File loaders: `.md`/`.markdown` (+ Obsidian wikilink metadata, + Notion-export quirk stripping — both are `.md` variants sniffed by content, not separate formats), plain `.txt`. md-only since the 2026-07-31 layered-context refocus — PDF/OCR/DOCX/HTML/JSON/Confluence (the genuinely separate formats) were deleted |
| `internal/chunk/` | `ChunkStrategy` + `Resolver` (auto per doc type): `character`, `sentence`, `paragraph`, `markdown`, `tokenizer` |
| `internal/context/` | Layered-context core (`neycontext`): `ScanRepos` (live git scan of `context.dev_roots`, repos described 8-wide through the hardened `gitCmd`), `LoadProfile`/`UpdateProfile` (`profile.md`), `Render` (L1 markdown blob), `WriteMemory` (`remember`'s md file writer). Pure/stateless — no DB table |
| `internal/discover/` | Setup-wizard repo scanner: thin wrapper over `context.ScanRepos` returning `Candidate{Path, Name, LastCommit, DocCount}` for the wizard's repo picker |
| `internal/pathfilter/` | Shared file/dir exclusion: built-in always-on deny (dotfiles + secret-name globs) + user `index.exclude` globs; nil `*Filter` is valid (built-ins only). Plus `ExcludedMode`, the by-file-type deny (only regular files are opened). Applied by the indexer walk, live scan, and `read_document` |
| `internal/index/` | `Indexer`: chunk + FTS write, hash-based skip, prune, rename detection |
| `internal/watch/` | File watcher with debounce for `ney watch`; accepts an external ctx + optional `Serialize`/prune-disable so `ney mcp` can run several under one shared mutex |
| `internal/search/` | `Retriever` — FTS5 keyword search, single-pass hydration of the ranked ID set, `SearchMeta` (degradation reason) |
| `internal/scan/` | Tier-0 "live scan": stateless filename + content-grep search of the raw filesystem, used before/without an index (`ney mcp` while initial indexing runs; `ney search` on an unindexed folder) |
| `internal/store/` | SQLite wrapper; schema in `schema.go`; FTS5 in `fts.go` |
| `internal/lockfile/` | Cross-process writer lock (`~/.ney/writer.lock`, kernel `flock(LOCK_EX\|LOCK_NB)`) held by every command that writes chunks. The fd *is* the lock — the kernel drops it when the holder dies, so there is no pid-liveness/staleness heuristic (a recycled pid can't wedge it). The pid/command JSON in the file is a human-readable label only, never consulted to decide whether the lock is held |
| `internal/citation/` | Formats chunk locations for display (`lines`/`pages`/`paragraphs` by doc type) |

**Key constraints:**

- SQLite runs with `SetMaxOpenConns(1)`. Because of this, `indexDocument` in `pipeline.go` upserts the document row *before* opening the transaction — doing it inside the tx would deadlock since the single connection is already held. The same rule applies to any new store method: **never issue a query while a prior query's `sql.Rows` is still open** — drain/close it first, or it hangs.
- Do **not** rely on `LastInsertId()` after unrelated statements on the single SQLite connection — always `SELECT` the row id after upsert (see `UpsertWorkspace`, `UpsertDocument`).
- `DeleteChunksByDocument` and `DeleteDocumentWithCleanup` still return the deleted chunk IDs, but **nothing consumes them any more** (they used to feed `VectorStore.Delete`). Do not delete the calls — they own the chunk + FTS row cleanup. Dropping one orphans chunk rows and leaves stale FTS entries, which surfaces as wrong search hits rather than an error. `TestIndexerReindexReplacesOldChunks` is the guard.
- `config.yaml` is user-owned. The only writer in the codebase is `config.SetDevRoots` (`internal/config/save.go`), a `yaml.Node` edit that preserves comments and unknown keys, re-parses the rendered bytes before committing, and renames atomically. **Never** re-marshal the whole file from a `Config` value, and **never** materialize `context.dev_roots: []` — `Load` uses `v.IsSet("context.dev_roots")` to decide whether to apply `defaultDevRoots()` (`~/workspace`), so an empty sequence permanently suppresses the repo scan.
- `internal/config.Load()` is silent — no stdout, no stderr. It is reachable from `ney mcp`. First-run hints belong in `cmd/ney/root.go`'s `loadConfig()`, gated on `cfg.CreatedDefault` and a TTY.
- `ney init` refuses to run when stdin is not a TTY, and `promptLine`/`promptDefault` latch on EOF so a default can never be auto-accepted — an exhausted stdin used to answer "y" to every AI-client registration prompt.
- Any command that writes chunks (`index`, `watch`, `mcp`, `reset`) must acquire `internal/lockfile.Acquire(config.NeyDir())` before opening the DB, and release it on exit — two processes running `Index`/`PruneMissing` against the same `index.db` fight over document rows and FTS state. Read-only commands (`search`, `status`) don't need it. `ney mcp` is the one writer that degrades instead of failing: on `lockfile.ErrLocked` it serves read-only (see `acquireWriterLock` in `cmd_mcp.go`).
- Security invariant: everything served to an MCP client must pass BOTH containment AND the secret/dotfile deny. Containment is: inside a served root (`resolveAllowedPath`, symlink-resolved on both sides), OR in the session's `serverState.discovered` set — paths surfaced by a user-directed `search_folder` call (itself bounded to $HOME and secret-blind). The same filter must gate the indexer walk and `internal/scan` — never add a file-discovery path that bypasses `pathfilter`.
- **Only regular files are ever opened.** Every other deny rule matches a *name*, and a name says nothing about what is behind it: a symlink called `readme.md` passes `ExcludedFile`, passes the extension check, reports the link's own tiny size via `DirEntry.Info` — and then `os.ReadFile`/`os.Open` follows it, which is how `~/.ssh/id_rsa` got indexed into FTS and returned as a search snippet. `pathfilter.ExcludedMode` (`!mode.IsRegular()`) is the single rule, applied in the **file branch** of `index.walkIndexable` and `scan.Scan` (it denies directories too, so it must come after the `IsDir()` check) and in `index.IndexPath` via `os.Lstat` — never `os.Stat`, which resolves the link and reports the target as regular. It also covers FIFOs: opening one for reading blocks until a writer appears, so a pipe named `notes.md` would wedge an indexing pass forever.
- The deny rule is evaluated **from $HOME down, not from the served root down** (`excludedForClient` in `mcp_tools.go` — the single chokepoint used by `read_document` AND by search-result emission, so the two can never drift; it takes the resolved `$HOME` as a *parameter* because it runs once per search result and `resolvedHomeDir` lstats every component — resolve it once per call, and don't cache it package-level: tests set a fake `HOME` per test in one process). `pathfilter.ExcludedPath` by design never checks the root itself, so evaluating from the root would let a root that is *itself* hidden (`~/.config`, `~/.ssh`) decapitate the whole dotfile protection. Two layers enforce this: `resolveAllowedScanDir` refuses to admit such a folder as a root at all (`index_folder`/`search_folder`), and `excludedForClient` re-checks at read/emit time to also cover roots that entered the DB from the CLI or an older version.
- `mcpRoot.Internal` marks roots ney registers itself (today only `~/.ney/memory`). It changes ONLY the deny-evaluation base (root-relative instead of $HOME-relative) — never containment. Without it the memory workspace would be unreadable, since it lives under the dotted `~/.ney`. Adding a new internal root means accepting that its own path components go unchecked, so keep the set tiny.
- `read_document` serves only `index.IsSupportedExt` extensions (`.md`/`.markdown`/`.txt`) and reads through a single opened handle validated with `os.SameFile` against the stat taken at containment-check time — closing the check/open swap window. Don't reintroduce a read-by-path-after-validation. Its one-entry content cache (`serverState.readCache`, so paginating a document doesn't re-read it per window) preserves that guarantee by requiring `os.SameFile` between the cached `FileInfo` and the caller's fresh stat, plus a size/mtime match — weaken either and it serves stale or swapped content.
- **Chunk boundaries are versioned.** `store.chunkerVersion` (in `index_meta`) is compared at `Open`; a mismatch blanks `documents.hash` so the next indexing pass re-chunks everything, and `Indexer.Index` calls `FinishChunkerRebuildIfDone` to FTS-`optimize` + `VACUUM` once no blank hashes remain. **Bump it whenever a change to `internal/chunk` makes stored chunks wrong or wasteful** — file contents don't change, so the hash-based skip would otherwise serve the old chunks forever with no signal. Blank hashes (not deleted chunks) on purpose: search keeps answering off the old rows until each document's turn comes, which matters because a *read-only* `ney mcp` may be the process that runs the migration and can never rebuild anything itself.
- `ney mcp`'s stdout is reserved for the MCP protocol, full stop — every diagnostic goes to stderr. Never call `fmt.Println`/`PrintJSON`/the spinner helpers (which write to stdout) from any code path `runMCP` can reach.
- The memory workspace (`~/.ney/memory`, target of `remember`) is registered internally by `runMCP` in BOTH read-write and read-only mode — it's always a served root so `read_document`/`search_documents` can reach it, added directly to the `rootSet` rather than through `index_folder`'s home-directory validation. Only write mode indexes and watches it (an index pass + a watcher through the same per-root pipeline as any other root); in read-only mode it's served but not indexed by this process — a live scan or the lock-holder's watcher covers freshness.
- `get_context`/`list_projects` are stateless: `context.ScanRepos` shells out to `git` live on every call (no DB table, no caching) and any per-repo git error just skips that repo — `get_context` must never return an MCP error. `remember` and `update_profile` (`internal/context`) only ever write plain markdown files (`memoryDir()`, `profile.md`) — no DB writes — so both work correctly when `ney mcp` is running read-only.

**Setup wizard (`cmd/ney/cmd_init.go`, `mcpclients.go`):** bare `ney` on a machine without DB meta `setup_completed` (and zero workspaces) offers the wizard; `ney init` reruns it. Steps: `[1/3]` scan `context.dev_roots` for git repos (prompt for a root if none configured and no `~/workspace`) → pick + index → `[2/3]` bootstrap `~/.ney/profile.md` from the embedded template, asking 2–3 short questions (skipped if a profile already exists — it's user-owned from then on) → `[3/3]` register AI clients (Claude Desktop JSON merge / `claude mcp add` / Codex TOML section — all take the config path as a parameter, back up `.bak` first, and refuse to overwrite unparseable files). Clients are registered args-less (`ney mcp`) with the path from `resolveNeyBinary()` (`cmd/ney/neybin.go`), which prefers the stable PATH entry and **never** calls `EvalSymlinks` — a Homebrew cask's `bin/ney` symlink points into a versioned Caskroom directory that `brew upgrade` deletes. The workspaces table is the single source of truth for what's served.

**Dynamic roots:** `ney mcp`'s served roots live in a `rootSet` (cmd_mcp.go) that `index_folder` appends to mid-session — read_document containment and live-scan targeting must always consult `rs.snapshot()`, never a captured slice.

**Workspace scoping (`cmd/ney/workspace.go`, `sync.go`):** `search` defaults to the workspace whose `root_path` contains the cwd (longest match wins); `--workspace` overrides, `--all` forces global. Before searching, `syncWorkspaceIfKnown` silently re-indexes the cwd workspace (reusing the already-open DB — never open a second writer) and never fails the caller's request. If a scoped search returns nothing, commands fall back to a global search and label results with their workspace. When the scope isn't backed by an indexed workspace yet (or has zero documents), `ney search` additionally runs a tier-0 `internal/scan` pass and appends results labeled `source: "live-scan"`.

**Adding a new loader:** implement `loader.Loader`, register it in `cmd/ney/indexer.go` (order matters for `.md` sniffing), add the extension to `supportedExts` in `internal/index/pipeline.go` if file-based.
