# Session handoff: ney as automatic cross-agent continuity — Design

Date: 2026-11-07
Status: Draft — awaiting user review

## Problem

Ney is not used in practice (`~/.ney` does not exist on the author's machine).
Root cause, per the user: **it needs too much manual input.** Memory only grows
when an AI remembers to call `remember` or the user writes notes/profile by hand,
so nothing accumulates. After four pivots (RAG/ask → MCP-first → layered context
→ keyword-only) the product identity is also unclear.

The user's sharpest pain is **handoff**: work is left mid-way in one coding agent
(Claude Code, Codex, Cursor), and opening another agent in the same repo means
re-explaining what was done, what was decided, and what is left.

Comparison point: Asymptote-Labs/agent-beacon already builds a deterministic
handoff brief (`internal/handoff/brief.go`), but it is **push-style** — the user
runs `beacon handoff resume <session> --agent <runtime>` — and requires
installing Beacon's endpoint capture (collector/daemon, sign-in flow defaulting
to Beacon Cloud).

## Positioning

**ney: open any coding agent in a repo and it already knows what you left
unfinished.** Automatic, pull-style handoff.

Differences from Beacon:

1. **No planned handoff.** Every new session in a repo gets a brief at start.
2. **No capture layer.** ney reads the transcripts harnesses already write. No
   daemon, collector, account, or network.
3. **Repo-level continuity.** The brief is built from the repo's most recent
   sessions regardless of which agent produced them, preferring an
   agent-written note when one exists.
4. **Personal layer kept.** `profile.md` and `remember` stay as small extras.

Audience: the author first. Scope follows the author's real workflow, not
harness coverage.

## Decisions (agreed with user)

1. **Primary job = handoff.** Lessons (Beacon-style distill) and cross-session
   recall are deferred to phase 2.
2. **Delivery = both, configurable.** Automatic injection via SessionStart hook
   (default on, disable per repo) **and** MCP tools for detail.
3. **Summarizer = A + C.** A deterministic digest built from transcript + live
   git state is the base (no LLM, no tokens). If an agent wrote a handoff note
   via `write_handoff` written during or after the latest session, it is shown first;
   the digest always follows. LLM-written summaries (session-end `claude -p`)
   are rejected for v1: token cost, latency, silent failure, harness CLI
   dependency.
4. **Cut document indexing.** Remove the indexing/search stack; it is the
   manual-input part and unrelated to handoff. Recoverable from git history.
5. **v1 has no database.** Sessions are read statelessly on demand (like
   `ScanRepos`). FTS returns in phase 2 only if recall needs it.
6. **Sources: Claude Code first, then Codex CLI.** Evidence from the author's
   machine (2026-11-07): 743 Claude Code session files in the last 30 days
   across 39 projects; no `~/.codex/sessions` (Codex Desktop's app-support dir
   holds browser data, not transcripts); no Cursor data. Codex is built and
   tested against fixtures. Cursor is phase 2, when actually used.

## Scope

### Removed

Packages: `internal/loader`, `internal/chunk`, `internal/index`,
`internal/watch`, `internal/scan`, `internal/search`, `internal/citation`,
`internal/store`, `internal/lockfile`, `internal/discover`,
`internal/pathfilter` (see Kept).

Commands: `index`, `search`, `watch`, `reset`, `status` (as an index status
command).

MCP tools: `search_documents`, `search_folder`, `read_document`,
`index_folder`, `index_status`, `list_projects` (folded into `get_context`).

Consequence: no writer lock, no read-only MCP mode, no served-root containment,
no chunker versioning. The corresponding CLAUDE.md/AGENTS.md invariants are
deleted along with the code.

### Kept

- `internal/config` — `index.*` keys removed; `handoff`/`sources` keys added.
  `SetDevRoots` and its rules stay.
- `internal/context` — profile, `remember` (`WriteMemory`), `ScanRepos`,
  `Render`, and the hardened `gitCmd` (reused for live git state).
- `internal/pathfilter` — removed as a package: ney no longer walks or serves
  user files. Its secret-name glob list moves into `internal/redact`, which
  uses it to mask values of secret-named keys.
- MCP server plumbing in `cmd/ney`, stdout-is-protocol rule.
- `mcpclients.go` registration pattern (backup `.bak`, refuse unparseable,
  JSON merge / TOML section), extended to hooks.
- `neybin.go` (`resolveNeyBinary`, never `EvalSymlinks`).

### Added

| Package | Role |
|---|---|
| `internal/session` | `Source` interface and the shared `Session` type |
| `internal/session/claudecode` | Claude Code transcript adapter |
| `internal/session/codex` | Codex CLI transcript adapter |
| `internal/handoff` | Builds and renders briefs |
| `internal/redact` | Secret redaction for every string that enters a brief |

## Components

### `internal/session`

```go
type Source interface {
    Name() string                                        // "claude-code", "codex"
    Recent(repo Repo, since time.Time) ([]Ref, error)    // newest first
    Load(ref Ref) (Session, error)
}

type Session struct {
    Agent, ID, Title, Branch, Cwd string
    Start, End                    time.Time
    FirstPrompt, LastPrompt       string
    PromptCount                   int
    LastMessage                   string        // last assistant text
    Files                         []FileChange  // path, ops, count
    Commands                      []CommandRun  // command, failed bool
    Tail                          []TailEntry   // last N steps (full brief only)
}
```

`Repo` is identified by `git rev-parse --show-toplevel`; a linked worktree maps
to its main checkout (`--git-common-dir`). A session belongs to a repo when its
recorded `cwd` resolves inside that repo.

Adapters must be **tolerant**: transcript formats are undocumented and change.
Unknown entry types and fields are skipped; a malformed line is skipped, never
fatal; a file that yields no real user prompt is not a session.

### `internal/session/claudecode`

- Location: `~/.claude/projects/<slug>/<session-id>.jsonl`, where `<slug>` is
  the cwd with `/` (and `.`) replaced by `-`. Candidate dirs = slugs of the repo
  root and any subdirectory path prefix match; files sorted by mtime.
- Skip `subagents/` directories and entries with `isSidechain: true`.
- Real user prompts: `type:"user"`, not `isMeta`, `message.content` is a string
  (or text blocks), excluding command wrappers (`<command-name>`,
  `<local-command-stdout>`, etc.) and `tool_result` blocks.
- Title: latest `ai-title` entry.
- Branch: `gitBranch` of the last entry.
- Files: `tool_use` named `Edit`/`Write`/`MultiEdit`/`NotebookEdit` →
  `input.file_path`.
- Commands: `tool_use` named `Bash` → `input.command`; failed when the matching
  `tool_result` has `is_error: true`.
- Last message: last assistant `text` block.
- Performance: `Recent` uses directory listing + mtime only. `Load` for the
  inject brief needs the first prompt (head of file) and the rest from the
  tail; read the head until the first real prompt and the last ~256 KB, not the
  whole file. Full brief may read the whole file.

### `internal/session/codex`

- Location: `$CODEX_HOME` or `~/.codex`, `sessions/YYYY/MM/DD/rollout-*.jsonl`.
- Repo match via the session-meta entry's `cwd`.
- Exact field mapping is confirmed against current Codex CLI output during
  planning; tests use scrubbed fixtures.

### `internal/redact`

Applied to every string before it enters a brief (prompts, messages, paths,
commands, tail bodies):

- Known token shapes: `sk-…`, `sk-ant-…`, `ghp_…`/`github_pat_…`, `xox[abp]-…`,
  `AKIA…`, JWTs, PEM private-key blocks, `Bearer …`.
- `KEY=value` / `"key": "value"` where the key name matches the secret-name
  globs (`*secret*`, `*password*`, `*token*`, `*api_key*`, …) → value masked.
- Output marks redactions as `[redacted]`. Redaction is best-effort; this is
  stated in the README.

### `internal/handoff`

`Build(sessions []Session, notes []Note, now GitState) Brief` — pure.

Session selection:
- same repo, excludes the current session ID (passed in by the hook),
- not older than `handoff.max_age_days` (default 14),
- primary = most recent; up to 2 others listed as one line each.

`GitState` (live, via hardened `gitCmd`): current branch, uncommitted file
count, commits since the primary session's end time.

Notes: `write_handoff` writes `~/.ney/handoff/<repo-key>/<RFC3339>-<agent>.md`
where `<repo-key>` is a filesystem-safe form of the repo root path. The newest
note is rendered first when it was written at or after the primary session's
start (i.e. during or after the most recent session); older notes are stale and
hidden.

**`RenderInject()`** — hard cap 3 KB, Thai/English agnostic (content is
whatever the user wrote):

```
## ney — unfinished work in <repo-name>
Last: <agent> · <relative time> · branch <b> · "<title>"
Goal: <first prompt, ≤300 chars>
Latest ask: <last prompt, ≤300 chars>
Ended with: <last assistant message, ≤800 chars>
Files touched: <path (edit×3)>, … (≤10)
Failed near the end: `<command>` (≤3)
Now: <n> uncommitted files, <k> commits since that session
Other recent: <agent> · <time> · "<title>" (≤2)
→ details: call ney MCP tool get_handoff
```

Fields shrink in a fixed order (tail-most first: other recent → files →
latest ask) until the cap is met. Every user-controlled value is rendered
inline-escaped so it cannot forge brief structure (newlines, `##`).

**`RenderFull()`** — hard cap 32 KB: everything above plus all commands with
outcome, all files, and the last ~30 tail steps; oldest tail steps dropped
first.

### `cmd/ney`

| Command | Purpose |
|---|---|
| `ney hook session-start --agent <name>` | Hook entrypoint (below) |
| `ney handoff [--full] [--session <id>]` | Print the brief for cwd's repo (debugging, terminal use) |
| `ney mcp` | MCP server |
| `ney init` | Interactive setup (below) |
| `ney doctor` | Health checks |
| `ney version` | Version |

### Hook flow

```
SessionStart → ney hook session-start --agent claude-code
  stdin JSON: session_id, cwd, source
  source ∈ {resume, compact, fork}     → print nothing
  handoff.auto=false or repo disabled  → print nothing
  else → Recent() per source → Load primary → Build → RenderInject
       → stdout: {"hookSpecificOutput":{"hookEventName":"SessionStart",
                  "additionalContext": "<brief>"}}
```

- **Never fails.** Any error → exit 0 with empty stdout; diagnostics to stderr.
- Target < 300 ms on a 4,000-line transcript.
- Codex: whether its SessionStart hook can inject context is verified during
  planning. If not, fallback is a line in the user's global Codex
  instructions (`AGENTS.md`) telling the agent to call `get_handoff` at the
  start of work in a repo. Verified 2026-11-08 from Codex source
  (codex-rs/hooks output_parser): SessionStart honours
  hookSpecificOutput.additionalContext; no AGENTS.md fallback needed.

### MCP tools (v1)

| Tool | Description |
|---|---|
| `get_handoff(repo?, session_id?, full?)` | Brief for cwd's/given repo; `full` defaults true |
| `list_sessions(repo?, limit?)` | Recent sessions across sources: agent, id, title, branch, time |
| `write_handoff(note)` | Agent-authored handoff note for the current repo |
| `get_context` | Profile + active repos (existing; absorbs `list_projects`) |
| `remember` | Existing |
| `update_profile` | Existing |

`ney mcp` is now a stateless reader plus markdown writer; stdout stays reserved
for the protocol.

### Config

```yaml
handoff:
  auto: true
  max_age_days: 14
  disabled_repos: []        # absolute repo roots
sources: [claude-code, codex]
context:
  dev_roots: [...]          # unchanged semantics
```

`config.yaml` stays user-owned; any new writer follows `SetDevRoots`'s
`yaml.Node` edit rules.

### `ney init`

Each step shows the exact change and asks for confirmation (TTY required, EOF
never auto-accepts — existing rules):

1. Claude Code: `claude mcp add` + SessionStart hook merged into
   `~/.claude/settings.json` (backup `.bak`, refuse unparseable, idempotent —
   never adds a second ney hook).
2. Codex: MCP TOML section + hook or `AGENTS.md` fallback line.
3. Optional profile bootstrap (existing).

### `ney doctor`

Checks: hook present per agent and pointing at an existing binary; each source
directory found with session count; `ney handoff` for cwd produces a brief;
MCP registration present.

## Security

- ney reads only the configured source directories and git metadata of the
  cwd's repo. It does not open arbitrary files referenced in transcripts.
- Every brief string passes `internal/redact`.
- Brief content is escaped so transcript text cannot forge brief structure.
  (It can still contain prompt-injection text written in an earlier session;
  the brief is labelled as a record of a past session, not instructions.)
- Notes and memories are written only under `~/.ney/`.

## Testing

- Scrubbed fixture transcripts per source, including: empty session,
  sidechain-only, meta-only, malformed JSON line, unknown entry types/fields,
  interrupted session (no final assistant message).
- Golden tests for `RenderInject`/`RenderFull`; size caps enforced by test.
- Redaction tests: secrets in Bash commands, stdout, prompts never reach a
  brief.
- Hook tests: malformed stdin, no sessions, disabled repo, resume source → exit
  0 with empty stdout.
- Benchmark: hook on a 4,000-line transcript < 300 ms.
- `go vet ./...` and `go test ./...` green.
- **Acceptance:** one week of real use by the author — opening a new session in
  a repo with unfinished work, the agent correctly states what was left without
  the user explaining.

## Implementation order

1. Remove the indexing/search stack and its commands/tools; update docs
   (CLAUDE.md, AGENTS.md, README, roadmap).
2. `internal/session` + `claudecode` + `redact` + `handoff` + `ney handoff`.
3. `ney hook session-start` + `init` + `doctor`.
4. MCP tools.
5. Codex adapter.

## Phase 2 (out of scope)

- Cursor adapter.
- Recall: search across past sessions (FTS over digests).
- Lessons: distill corrections/conventions from sessions; promote to
  `.agents/skills`.
