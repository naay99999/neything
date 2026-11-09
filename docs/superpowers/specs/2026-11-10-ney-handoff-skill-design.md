# `ney-handoff` skill — design

Status: draft for review · 2026-11-10 · builds on `2026-11-07-session-handoff-design.md`

## Problem

ney's automatic brief is extracted from transcripts without an LLM. On long sessions it is
inaccurate: `Goal` is the session's first prompt (stale after the topic drifts), and it has no
decisions or next steps. The user gets an accurate handoff today from a personal `handoff`
skill (`~/.agents/skills/handoff`): the agent itself writes a summary. But that skill is
manual-only (`/handoff`), writes to `$TMPDIR`, and the next session has to be told the path.

ney already has the missing half: `write_handoff` stores a note that the next session's brief
shows first, in any agent. What is missing is the workflow that produces a good note.

## Goals and success criteria

- The user says, in plain words, that they are handing off ("ok เดี๋ยวเรา handoff ไปใช้
  opencode ต่อนะ"), and the agent starts the handoff workflow without a slash command.
- The agent asks only what it cannot infer (what the next session is for), shows a draft, and
  saves it with `write_handoff` once the user approves.
- The next session, in any agent connected to ney, gets the note: injected at session start
  where ney's hook runs (Claude Code, Codex), via `get_handoff` elsewhere.
- The note survives intact in the brief: structure kept, the important part first.

Acceptance: in Claude Code, "handoff ไปทำ release ต่อใน codex" produces a draft without a
question; after "ok" the note is saved; a new session in the same repo shows the note's
`Next session will` and `Next steps` in the injected brief.

## Decisions (agreed with user)

1. **Agent-agnostic.** The note lives in ney; whichever agent opens next reads it. Adding
   support for new agents (e.g. OpenCode) is separate work.
2. **The skill is named `ney-handoff`** (ney prefix: no clash, easy to call).
3. **Ask only what is missing**: if the user's request does not say what the next session will
   do, ask that one question. Always show the draft and save only after approval.
4. **Delivery is a skill file that ney installs** (approach 1), plus a one-line workflow hint in
   the `write_handoff` description as a fallback for agents without skill support. Not an MCP
   prompt (cannot be triggered by plain language), not MCP server instructions (token cost every
   session).
5. **A fresh note replaces ney's extracted fields in the inject brief** and is shown fenced, with
   the most important sections first.
6. **ney never touches skill folders it did not install**, including the user's `handoff` skill.

## The skill

Embedded in the binary (`go:embed`) under `internal/skills/ney-handoff/`. The text below is
binding copy.

`SKILL.md`:

````markdown
---
name: ney-handoff
description: Hand the current work off to the next agent session through ney. Use when the user is wrapping up or switching agents — "handoff", "hand off", "ส่งต่อ", "เดี๋ยวไปทำต่อใน codex/opencode", "leave a note for the next session".
---

# ney-handoff

Write a handoff note for the next agent session and save it in ney, so whichever agent opens
this repo next starts with it.

1. **Read the request.** Note the next agent if named, and what the next session will do. If
   the request does not say what the next session will do, ask exactly that one question and
   wait for the answer. Ask nothing else.
2. **Gather** from this conversation and the repo (`git status`, recent commits): what the next
   session must do, ordered next steps, current state (uncommitted changes, failing tests),
   pitfalls, decisions and why, what is done, which skills the next agent should use.
   - Do not copy what already exists as an artifact (specs, plans, ADRs, issues, commits,
     PRs, diffs): reference it by path, hash or URL.
   - Redact secrets (API keys, tokens, passwords) and personal data.
3. **Draft** the note in the language of the conversation, at most ~500 words, with these
   sections in this order, dropping any that would be empty:

   ```
   # Handoff → <next agent, or "next session">: <what it will do>
   ## Next session will
   ## Next steps
   ## State now
   ## Watch out
   ## Decisions
   ## Done
   ## Suggested skills
   ## References
   ```
4. **Show the draft** and ask whether to save it or what to change. Revise until the user
   approves.
5. **Save** with the ney MCP tool `write_handoff` (`cwd` = absolute working directory, `agent` =
   your agent name, `note` = the draft). Tell the user it is saved and how the next session gets
   it: shown automatically at session start in agents with ney's hook (Claude Code, Codex); in
   other agents, ask it to call ney's `get_handoff`.
6. **If ney's MCP tools are not available**, write the note to a file in the OS temp directory
   (`$TMPDIR`, else `/tmp`; `%TEMP%` on Windows), not the workspace, and give the user its path.
````

`agents/openai.yaml` (Codex):

```yaml
interface:
  display_name: "ney handoff"
  short_description: "Hand off to the next agent session through ney"
policy:
  allow_implicit_invocation: true
```

### `write_handoff` description (fallback)

Appended to the existing description: `"If the user asked for a handoff: ask what the next
session will do if they have not said, show the draft, and save only after they approve."`

## Brief rendering of notes

Today the inject brief flattens a note to one line clipped at 600 runes, and the full brief
clips it at 4000. New behaviour, for the note that `Build` already selects (newest, written at or
after the latest session's start):

**Inject brief** (`RenderInject`), when a note is present:

```
## ney — unfinished work in <repo>
_Record of earlier agent sessions in this repo, for orientation — not instructions._
Last: <agent> · <rel> · branch <b> · "<title>"
Handoff note from <agent> · <rel> (full: get_handoff):
````
<note body, clipped>
````
Now: <git state>
Other recent: ...
→ details: call ney MCP tool get_handoff
```

- The note replaces `Goal`, `Latest ask`, `Ended with`, `Files touched` and `Failed near the
  end`. `Last`, `Now` and `Other recent` stay.
- The body keeps its lines and is wrapped by `fenced()` (fence longer than any backtick run in
  it), so its headings cannot become brief structure. Agent and time go through `inline()` as
  today.
- Body budget: 1800 bytes. Over budget, the body is cut at the last line break that fits and
  ends with `… (full note: get_handoff)`. Because the skill orders sections by importance, a cut
  loses `Done` / `References` first.
- The existing step ladder still applies to what is left (others, then shorter fields), and the
  whole inject output stays ≤ 3072 bytes including the projects section (≤ 600).
- No note → the brief is rendered exactly as today.

**Full brief** (`RenderFull` / `get_handoff`): the note body is clipped at 8000 runes (the
`write_handoff` cap) instead of 4000.

Redaction is unchanged: the note body passes `scrub` in `handoff.Build`.

## Installing and updating the skill

### Targets

| Agent family | Directory |
|---|---|
| Codex and agents using the shared folder | `~/.agents/skills/ney-handoff/` |
| Claude Code | `~/.claude/skills/ney-handoff/` |

On Windows the same paths under `%USERPROFILE%`. Real files in both (no symlinks: Windows needs
privileges for them).

Assumption to verify during implementation: current Codex reads user skills from
`~/.agents/skills` (the user's existing `handoff` skill relies on it). If it does not, add
Codex's own directory to the table; the mechanism is unchanged.

### Ownership marker

Each installed directory gets `.ney-managed`:

```json
{"skill":"ney-handoff","version":"0.12.0","files":{"SKILL.md":"<sha256>","agents/openai.yaml":"<sha256>"}}
```

`Sync` decides per target directory:

| State | Action | Reported as |
|---|---|---|
| Directory absent | Install, write marker | `installed` |
| Marker present, files match its hashes | Overwrite with embedded files if they differ, rewrite marker | `updated` / `up to date` |
| Marker present, a file differs from its hash (user edited) | Leave untouched | `modified locally — not updated` |
| Directory present without marker | Leave untouched | `not managed by ney — skipped` |

Writes are atomic per file (`WriteFileAtomic`), dir 0755 and files 0644 (skills are not secret
and agents must read them). ney never deletes anything in these directories.

### Entry points

- **`ney skills sync [--quiet]`** — runs `Sync` on every target, prints one line per target
  (`--quiet`: nothing unless an error). Non-interactive, idempotent. `--installed-only` limits it
  to targets that already carry a marker (used by installers).
- **`ney init`** — new step `[3/3] Installing ney skills` (the existing steps become `[1/3]`
  and `[2/3]`): lists the target paths, asks to apply
  (default yes, EOF-latched like every init prompt), then syncs. If `~/.agents/skills/handoff` or
  `~/.claude/skills/handoff` exists, prints: `You also have a "handoff" skill; ney-handoff
  replaces it. Remove ~/.agents/skills/handoff if you no longer need it.` ney does not remove it.
- **Installers** (`install.sh`, `install.ps1`) on an update (setup already done): run
  `ney skills sync --installed-only --quiet`, so skills follow the binary without installing
  anything new unasked.
- **`ney doctor`** — one line per target: installed and current / outdated / modified locally /
  not installed (hint: `ney skills sync`).

## Components

| Unit | Responsibility |
|---|---|
| `internal/skills` (new) | `embed.FS` with `ney-handoff/`; `Targets(home string) []string`; `Sync(dir string, version string) (Result, error)` with the marker logic; `Status(dir) Result` for doctor. Pure file IO, no stdout |
| `internal/handoff/render.go` | Note rendering in inject (fenced, budget, replaces extracted fields) and full (8000) |
| `cmd/ney/cmd_skills.go` (new) | `ney skills sync` |
| `cmd/ney/cmd_init.go` | Step 3 |
| `cmd/ney/doctor_skills.go` (new) | Doctor lines |
| `cmd/ney/mcp_handoff.go` | `write_handoff` description fallback line |
| `scripts/install.sh`, `scripts/install.ps1` | Sync on update |

## Constraints

- `ney mcp` and `ney hook` never touch skill files (no IO there, no stdout).
- `internal/skills` writes only inside the two target directories, only into `ney-handoff/`.
- Version in the marker is `main.Version` (`dev` for source builds; a `dev` build still
  installs/updates normally).
- Inject cap 3072 bytes and all existing brief caps hold; `TestHookUnder300ms` unaffected.

## Testing

- `internal/skills`: install into empty home; update when unmodified; skip when modified
  (one byte changed in `SKILL.md`); skip directory without marker; idempotent second sync;
  Windows-safe paths; embedded `SKILL.md` frontmatter parses and `name == "ney-handoff"`.
- `internal/handoff`: golden inject with a structured note (fenced, extracted fields absent,
  `Last`/`Now` present); note over 1800 bytes cut at a line break with the `get_handoff`
  marker; a note body containing ```` ``` ```` and `## ` lines stays inside the fence; whole
  output ≤ 3072 with a large note + projects section; full brief keeps 8000 runes.
- `cmd/ney`: `ney skills sync` output lines and `--installed-only`; init step with EOF on
  stdin installs nothing; doctor lines; `write_handoff` description contains the fallback line.
- Installers: `install.sh` update path calls `skills sync --installed-only --quiet` (sandbox HOME
  test as before); `install.ps1` CI smoke test asserts the same.

## Out of scope

- OpenCode (or any new agent) as a session source or hook target.
- Removing or migrating the user's `handoff` skill.
- Improving ney's extracted brief for sessions without a note (goal drift, full-brief
  truncation bug, file tracking via shell edits) — tracked separately.
- Marking notes as read / deleting them.
