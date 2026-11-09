# Project guide (CLAUDE.md = AGENTS.md)

This file provides guidance to AI coding agents (Claude Code, Codex) working in this repository. `CLAUDE.md` and `AGENTS.md` are byte-identical mirrors — keep them in sync when editing.

## Build & run

```bash
go build -o ney ./cmd/ney   # build binary
go vet ./...                 # lint
go test ./...                # run all tests
go test ./internal/handoff -run TestName   # run a single test
```

No database, no network, no LLM calls anywhere. CI (.github/workflows/release.yml) only cuts GitHub releases (binaries for `scripts/install.sh`; no Homebrew) — there is **no lint/test gate**, so run gofmt + vet + tests locally before pushing.

## Architecture

Ney is **automatic cross-agent session handoff**: open any coding agent in a repo and it already knows what the previous session (in any agent) left unfinished. Ney reads the transcripts the harnesses already write (Claude Code: `~/.claude/projects`; Codex: `$CODEX_HOME` or `~/.codex`, `sessions/`), builds a redacted brief (latest session(s) + live git state + agent-written notes), injects it at SessionStart through a hook, and serves it over MCP. Spec: `docs/superpowers/specs/2026-11-07-session-handoff-design.md`. The document indexing/search stack of earlier versions was removed — do not reintroduce a database.

CLI commands: `init`, `handoff [--full] [--session]`, `hook session-start`, `mcp`, `doctor`, `config`, `version`. MCP tools: `get_handoff`, `list_sessions`, `write_handoff`, `get_context`, `remember`, `update_profile`. Data lives in `~/.ney/`: `config.yaml` (user-owned), `profile.md` (user-owned, AI-editable via `update_profile`), `memory/` (one markdown file per `remember`), `handoff/<repo-key>/` (notes from `write_handoff`).

**Request flows:**

```
Hook:    agent SessionStart → `ney hook session-start --agent X` (JSON on stdin)
         → config.Load → handoff.auto / disabled_repos / source (resume|compact|fork → silent)
         → buildBrief: session sources (Recent/Load) + notes + live git state
         → handoff.Build (redacts) → RenderInject (≤3 KB)
         → stdout: {"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":...}}
         or nothing. Always exit 0.
CLI:     ney handoff [--full] → same buildBrief → RenderInject / RenderFull (≤32 KB)
MCP:     ney mcp → newMCPServer(mcpDeps) → stdio. get_handoff / list_sessions /
         write_handoff take an absolute `cwd`; get_context / remember / update_profile
         use ScanRepos + profile.md + memory/ (no DB).
```

**Package map:**

| Package | Role |
|---|---|
| `cmd/ney/` | Cobra commands; thin wrappers that wire internal packages. `cmd_hook.go` the hook, `cmd_handoff.go` + `handoffsvc.go` (`buildBrief`) the CLI/shared brief path, `cmd_mcp.go` + `mcp_tools.go` + `mcp_handoff.go` the MCP server, `cmd_init.go` + `mcpclients.go` + `hooksreg.go` the setup wizard and hook registration, `cmd_doctor.go` + `doctor_handoff.go` the checks, `neybin.go` binary path resolution |
| `internal/config/` | Config loading (viper) + validation (`config.go`: `handoff.*`, `sources`, `context.*`), and the single config writer `SetDevRoots` (`save.go`) |
| `internal/context/` | `neycontext`: `ScanRepos` (live git scan of `context.dev_roots` through the hardened `gitCmd`), `ResolveRepo`, `LoadProfile`/`UpdateProfile`, `Render`, `WriteMemory`, and the 45s-TTL `ScanCache` that only long-running processes (`ney mcp`) may hold. No DB |
| `internal/session/` | `Source` interface and shared `Session` type; tolerant JSONL reading |
| `internal/session/claudecode/` | Claude Code transcript adapter (`~/.claude/projects/<slug>/*.jsonl`) |
| `internal/session/codex/` | Codex CLI transcript adapter (`sessions/YYYY/MM/DD/rollout-*.jsonl`) |
| `internal/handoff/` | `Build` (pure; selects sessions, applies caps, redacts), `RenderInject`/`RenderFull`, handoff notes (`notes.go`) |
| `internal/redact/` | Best-effort secret redaction (`redact.String`): token shapes, PEM blocks, `Bearer`, values of secret-named keys → `[redacted]` |

## Key constraints

- **The hook never fails.** `ney hook session-start` always exits 0; any error (bad stdin, config, git, panic) is one line on stderr and empty stdout. Its stdout is either nothing or exactly the one JSON object above — nothing else may reach it. It must stay fast (target < 300 ms on a 4,000-line transcript).
- **Redaction chokepoint is `handoff.Build`.** Every string that ends up in a brief passes `redact.String` there and nowhere else — do not redact in adapters or renderers, and do not add a path that puts transcript text in a brief without going through `Build`. Redaction is best-effort; the README says so.
- **Adapters are tolerant and never redact.** Transcript formats are undocumented and change: unknown entry types/fields are skipped, a malformed line is skipped (never fatal), a file with no real user prompt is not a session. Adapters return raw text; only `Build` redacts.
- **Every user-controlled value rendered inline goes through the single `inline()` helper** (newlines → space, leading `#` stripped) so transcript text cannot forge brief structure. The inject brief is labelled as a record of earlier sessions, not instructions.
- Caps: inject brief ≤ 3072 bytes, full ≤ 32768; inject field caps in runes (goal 300, latest ask 300, ended-with 800; files ≤ 10, failed commands ≤ 3, other recent ≤ 2).
- **`ney mcp` and `ney hook` stdout rules.** `ney mcp`'s stdout is reserved for the MCP protocol, full stop; `ney hook`'s stdout is only the hook JSON or nothing. Every diagnostic goes to stderr. Never call `fmt.Println`/`PrintJSON`/spinner helpers from any code path `runMCP` or the hook can reach. `TestMCPStdoutHygiene` guards the MCP side.
- **Settings/hooks file edits** (`~/.claude/settings.json`, Codex `hooks.json`/`config.toml`, via `registerSessionStartHook`, `registerCodex`): back up `.bak` first, refuse unparseable files, preserve all unrelated keys, tolerate `null` values, keep symlinks (write through to the target, never replace the link), no HTML escaping when encoding JSON, atomic write, and idempotent (re-running never duplicates the entry).
- `config.yaml` is user-owned. The only writer is `config.SetDevRoots` (`internal/config/save.go`), a `yaml.Node` edit that preserves comments and unknown keys, re-parses the rendered bytes before committing, and renames atomically. **Never** re-marshal the whole file from a `Config` value, and **never** materialize `context.dev_roots: []` — `Load` uses `v.IsSet("context.dev_roots")` to decide whether to apply `defaultDevRoots()` (`~/workspace`), so an empty sequence permanently suppresses the repo scan.
- Existing users' `config.yaml` may still hold keys of removed features (`retrieval:`, `index:`, `chunking:`, …). `Load` must keep ignoring unknown keys and never validate them.
- `internal/config.Load()` is silent — no stdout, no stderr. It is reachable from `ney mcp` and the hook. First-run hints belong in `cmd/ney/root.go`'s `loadConfig()`, gated on `cfg.CreatedDefault` and a TTY.
- `ney init` refuses to run when stdin is not a TTY, and `promptLine`/`promptDefault` latch on EOF so a default can never be auto-accepted — an exhausted stdin used to answer "y" to every AI-client registration prompt. Each client change is shown and confirmed separately.
- `resolveNeyBinary()` (`cmd/ney/neybin.go`) prefers the stable PATH entry and **never** calls `EvalSymlinks` — a package manager's `bin/ney` symlink (e.g. a Homebrew cask) points into a versioned directory that the next upgrade deletes. Clients are registered args-less (`ney mcp`) and the hook as `ney hook session-start --agent <agent>`.
- `ScanCache` belongs to long-running processes only (`ney mcp`); CLI one-shot paths and the hook must call `ScanRepos` directly. `ScanRepos` and live git state go through the hardened `gitCmd`; any per-repo git error just skips that repo. Keep that hardening.
- `get_context` must never fail as an MCP tool: every failure degrades into a note in the rendered text.
- Codex only runs a newly added hook after the user trusts it with `/hooks`; `ney init` says so, do not try to automate it.
