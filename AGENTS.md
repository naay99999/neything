# Project guide (CLAUDE.md = AGENTS.md)

This file provides guidance to AI coding agents (Claude Code, Codex) working in this repository. `CLAUDE.md` and `AGENTS.md` are byte-identical mirrors — keep them in sync when editing.

## Build & run

```bash
go build -o ney ./cmd/ney   # build binary
go vet ./...                 # lint
go test ./...                # run all tests
go test ./internal/context -run TestName   # run a single test
```

No database, no network, no LLM calls anywhere. CI (.github/workflows/release.yml) only cuts releases — there is **no lint/test gate**, so run vet + tests locally before pushing.

## Direction

The project is being rebuilt around **automatic cross-agent session handoff**: when a coding-agent session (Claude Code, Codex) ends, ney captures a redacted brief from its transcript, and the next session — in either agent — gets it injected at start. Spec: `docs/superpowers/specs/2026-11-07-session-handoff-design.md`; read its "Scope" section for what is removed vs kept. Planned new packages: `internal/redact` (secret redaction, applied to every string in a brief), `internal/session` (transcript parsing), `internal/handoff` (brief building/storage). Until they land, the codebase is the trimmed context server described below.

## Architecture

Ney is a local-first **personal context server for AI**, MCP-first: the surface is `ney mcp`, which exposes exactly three tools — `get_context`, `remember`, `update_profile`. CLI commands: `init`, `mcp`, `doctor`, `config`, `version`. Data lives in `~/.ney/`: `config.yaml` (user-owned), `profile.md` (user-owned, AI-editable via `update_profile`), and `memory/` (one markdown file per `remember` call).

The document indexing/search stack (SQLite FTS5, loaders, chunkers, watchers, live scan, writer lock, `search_documents`/`read_document`/`index_folder`/…) was removed in the session-handoff refocus — do not reintroduce a database.

**Request flows:**

```
Context: get_context → context.ScanRepos(cfg.Context.DevRoots) live git scan
         (via the 45s-TTL ScanCache in `ney mcp`) + LoadProfile(profile.md)
         → Render. Stateless, never fails.
Write:   remember / update_profile write plain md files directly
         (memoryDir() / profile.md). No other state.
MCP:     ney mcp → newMCPServer(mcpDeps{cfg, scanCache}) → stdio.
```

**Package map:**

| Package | Role |
|---|---|
| `cmd/ney/` | Cobra commands; thin wrappers that wire internal packages. `cmd_mcp.go` + `mcp_tools.go` implement `ney mcp` (`newMCPServer(deps mcpDeps)`); `cmd_init.go` + `mcpclients.go` the setup wizard; `neybin.go` resolves the binary path to register |
| `internal/config/` | Config loading (viper) + validation (`config.go`), and the single config writer `SetDevRoots` (`save.go`) |
| `internal/context/` | Layered-context core (`neycontext`): `ScanRepos` (live git scan of `context.dev_roots`, repos described 8-wide through the hardened `gitCmd`), `LoadProfile`/`UpdateProfile` (`profile.md`), `Render` (markdown blob), `WriteMemory` (`remember`'s md file writer), plus the 45s-TTL `ScanCache` that only long-running processes should hold. Pure — no DB |

## Key constraints

- `config.yaml` is user-owned. The only writer in the codebase is `config.SetDevRoots` (`internal/config/save.go`), a `yaml.Node` edit that preserves comments and unknown keys, re-parses the rendered bytes before committing, and renames atomically. **Never** re-marshal the whole file from a `Config` value, and **never** materialize `context.dev_roots: []` — `Load` uses `v.IsSet("context.dev_roots")` to decide whether to apply `defaultDevRoots()` (`~/workspace`), so an empty sequence permanently suppresses the repo scan.
- Existing users' `config.yaml` may still hold keys of removed features (`retrieval:`, `index:`, `chunking:`, …). `Load` must keep ignoring unknown keys and never validate them.
- `internal/config.Load()` is silent — no stdout, no stderr. It is reachable from `ney mcp`. First-run hints belong in `cmd/ney/root.go`'s `loadConfig()`, gated on `cfg.CreatedDefault` and a TTY.
- `ney init` refuses to run when stdin is not a TTY, and `promptLine`/`promptDefault` latch on EOF so a default can never be auto-accepted — an exhausted stdin used to answer "y" to every AI-client registration prompt. Client config edits back up `.bak` first, refuse unparseable files, and are idempotent.
- `ney mcp`'s stdout is reserved for the MCP protocol, full stop — every diagnostic goes to stderr. Never call `fmt.Println`/`PrintJSON` from any code path `runMCP` can reach. `TestMCPStdoutHygiene` guards it.
- `ScanCache` (`internal/context/scancache.go`) belongs to long-running processes only (`ney mcp`); CLI one-shot paths must call `ScanRepos` directly.
- `ScanRepos` shells out to `git` through the hardened `gitCmd`; any per-repo git error just skips that repo. Keep that hardening when touching it.
- `get_context` must never fail as an MCP tool: every failure (unreadable profile, no repos) degrades into a note in the rendered text.

**Setup wizard (`cmd/ney/cmd_init.go`, `mcpclients.go`):** bare `ney` on a machine without `~/.ney/profile.md` offers the wizard; `ney init` reruns it. Steps: `[1/2]` bootstrap `~/.ney/profile.md` from the embedded template, asking 2–3 short questions (skipped if a profile already exists — it's user-owned from then on) → `[2/2]` register AI clients (Claude Code: `claude mcp add --scope user` plus the SessionStart handoff hook merged into `~/.claude/settings.json` via `registerSessionStartHook` (`hooksreg.go`) / Codex TOML section — each change is shown and confirmed separately, config paths are parameters, `.bak` is written first, and unparseable files are refused). Clients are registered args-less (`ney mcp`) with the path from `resolveNeyBinary()` (`cmd/ney/neybin.go`), which prefers the stable PATH entry and **never** calls `EvalSymlinks` — a Homebrew cask's `bin/ney` symlink points into a versioned Caskroom directory that `brew upgrade` deletes.
