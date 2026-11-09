# Project memory (cross-project cards) — design

Status: draft for review · 2026-11-10 · builds on `2026-11-07-session-handoff-design.md`

## Problem

Session handoff (v0.11) solves continuity *inside one repo across agents*. It does not solve
knowledge *across projects*: in a session in `portfolio`, the agent knows nothing about
`neything` — not that it exists, where it lives, or what it is. The user has to re-explain.

The user wants every agent session to know, cheaply, which projects they have, and to be
able to pull a short summary of any of them on demand, with a pointer to the real files
for deeper digging. Detail stays in the repo; ney holds the table of contents.

Today's partial pieces do not cover it:

- `get_context` lists repos with git activity in the last 14 days (name, path, branch, last
  commit) but has no summary of what a project is, hides paused projects, and is only seen
  when an agent decides to call it — which in practice it rarely does.
- `remember` writes to `~/.ney/memory/` but nothing ever reads those files back. It is
  write-only memory.

## Goals and success criteria

- In any session, in any repo, the agent recognises the user's project names and knows to
  ask ney before guessing.
- `get_project <name>` returns a short card: what it is, where it is, stack, status, which
  files to read for more, and a few durable facts.
- Cards are written and kept current by the agents themselves (ney makes no LLM calls),
  prompted only when a card is missing or stale.
- Token cost is minimal: one short line per session start; cards cost tokens only when
  requested; writes are patches, not rewrites.

Acceptance: in a session in repo A, the user asks about project B by name; the agent calls
`get_project` and answers correctly about B (what it is, path) without the user explaining
and without reading B's files. Cards for the user's active projects exist after a week of
normal use without the user asking for them.

## Decisions (agreed with user)

1. **Main problem is cross-project knowledge.** Same-repo cross-agent continuity is already
   handled by handoff.
2. **Agents write the cards** (not ney extracting from README). ney stays LLM-free; it decides
   *when* to ask for a card, and stores and serves it.
3. **Session start injects one line**: the user's project names plus a pointer to ney.
   Everything else loads on demand. Names are included (≈30–50 extra tokens) so the agent
   recognises a bare project name instead of treating it as an unknown word.
4. **Writes are prompted only when needed**: a card for the current repo is missing or stale.
   No end-of-session (Stop hook) prompts.
5. **Cards are small and structured**, with hard caps; updates are field patches.
6. **Storage is `~/.ney/projects/`**, never inside the user's repos. Plain markdown + YAML
   frontmatter, Obsidian-compatible, with a generated `index.md` using `[[wikilinks]]`.
7. **`get_context` and `remember` are removed.** Project knowledge lives in cards
   (`update_project`), knowledge about the user lives in `profile.md` (`update_profile`).

## User-visible behaviour

### Session start (hook)

The injected `additionalContext` gains a projects section after the handoff brief (or alone,
when there is no handoff to give):

```
## ney — projects
Your projects (memory in ney; call get_project before guessing): neything, portfolio, sia-diao-farm, suan-ops (+6 more)
This project has no ney card yet. Once you understand it, call update_project.
```

- Line 1 lists known projects *other than the current repo*; it is omitted when there are
  none. Names are ordered by most recent activity, capped at `projects.names_in_brief`
  (default 20), with `(+N more)` for the rest.
- Line 2 appears only for the current repo, in one of two forms:
  - missing: `This project has no ney card yet. Once you understand it, call update_project.`
  - stale: `This project's ney card is out of date (<reason>). If anything below changed, call update_project: summary, status, facts.`
    where `<reason>` is e.g. `42 commits since it was written; README.md, go.mod changed`.
  - fresh: no line 2.
- The section is ≤ 600 bytes. The 3072-byte inject cap still holds for the whole output:
  the handoff part is rendered against `3072 − len(projects section)`.

### MCP tools

| Tool | Input | Returns |
|---|---|---|
| `list_projects` | — | Profile headline (first ≤ 300 chars of `profile.md`) and the table of contents: one line per project — `name · path · summary-first-sentence-or "(no card yet)" · last activity` |
| `get_project` | `name` (or absolute `path`) | The card (frontmatter fields + body) plus one live line: branch, last commit (relative), dirty. Unknown name → error listing the closest names |
| `update_project` | absolute `cwd`, optional `summary`, `stack`, `status`, `read_more` (list, replaces), `facts_add` (list), `facts_remove` (list of 1-based indexes as shown by `get_project`) | The updated card. Over-cap input is rejected with which field and by how much |
| `update_profile` | unchanged | unchanged |
| `get_handoff`, `list_sessions`, `write_handoff` | unchanged | unchanged |

Removed: `get_context`, `remember`.

Tool descriptions tell the agent what belongs in a card: durable things someone working in a
*different* project would need — what it is, stack, status, where to read more, and decisions
or constraints. Not session progress (handoff covers it) and not code detail (the files are
the source of truth).

### CLI

- `ney projects` — prints the table of contents (same as `list_projects`).
- `ney projects show <name>` — prints a card (same as `get_project`).
- `ney doctor` — adds: number of cards, and the current repo's card state (missing / stale
  with reason / fresh).

## Data model

### Card file — `~/.ney/projects/<name>.md`

```markdown
---
name: neything
path: /Users/naay/workspace/naay/neything
remote: github.com/naay99999/neything
stack: Go, Cobra, MCP
status: active — v0.11.0 released; designing project memory
commit: dfa6faf2b7...            # HEAD when the card was last written
updated: 2026-11-10T04:12:00Z
updated_by: claude-code
---
Automatic cross-agent session handoff CLI + MCP server. Reads Claude Code / Codex
transcripts and injects a brief at session start.

## Read more
- README.md
- CLAUDE.md

## Facts
- No LLM calls, no DB, no network — by design
- Distributed via curl / PowerShell installer only (Homebrew dropped)
```

Caps (runes): `summary` ≤ 300, `stack` ≤ 100, `status` ≤ 150, `read_more` ≤ 5 entries of
≤ 200 each (repo-relative paths), `facts` ≤ 8 entries of ≤ 150 each. A whole card is
therefore well under 2 KB.

`path`, `remote`, `commit`, `updated`, `updated_by` are written by ney, never by the agent.

The file is the source of truth and the user may edit it by hand. Parsing is tolerant:
unknown frontmatter keys and extra body sections are preserved on rewrite; a card that fails
to parse is reported by `doctor` and treated as missing by the hook (never overwritten
without first being backed up to `<name>.md.bak`).

### Naming and identity

- Card name = repo root directory basename, lower-cased, non-`[a-z0-9-]` runes → `-`.
- Collision (two different repos, same basename): the later one becomes
  `<basename>-<parent-basename>`; still colliding → append `-2`, `-3`.
- A card is matched to a repo by `path` first, then by `remote` (so a moved or renamed
  checkout re-links; `path` is then rewritten on the next update).

### Registry — `~/.ney/projects/.registry.json`

The list of known repos, so the hook never has to scan the disk:

```json
{"repos":[{"path":"/Users/naay/workspace/naay/neything","remote":"github.com/naay99999/neything","name":"neything","last_seen":"2026-11-10T04:12:00Z"}]}
```

- The hook upserts the current repo (`last_seen = now`) on every session start. Best effort:
  any error is ignored; the write is atomic.
- `list_projects` and `ney projects` also merge in `ScanRepos(context.dev_roots)` results
  (repos with a commit in the last 30 days) and upsert them, so projects appear before an
  agent session has been opened there.
- Names in the session-start line = registry entries with a card, plus entries with
  `last_seen` or last commit within 30 days, newest first.

### Index — `~/.ney/projects/index.md`

Regenerated after every card write and by `list_projects` / `ney projects` (not by the hook,
which must stay fast):

```markdown
# Projects
- [[neything]] — Automatic cross-agent session handoff CLI + MCP server. · ~/workspace/naay/neything
- [[portfolio]] — (no card yet) · ~/workspace/naay/portfolio
```

It exists for the user (Obsidian: open `~/.ney` as a vault). ney never reads it.

## Staleness

Computed in the hook for the current repo only, via the hardened `gitCmd`, within the
existing `handoffGitTimeout`:

- `git rev-list --count <commit>..HEAD` ≥ `projects.stale_commits` (default 30), or
- `git diff --name-only <commit> HEAD -- ':(glob)README*' CLAUDE.md AGENTS.md package.json
  go.mod pyproject.toml Cargo.toml` is non-empty, or
- `updated` older than `projects.stale_days` (default 60).

If `<commit>` is unknown to the repo (rebased away, shallow clone), fall back to the age rule
only. Any git error → treat the card as fresh (never nag on errors).

## Components

| Unit | Responsibility | Depends on |
|---|---|---|
| `internal/projects` (new) | `Card` type; `Load`/`Save` (tolerant parse, atomic write, caps); `Patch` (apply an `update_project` input); `Name` (naming + collisions); `Registry` (load/upsert/save); `Index` (render TOC lines and `index.md`); `Staleness` (pure: inputs are counts/changed files/time) | `internal/redact` |
| `internal/handoff` | `Build` gains a `Projects` input (names, current-card state + stale reason) and redacts/`inline`s it like every other brief string; `RenderInject` sizes the projects section first, renders the handoff part within the remaining budget, and places the section after it. `Brief.Empty()` is false when either part has content | — |
| `cmd/ney` | `mcp_projects.go` (three tools), `cmd_projects.go` (CLI), hook wiring (registry upsert, staleness git calls, projects line), doctor checks; removal of `get_context`/`remember` wiring | `internal/projects`, `internal/context` (`ScanRepos`, `gitCmd`, `LoadProfile`) |
| `internal/context` | Loses `Render`/`WriteMemory` (Layer-1 render and memory writer). Keeps `ScanRepos`, `ResolveRepo`, profile, `ScanCache`, git state | — |

## Security and privacy

- **Redaction**: card text comes from agents, not transcripts. `update_project` passes every
  agent-supplied string through `redact.String` before it is stored — this is the card
  chokepoint, the counterpart of `handoff.Build` for briefs. Names in the session-start line
  are directory basenames and go through `handoff.Build`'s existing redaction and `inline()`.
- Every value rendered inline (names, reasons, card fields in `list_projects` lines) goes
  through `inline()` so a card cannot forge brief structure.
- The projects section, like the brief, is labelled as reference, not instructions. Line 2
  is ney's own text (not user- or transcript-controlled).
- Cards live under `~/.ney` (0700 dir, 0600 files). ney never writes inside a repo.
- `config.yaml` stays user-owned; new keys have defaults and are only read.

## Config

```yaml
projects:
  inject: true          # add the projects section at session start
  stale_commits: 30
  stale_days: 60
  names_in_brief: 20
```

`handoff.auto: false` disables the whole hook as today; `projects.inject: false` drops only
the projects section. `handoff.disabled_repos` also silences the projects section for those
repos (and they are not upserted into the registry).

## Hook constraints (unchanged, restated)

Always exits 0; stdout is the one JSON object or nothing; the projects work adds at most two
git calls and two small file reads/one atomic write; target remains < 300 ms on a 4,000-line
transcript. If the projects part fails, the hook still emits the handoff part.

## Testing

- `internal/projects`: parse/serialize round-trip preserving unknown keys and sections;
  caps (exact limits, rune counting, error messages); `Patch` (each field, `facts_add`
  overflow, `facts_remove` bad index); redaction of each field; naming + collisions + remote
  re-link; registry upsert idempotence; index golden; staleness table tests.
- Hook: golden inject outputs for {no card, stale, fresh} × {with handoff, without}; the 3072
  cap with a long names line; disabled repo; git failure → fresh; registry write failure →
  still emits.
- MCP: the three tools via in-memory transport; unknown name suggestions; `TestMCPStdoutHygiene`
  still passes; removed tools are absent.
- Windows: paths in cards and registry (case-insensitive `path` match, forward-slash
  `read_more`), run in the existing Windows CI job.

## Migration

- `get_context` and `remember` disappear from the tool list. Existing `~/.ney/memory/` files
  are left untouched and documented as no longer read.
- No card exists on day one. The first session in each repo gets the "no card yet" line.
- README: replace the `get_context`/`remember` rows, add a "Project memory" section, document
  `~/.ney/projects/` and opening `~/.ney` in Obsidian. CLAUDE.md/AGENTS.md: package map,
  tool list, the new card redaction chokepoint.

## Out of scope

- Extracting summaries from README (option A) — revisit only if cards stay empty in practice.
- Syncing `~/.ney` across machines.
- Cross-project search over transcripts; Cursor as a source (phase 2 of handoff).
- Agent-written knowledge about non-project topics beyond `profile.md`.
