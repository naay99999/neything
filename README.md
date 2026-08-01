# Ney

**A personal context server for AI.** Ney is a local-first MCP server + CLI that gives AI clients (Claude Code, Claude Desktop, Cursor) a memory that survives across sessions and tools: who you are, what you're working on, and where the knowledge lives. Your files never leave your machine, and secret files are never indexed or served.

The daily loop: at the start of a session, your AI calls `get_context` and instantly knows your profile and which projects you've touched recently. It drills down with `search_documents` when it needs specifics, and saves decisions or facts for next time with `remember`.

```bash
claude mcp add ney -- ney mcp
```

Or better: just install and type `ney` — the **setup wizard** scans your dev folders for git repos, lets you pick which to index, bootstraps your profile (`~/.ney/profile.md`) with a couple of quick questions, and registers ney with your AI clients (Claude Desktop, Claude Code, Codex) automatically. Zero configuration, permanently: no model server, no API key, nothing to tune. Search is keyword (SQLite FTS5) and runs the moment the server starts.

---

## Use cases

What this actually looks like day to day. In each case you're just talking to your AI client — ney is the thing that makes the answer possible.

**1. Start a session without re-explaining yourself**

> **You:** what was I working on?
>
> *AI calls `get_context` → gets your profile plus every project with recent git activity*
>
> **AI:** You're on `neything` (branch `main`, dirty, last commit 2h ago — "Performance overhaul"), and you touched `client-portal` on Tuesday. Your profile says you're mid-migration to the MCP-first architecture. Want to pick up the perf work?

No "let me look around the repo first", no re-pasting context you already gave it yesterday.

**2. Find the one fact buried in three years of notes**

> **You:** how much did we charge for invoice order-1233?
>
> *AI calls `search_documents("order-1233")` → keyword search (FTS5) across every indexed workspace*
>
> **AI:** 4,200 THB, paid by Alice — from `billing/2024-invoices.md:31`.

Exact-token lookups (order numbers, error codes, names) work with **zero setup** — the keyword index needs no API key and no model server.

**3. Decide something in one AI client, have another one know it**

> **You (in Claude Code):** we're standardising on flock for the writer lock instead of pid checks — remember that.
>
> *AI calls `remember{title, content, project}` → writes `~/.ney/memory/2026-08-01-writer-lock-flock.md`*
>
> — later, in Claude Desktop —
>
> **You:** why did we pick flock again?
>
> *AI calls `search_documents` → finds the memory file*
>
> **AI:** Because a recycled pid could wedge the lock, and flock is kernel-released when the holder dies.

Memory is a folder of plain markdown files, shared by every MCP client on the machine — not a per-app silo you can't read or grep yourself.

**4. Find a file when you can't remember where you put it**

> **You:** I downloaded a quotation from the vendor last month, can't find it
>
> **AI:** I don't see it in the indexed workspaces. Where might it be — Downloads, Desktop, or a project folder?
>
> **You:** probably Downloads
>
> *AI calls `search_folder("~/Downloads", "quotation vendor")` → live filesystem scan, no indexing needed*
>
> **AI:** Found `~/Downloads/acme-quotation-mar.md`. Want me to read it?

The AI asks *you* where to look rather than crawling your disk on its own — and hidden/secret folders are refused outright even if it tries.

**5. Make a folder permanently searchable, mid-conversation**

> **You:** index my meeting notes at ~/Documents/meetings so you can search them from now on
>
> *AI calls `index_folder("~/Documents/meetings")` → indexed, watched for changes, added to the workspace table*
>
> **AI:** Indexed 214 files (1,830 chunks). It's searchable now — and it'll stay that way for every AI client you connect, no restart needed.

**6. Keep an Obsidian vault answerable**

```bash
ney index ~/Obsidian/vault --workspace vault
```

> **You:** what did I conclude about the pricing experiment?
>
> **AI:** *(searches `vault`)* Your note `experiments/pricing-q2.md` says the 20% tier lifted conversion but cut ARPU 8% — you flagged it "not worth shipping".

Wikilinks (`[[note]]`) and Notion-export property tables are both understood as markdown variants.

**7. Let your profile evolve instead of rotting**

> **You:** I've moved off the billing project, I'm on infra now
>
> *AI calls `update_profile("Current focus", ...)` → rewrites that one section of `~/.ney/profile.md`*

Next session, `get_context` leads with the new focus. The file is yours — plain markdown you can edit by hand any time.

---

## Install

**macOS / Linux — curl:**
```bash
curl -sSL https://raw.githubusercontent.com/naay99999/neything/main/scripts/install.sh | sh
```
The installer verifies the release SHA-256 before installing and offers to run `ney init` for you.
Pin a version with `NEY_VERSION=v0.1.0`, change the target with `NEY_INSTALL_DIR=~/bin`, or skip the
setup prompt with `NEY_NO_INIT=1`. There is no Windows build — use WSL2 or `go install`.

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

Single binary, no runtime dependencies. Then run `ney init` for guided setup.

---

## MCP — connect your AI

`ney mcp` starts answering the moment it starts up, and layers in better results as indexing catches up in the background:

- **Tier 0 (live scan)** — instantly, before anything is indexed: filename + content grep straight off disk.
- **Tier 1 (keyword/FTS)** — seconds after startup, once the initial parse → chunk → SQLite FTS5 pass finishes for a root. `index_status` reports which roots are still scanning, so a client can tell partial results from final ones.

**Claude Code:**
```bash
claude mcp add ney -- ney mcp
```

**Claude Desktop** (`claude_desktop_config.json`):
```json
{
  "mcpServers": {
    "ney": {
      "command": "ney",
      "args": ["mcp"]
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
      "args": ["mcp"]
    }
  }
}
```

Clients are registered args-less: what gets served comes from the workspaces table in `~/.ney/index.db` (built by the wizard or `ney index`), plus whatever a client asks for at runtime via `index_folder`. Pass `--root <path>` to index+serve a folder directly, e.g. for a one-off machine without a prior `ney index`.

### The 9 MCP tools

| Layer | Tool | What it does |
|---|---|---|
| L1 | `get_context` | Call this first, every session: a small markdown blob — your profile, recently active projects (git activity across `context.dev_roots` + indexed workspaces), and how to dig deeper. Never fails. |
| L1.5 | `list_projects` | Full detail per project: path, branch, dirty state, last commit, indexed?, doc/chunk counts. Use when `get_context`'s one-liner isn't enough. |
| Write | `remember` | Save a fact or decision — `{title, content, project?, tags?}` — as a markdown file in `~/.ney/memory`. Ney derives the filename; no path argument. Searchable within moments via `search_documents`, even in read-only mode. |
| Write | `update_profile` | Edit one section of `~/.ney/profile.md` (replace or append), so `get_context` reflects it next session. Read-only-safe. |
| L2 | `search_documents` | Keyword search (SQLite FTS5) across every indexed workspace, including memory. When it finds nothing it returns a `next_step` telling the client what to do instead. |
| L2 | `search_folder` | Whole-machine fallback: live-scans a folder you point it at when the index has no match — bounded, home-directory-only, secret-blind. Files it surfaces become readable via `read_document` for the session. |
| L2 | `read_document` | Read one file's text (`.md`/`.markdown`/`.txt`), windowed by offset/length. |
| Mgmt | `index_folder` | Ask your AI to "index this folder" and it becomes a permanent, watched workspace — searchable from then on, no config edits or restarts. Hidden/secret folders and `$HOME` itself are refused. |
| Mgmt | `index_status` | Per-workspace document/chunk counts, which roots are still scanning, read-only mode, watcher state. |

### Security

Ney is built to hand an AI *your documents* — not your secrets, and not the rest of your disk:

- **Root containment** — `read_document` only serves files inside served workspace roots (including the built-in `~/.ney/memory` root), plus files a user-directed `search_folder` call surfaced this session. Absolute paths, `..` traversal, and symlinks pointing outside a root are all rejected (paths are symlink-resolved on both sides before comparison).
- **Secret files are never indexed or served** — dotfiles/dot-directories (`.env`, `.ssh/`, `.git/`) plus a built-in deny list of secret-looking names (`*secret*`, `*credential*`, `*password*`, `*.key`, `*.pem`, `id_rsa*`, `*.env`, `known_hosts*`, `authorized_keys*`, `*.netrc`, `*.kubeconfig`, keystores, ...) are excluded everywhere: the indexer, the live scan, and `read_document`. This is always on and cannot be disabled.
- **A hidden folder can never *become* a root** — `index_folder` and `search_folder` refuse a target whose own path is hidden or secret-looking (`~/.config`, `~/.ssh`, `~/.aws`), including via a symlink or `..` that lands there, and refuse `$HOME` itself as a wholesale index target. The deny rule is evaluated from your home directory down rather than from the root down, so pointing ney at a hidden folder can't lift the protection for everything beneath it. Both layers are enforced independently — admission time *and* read time — so a root added by an older version or straight from the CLI is still denied at read.
- **Reads are narrow by construction** — `read_document` serves `.md`, `.markdown`, and `.txt` only (the same set the indexer accepts), caps file size, and reads through a single validated file handle so the file it checked is the file it read.
- **Your own excludes** — add glob patterns under `index.exclude` in the config (see below). Files that were indexed before a pattern matched them are pruned automatically on the next index pass.
- **Local-first, unconditionally** — ney makes no network calls at all. There is no provider to configure, so there is no path by which your documents leave the machine.

### Concurrent access

Only one writer process (`index`, `watch`, `mcp`, `reset`) holds `~/.ney/writer.lock` at a time. If a second `ney mcp` starts while one is already running — say Claude Desktop and Claude Code both spawn one — the second **serves read-only** instead of failing: `get_context`, `search_documents`, `read_document`, `remember`, and `update_profile` all still work off the existing index and by writing plain files directly; indexing and watching stay with the first process. `index_status` reports `mode: "read-only"` so clients can tell, and `index_folder` is declined in that mode. `ney index`/`ney reset` from another terminal still fail fast with the holder's pid rather than racing the first process's writes. Read-only commands (`search`, `status`) never need the lock.

The lock is a kernel `flock` held on an open file descriptor, so the OS releases it the moment the holding process exits — including a crash or `kill -9`. There is no stale-lock state to clean up by hand, and a leftover `writer.lock` file carries no authority on its own.

---

## CLI

The same engine is usable directly from the terminal. First run:

```bash
ney            # no arguments — offers the guided setup wizard
```

The wizard walks three short steps: **[1/3]** scan `context.dev_roots` (default `~/workspace` if present) for git repos and pick which to index, **[2/3]** bootstrap `~/.ney/profile.md` with a couple of quick questions (skipped if a profile already exists — it's yours from then on), **[3/3]** register the AI clients found on your machine. Rerun it anytime with `ney init` — it only ever writes the one config key it asked you about, so your `config.yaml` edits survive.

Manual usage:

```bash
ney index ~/my-notes
ney search "order-1233"
```

Indexing writes the chunk + keyword (FTS5) index — fast, CPU-only, fully offline. This is enough for exact lookups (order numbers, names, error codes) and is all ney does: the AI client you connect is the part that reasons about what it finds.

**Check everything is ready:**

```bash
ney doctor
```

### Commands

| Command | Description |
|---|---|
| `ney mcp` | Serve the 9 MCP tools (`get_context`, `list_projects`, `search_documents`, `search_folder`, `index_folder`, `read_document`, `remember`, `update_profile`, `index_status`) over stdio — see [MCP](#mcp--connect-your-ai) above |
| `ney init` | Guided setup wizard — discover repos, bootstrap profile, connect AI clients |
| `ney index <path>` | Index files recursively (`.md`, `.markdown`, `.txt`); prunes files that no longer exist |
| `ney watch <path>` | Watch directory and re-index on changes (debounced; Ctrl+C to stop) |
| `ney search "<query>"` | Keyword search (FTS5), grouped by file with snippets; live-scans folders that aren't indexed yet |
| `ney status` | Index stats: files, chunks, DB size, last indexed |
| `ney config` | Print current config (`config show` / `config edit` also work) |
| `ney doctor` | Check config, SQLite, index health, MCP readiness |
| `ney reset` | Clear the index (add `--workspace <name>` for partial reset) |
| `ney version` | Print version |

### Flags (all commands)

| Flag | Description |
|---|---|
| `--workspace <name>` | Target a specific workspace |
| `--top-k <n>` | Number of chunks to retrieve (default: 8) |
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

None. No API keys, no model server, nothing to configure — ney's only dependency is the
filesystem it indexes.

---

## Configuration reference

`~/.ney/config.yaml`

```yaml
# retrieval settings
retrieval:
  top_k: 8                  # chunks retrieved per query

# indexing — extra exclude patterns (globs matched case-insensitively against
# file/dir names), on top of the built-in always-on excludes (dotfiles +
# secret-file names — see Security above)
index:
  exclude: []
  # exclude: ["*.bak", "drafts-*", "node_modules"]

chunking:
  strategy: markdown        # auto | character | sentence | paragraph | markdown | tokenizer
  target_chars: 1200
  overlap_chars: 150
  target_tokens: 300        # tokenizer strategy (~4 chars/token)
  overlap_tokens: 50
  # by_format:              # used when strategy: auto
  #   md: markdown
  #   txt: paragraph

# layered context (get_context / list_projects)
context:
  # dev_roots defaults to ["~/workspace"] if that directory exists, else no
  # repo scan runs — uncomment to override:
  # dev_roots: ["~/workspace", "~/code"]
  active_days: 14           # recency window (days) for "active" projects

telemetry: false            # always off
```

`config.yaml` is yours. `ney init` only ever writes the single key it asked you about
(`context.dev_roots`), preserving your comments and any key it doesn't recognise.

---

## How it works

```
Index:   Files → Loader → Chunker → SQLite + FTS   (instant, CPU-only)
Search:  Query → FTS5 → ranked chunks
Context: get_context → live git scan of dev_roots + indexed workspaces → profile.md → rendered L1 blob
```

- **Loaders** — md-only:

  | Extension | Handling |
  |---|---|
  | `.md`, `.markdown` | Markdown; Obsidian wikilinks (`[[note]]`) and Notion-export property tables are both detected by content and handled as markdown variants — not separate formats |
  | `.txt` | Plain text, no parsing |

  That's the whole supported set. PDF, DOCX, HTML, JSON, and Confluence XML loaders were removed — see [Data & privacy](#data--privacy) for the migration note if you're upgrading from an older ney
- **Path filter** (always on) excludes dotfiles and secret-file patterns from every surface — indexing, live scan, MCP reads, and the admission check that decides whether a folder may become a served root at all
- **Chunkers** split text with format-aware defaults (`chunking.strategy: auto`) — markdown by heading, plain text by paragraph
- **SQLite** (`~/.ney/index.db`) stores metadata, chunk content, and workspace info
- **Context** (`internal/context`) is stateless: `get_context`/`list_projects` scan git repos on disk live, no DB table involved; `remember`/`update_profile` write plain markdown files directly
- **Hash-based skip** — unchanged files are not re-chunked on re-index
- **Incremental sync** — deleted files are removed on re-index; renames detected by content hash
- **Offline by construction** — ney makes no network calls, so it runs air-gapped with no setup
- **Tiered search** — `ney mcp` (and `ney search` on a not-yet-indexed folder) never dead-ends: live filesystem scan → keyword/FTS, whichever tiers are ready, plus a `next_step` when there is genuinely nothing to return

---

## Data & privacy

- All data lives in `~/.ney/` — and ney makes no network calls, so nothing is sent anywhere at all
- `telemetry: false` is the default and cannot be flipped remotely
- Secret files (dotfiles, keys, credentials — see [Security](#security)) are never indexed, never searchable, and never served over MCP
- Only regular files are ever opened. Symlinks are not followed during indexing or scanning — a link's name says nothing about its target, so an innocuously-named one could otherwise pull a file from outside the folder you pointed ney at
- The index holds the full text of everything you indexed, so `~/.ney/` is `0700` **and** its files (`index.db` and its WAL sidecars, `profile.md`, `config.yaml`, `memory/`) are `0600` — one layer isn't enough when a backup, `rsync`, or a container bind-mount can loosen a directory. Files created `0644` by an older ney are tightened in place the next time ney opens them.

**Upgrading from a pre-release build:** semantic search was removed along with every provider. Old `embedder:` / `reranker:` / `vector_store:` keys in `config.yaml` are simply ignored — no migration needed. Delete the now-unused `~/.ney/vectors.bin` and `~/.ney/vectors.hnsw*` by hand if they're lying around. ney also indexes markdown and `.txt` only; if your `index.db` has chunks from the removed PDF/DOCX/HTML/JSON loaders, run `ney reset && ney index <path>`.

A chunker defect fixed on 2026-08-01 had been writing roughly 20x more chunks than it should (see [roadmap](docs/roadmap.md)). Nothing to do about it: the first `ney index` or `ney mcp` after upgrading re-chunks every document and reclaims the disk automatically. Expect that first run to take a few times longer than usual, and `index.db` to end up several times smaller.

---

## Roadmap

Ney is focused on being the best personal context server for AI clients working with your local documents and projects. Next up:

- Auto-injecting a "call get_context first" instruction into client system prompts (e.g. `~/.claude/CLAUDE.md`)
- Better MCP ergonomics: resource templates, per-root tool scoping
- Index compaction and smarter incremental sync

See [docs/roadmap.md](docs/roadmap.md) for history and details.
