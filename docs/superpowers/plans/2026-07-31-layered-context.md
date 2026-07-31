# Implementation plan: layered context (personal context server)

Spec: `docs/superpowers/specs/2026-07-31-layered-context-design.md`
Each step leaves `go build ./... && go vet ./... && go test ./...` green.

## Step 1 — md-only loader cut

Delete: `internal/loader/pdf.go`, `ocr.go`, `pdf_ocr_test.go`, `docx.go`,
`html.go`, `html_test.go`, `json.go`, `confluence.go`, and `exec.go` if OCR
was its only consumer. Keep: `markdown.go`, `markdown_parse.go`,
`obsidian.go`, `types.go`, `registry.go`, `helpers.go`.

- New `internal/loader/text.go`: trivial `.txt` loader (read file, one
  section, no parsing). Register it in `newLoaderRegistry`
  (`cmd/ney/indexer.go`) after the md loaders.
- `supportedExts` (`internal/index/pipeline.go:54`) → `.md`, `.markdown`,
  `.txt`.
- `internal/chunk`: delete the `page` strategy + its resolver mapping (PDF
  only); `.txt` resolves to `paragraph`.
- `internal/config`: drop OCR fields from the Config struct, validation, and
  default-config template; old YAML keys still load (viper skips unknowns).
  Update `ney doctor` / `ney models` output that mentions OCR/PDF.
- Delete now-unused testdata; fix/remove tests referencing deleted loaders
  (`cmd/ney` tests that index pdf/docx fixtures switch to md fixtures).
- Grep gate: `grep -ri "ocr\|pdftoppm\|tesseract\|docx\|confluence" --include=*.go`
  returns nothing outside comments/docs slated for Step 7.

## Step 2 — `internal/context` package (L1 assembly)

New files: `internal/context/projects.go`, `profile.go`, `render.go`, tests.

- `type Project struct { Name, Path, Branch, LastCommitSubject string; LastCommit time.Time; Dirty, Indexed bool }`
- `func ScanRepos(ctx context.Context, roots []string) []Project` — walk each
  root (depth cap 4; skip `node_modules`, `vendor`, dot-dirs) looking for
  `.git` dirs; don't descend into a repo. Per repo, shell out to git
  (`git -C <p> log -1 --format=%ct%x00%s`, `rev-parse --abbrev-ref HEAD`,
  `status --porcelain` head-only); any git error → skip that repo, never
  fail the scan. Sort by LastCommit desc. (git-exec is acceptable: this is a
  dev-machine tool; no git on PATH → empty list, get_context still works.)
- `func LoadProfile(path string) (content string, created bool, err error)` —
  missing file → write embedded template (name/role/style headings) and
  return it with `created=true`.
- `func Render(profile string, projects []Project, activeDays, maxShown int) string`
  — the L1 markdown per spec: profile → active projects (window filter, cap,
  "+N more" remainder line) → static digging-deeper section. Pure function.
- Config (`internal/config`): add `context.dev_roots` (default
  `["~/workspace"]` if it exists, else empty) and `context.active_days`
  (default 14); expand `~` on load.
- Tests: fixture repos via `git init` + committing in `t.TempDir` (skip if no
  git on PATH); broken repo skipped; depth cap; profile template creation;
  Render golden-ish assertions (contains project line, remainder count,
  never errors on empty inputs).

## Step 3 — memory writes (`remember` / `update_profile` core)

New files: `internal/context/memory.go`, `memory_test.go` (+ profile edit in
`profile.go`).

- `type Memory struct { Title, Content, Project string; Tags []string; Date time.Time }`
- `func WriteMemory(dir string, m Memory) (path string, err error)` —
  filename `YYYY-MM-DD-<slug>.md`; slug = lowercased title, non
  `[a-z0-9-]` → `-`, collapse repeats, cap 60 chars; collision → `-2`, `-3`;
  frontmatter (date, project?, tags?) + body; atomic via temp file +
  `os.Rename`; `MkdirAll(dir, 0700)`.
- `func UpdateProfile(path, section, content string, appendMode bool) error` —
  split on `## ` headings; replace named section's body or append content to
  it; section not found → append new `## <section>` at EOF; atomic write.
- Tests: slug sanitization (path separators, `..`, Thai text → safe),
  collision suffix, frontmatter shape, atomicity (no temp leftovers),
  profile section replace/append/create.

## Step 4 — MCP tool surface rewire

Modify: `cmd/ney/mcp_tools.go`, `cmd_mcp.go`, `mcp_test.go`.

- `memoryDir()` = `filepath.Join(config.NeyDir(), "memory")`.
- Startup (`runMCP`): ensure memory dir exists; add it to the `rootSet` in
  BOTH modes (read/serve works read-only). Writer mode only: upsert a
  built-in workspace (name `memory`) so Phase A indexes it and a watcher
  covers it — registered internally, NOT through `index_folder` validation.
- New tools (all registered in `newMCPServer`):
  - `get_context` — `context.ScanRepos(cfg.Context.DevRoots)` ∪ workspace
    roots (mark Indexed by longest-prefix match against `rs.snapshot()`),
    `LoadProfile`, `Render`. Never returns an MCP error; degraded inputs
    noted in the text.
  - `list_projects` — same project set, one detail block per project; folds
    in per-workspace doc counts via `computeWorkspaceInfo`. Delete
    `list_workspaces` (tool + handler + its output struct).
  - `remember` — input `{title, content, project?, tags?}` → `WriteMemory`
    into `memoryDir()`; response includes the path and a note that it is
    searchable shortly. Works in read-only mode (file write only; indexing
    is the lock-holder's watcher's job).
  - `update_profile` — input `{section, content, append?}` →
    `UpdateProfile` on `config.NeyDir()/profile.md`. Read-only-safe, same
    reasoning.
- Update the how-to text in `get_context`'s "Digging deeper" to name the
  real remaining tools.
- Tests (`mcp_test.go`, in-memory transport): full loop — `get_context`
  (contains profile template note + fixture repo name) → `remember` →
  poll `search_documents` until the memory text is found; `list_workspaces`
  gone; `remember` under a second (read-only) server instance still writes.

## Step 5 — `internal/discover` rewrite (repo scan)

Modify: `internal/discover/discover.go`, `discover_test.go`.

- Replace doc-cluster heuristic with a thin wrapper over
  `context.ScanRepos`: `Discover` returns repo candidates
  (`Candidate{Path, Name, LastCommit, DocCount}` — DocCount = count of
  supported files in the repo, for display). Keep the progress callback and
  ctx handling. Delete the concentration/ByExt logic and its tests; new
  tests use git fixtures.

## Step 6 — wizard reflow

Modify: `cmd/ney/cmd_init.go` (+ its tests). `mcpclients.go` unchanged.

- New step order: [1/4] scan `context.dev_roots` (prompt for a root if
  default missing) → render repo picks → index selected → [2/4] bootstrap
  `profile.md` (skip if exists): 2–3 prompts (name/role, current focus,
  working style) written into the template sections → [3/4] register AI
  clients (existing flow, unchanged) → [4/4] optional embedder (existing).
- Delete the OCR step and its brew helpers.
- Memory workspace note printed at the end (where it lives, how `remember`
  works).

## Step 7 — docs + final sweep

- README: reposition (personal context server; layers; tool table; md-only),
  delete OCR/loader sections, add `ney reset` migration note for pre-md-only
  indexes.
- CLAUDE.md: update package map (loader list, discover, new
  `internal/context`), tool list, remove OCR references, add memory-root
  invariant note.
- `docs/roadmap.md`: dated entry for this refocus (what was removed/added),
  mirroring the 2026-07-19 entries' style.
- Final gates: `go vet ./...`, `go test ./...`, grep for removed symbols
  (`list_workspaces`, `ocr`, `page` strategy), and the Step-1 grep gate
  re-run including docs.
