# Implementation plan: first-run setup wizard + index_folder

Spec: `docs/superpowers/specs/2026-07-19-first-run-setup-design.md`
Each step leaves `go build ./... && go vet ./... && go test ./...` green.

## Step 1 — `internal/discover` package

New files: `internal/discover/discover.go`, `discover_test.go`.

- `type Candidate struct { Path string; DocCount int; ByExt map[string]int }`
- `type Options struct { Roots []string; Filter *pathfilter.Filter; MaxCandidates int }`
  (default roots: `$HOME` + iCloud Drive path if it exists; MaxCandidates
  default 10)
- `func Discover(ctx context.Context, opts Options, progress func(dirs int)) ([]Candidate, error)`
- Walk each root with `filepath.WalkDir`:
  - skip dirs: `Filter.ExcludedDir(name)` (nil-safe), plus junk set
    `{"Library", "node_modules", "vendor", "venv", ".venv", "Applications",
    "Music", "Movies", "Pictures"}` and any dir name ending in
    `.app`, `.photoslibrary`, `.musiclibrary`, `.tv-library`;
    **exception**: when walking `$HOME`, descend into
    `Library/Mobile Documents/com~apple~CloudDocs` only (special-case the
    two intermediate components).
  - Simpler equivalent (choose at impl time): walk `$HOME` with `Library`
    skipped entirely, and walk the iCloud path as a second root.
  - skip files: `Filter.ExcludedFile(name)`; count only
    `index.IsSupportedExt(path)` files.
  - progress callback every 500 dirs; honor ctx cancellation.
- Aggregation: build dir→count map (each file increments every ancestor up
  to its walk root). Candidate selection per root, recursive from the top:
  `pick(dir)`: if some child holds ≥80% of dir's docs → `pick(child)`; else
  emit dir (if DocCount > 0) — then also emit any child subtree whose count
  ≥ 20% of the root total, to surface secondary clusters. Sort by DocCount
  desc, cap at MaxCandidates. Home root itself is never a candidate.
- Tests: temp tree with junk dirs + secrets + docs; heuristic promotes the
  concentrated child; secondary cluster surfaces; ByExt counts; ctx cancel.

## Step 2 — client registration helpers

New files: `cmd/ney/mcpclients.go`, `mcpclients_test.go`.

- `type clientReg struct { Name string; Detected bool; Register func() error; Manual string }`
- `func detectClients(neyBin string) []clientReg` — builds the three entries
  with real paths; each Register closure calls one of:
- `func registerClaudeDesktop(configPath, neyBin string) error` — read JSON
  (missing file → start `{}`), `json.Unmarshal` into `map[string]any`;
  parse error → return error (caller warns + skips). Set
  `mcpServers.ney = {"command": neyBin, "args": ["mcp"]}` preserving all
  other keys. Write `.bak` of the original first (only when file existed),
  then write with original perms (default 0600).
- `func registerCodex(configPath, neyBin string) error` — read TOML as text.
  If a line `[mcp_servers.ney]` exists: replace from that header up to (not
  including) the next line starting with `[` or EOF. Else append:
  `\n[mcp_servers.ney]\ncommand = "<neyBin>"\nargs = ["mcp"]\n`.
  `.bak` first when file existed; create `~/.codex/` only if it already
  exists (missing dir = Codex not installed → Detected=false).
- Claude Code: no file editing — `Register` shells out to
  `claude mcp add --scope user ney -- <neyBin> mcp` (Detected = `claude` on
  PATH). Note: `claude mcp add` errors if the server exists → run
  `claude mcp remove --scope user ney` first, ignoring its error.
- Detection rules: Claude Desktop = its `Application Support/Claude` dir
  exists; Codex = `~/.codex/` exists; Claude Code = binary on PATH.
- `neyBin` = `os.Executable()` resolved.
- Tests (all against temp paths): JSON merge preserves unrelated keys;
  existing ney entry replaced not duplicated; unparseable JSON errors
  without writing; `.bak` created; TOML append vs replace vs rerun-idempotent.

## Step 3 — wizard rework + bare-`ney` trigger

Modify: `cmd/ney/cmd_init.go` (rewrite `runInit`), `cmd/ney/root.go`.

- `root.go` `main()`: when `len(os.Args) == 1`, call `maybeOfferSetup()`:
  open DB best-effort (`store.Open(config.DBPath())`), read meta
  `setup_completed`; if empty and stdin is a TTY → prompt
  "Ney ยังไม่ได้ตั้งค่า — เริ่ม setup เลยไหม? [Y/n]"; Y → run the wizard and
  exit; else fall through to `Execute()` (help). Non-TTY → straight to help.
- `runInit` becomes the 4-step wizard (spec UX). Structure each step as a
  pure-ish helper + thin prompt loop:
  - `stepFolders`: spinner + progress line during `discover.Discover`;
    render candidates (Thai labels, counts by ext, `displayPath`); parse
    selection (`parseSelection(input, n) ([]int, []string)` — numbers,
    `a`, extra paths); validate extra paths (exist, dir, tilde-expand;
    under `/Volumes` → print unplug warning, still allowed).
  - `stepOCR`: skip silently if `loader.OCRToolsAvailable` already true
    (just enable config if disabled); else offer
    `brew install tesseract poppler tesseract-lang` (detect brew on PATH;
    absent → print manual instructions). Run with stdout/stderr streamed.
    On success set config `ocr.enabled: true, lang: tha+eng`.
  - `stepClients`: iterate `detectClients`; per detected client prompt
    `[Y/n]`; call Register; print result. Print `Manual` snippet for
    undetected ones.
  - `stepEmbedder`: "[Enter=ข้าม]" → existing wizardOllama/LMStudio/Cloud
    flows only on opt-in.
  - Finale: acquire writer lock (`lockfile.Acquire`; held by `ney mcp`? →
    tell the user to close AI clients and rerun `ney init`, skip indexing
    but still complete registration steps), `initAppWithOptions`,
    `newIndexer`, `Index` per selected folder (workspace name =
    `filepath.Base`; collision with different root → append parent dir name
    `base+"-"+parentBase`), then `db.SetMeta("setup_completed", timestamp)`.
- Config writes: wizard updates `~/.ney/config.yaml` — extend
  `writeWizardConfig` to take OCR settings and optional embedder (embedder
  section omitted when skipped, provider stays none).
- Tests: `parseSelection` table; workspace-name disambiguation; trigger
  logic (`setupCompleted(db)` helper).

## Step 4 — dynamic root set + shared scan scope

Modify: `cmd/ney/cmd_mcp.go`, `cmd/ney/mcp_tools.go`, `mcp_test.go`.

- `type rootSet struct { mu sync.Mutex; roots []mcpRoot }` with
  `snapshot() []mcpRoot` and `add(r mcpRoot)` (no-op if Name already
  present). `runMCP` wraps resolved roots in a `*rootSet`; every handler
  signature switches `roots []mcpRoot` → `rs *rootSet` and calls
  `rs.snapshot()` at call time. `rootNames`, watcher loop, Phase A loop
  keep using the startup snapshot.
- `resolveAllowedScanDir(raw string) (string, error)` replaces
  `resolveHomeBoundDir`: allowed under `$HOME` **or** the iCloud Drive root
  (which lives under `$HOME` anyway on macOS — so implementation reduces to
  the existing home check; keep the helper rename + a test asserting the
  iCloud path passes). `search_folder` switches to it.
- mcp_test.go: env builders construct `newRootSet(roots)`.

## Step 5 — `index_folder` tool

Modify: `cmd/ney/mcp_tools.go`, `cmd/ney/cmd_mcp.go`, `mcp_test.go`.

- `newMCPServer` gains `deps mcpDeps` (bundles `ix *index.Indexer`,
  `serialize func(func())`, `worker`, `rs *rootSet`, `flt`, `state`) to stop
  parameter growth; existing handlers pull from it.
- Tool `index_folder`:
  - input `{path string}`; description: permanent indexing for folders the
    user wants searchable from now on; prefer `search_folder` for one-off
    lookups; served to every AI client from now on.
  - handler: `state.isReadOnly()` → error ("another ney process holds the
    writer lock…"). `resolveAllowedScanDir` → collision check via
    `db.GetWorkspaceByName` (same rule as resolveMCPRoots: same name,
    different resolved root → error) → `UpsertWorkspace` →
    `serialize(func(){ stats, err = ix.Index(ctx, dir, name) })` →
    `worker.Notify()` → `rs.add(root)` → spawn watcher goroutine
    (`DisablePrune: true`, `Serialize: serialize`, ctx from server) →
    return `{workspace, files_indexed, chunks, duration_ms}`.
  - In read-only mode the tool is still registered but always errors (so
    clients see a consistent tool list).
- Tests: index temp folder → search finds content + read allowed under new
  root; read-only env → error; `/etc` → scope error; collision → error;
  second call same folder → idempotent re-index (skip-by-hash, no error).

## Step 6 — docs + memory

- README: quick start becomes "install → `ney` → wizard"; tools 5 → 6;
  Security section: home+iCloud scope for search_folder/index_folder;
  Concurrent access: index_folder unavailable in read-only mode.
- CLAUDE.md: `internal/discover` row; mcpclients.go note; dynamic rootSet
  invariant (containment consults the live set); wizard trigger.
- docs/roadmap.md dated entry. Memory file update after implementation.

## Step 7 — verification

1. `go build ./... && go vet ./... && go test ./...` green; `go mod tidy`.
2. Fresh `$HOME` smoke: `ney` → wizard end-to-end with a fake corpus
   (pick folders, skip OCR install, register into temp client config paths
   via env override or manual check, skip embedder) → `setup_completed`
   set; rerun `ney` shows help.
3. `tools/list` over stdio shows 6 tools; `index_folder` on a temp dir then
   `search_documents` finds its content in the same session.
4. Real machine: rerun `ney init`, register Claude Desktop (args-less
   form replaces the old `--root docs` entry), restart Claude Desktop,
   verify search + "index โฟลเดอร์นี้ให้หน่อย" flow live.
