# Layered context: ney as a personal context server — Design

Date: 2026-07-31
Status: Approved (brainstorm session with user)

## Problem

Ney today is a document search engine, but the user (a developer whose docs are
`.md` files scattered across git repos) needs something else: **AI clients that
know who they are, what projects they're working on, and where the knowledge
lives — across every session and every client.** Today each AI session starts
cold; Claude Code memory is per-project and per-tool, and nothing answers
"what am I working on right now?" across repos.

Repositioning: ney becomes a **personal context server for AI** — search stays
as the internal recall mechanism, no longer the product.

## Decisions (agreed with user)

1. **Layered context.** Layer 1 (bootstrap, small) = profile + how-to +
   active-projects list, fetched at session start. Layer 2 (deep, on demand) =
   the existing indexed search over docs + memory. AI drills down only when
   needed.
2. **Delivery = MCP tool only (default).** `get_context` is a tool every
   client calls; ney stays client-agnostic. Embedding a "call get_context
   first" instruction into `~/.claude/CLAUDE.md` etc. is a later `ney init`
   enhancement, not part of this design's core.
3. **Profile ownership: user-edited + AI-proposed.** `~/.ney/profile.md` is a
   plain markdown file the user hand-edits; AI may update it through an
   `update_profile` tool. The file is the single source of truth.
4. **Layer 1 scope**: profile + how-to + active projects (one line per
   project, sorted by git recency, capped). Target ≤ ~50 lines total. Recent
   memories are NOT in L1 — they are reachable via L2 search.
5. **Memory = one markdown file per entry** in `~/.ney/memory/`, written via a
   `remember` tool, with frontmatter (`date`, optional `project`, `tags`).
   Indexed and watched like any workspace, so searchable seconds after write.
6. **Cut hard to md-only.** Delete PDF (+OCR), DOCX, HTML, JSON, Confluence,
   Notion loaders. Keep Markdown (+Obsidian wikilinks) and `.txt`.
7. **Stateless project awareness** (approach 1 of 3 considered). No new DB
   tables: `get_context` scans git repos live on each call. Disk is the state,
   mirroring the Phase-B "diff, don't record" philosophy. If repo count ever
   makes this slow, a `projects` table can replace the data source without
   changing any tool contract.

## Data layout

```
~/.ney/
  profile.md        # user-owned; AI proposes edits via update_profile
  memory/           # built-in workspace: one md file per memory, always indexed + watched
    2026-07-31-ney-pivot-decision.md
  config.yaml       # new section: context.dev_roots, context.active_days
  index.db, vectors.bin|vectors.hnsw, writer.lock   # unchanged
```

Memory file shape:

```markdown
---
date: 2026-07-31
project: neything        # optional
tags: [decision, roadmap] # optional
---
Decided to reposition ney as a personal context server; search becomes
the recall layer. Cut non-md loaders.
```

New config keys:

```yaml
context:
  dev_roots: ["~/workspace"]   # where to scan for git repos
  active_days: 14              # window for "active" in get_context
```

## MCP tool surface (9 tools)

| Layer | Tool | Behavior |
|---|---|---|
| L1 | `get_context` (new) | One small markdown blob: profile + active projects + how-to-dig-deeper. Never fails. |
| L1.5 | `list_projects` (new) | Per-project detail: path, branch, dirty?, last commit (time + subject), indexed?. Replaces `list_workspaces` (removed). |
| Write | `remember` (new) | Args: `title`, `content`, optional `project`, `tags`. Server derives the filename (date + sanitized slug); **no path argument accepted**. |
| Write | `update_profile` (new) | Section-based edit of profile.md: replace or append a named `##` section. |
| L2 | `search_documents`, `read_document`, `search_folder` | Unchanged. |
| Mgmt | `index_folder`, `index_status` | Unchanged. |

### `get_context` output

```markdown
## Who you're working with
(full profile.md content — intended < 40 lines)

## Active projects (git activity, last 14 days)
- neything — ~/workspace/naay/neything · main · 2h ago: "Update README…" · indexed
- foo-api — ~/workspace/naay/foo · feat/x · 3d ago · not indexed
(sorted by last commit desc, max 10; remainder summarized as a count)

## Digging deeper
- search_documents: search docs + memory across all projects
- remember: save a new fact/decision
- list_projects / read_document / index_folder: …
```

Project set = union of (a) git repos found under `context.dev_roots`
(live scan: locate `.git`, read HEAD + last commit — milliseconds per repo)
and (b) indexed workspaces. No cache, no DB writes on the read path.

## Legacy cuts (md-only refocus)

- **Delete loaders**: PDF + the whole OCR path (pdftoppm/tesseract), DOCX,
  HTML, JSON, Confluence XML, Notion export. Keep: Markdown (+Obsidian
  wikilink metadata) and plain `.txt` (zero parse cost, devs have stray
  notes/README.txt).
- **Delete config keys**: `loaders.ocr.*`; chunk strategy `page` (existed for
  PDF). Old configs containing them still load — viper skips unknown keys
  (same policy as the 2026-07 MCP-first refocus).
- **`internal/discover` rewritten**: from "find document clusters under
  Home/iCloud" to "find git repos under dev_roots" (default `~/workspace` if
  present, else wizard asks).
- **Wizard reflow**: scan repos → pick which to index → bootstrap
  `profile.md` (2–3 questions: name/role/working style) → register AI clients
  (unchanged) → optional embedder (unchanged). OCR step removed.
- **`supportedExts`** shrinks to `.md`, `.txt` (+ existing md variants).
- **Migration**: existing indexes containing pdf/docx chunks → document
  `ney reset` + re-index as the upgrade path (acceptable: single-user stage).

## Error handling

Principle: **Layer 1 must never fail.**

- `get_context`: profile.md missing → create from embedded template, note it
  in the output. A repo whose git state can't be read → skipped. Empty
  dev_roots → workspaces only, plus a hint to run `ney init`.
- `remember`: atomic write (temp file + rename); slug collision → `-2`
  suffix.
- **Read-only mode** (writer lock held elsewhere): `remember` and
  `update_profile` **still work** — they write plain md files and never touch
  DB/vectors; indexing the new file is the lock-holder's watcher's job. This
  falls out of the existing architecture for free.

## Security

- `remember`/`update_profile` write to exactly two fixed destinations:
  under `~/.ney/memory/` and `profile.md`. No AI-supplied paths; slugs pass a
  sanitizer (strip separators, `..`, length cap).
- `~/.ney` is a dot-dir, which `pathfilter` denies by design. Add an
  **explicit exemption for the built-in memory workspace root only**, lifting
  only the dotfile rule — secret-name globs still apply inside it. The MCP
  invariant (everything served passes containment + pathfilter) is unchanged;
  the memory root becomes a served root like any other.

## Testing

- Unit: repo scanner (real fixture repos via `git init` in `t.TempDir`),
  context assembly (missing profile / broken repo / empty roots), remember
  (slug, collision, atomicity), update_profile (section replace/append).
- MCP-level: extend the existing harness — one test drives the full loop
  `get_context` → `remember` → `search_documents` finds the new memory.
- Regression: after loader deletion, `go test ./...` green, no dangling
  references (`go vet`, grep for removed symbols).

## Out of scope (this design)

- Auto-injection into client system prompts / CLAUDE.md (later `ney init` step).
- Background daemon / launchd service.
- Projects table in SQLite (upgrade path if live scan gets slow).
- Recency-weighted ranking inside search.
