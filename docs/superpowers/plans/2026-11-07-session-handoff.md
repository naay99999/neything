# Session Handoff Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn ney into an automatic cross-agent handoff tool: any new Claude Code / Codex session in a repo starts with a brief of the repo's most recent session, built from the harnesses' own transcripts.

**Architecture:** Delete the document indexing/search stack. Add stateless transcript adapters (`internal/session/...`) behind a `Source` interface, a pure brief builder/renderer (`internal/handoff`) that is the single redaction chokepoint (`internal/redact`), and three delivery surfaces: a SessionStart hook command, a `ney handoff` CLI command, and MCP tools. No database.

**Tech Stack:** Go 1.26, cobra, viper, `github.com/modelcontextprotocol/go-sdk`, `go.yaml.in/yaml/v3`. `modernc.org/sqlite` and `fsnotify` are removed.

**Spec:** `docs/superpowers/specs/2026-11-07-session-handoff-design.md`

## Global Constraints

- Module path `github.com/naay99999/neything`; `internal/context` is imported as `neycontext`.
- No database, no network, no LLM calls anywhere.
- Inject brief hard cap: **3072 bytes**. Full brief hard cap: **32768 bytes**.
- Inject field caps (runes): goal 300, latest ask 300, ended-with 800; files ≤10; failed commands ≤3; other recent ≤2.
- Full brief: last 30 tail steps, oldest dropped first when over cap.
- Default `handoff.auto: true`, `handoff.max_age_days: 14`, `handoff.disabled_repos: []`, `sources: [claude-code, codex]`.
- Hook target latency < 300 ms on a 4,000-line transcript.
- The hook never fails: any error → exit 0, empty stdout, diagnostics to stderr.
- `ney mcp` stdout is protocol-only.
- Every string in a brief passes `redact.String` inside `handoff.Build` — nowhere else.
- Every user-controlled value rendered inline goes through one `inline()` helper (newlines → space, leading `#` stripped).
- Config writes follow the `SetDevRoots` rules (`yaml.Node` edit, never re-marshal, never materialize `context.dev_roots: []`).
- Client config edits: back up `.bak`, refuse unparseable files, idempotent.
- After each task: `go vet ./...` and `go test ./...` green. Do not commit unless the user asked; the commit steps below run only under an explicit user go-ahead for this plan.

## Review Focus

1. **Repo paths with non-alphanumeric characters** (dots, spaces, Thai) — the Claude Code slug maps every non `[A-Za-z0-9]` rune to `-`; sessions must still be found. Test in Task 4.
2. **Very long JSONL lines** (a single tool result of several MB) — line reading must not fail at bufio's 64 KB default; lines up to 16 MB parse, longer lines are skipped. Test in Task 4.
3. **Transcript being written while read** (truncated final line) — the partial line is skipped, the rest of the session still loads. Test in Task 4.
4. **Hook run outside a git repo** (`$HOME`, `/tmp`) — repo falls back to the cwd, no git state, no crash. Test in Task 7.
5. **Existing user SessionStart hooks in `~/.claude/settings.json`** — merge preserves them; re-running `ney init` never adds a second ney hook. Test in Task 8.

---

### Task 1: Remove the indexing/search stack

**Files:**
- Delete: `internal/loader/`, `internal/chunk/`, `internal/index/`, `internal/watch/`, `internal/scan/`, `internal/search/`, `internal/citation/`, `internal/store/`, `internal/lockfile/`, `internal/discover/`, `internal/pathfilter/` (its secret-name rule is superseded by Task 2's key/value redaction)
- Delete: `cmd/ney/cmd_index.go`, `cmd_search.go`, `cmd_watch.go`, `cmd_reset.go`, `cmd_status.go`, `indexer.go`, `retrieve.go`, `sync.go`, `workspace.go`, `app.go`, `spinner.go` (if unused after removal), and their `_test.go` files, plus `mcp_security_*_test.go`
- Rewrite: `cmd/ney/mcp_tools.go` (keep only `get_context`, `remember`, `update_profile`), `cmd/ney/cmd_mcp.go` (no lock, no roots, no indexing)
- Modify: `internal/config/config.go` (drop `Retrieval`, `Index`, `Chunking`; drop `DBPath`), `internal/context/projects.go` + `render.go` (drop `Project.Indexed`), `cmd/ney/cmd_init.go` (steps become `[1/2] profile`, `[2/2] clients`; drop `stepRepos`, `indexSelected`, `workspaceNameFor`, `setupCompleted`'s DB check → file check of `~/.ney/profile.md`), `cmd/ney/cmd_doctor.go` (drop DB/index checks), `cmd/ney/root.go`, `go.mod`/`go.sum`
- Modify: `CLAUDE.md`, `AGENTS.md` (keep identical): replace Architecture/flows/package map/constraints with the new design; delete every invariant about DB, lock, chunker, served roots, pathfilter, read_document
- Test: `cmd/ney/mcp_test.go` (rewrite)

**Interfaces:**
- Produces: `newMCPServer(deps mcpDeps) *mcp.Server` with `type mcpDeps struct { cfg *config.Config; scanCache *neycontext.ScanCache }`; later tasks add fields to `mcpDeps` and tools to `newMCPServer`.
- Produces: `memoryDir() string` unchanged.

- [ ] **Step 1: Rewrite `cmd/ney/mcp_test.go`** with `TestMCPListsOnlyContextTools` (in-memory transport, `ListTools` names == `{"get_context","remember","update_profile"}`), keeping the existing get_context/remember/update_profile behaviour tests that do not touch the DB.
- [ ] **Step 2: Delete the packages and files listed above; rewrite `mcp_tools.go`/`cmd_mcp.go`; trim config, init, doctor, root.** `get_context` uses `scanCache.Get(ctx, cfg.Context.DevRoots)` directly (no workspace union).
- [ ] **Step 3: `go mod tidy`.** Expected: `modernc.org/sqlite` and `github.com/fsnotify/fsnotify` gone from `go.mod`.
- [ ] **Step 4: Verify.** Run `go build -o ney ./cmd/ney && go vet ./... && go test ./...` — all PASS; `./ney --help` lists only `init`, `mcp`, `doctor`, `config`, `version` (plus cobra's `help`/`completion`).
- [ ] **Step 5: Update `CLAUDE.md` and `AGENTS.md`** (identical content) to describe the post-cut state plus the planned handoff packages; `diff CLAUDE.md AGENTS.md` prints nothing.
- [ ] **Step 6: Commit** `refactor: remove document indexing and search stack`

---

### Task 2: `internal/redact`

**Files:**
- Create: `internal/redact/redact.go`, `internal/redact/redact_test.go`

**Interfaces:**
- Produces: `func String(s string) string` — returns `s` with secrets replaced by `[redacted]`.

- [ ] **Step 1: Write failing table test `TestString`**, each case asserting the secret substring is absent and `[redacted]` present:

```go
cases := []struct{ in, secret string }{
    {"export OPENAI_API_KEY=sk-proj-abc123def456ghi789jkl", "sk-proj-abc123def456ghi789jkl"},
    {"key sk-ant-api03-AAAABBBBCCCCDDDDEEEE", "sk-ant-api03-AAAABBBBCCCCDDDDEEEE"},
    {"git clone https://ghp_0123456789abcdefghijklmnopqrstuvwxyz@github.com/x", "ghp_0123456789abcdefghijklmnopqrstuvwxyz"},
    {"github_pat_11ABCDEFG0123456789_abcdefghijklmnop", "github_pat_11ABCDEFG0123456789_abcdefghijklmnop"},
    {"SLACK=xoxb-1234-5678-abcdefgh", "xoxb-1234-5678-abcdefgh"},
    {"aws AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE"},
    {"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig_abc-123", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig_abc-123"},
    {"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXk\n-----END OPENSSH PRIVATE KEY-----", "b3BlbnNzaC1rZXk"},
    {"DB_PASSWORD=hunter2hunter2", "hunter2hunter2"},
    {`{"client_secret": "s3cr3tvalue"}`, "s3cr3tvalue"},
    {"MY_TOKEN: abc.def.ghi", "abc.def.ghi"},
}
```

Plus `TestStringLeavesOrdinaryTextAlone`: `"go test ./... && bun run build"` and `"token budget is 300"` are returned unchanged.

- [ ] **Step 2: Run** `go test ./internal/redact` — FAIL (undefined: String).
- [ ] **Step 3: Implement `String`** with precompiled regexps for the token shapes above, the PEM block (`-----BEGIN [A-Z ]*PRIVATE KEY-----` … `-----END …-----`, whole block replaced), and a key/value rule: a key matching (case-insensitive) `secret|password|passwd|token|api[_-]?key|private[_-]?key|credential|auth` followed by `=`, `:` or `": "` has its value (up to whitespace, quote, or comma) replaced. The bare word "token" not followed by `=`/`:` is untouched.
- [ ] **Step 4: Run** `go test ./internal/redact` — PASS.
- [ ] **Step 5: Commit** `feat(redact): add secret redaction for brief text`

---

### Task 3: Repo identity and live git state

**Files:**
- Create: `internal/context/repo.go`, `internal/context/repo_test.go`
- Modify: `internal/context/render.go` (export `relativeTime` as `RelativeTime`; update callers)

**Interfaces:**
- Consumes: existing hardened `gitCmd(ctx, repo string, args ...string) *exec.Cmd`.
- Produces:

```go
// ResolveRepo returns the main checkout root for dir (a linked worktree maps
// to its main checkout). ok=false when dir is not inside a git repo; root is
// then filepath.Clean(dir).
func ResolveRepo(ctx context.Context, dir string) (root string, ok bool)

type GitState struct {
    Branch       string
    Uncommitted  int // lines of `git status --porcelain`
    CommitsSince int // commits on HEAD after Since
}
func RepoState(ctx context.Context, root string, since time.Time) (GitState, error)

func RelativeTime(t, now time.Time) string
```

- [ ] **Step 1: Write failing tests** using a temp repo built with `git init`/`git commit` (helper sets `GIT_AUTHOR_DATE`/`GIT_COMMITTER_DATE`):
  - `TestResolveRepoSubdir`: `ResolveRepo(ctx, root+"/a/b")` == `(root, true)` (compare via `filepath.EvalSymlinks` on both).
  - `TestResolveRepoWorktree`: after `git worktree add ../wt`, `ResolveRepo(ctx, wt)` == main root.
  - `TestResolveRepoNotGit`: temp dir → `(dir, false)`.
  - `TestRepoState`: two commits at T-2h and T-30m, one untracked file; `RepoState(ctx, root, T-1h)` == `{Branch: "main", Uncommitted: 1, CommitsSince: 1}` (init with `-b main`).
- [ ] **Step 2: Run** `go test ./internal/context -run 'Repo'` — FAIL.
- [ ] **Step 3: Implement.** `ResolveRepo`: `git rev-parse --path-format=absolute --git-common-dir`; if the result's base is `.git`, root = its parent; else (bare/odd) `git rev-parse --show-toplevel`. `RepoState`: `rev-parse --abbrev-ref HEAD`, `status --porcelain`, `rev-list --count --since=<RFC3339> HEAD`.
- [ ] **Step 4: Run** `go test ./internal/context` — PASS.
- [ ] **Step 5: Commit** `feat(context): add repo resolution and live git state`

---

### Task 4: Session model and Claude Code adapter

**Files:**
- Create: `internal/session/session.go`, `internal/session/jsonl.go`, `internal/session/jsonl_test.go`
- Create: `internal/session/claudecode/claudecode.go`, `internal/session/claudecode/claudecode_test.go`, `internal/session/claudecode/testdata/*.jsonl`

**Interfaces:**
- Produces (`internal/session`):

```go
type Ref struct {
    Agent    string    // "claude-code" | "codex"
    ID       string
    Path     string
    Modified time.Time
}
type FileChange struct { Path string; Ops []string; Count int }   // Ops e.g. ["edit","write"]
type CommandRun struct { Command string; Failed bool }
type TailEntry  struct { Kind, Head, Body string; At time.Time }  // Kind: user|assistant|command|file|failure
type Session struct {
    Agent, ID, Title, Branch, Cwd string
    Start, End                    time.Time
    FirstPrompt, LastPrompt       string
    PromptCount                   int
    LastMessage                   string
    Files                         []FileChange // first-touch order
    Commands                      []CommandRun // chronological
    Tail                          []TailEntry  // chronological, full loads only
}
type Source interface {
    Name() string
    Recent(repoRoot string, since time.Time) ([]Ref, error) // newest Modified first
    Load(ref Ref, full bool) (Session, error)               // ErrNotASession when no real user prompt
}
var ErrNotASession = errors.New("session: no user prompt")

// Lines calls fn for each complete line of r; lines over maxLine (16 MiB)
// and a final line without '\n' are skipped.
func Lines(r io.Reader, fn func(line []byte)) error
// TailReader returns a reader over the last n bytes of f, starting after the
// first '\n' in that window (so it never yields a partial first line).
func TailReader(f *os.File, n int64) (io.Reader, error)
```

- Produces (`claudecode`): `func New(projectsDir string) session.Source` (`projectsDir` = `~/.claude/projects`), `func Slug(path string) string`.

- [ ] **Step 1: Write fixtures** (hand-written, no real content) in `testdata/`:
  - `basic.jsonl`: meta user entry (`isMeta:true`), a `<command-name>` user string, two real user prompts, assistant `thinking` + `text`, `tool_use` Edit (`file_path:/repo/a.go`) ×2, Write (`/repo/b.go`), Bash `go test ./...` whose `tool_result` has `is_error:true`, Bash `ls` ok, two `ai-title` entries (last = `"Fix payment flow"`), `gitBranch:"feat/pay"`, `cwd:"/repo"`, one unknown `type:"future-thing"`, final assistant text `"Done: tests still failing in pay_test.go"`.
  - `sidechain.jsonl`: all entries `isSidechain:true`.
  - `metaonly.jsonl`: only meta/command entries.
  - `truncated.jsonl`: `basic.jsonl` minus its final newline, cut mid-object on the last line.
  - `longline.jsonl`: a valid session plus one `tool_result` line of 2 MB and one of 17 MB (generate the long lines in the test, not on disk).
- [ ] **Step 2: Write failing tests:**
  - `TestLoadBasic`: `Title=="Fix payment flow"`, `Branch=="feat/pay"`, `PromptCount==2`, `FirstPrompt`/`LastPrompt` are the two real prompts, `LastMessage=="Done: tests still failing in pay_test.go"`, `Files==[{"/repo/a.go",["edit"],2},{"/repo/b.go",["write"],1}]`, `Commands==[{"go test ./...",true},{"ls",false}]`.
  - `TestLoadSidechainAndMetaOnly`: both return `ErrNotASession`.
  - `TestLoadTruncated`: loads; `PromptCount==2`.
  - `TestLoadLongLines`: 2 MB line parsed (no error), 17 MB line skipped, session loads.
  - `TestLoadPartialMatchesFull`: for `basic.jsonl`, `Load(ref,false)` equals `Load(ref,true)` on every field except `Tail` (empty when not full).
  - `TestSlug`: `Slug("/Users/x/my.app/ไทย repo")=="-Users-x-my-app-----repo"` — every non `[A-Za-z0-9]` rune → one `-`.
  - `TestRecent`: temp `projectsDir` with dirs `Slug(root)`, `Slug(root+"/sub")`, `Slug(root+"-other")`, and a `subagents/` dir; files with set mtimes. `Recent(root, since)` returns only files from the first two dirs whose first `cwd` is inside `root`, excludes `subagents/` and files older than `since`, newest first.
- [ ] **Step 3: Run** `go test ./internal/session/...` — FAIL.
- [ ] **Step 4: Implement.**
  - `Recent`: list `projectsDir`; candidate dirs are `Slug(root)` and names with prefix `Slug(root)+"-"`; for each `*.jsonl` (not recursing) with mtime ≥ since, read the first entry carrying `cwd` and keep it when `cwd == root` or has prefix `root+"/"`.
  - `Load`: non-full reads lines from the file start until the first real prompt, then from `TailReader(f, 256<<10)`; if the file is ≤ 256 KB read it once. Full reads all lines and fills `Tail` (keep last 30).
  - Real prompt: `type=="user"`, `!isMeta`, `!isSidechain`, content is a string or `text` blocks, not starting with `<command-name>`, `<command-message>`, `<local-command-stdout>`, `<local-command-caveat>`, `<system-reminder>`; `tool_result` blocks never count.
  - Files from `tool_use` names `Edit`/`MultiEdit` → `edit`, `Write` → `write`, `NotebookEdit` → `edit` (`input.file_path` / `input.notebook_path`). Commands from `Bash` → `input.command`; `Failed` from the matching `tool_result.is_error` (join by `tool_use_id`).
  - `Start`/`End` from first/last `timestamp`; `Branch` from the last entry's `gitBranch`; `Title` from the last `ai-title` entry's title field (inspect a real transcript's `ai-title` keys with `jq` to name the field; fall back to empty).
- [ ] **Step 5: Run** `go test ./internal/session/...` — PASS.
- [ ] **Step 6: Commit** `feat(session): add session model and Claude Code transcript adapter`

---

### Task 5: Brief builder and renderers

**Files:**
- Create: `internal/handoff/brief.go`, `internal/handoff/notes.go`, `internal/handoff/render.go`, tests `brief_test.go`, `notes_test.go`, `render_test.go`, goldens `testdata/inject.golden`, `testdata/full.golden`

**Interfaces:**
- Consumes: `session.Session`, `session.Ref`, `neycontext.GitState`, `neycontext.RelativeTime`, `redact.String`.
- Produces:

```go
type Note struct { Agent string; At time.Time; Body string }
type Brief struct {
    RepoName string
    Primary  *session.Session    // nil when no session in window
    Others   []session.Session   // ≤2, title/agent/time used
    Note     *Note               // newest note, only if newer than Primary.End (or Primary nil)
    Git      neycontext.GitState
    GitOK    bool
    Now      time.Time
}
func Build(repoName string, sessions []session.Session, notes []Note, git neycontext.GitState, gitOK bool, now time.Time) Brief
func (b Brief) Empty() bool          // Primary == nil && Note == nil
func (b Brief) RenderInject() string // ≤3072 bytes, "" when Empty
func (b Brief) RenderFull() string   // ≤32768 bytes, "" when Empty

func NotesDir(neyDir, repoRoot string) string   // <neyDir>/handoff/<repo-key>
func RepoKey(repoRoot string) string            // same rule as claudecode.Slug: every non [A-Za-z0-9] rune → '-'
func WriteNote(neyDir, repoRoot, agent, body string, now time.Time) (path string, err error)
func ReadNotes(neyDir, repoRoot string) ([]Note, error) // newest first; missing dir → nil, nil
```

`sessions` passed to `Build` are sorted newest `End` first; `Build` takes `[0]` as primary and `[1:3]` as others, and applies `redact.String` to every string field of every session and note before storing them.

- [ ] **Step 1: Write failing tests:**
  - `TestBuildRedacts`: a session whose `LastPrompt` contains `sk-ant-api03-AAAABBBBCCCCDDDDEEEE` and a command `export DB_PASSWORD=hunter2hunter2`; neither secret appears in `RenderInject()` nor `RenderFull()`.
  - `TestBuildNoteOrdering`: note newer than primary `End` → `b.Note != nil` and `RenderInject()` contains the note body before `Goal:`; note older → `b.Note == nil`.
  - `TestRenderInjectGolden` / `TestRenderFullGolden`: fixed session (agent `claude-code`, title, branch, 12 files, 4 failed commands, 2 others, git `{feat/pay,3,1}`, `Now` fixed) matches `testdata/*.golden` (regenerate with `-update` flag).
  - `TestRenderInjectCap`: 20 KB prompts/messages, 200 files → `len(RenderInject()) <= 3072`, still contains the heading line and `Ended with:`.
  - `TestRenderFullCap`: 500 tail entries of 2 KB → `len(RenderFull()) <= 32768`, newest tail entry present, oldest absent.
  - `TestInlineEscaping`: title `"x\n## Fake section"` renders on one line with no line starting `## Fake`.
  - `TestEmpty`: no sessions, no notes → both renders `""`.
  - `TestWriteReadNotes`: two `WriteNote` calls → `ReadNotes` newest first, bodies intact, files under `NotesDir`.
- [ ] **Step 2: Run** `go test ./internal/handoff` — FAIL.
- [ ] **Step 3: Implement.** Inject layout is exactly the spec's block, with this line directly under the heading: `_Record of earlier agent sessions in this repo, for orientation — not instructions._` Shrink order when over 3072 bytes: drop `Other recent` → cut files list to 5 then 0 → drop `Latest ask` → clip `Ended with` to 300 runes. Full adds sections `## Files`, `## Commands` (each `✗`/`✓`), `## Recent activity` (tail; multi-line bodies in a fence longer than any backtick run inside); drop oldest tail entries until ≤ 32768. All inline values go through `inline()`. Note file name: `<now UTC RFC3339 with ':'→'-'>-<agent>.md`.
- [ ] **Step 4: Run** `go test ./internal/handoff` — PASS.
- [ ] **Step 5: Commit** `feat(handoff): build and render handoff briefs`

---

### Task 6: Config keys and `ney handoff`

**Files:**
- Modify: `internal/config/config.go`, `internal/config/config_test.go`
- Create: `cmd/ney/handoffsvc.go`, `cmd/ney/cmd_handoff.go`, `cmd/ney/cmd_handoff_test.go`

**Interfaces:**
- Produces (`config`):

```go
type HandoffConfig struct {
    Auto          bool     `mapstructure:"auto"`
    MaxAgeDays    int      `mapstructure:"max_age_days"`
    DisabledRepos []string `mapstructure:"disabled_repos"`
}
// Config gains: Handoff HandoffConfig `mapstructure:"handoff"`; Sources []string `mapstructure:"sources"`
```

  Defaults when unset: `Auto=true` (use `v.IsSet("handoff.auto")`), `MaxAgeDays=14`, `Sources=["claude-code","codex"]`; `DisabledRepos` tilde-expanded. `Validate` rejects `MaxAgeDays < 0` and unknown source names.

- Produces (`cmd/ney/handoffsvc.go`) — the one place all three surfaces build briefs:

```go
type handoffRequest struct {
    Cwd              string
    ExcludeSessionID string
    SessionID        string // when set, that session is primary
    Now              time.Time
}
func sourcesFor(cfg *config.Config) []session.Source       // claude-code now; codex added in Task 10
func buildBrief(ctx context.Context, cfg *config.Config, req handoffRequest, full bool) (handoff.Brief, string /*repoRoot*/, error)
func listSessions(ctx context.Context, cfg *config.Config, cwd string, limit int) ([]session.Session, string, error)
```

  `buildBrief`: `ResolveRepo` → each source's `Recent(root, now - MaxAgeDays)` → merge newest first → drop `ExcludeSessionID` → `Load` up to 3 (skip `ErrNotASession`, keep going until 3 loaded or refs exhausted; primary loaded with `full`) → `ReadNotes` → `RepoState(root, primary.End)` → `handoff.Build`. A source error is skipped, not returned.

- [ ] **Step 1: Write failing tests:** `TestLoadHandoffDefaults` (empty config → `Auto==true`, `MaxAgeDays==14`, `Sources==["claude-code","codex"]`); `TestLoadHandoffAutoFalse`; `TestValidateUnknownSource`; `TestHandoffCommand` — fake `HOME` with a `~/.claude/projects/<Slug(repo)>/s1.jsonl` copied from Task 4's `basic.jsonl` with its `cwd` rewritten to a temp git repo; running the command with cwd in that repo prints text containing `Fix payment flow` and `Ended with:`; `--full` output contains `## Recent activity`; a repo with no sessions prints `no recent sessions for <repo>` to stderr and exits 0.
- [ ] **Step 2: Run** `go test ./internal/config ./cmd/ney -run 'Handoff'` — FAIL.
- [ ] **Step 3: Implement** config changes, `handoffsvc.go`, and `ney handoff [--full] [--session <id>]` (prints `RenderInject()` by default, `RenderFull()` with `--full`).
- [ ] **Step 4: Run** `go vet ./... && go test ./...` — PASS.
- [ ] **Step 5: Commit** `feat: add handoff config and ney handoff command`

---

### Task 7: `ney hook session-start`

**Files:**
- Create: `cmd/ney/cmd_hook.go`, `cmd/ney/cmd_hook_test.go`, `cmd/ney/hook_bench_test.go`

**Interfaces:**
- Consumes: `buildBrief`, `config.Load`.
- Produces: command `ney hook session-start --agent <claude-code|codex>`; reads stdin JSON `{"session_id","cwd","source","hook_event_name"}`; writes

```json
{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"<RenderInject()>"}}
```

  or nothing.

- [ ] **Step 1: Write failing tests** (run the cobra command in-process with a fake `HOME`):
  - `TestHookInjects`: startup source in a repo with a session → stdout parses as the JSON above, `additionalContext` contains `Fix payment flow`.
  - `TestHookExcludesCurrentSession`: stdin `session_id` equal to the only session's ID → empty stdout.
  - `TestHookSkips`: for each of `source:"resume"`, `source:"compact"`, `handoff.auto:false`, repo in `disabled_repos` → empty stdout, exit 0.
  - `TestHookNeverFails`: stdin `not json`, empty stdin, cwd `/nonexistent`, config.yaml unparseable → exit 0, empty stdout.
  - `TestHookOutsideGit`: cwd = temp non-git dir with a matching Claude session → brief present, no `Now:` line.
  - `BenchmarkHook`: generated 4,000-line transcript (realistic mix of Bash/Edit/tool_result entries) + 2 older sessions; assert in a companion test that one run takes < 300 ms (skip under `-short`).
- [ ] **Step 2: Run** `go test ./cmd/ney -run 'Hook'` — FAIL.
- [ ] **Step 3: Implement.** Wrap the whole run in `defer recover()`; every error path writes one line to stderr and returns nil; the command's `SilenceErrors`/`SilenceUsage` are true; a 2 s context timeout bounds git calls. `Now:` line rendered only when `GitOK`.
- [ ] **Step 4: Run** `go test ./cmd/ney -run 'Hook' && go test -run XXX -bench Hook ./cmd/ney` — PASS, benchmark < 300 ms/op.
- [ ] **Step 5: Commit** `feat: add SessionStart hook command`

---

### Task 8: Register the Claude Code hook in `ney init`; doctor checks

**Files:**
- Create: `cmd/ney/hooksreg.go`, `cmd/ney/hooksreg_test.go`
- Modify: `cmd/ney/mcpclients.go` (drop Claude Desktop entry; Claude Code entry also registers the hook), `cmd/ney/cmd_init.go`, `cmd/ney/cmd_doctor.go`

**Interfaces:**
- Produces:

```go
// hookCommand is the exact command string ney writes, used to detect its own entry.
func hookCommand(neyBin, agent string) string // `<neyBin> hook session-start --agent <agent>` (neyBin shell-quoted when it contains spaces)
// registerSessionStartHook merges a SessionStart command hook into a
// Claude-style hooks file ({"hooks":{"SessionStart":[{"hooks":[{type,command,timeout}]}]}}).
// Backs up to .bak, refuses unparseable JSON, preserves all other keys and
// hooks, replaces an existing ney entry instead of adding a second.
func registerSessionStartHook(path, neyBin, agent string) (changed bool, err error)
func hookInstalled(path, agent string) (installed bool, cmd string)
```

  An existing ney entry is any hook whose command ends with ` hook session-start --agent <agent>`. Timeout written: `10`.

- [ ] **Step 1: Write failing tests:** `TestRegisterHookFresh` (missing file → created with one group); `TestRegisterHookPreservesUserHooks` (existing `SessionStart` group with a user command + unrelated `permissions` key → both preserved, ney added as a new group, `.bak` written); `TestRegisterHookIdempotent` (twice → one ney entry; second call `changed==false`); `TestRegisterHookReplacesStalePath` (old ney path → replaced, still one entry); `TestRegisterHookRefusesInvalidJSON` (file untouched, error); `TestDoctorReportsHook` (doctor output contains `Claude Code hook: installed` / `not installed`, and `Claude Code sessions: <n>` for the fake projects dir).
- [ ] **Step 2: Run** `go test ./cmd/ney -run 'Hook|Doctor'` — FAIL.
- [ ] **Step 3: Implement** and wire into `ney init` step `[2/2]`: for Claude Code, show the two changes (`claude mcp add …` and the settings.json hook entry) and ask once per change via `promptDefault` (EOF never accepts). Doctor adds: hook installed + binary exists; ney MCP registered (`claude mcp get ney` exit 0); sessions found per enabled source; `buildBrief` for cwd non-empty or "no recent sessions".
- [ ] **Step 4: Run** `go vet ./... && go test ./...` — PASS. Manual: `./ney init` in a scratch `HOME` (`HOME=$(mktemp -d) ./ney init`) shows the hook diff.
- [ ] **Step 5: Commit** `feat(init): register Claude Code SessionStart hook; doctor checks`

---

### Task 9: MCP tools `get_handoff`, `list_sessions`, `write_handoff`

**Files:**
- Modify: `cmd/ney/mcp_tools.go`, `cmd/ney/mcp_test.go`
- Create: `cmd/ney/mcp_handoff.go`

**Interfaces:**
- Consumes: `buildBrief`, `listSessions`, `handoff.WriteNote`.
- Produces tools:
  - `get_handoff{cwd string (required), session_id string, full *bool (default true)}` → text of `RenderFull()`/`RenderInject()`; empty brief → text `no recent sessions for <repo>` (not an error).
  - `list_sessions{cwd string (required), limit int (default 10, max 50)}` → JSON array of `{agent,id,title,branch,start,end,prompts}`.
  - `write_handoff{cwd string (required), agent string (required), note string (required, ≤8000 runes)}` → `{path}`.

  `cwd` is required because an MCP server's own cwd is not the client's repo; tool descriptions tell the agent to pass its working directory.

- [ ] **Step 1: Write failing tests** (in-memory transport, fake `HOME`): `TestMCPListsTools` (6 names exactly); `TestGetHandoff` (contains `Fix payment flow`, `## Recent activity`); `TestGetHandoffNoSessions`; `TestListSessions` (2 sessions newest first); `TestWriteHandoffThenGet` (note body appears first in `get_handoff`); `TestWriteHandoffRejectsOversize` (8001 runes → tool error).
- [ ] **Step 2: Run** `go test ./cmd/ney -run 'MCP|Handoff'` — FAIL.
- [ ] **Step 3: Implement** in `mcp_handoff.go`; register in `newMCPServer`.
- [ ] **Step 4: Run** `go vet ./... && go test ./...` — PASS.
- [ ] **Step 5: Commit** `feat(mcp): add get_handoff, list_sessions, write_handoff`

---

### Task 10: Codex adapter and Codex registration

**Files:**
- Create: `internal/session/codex/codex.go`, `codex_test.go`, `testdata/*.jsonl`
- Modify: `cmd/ney/handoffsvc.go` (`sourcesFor` adds codex), `cmd/ney/mcpclients.go`, `cmd/ney/cmd_init.go`, `cmd/ney/cmd_doctor.go`

**Interfaces:**
- Produces: `func New(codexHome string) session.Source`; `func Home() (string, bool)` — literal `$CODEX_HOME` when non-empty and existing, else `~/.codex`; `ok=false` when the dir does not exist.

- [ ] **Step 1: Write fixtures** (`rollout-2026-11-01T10-00-00-<uuid>.jsonl` under `sessions/2026/11/01/`): `session_meta` (`payload.id`, `payload.cwd:"/repo"`, `payload.git.branch:"main"`), user `response_item` messages including injected ones starting with `<environment_context>`, `<user_instructions>`, `# AGENTS.md instructions for`; `function_call` `shell` with `arguments:"{\"command\":[\"bash\",\"-lc\",\"go test ./...\"]}"` and its `function_call_output` reporting exit code 1; `custom_tool_call` `apply_patch` whose input contains `*** Update File: a.go` and `*** Add File: b.go`; assistant message `output_text`; `event_msg` `task_complete` with `last_agent_message`.
- [ ] **Step 2: Write failing tests:** `TestCodexLoad` (`PromptCount` counts only non-injected prompts, `Commands==[{"go test ./...",true}]`, `Files` a.go `edit` + b.go `write`, `LastMessage` = `task_complete.last_agent_message` when present, else last assistant text, `Branch=="main"`, `Title==""`); `TestCodexRecent` (date dirs within `since` only; `cwd` match on `session_meta`); `TestHomeHonorsCodexHome`.
- [ ] **Step 3: Run** `go test ./internal/session/codex` — FAIL.
- [ ] **Step 4: Implement.** Command text: `arguments.command` array → its last element when the array is `[sh|bash|zsh, -lc|-c, X]`, else space-joined; also accept `arguments.cmd` string. Failure: output JSON `metadata.exit_code != 0`, or text matching `(?m)^Exit code: [1-9]`. When `Title` is empty, renderers fall back to `FirstPrompt` clipped to 60 runes (add `TestTitleFallback` in `internal/handoff`).
- [ ] **Step 5: Run** `go test ./...` — PASS.
- [ ] **Step 6: Register in `ney init`.** Codex MCP via existing `registerCodex`; hook via `registerSessionStartHook(<codexHome>/hooks.json, neyBin, "codex")`, then print: `Codex runs new hooks only after you trust them: run /hooks inside Codex once.` Doctor reports Codex hook + sessions.
- [ ] **Step 7: Verify Codex injects hook context.** With Codex CLI available (`codex --version`), in a repo that has a Claude session: start `codex`, trust the hook via `/hooks`, ask "what was left unfinished in this repo?". Pass = the answer cites the brief. If Codex ignores `additionalContext`: change the hook to also print the brief as plain stdout when `--agent codex`, re-test; if still ignored, make `ney init` instead append (with confirmation, `.bak`, idempotent marker `<!-- ney:handoff -->`) to `<codexHome>/AGENTS.md`: `At the start of work in a repository, call the ney MCP tool get_handoff with your working directory.` Record the outcome in the spec's Codex note.
- [ ] **Step 8: Commit** `feat(session): add Codex adapter and Codex hook registration`

---

### Task 11: Docs and real-use acceptance

**Files:**
- Modify: `README.md`, `docs/roadmap.md`, `CLAUDE.md`, `AGENTS.md`

- [ ] **Step 1: Rewrite README** around: what ney does (automatic handoff), install, `ney init`, `ney handoff`, MCP tools, privacy (reads only `~/.claude/projects` and Codex sessions; redaction is best-effort; nothing leaves the machine), config keys.
- [ ] **Step 2: Add a roadmap entry** `2026-11 — Session handoff refocus` summarizing the cut and the new design; list phase 2 (Cursor, recall, lessons).
- [ ] **Step 3: Update `CLAUDE.md`/`AGENTS.md`** to the final package map and invariants (hook never fails; redaction chokepoint in `handoff.Build`; adapters tolerant; stdout rules for `mcp` and `hook`); `diff CLAUDE.md AGENTS.md` empty.
- [ ] **Step 4: Verify** `go build -o ney ./cmd/ney && go vet ./... && go test ./...` — PASS; `./ney init` on the real machine; open a new Claude Code session in a repo with recent work and confirm the brief appears.
- [ ] **Step 5: Commit** `docs: reposition ney as automatic session handoff`
- [ ] **Step 6: Acceptance (user, one week):** new sessions in repos with unfinished work state what was left without the user explaining. Findings feed the next iteration.
