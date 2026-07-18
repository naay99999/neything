# First-run setup wizard + mid-session folder indexing — Design

Date: 2026-07-19
Status: Approved (brainstorm session with user)

## Problem

Getting ney useful today takes too many manual steps: the user must know which
folders to index, run `ney index` per folder, hand-edit each AI client's MCP
config, and OCR never gets set up. Folders added later require editing every
client config again. The user wants: install → type `ney` → guided setup that
discovers documents, explains the benefit, sets up OCR and AI clients
(Claude + Codex baseline), and the ability to index more folders mid-session
by just asking the AI.

## Decisions (agreed with user)

1. **Trigger**: bare `ney` on a not-yet-set-up machine offers the wizard;
   `ney init` reruns it anytime. Setup-complete state = DB meta key
   `setup_completed`, written only when the wizard finishes (Ctrl+C mid-run →
   next bare `ney` offers again; every step is idempotent).
2. **Client registration**: detect installed clients, confirm per client
   (y/n each). Clients: Claude Desktop, Claude Code, Codex CLI. Undetected
   clients get copy-paste instructions printed instead.
3. **Discovery scope**: deep scan of the whole home directory plus iCloud
   Drive (`~/Library/Mobile Documents/com~apple~CloudDocs`), excluding
   sensitive/junk dirs. Slow is acceptable (one-time); show progress.
   External volumes are NOT scanned — manual path entry covers them (with a
   warning about unplugged drives when the path is under /Volumes).
4. **OCR**: detect tesseract/poppler; offer to run
   `brew install tesseract poppler tesseract-lang` with consent, streaming
   output; on success set `ocr.enabled: true, lang: tha+eng`. No brew or
   install failure → print manual instructions and continue (never block).
5. **Source of truth for served folders**: the ney DB's `workspaces` table.
   Clients are registered with **no args** (`ney mcp`), which already serves
   every workspace in the DB. All clients therefore always see the same set,
   and folders added later (wizard rerun, `ney index`, `index_folder`) appear
   everywhere without touching client configs again.
6. **Semantic search**: optional final wizard step, default = skip (Enter).
   Reuses the existing embedder flows (Ollama / LM Studio / cloud).
7. **Mid-session indexing**: new MCP tool `index_folder`, same path scope as
   `search_folder` (home + iCloud), read-write mode only.

## UX flow (wizard)

```
$ ney                       # fresh machine
Ney ยังไม่ได้ตั้งค่าบนเครื่องนี้ — เริ่ม setup เลยไหม? [Y/n]

[1/4] สแกนหาเอกสาร (Home + iCloud Drive)... progress: "สแกนแล้ว N โฟลเดอร์"
      พบเอกสารกระจุกอยู่ที่:
      [1] ~/Documents              142 ไฟล์ (89 pdf, 40 md, 13 docx)
      [2] ~/workspace/onelamarket  913 ไฟล์ (ส่วนใหญ่ md)
      ...
      เลือก (เช่น 1,3 / a=ทั้งหมด / พิมพ์ path เพิ่ม): _

[2/4] OCR: ตรวจ tesseract/poppler → เสนอ brew install → เปิด tha+eng

[3/4] AI clients: ตรวจพบรายตัว → ยืนยันรายตัว → เขียน config (backup .bak)

[4/4] Semantic search (optional): Enter = ข้าม

      index โฟลเดอร์ที่เลือก (Phase A) พร้อมสรุป → setup_completed → เสร็จ
```

`ney init` = same wizard, rerunnable (add/remove folders, enable OCR later).

Mid-session: user tells the AI "index ~/some/folder" → AI calls `index_folder`
→ folder becomes a permanent workspace served to every client.

## Components

### 1. `internal/discover` (new package)

- `Discover(ctx, opts, progress func(dirsScanned int)) ([]Candidate, error)`
- `Candidate{Path string, DocCount int, ByExt map[string]int}`
- Walks home + iCloud Drive root. Skips: everything `pathfilter` denies
  (dot-dirs, secret names), plus a junk list: `Library` (except the iCloud
  special case), `node_modules`, `vendor`, `venv`/`.venv`, cache dirs,
  `*.app`/`*.photoslibrary`-style bundles, `Applications`, `Music`,
  `Movies`, `Pictures`.
- Counts files matching the indexer's `supportedExts`, rolled up per dir.
- Concentration heuristic: a directory is presented as a candidate unless
  ≥80% of its documents live in a single child, in which case descend and
  present the child instead. Output ranked by DocCount, top ~10.
- No hard time cap (user accepted slow); ctx cancellation honored.

### 2. Wizard rework (`cmd/ney/cmd_init.go` + `root.go`)

- `root.go` bare-`ney` branch: if DB meta `setup_completed` is empty →
  prompt "เริ่ม setup เลยไหม [Y/n]"; Y runs the wizard, n falls through to
  help. Machines already set up see plain help.
- Steps as in UX flow. Folder picker parses "1,3", "a", and free-form paths
  (tilde-expanded; nonexistent → re-prompt; /Volumes paths get the unplug
  warning).
- Workspace naming: `filepath.Base(root)`; on name collision with a
  different root_path, disambiguate by appending the parent dir name
  (mirrors the resolveMCPRoots collision rule rather than erroring).
- OCR step: reuse `loader.OCRToolsAvailable`; run brew via exec with stdout
  streamed; update config file (`ocr.enabled`, `lang: tha+eng`).
- Client registration (new file `cmd/ney/mcpclients.go`; every function
  takes the config path as a parameter for testability, backups written as
  `<file>.bak` before any modification):
  - **Claude Desktop** — `~/Library/Application Support/Claude/
    claude_desktop_config.json`: JSON read-modify-write, set
    `mcpServers.ney = {command: <abs ney path>, args: ["mcp"]}`, preserve
    all other keys. Unparseable JSON → skip with warning, never overwrite.
  - **Claude Code** — if `claude` binary on PATH: run
    `claude mcp add --scope user ney -- <abs ney path> mcp`.
  - **Codex** — `~/.codex/config.toml`: if a `[mcp_servers.ney]` section
    exists, replace that section (up to the next `[` header or EOF); else
    append `[mcp_servers.ney]\ncommand = "<abs>"\nargs = ["mcp"]`.
  - Detection = config dir/file or binary present. Undetected → print the
    manual snippet.
  - Existing entries (e.g. the current `--root docs` Claude Desktop entry)
    are overwritten with the args-less form; indexed workspaces already in
    the DB keep serving the same content.
- Final step indexes each selected folder (Phase A, sequential, progress to
  stderr as today), then sets `setup_completed`.
- Embedder step: existing `wizardOllama`/`wizardLMStudio`/`wizardCloud`
  flows, entered only if the user opts in.

### 3. `index_folder` MCP tool (`cmd/ney/mcp_tools.go`, `cmd_mcp.go`)

- Input: `{path string}`. Path guard: shared helper with `search_folder`
  extended to **home + iCloud Drive** (`resolveAllowedScanDir`), both tools
  use it so the rules stay identical.
- Read-only mode → error: "another ney process holds the writer lock — ask
  again after closing it, or use search_folder for a one-off scan".
- Flow: resolve dir → workspace-name collision check (same rule as
  resolveMCPRoots) → `UpsertWorkspace` → Phase A `Index` under the shared
  `indexMu` (a `serialize func(func())` is threaded into the handler) →
  `EmbedWorker.Notify()` if present → add root to the dynamic root set →
  spawn a watcher goroutine for the new root (`DisablePrune: true`,
  `Serialize` via indexMu) → return `{files, chunks, duration}`.
- **Dynamic roots**: `roots []mcpRoot` becomes a small thread-safe holder
  (`rootSet` with mutex; snapshot method). `resolveAllowedPath`,
  live-scan targeting, and `index_status`/`list_workspaces` read snapshots.
  Handlers receive the holder instead of a slice.
- Tool description teaches the AI: prefer `search_folder` for one-off
  lookups; use `index_folder` when the user wants a folder permanently
  searchable ("ค้นลึก/ใช้ประจำ").

### 4. Docs

- README: wizard section replaces the "Add semantic search" quick start;
  tool count 5 → 6 with `index_folder` described; Security section notes
  the home+iCloud scope shared by search_folder/index_folder.
- CLAUDE.md: package map row for `internal/discover`, `mcpclients.go`,
  dynamic root set invariant (read_document containment must consult the
  live root set), wizard trigger note.
- docs/roadmap.md: dated entry.

## Error handling

- Discovery: unreadable dirs skipped silently (same as indexer walk); ctx
  cancel (Ctrl+C) aborts cleanly.
- brew missing/failed: print manual command, continue; OCR stays off.
- Client config writes: `.bak` backup first; parse failures skip that
  client with a warning; wizard never aborts on a registration failure.
- Folder indexing failures: warn per folder, continue with the rest
  (existing Index behavior); wizard still completes.
- `index_folder`: read-only error, out-of-scope error, collision error —
  all returned as tool errors with actionable messages.
- Rerunning the wizard is always safe: UpsertWorkspace/config writes/
  registrations are idempotent.

## Testing

- `internal/discover`: temp tree with junk (`node_modules`, `.git`,
  `Library`, secrets) + real docs → only expected candidates; concentration
  heuristic promotes the right child; ByExt counts correct.
- `mcpclients_test.go`: JSON merge preserves unrelated keys; unparseable
  JSON refused; TOML append vs replace; rerun does not duplicate; `.bak`
  written.
- `index_folder` (in-memory MCP env): index temp folder → search_documents
  finds its content, read_document allowed under the new root; read-only
  env rejected; out-of-scope path rejected; name collision rejected.
- Wizard: pure helpers (selection parsing "1,3"/"a"/paths, candidate
  rendering) unit-tested; interactive loop kept thin.
- Manual smoke: fresh `$HOME` → `ney` → full wizard run → tools/list shows
  6 tools → Claude Desktop config written correctly.

## Out of scope

- Windows/Linux client-config paths (macOS only for now — matches the
  brew/Claude Desktop assumptions; Codex TOML path is OS-independent).
- Scanning external volumes automatically.
- A GUI; everything is terminal prompts.
- Uninstall/unregister flow.
