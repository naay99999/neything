# Project Memory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every agent session knows the user's project names and can pull a short, agent-written card for any project from ney; cards are requested only when missing or stale.

**Architecture:** A new pure package `internal/projects` owns the card file format, caps, patching (with redaction), the repo registry, naming, the TOC/index and staleness rules. `internal/context` gains small exported git helpers. `internal/handoff` renders a ≤600-byte projects section inside the existing 3072-byte inject budget. `cmd/ney` wires the hook, three MCP tools, a `ney projects` command and doctor checks, and removes `get_context` and `remember`.

**Tech Stack:** Go, cobra, viper, `github.com/modelcontextprotocol/go-sdk/mcp`, `gopkg.in/yaml.v3` (already in go.mod), git CLI via the hardened `gitCmd`.

**Spec:** `docs/superpowers/specs/2026-11-10-project-memory-design.md`

## Global Constraints

- ney makes no LLM calls, no network calls, uses no database.
- Hook: always exit 0; stdout is exactly one JSON object or nothing; any projects failure must not suppress the handoff part; target < 300 ms (`TestHookUnder300ms` must keep passing).
- Inject output ≤ 3072 bytes total; projects section ≤ 600 bytes.
- Card caps (runes): summary 300, stack 100, status 150, read_more ≤ 5 entries × 200, facts ≤ 8 entries × 150.
- Every agent-supplied card string passes `redact.String` in `projects.(*Card).Apply`; every brief string passes `handoff` scrubbing; every inline value passes `inline()`.
- `~/.ney/projects/` dir 0700, files 0600, writes atomic. ney never writes inside a repo.
- Stale when: commits since card ≥ `projects.stale_commits` (30), or a key file changed, or `updated` older than `projects.stale_days` (60). Git error → fresh.
- Names line: others than the current repo; card holders + entries active within 30 days; newest first; cap `projects.names_in_brief` (20) + `(+N more)`.
- Exact copy:
  - heading `## ney — projects`
  - `Your projects (memory in ney; call get_project before guessing): <names>[ (+N more)]`
  - `This project has no ney card yet. Once you understand it, call update_project.`
  - `This project's ney card is out of date (<reason>). If anything below changed, call update_project: summary, status, facts.`
- Code that builds paths must work on Windows (case-insensitive path match; CI runs Windows).
- `config.yaml` is never written by this feature.
- Commit after each task (Conventional Commits) only if the user has authorized commits for this execution.

## Review Focus

1. A hand-edited card with broken frontmatter: the hook must treat it as missing and never crash; `update_project` must back it up to `<name>.md.bak` before rewriting. → Task 3 test `TestSaveBacksUpUnparseable`, Task 6 test `TestHookBrokenCardIsMissing`.
2. Two repos with the same basename (`~/a/portfolio`, `~/b/portfolio`): distinct names, stable across runs. → Task 4 `TestNameCollision`.
3. Repo moved/renamed on disk: card re-links by remote instead of creating a duplicate. → Task 4 `TestLookupByRemoteAfterMove`.
4. Long names list (30+ repos): section stays ≤ 600 bytes and whole inject ≤ 3072 with a large handoff. → Task 5 `TestInjectBudgetWithManyProjects`.
5. Card text containing a secret or a forged heading (`"## Facts\n- x"` in summary, an `sk-…` key): redacted and flattened. → Task 3 `TestApplyRedactsAndFlattens`.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/config/config.go` (modify) | `ProjectsConfig`, defaults, template comment |
| `internal/context/atomic.go` (modify) | export `WriteFileAtomic` |
| `internal/context/gitinfo.go` (create) | `RepoRemote`, `HeadCommit`, `LastCommit`, `CardDrift` |
| `internal/projects/card.go` (create) | `Card`, `Parse`, `Marshal`, `Load`, `Save`, caps |
| `internal/projects/patch.go` (create) | `Patch`, `(*Card).Apply`, `CapError` |
| `internal/projects/registry.go` (create) | `Registry`, `Entry`, `Upsert`, `Lookup`, `Find`, `NameFor`, `Closest` |
| `internal/projects/index.go` (create) | `Item`, `TOCLine`, `WriteIndex`, `FirstSentence` |
| `internal/projects/stale.go` (create) | `KeyFiles`, `State`, `Staleness` |
| `internal/handoff/projects.go` (create) | `Projects`, `(*Brief).AddProjects`, `projectsSection` |
| `internal/handoff/brief.go`, `render.go` (modify) | `Empty()`, inject budget |
| `cmd/ney/projectsvc.go` (create) | shared logic for hook/MCP/CLI |
| `cmd/ney/cmd_hook.go` (modify) | projects in the hook |
| `cmd/ney/mcp_projects.go` (create) | three MCP tools |
| `cmd/ney/mcp_tools.go` (modify) | remove `get_context`, `remember`, `memoryDir` |
| `cmd/ney/cmd_projects.go` (create) | `ney projects`, `ney projects show` |
| `cmd/ney/doctor_projects.go` (create) | doctor checks |
| `internal/context/render.go`, `memory.go` (delete) | removed Layer-1 render and memory writer |

---

### Task 1: Config `projects:` block

**Files:** Modify `internal/config/config.go`; Test `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.Config.Projects ProjectsConfig` with `Inject bool` (`inject`, default true), `StaleCommits int` (`stale_commits`, 30), `StaleDays int` (`stale_days`, 60), `NamesInBrief int` (`names_in_brief`, 20).

- [ ] **Step 1: Write failing tests** `TestProjectsDefaults` (empty config → true/30/60/20) and `TestProjectsOverrides` (yaml sets `inject: false`, `stale_commits: 5`, `stale_days: 7`, `names_in_brief: 3` → those values); `TestValidateProjects` rejects negative `stale_commits`, `stale_days`, `names_in_brief` with an error naming the key. Use `setHome`.
- [ ] **Step 2:** `go test ./internal/config -run 'Projects'` → FAIL (field missing).
- [ ] **Step 3:** Add the struct, `v.SetDefault` lines, `Validate` checks. In `defaultConfig`, replace the header lines mentioning `get_context, remember` with: `# 'ney mcp' gives Claude Code / Codex session handoff and project memory.` and add a commented `projects:` block listing the four keys with defaults. Remove the `(get_context)` wording above `context:` (the scan now feeds `list_projects`).
- [ ] **Step 4:** `go test ./internal/config` → PASS.
- [ ] **Step 5:** Commit `feat(config): add projects settings`.

### Task 2: Git helpers

**Files:** Create `internal/context/gitinfo.go`, `internal/context/gitinfo_test.go`; Modify `internal/context/atomic.go` (rename `writeFileAtomic` → `WriteFileAtomic`, update callers)

**Interfaces:**
- Produces (package `neycontext`), all via `gitCmd`, never failing loudly:
  - `RepoRemote(ctx context.Context, root string) string` — `origin` URL normalised to `host/owner/repo`: strip scheme, `user@`, trailing `.git`, turn `host:path` into `host/path`, lower-case host. `""` if none.
  - `HeadCommit(ctx context.Context, root string) string` — full SHA or `""`.
  - `LastCommit(ctx context.Context, root string) (subject string, at time.Time, err error)`.
  - `CardDrift(ctx context.Context, root, since string, files []string) (commits int, changed []string, known bool, err error)` — `known=false, err=nil` when `since` is empty or `git cat-file -e <since>^{commit}` fails; else `rev-list --count <since>..HEAD` and `diff --name-only <since> HEAD -- <files...>` (sorted, unique).
  - `WriteFileAtomic(path string, data []byte, perm fs.FileMode) error`.

- [ ] **Step 1: Write failing tests** using a temp repo (`git init`, commits via `git -c user.email=t@t -c user.name=t commit`):
  - `TestRepoRemoteNormalises`: table — `git@github.com:Naay/X.git`→`github.com/Naay/X`, `https://user:tok@GitHub.com/a/b.git`→`github.com/a/b`, `ssh://git@host:22/a/b`→`host:22/a/b`, no remote→`""`.
  - `TestCardDrift`: card at commit A; add 3 commits touching `go.mod` and `x.go` → `commits==3`, `changed==["go.mod"]` with files `[]string{"go.mod","README.md"}`, `known==true`; `since="deadbeef"` → `known==false, err==nil`.
  - `TestLastCommit`: subject and time of HEAD.
- [ ] **Step 2:** `go test ./internal/context -run 'RepoRemote|CardDrift|LastCommit'` → FAIL.
- [ ] **Step 3:** Implement in `gitinfo.go`; the URL normalisation is a pure helper `normaliseRemote(url string) string` tested by the table.
- [ ] **Step 4:** `go test ./internal/context` → PASS.
- [ ] **Step 5:** Commit `feat(context): git helpers for project cards`.

### Task 3: Card model, caps, patch, save

**Files:** Create `internal/projects/card.go`, `patch.go`, `card_test.go`, `patch_test.go`

**Interfaces:**
- Produces:
  ```go
  const (MaxSummary=300; MaxStack=100; MaxStatus=150; MaxReadMore=5; MaxReadMoreLen=200; MaxFacts=8; MaxFactLen=150)
  type Card struct {
      Name, Path, Remote, Stack, Status, Commit, UpdatedBy string
      Updated  time.Time
      Summary  string   // body text before the first "## " section
      ReadMore []string // "## Read more" bullets
      Facts    []string // "## Facts" bullets
      extraFront []*yaml.Node // unknown frontmatter key/value pairs, in order
      extraBody  string       // unknown "## " sections, verbatim, in order
  }
  func Parse(data []byte) (Card, error)
  func (c Card) Marshal() []byte          // layout exactly as in the spec example; empty sections omitted
  func Load(path string) (Card, error)    // errors.Is(err, fs.ErrNotExist) when absent
  func Save(path string, c Card) error    // MkdirAll(dir, 0700); if an existing file fails Parse, copy it to path+".bak" first; WriteFileAtomic 0600
  type Patch struct {
      Summary, Stack, Status *string
      ReadMore    *[]string // replaces
      FactsAdd    []string
      FactsRemove []int     // 1-based, applied before FactsAdd
  }
  type CapError struct{ Field string; Limit, Got int } // Error(): "<field> is <got> characters; the limit is <limit>" (for counts: "<field> has <got> entries; the limit is <limit>")
  func (c *Card) Apply(p Patch) error // all-or-nothing
  ```
- Consumes: `neycontext.WriteFileAtomic`, `redact.String`.

- [ ] **Step 1: Write failing tests:**
  - `TestParseMarshalRoundTrip`: spec example card → Parse → Marshal equals input byte-for-byte.
  - `TestParsePreservesUnknown`: extra frontmatter key `owner: me` and extra section `## Links\n- x` survive Parse→Marshal.
  - `TestParseRejectsNoFrontmatter`: `"hello"` → error.
  - `TestApplyCaps`: summary of 301 runes (Thai text, to prove runes not bytes) → `CapError{Field:"summary",Limit:300,Got:301}`; 9 facts after add → `CapError{Field:"facts",Limit:8,Got:9}`; card unchanged after any error.
  - `TestApplyFactsRemoveThenAdd`: facts `[a b c]`, remove `[2]`, add `[d]` → `[a c d]`; remove `[0]` or `[4]` → error `facts_remove: no fact 4`.
  - `TestApplyRedactsAndFlattens`: summary `"## Facts\n- x sk-ant-api03-AAAA…"` (a token shape `redact` covers) → stored summary has no newline-led `#` heading and contains `[redacted]`; facts entries are flattened to one line (`inline`-equivalent: whitespace runs → one space, leading `#` stripped).
  - `TestSaveBacksUpUnparseable`: existing file `"garbage"` → Save writes the new card and `path+".bak"` contains `"garbage"`; file mode 0600 (skip mode check on Windows).
- [ ] **Step 2:** `go test ./internal/projects` → FAIL (package missing).
- [ ] **Step 3:** Implement. Frontmatter via `yaml.v3` `yaml.Node` (mapping node) so known keys are read/written and unknown pairs kept in order; `updated` in RFC 3339 UTC. Sections split on lines starting with `## `.
- [ ] **Step 4:** `go test ./internal/projects` → PASS.
- [ ] **Step 5:** Commit `feat(projects): card model with caps, patching and redaction`.

### Task 4: Registry, naming, lookup, index, staleness

**Files:** Create `internal/projects/registry.go`, `index.go`, `stale.go` and their `_test.go`

**Interfaces:**
- Produces:
  ```go
  func Dir(neyDir string) string                 // <neyDir>/projects
  func CardPath(dir, name string) string         // <dir>/<name>.md
  type Entry struct { Path, Remote, Name string; LastSeen time.Time `json:"last_seen"` }
  type Registry struct { Repos []Entry `json:"repos"` }
  func LoadRegistry(dir string) (Registry, error) // missing file → empty, nil; corrupt → empty, error
  func (r Registry) Save(dir string) error         // .registry.json, atomic 0600
  func (r *Registry) Upsert(e Entry) Entry         // match Lookup(e.Path, e.Remote); keep existing Name; update Path/Remote when non-empty; LastSeen = later of the two; new entry gets NameFor; returns the stored entry
  func (r Registry) Lookup(path, remote string) (Entry, bool) // path match first (case-insensitive on Windows, filepath.Clean), then non-empty remote
  func (r Registry) Find(nameOrPath string) (Entry, bool)     // name (case-insensitive) or absolute path
  func NameFor(root string, taken map[string]bool) string     // basename lower-cased, non [a-z0-9-] → '-', trimmed of '-'; taken → "<base>-<parent>", then "-2", "-3"…
  func Closest(names []string, q string, n int) []string      // prefix/substring matches first, then smallest edit distance; at most n
  type Item struct { Name, Path, Summary string; HasCard bool; Activity time.Time }
  func FirstSentence(s string) string            // up to and including the first ". " / "。" / end; ≤ 120 runes with "…"
  func TOCLine(it Item, now time.Time) string    // "<name> · <path with ~> · <FirstSentence(summary) | (no card yet)> · <RelativeTime(activity)>"
  func WriteIndex(dir string, items []Item) error // index.md: "# Projects\n" + "- [[<name>]] — <first sentence | (no card yet)> · <path with ~>\n" per item, sorted by name
  var KeyFiles = []string{":(glob)README*", "CLAUDE.md", "AGENTS.md", "package.json", "go.mod", "pyproject.toml", "Cargo.toml"}
  type State int; const (Fresh State = iota; Missing; Stale)
  func Staleness(updated time.Time, commits int, changed []string, known bool, now time.Time, staleCommits, staleDays int) (State, string)
  ```
  Reason format (exact): parts joined by `"; "` — `"<n> commits since it was written"` when `known && commits >= staleCommits`; `"<a>, <b> changed"` when `known && len(changed) > 0` (strip the `:(glob)` magic, show the real file names); `"written <d> days ago"` when `now-updated >= staleDays`. No parts → `Fresh, ""`.
- Consumes: `neycontext.WriteFileAtomic`, `neycontext.RelativeTime` (moved out of `render.go` into `internal/context/reltime.go` in this task, since `render.go` is deleted in Task 8).

- [ ] **Step 1: Write failing tests:** `TestNameCollision` (`/a/portfolio`→`portfolio`, `/b/portfolio`→`portfolio-b`, third → `portfolio-b-2`; re-upserting `/b/portfolio` keeps `portfolio-b`); `TestLookupByRemoteAfterMove` (entry at `/old/x` remote `github.com/u/x`; Upsert `/new/x` same remote → one entry, Path `/new/x`, Name unchanged); `TestRegistryRoundTrip` (Save/Load equal; corrupt file → empty + error); `TestClosest` (`["neything","portfolio","suan-ops"]`, `"neyth"` → first `neything`); `TestLookupWindowsCase` (Windows only, `t.Skip` elsewhere: `C:\\Repo\\X` finds the entry stored as `c:\\repo\\x`); `TestTOCLineAndIndex` (golden strings for card/no-card items, home path shown as `~`); `TestStaleness` table (fresh; 30 commits → `"30 commits since it was written"`; changed `go.mod` → `"go.mod changed"`; both + 75 days → all three parts in that order; `known=false` with 99 commits → only age rule applies).
- [ ] **Step 2:** `go test ./internal/projects` → FAIL.
- [ ] **Step 3:** Implement; move `RelativeTime` to `internal/context/reltime.go` unchanged.
- [ ] **Step 4:** `go test ./internal/projects ./internal/context` → PASS.
- [ ] **Step 5:** Commit `feat(projects): registry, naming, index and staleness`.

### Task 5: Projects section in the inject brief

**Files:** Create `internal/handoff/projects.go`, `internal/handoff/projects_test.go`; Modify `brief.go` (`Empty`), `render.go` (`RenderInject` budget; `RenderFull` unchanged)

**Interfaces:**
- Produces:
  ```go
  type Projects struct {
      Names       []string // newest first, already excluding the current repo
      CardState   string   // "", "missing", "stale"
      StaleReason string
      Max         int      // names cap (projects.names_in_brief); 0 = no cap
  }
  func (b *Brief) AddProjects(p Projects) // scrubs Names/StaleReason with the package's scrub(); stores on b.Projects
  ```
  `Brief.Empty()` returns false if `len(b.Projects.Names) > 0 || b.Projects.CardState != ""`. `RenderInject()` = handoff part rendered against `injectCap - len(section) - 1` (existing step ladder) + `"\n"` + section; section only when no handoff part. Section is built by `projectsSection(p Projects) string`, using `inline()` per name, dropping names from the end into `(+N more)` until ≤ 600 bytes.
- [ ] **Step 1: Write failing tests:** `TestProjectsSectionCopy` (golden: exact copy lines from Global Constraints for missing/stale/none); `TestProjectsSectionCap` (25 names, Max 20 → 20 names + `(+5 more)`); `TestInjectBudgetWithManyProjects` (60 names of 30 chars + a handoff brief from the existing large fixture → total ≤ 3072, section ≤ 600, handoff heading still present); `TestProjectsOnlyBrief` (no sessions, names only → non-empty, starts with `## ney — projects`); `TestProjectsNamesScrubbed` (name `"#evil\nline"` renders as `evil line`). Existing golden tests must still pass unchanged when `Projects` is zero.
- [ ] **Step 2:** `go test ./internal/handoff` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./internal/handoff` → PASS.
- [ ] **Step 5:** Commit `feat(handoff): projects section in the session-start brief`.

### Task 6: Hook wiring

**Files:** Create `cmd/ney/projectsvc.go`; Modify `cmd/ney/cmd_hook.go`; Test `cmd/ney/cmd_hook_projects_test.go`

**Interfaces:**
- Consumes: Tasks 1–5.
- Produces (in `projectsvc.go`, reused by Tasks 7–8):
  ```go
  const projectActiveWindow = 30 * 24 * time.Hour
  func projectsDir() string // projects.Dir(config.NeyDir())
  // hookProjects never returns an error: every failure degrades to less output.
  func hookProjects(ctx context.Context, cfg *config.Config, root string, inRepo bool, now time.Time) handoff.Projects
  // cardState loads the card for e and computes its state.
  func cardState(ctx context.Context, cfg *config.Config, e projects.Entry, now time.Time) (projects.State, string, *projects.Card)
  ```
  `hookProjects`: return zero if `!cfg.Projects.Inject`; load registry (error → empty); if `inRepo`: `Upsert(Entry{Path: root, Remote: RepoRemote, LastSeen: now})`, `Save` (error ignored); names = other entries whose card file exists or `now-LastSeen ≤ 30d`, sorted by `LastSeen` desc; `Max = cfg.Projects.NamesInBrief`; if `inRepo`, `cardState` → `"missing"` (absent or unparseable), `"stale"` + reason, or `""`. `cardState` uses `CardDrift(ctx, path, card.Commit, projects.KeyFiles)`; any error → `Fresh`.
  In `hookSessionStart`: keep the `root` from `ResolveRepo`; after `buildBrief`, `brief.AddProjects(hookProjects(ctx, cfg, root, rootOK, time.Now()))`, inside a `recover` so a panic there still lets the handoff part out; then the existing `Empty()` check.
- [ ] **Step 1: Write failing tests** (pattern of `cmd_hook_test.go`, `setHome`): `TestHookProjectsMissingCard` (repo with no card + one other registered repo → JSON `additionalContext` contains `## ney — projects`, the other name, the missing-card line; registry now contains the current repo); `TestHookProjectsStaleCard` (card with `commit` = first commit, 30 more commits → stale line with `30 commits since it was written`); `TestHookProjectsFresh` (card at HEAD → no card line); `TestHookBrokenCardIsMissing` (card file `"garbage"` → missing line, exit 0); `TestHookProjectsDisabled` (`projects: {inject: false}` → no section, handoff still present); `TestHookProjectsGitFailure` (card with `commit: deadbeef` and `updated` today → fresh); `TestHookDisabledRepoNotRegistered` (`handoff.disabled_repos` contains the repo → no output and registry not written). `TestHookUnder300ms` keeps passing.
- [ ] **Step 2:** `go test ./cmd/ney -run 'HookProjects|HookBroken|HookDisabledRepo'` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./cmd/ney` → PASS.
- [ ] **Step 5:** Commit `feat(hook): inject project names and card nudges`.

### Task 7: MCP tools; remove `get_context` and `remember`

**Files:** Create `cmd/ney/mcp_projects.go`, `cmd/ney/mcp_projects_test.go`; Modify `cmd/ney/mcp_tools.go`, `cmd/ney/projectsvc.go`, `cmd/ney/cmd_init.go` (drop the memory line), `cmd/ney/mcp_test.go`

**Interfaces:**
- Consumes: Tasks 2–6; `mcpDeps.scanCache`.
- Produces (in `projectsvc.go`, reused by Task 8):
  ```go
  // collectItems merges registry + scan (repos with LastCommit within 30 days are upserted
  // with LastSeen = max(LastSeen, LastCommit)), saves the registry, rewrites index.md, and
  // returns items sorted by Activity desc.
  func collectItems(ctx context.Context, cfg *config.Config, scan []neycontext.Project, now time.Time) []projects.Item
  func renderProjectList(profileHeadline string, items []projects.Item, now time.Time) string // "## Who you're working with\n<headline>\n\n## Projects\n- <TOCLine>\n…"
  func renderProject(ctx context.Context, cfg *config.Config, query string, now time.Time) (string, error) // card Marshal() text with frontmatter shown as "key: value" lines, plus "Live: branch <b> · last commit <rel> \"<subject>\" · <n> uncommitted"; no card → "<name> · <path> — no card yet. Read its README.md / CLAUDE.md for details."; unknown → error "no project named %q; closest: a, b, c"
  func updateProjectCard(ctx context.Context, cfg *config.Config, cwd, agent string, p projects.Patch, now time.Time) (string, error) // cwd must resolve to a repo ("cwd is not inside a git repository"); Upsert; Load or new; Apply; set Name, Path, Remote, Commit=HeadCommit, Updated=now, UpdatedBy=agent; Save; WriteIndex; returns renderProject output
  ```
  Profile headline: first 300 runes of `profile.md` with headings and blank lines removed; `(no profile yet — use update_profile)` when empty.
- Tools (descriptions, exact):
  - `list_projects`: `"The user's projects across their machine, one line each: name, path, what it is (from its ney card), last activity. Call when the user mentions a project you don't know, or asks what they are working on."`
  - `get_project` (input `name` string, required): `"Get the ney card for one of the user's projects: what it is, where it lives, stack, status, files to read for detail, key facts, and live git state. Call before guessing about a project the user names. For deeper detail, read the files it points to."`
  - `update_project` (inputs `cwd` required absolute, `agent` required, optional `summary`, `stack`, `status`, `read_more` []string, `facts_add` []string, `facts_remove` []int): `"Create or update the ney card for the project at cwd. Send only fields that changed. A card holds durable things someone working in a DIFFERENT project would need: what it is (summary ≤300 chars), stack (≤100), status (≤150), read_more (≤5 repo-relative paths), facts (≤8, ≤150 chars each: decisions, constraints). Not session progress, not code detail."`
  - `update_profile` description: replace "which get_context reads every session" with "which list_projects shows".
- [ ] **Step 1: Write failing tests** with the in-memory transport helper in `mcp_test.go`: `TestMCPToolList` (exactly `get_handoff, get_project, list_projects, list_sessions, update_profile, update_project, write_handoff`); `TestUpdateThenGetProject` (update with summary/stack/facts_add in a temp repo → get_project by name shows them and `Live:`; `index.md` contains `[[<name>]]`); `TestUpdateProjectCapError` (301-rune summary → tool error text contains `summary is 301 characters; the limit is 300`, card file absent); `TestUpdateProjectNotRepo` (cwd temp dir without git → error); `TestGetProjectUnknown` (`"neyth"` → error contains `closest: neything`); `TestListProjects` (two registered repos, one carded → both lines, `(no card yet)` on the other); `TestMCPStdoutHygiene` updated to call the new tools. Delete `get_context`/`remember` tests.
- [ ] **Step 2:** `go test ./cmd/ney -run 'MCP|Project'` → FAIL.
- [ ] **Step 3:** Implement; register via `registerProjectTools(server, deps)` called from `newMCPServer`; remove `getContext*`, `remember*`, `memoryDir`.
- [ ] **Step 4:** `go test ./cmd/ney` → PASS.
- [ ] **Step 5:** Commit `feat(mcp)!: project tools replace get_context and remember`.

### Task 8: CLI, doctor, cleanup, docs

**Files:** Create `cmd/ney/cmd_projects.go`, `cmd/ney/doctor_projects.go` + tests; Modify `cmd/ney/cmd_doctor.go`; Delete `internal/context/render.go`, `internal/context/memory.go` and their tests; Modify `README.md`, `CLAUDE.md` + `AGENTS.md` (byte-identical), `docs/roadmap.md`

**Interfaces:**
- Consumes: `collectItems`, `renderProjectList`, `renderProject`, `cardState`.
- Produces: `ney projects` (prints `renderProjectList` with an uncached `ScanRepos`), `ney projects show <name>` (prints `renderProject`; unknown → exit 1 with the error); `projectChecks(ctx, cfg, cwd string, now time.Time) []checkResult`: `"Project cards: N"` (OK), and for the current repo `"Project card: fresh"` (OK) / `"Project card: missing"` (not OK, hint `"Open an agent here; it will be asked to write one"`) / `"Project card: stale (<reason>)"` (not OK, same hint).

- [ ] **Step 1: Write failing tests:** `TestProjectsCommandList`, `TestProjectsCommandShowUnknown` (exit error contains `closest:`), `TestDoctorProjectChecks` (missing → `Project card: missing`).
- [ ] **Step 2:** `go test ./cmd/ney -run 'ProjectsCommand|DoctorProject'` → FAIL.
- [ ] **Step 3:** Implement; delete `render.go`/`memory.go` (+tests) and fix compile errors; `go vet ./...` clean.
- [ ] **Step 4:** Docs:
  - README: MCP tools table — remove `get_context`/`remember`, add the three tools; new section "Project memory" (what the session-start line looks like, cards in `~/.ney/projects/`, open `~/.ney` as an Obsidian vault, `ney projects`); Configuration block gains `projects:`; Privacy: ney writes `projects/` under `~/.ney`; Upgrading: `get_context`/`remember` removed, `~/.ney/memory/` no longer read.
  - CLAUDE.md/AGENTS.md: CLI command list (`projects`), MCP tools list, data layout (`projects/`), package map row for `internal/projects`, key constraint "card redaction chokepoint is `projects.(*Card).Apply`", remove `get_context`-must-never-fail and memory mentions. Verify `cmp CLAUDE.md AGENTS.md`.
  - roadmap: a dated "2026-11 — Project memory" entry summarising the spec.
- [ ] **Step 5:** Full verification: `gofmt -l .` empty, `go vet ./...`, `GOOS=windows go vet ./...`, `go test ./...` all PASS.
- [ ] **Step 6:** Commit `feat(cli): ney projects and doctor card checks; docs`.
