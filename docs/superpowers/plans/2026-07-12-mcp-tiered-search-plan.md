# Implementation Plan — MCP Server + Tiered Zero-Setup Search

> ทำตาม spec: `docs/superpowers/specs/2026-07-12-mcp-tiered-search-design.md`
> แบ่ง 4 milestones — แต่ละอันจบในตัว, merge ได้, `go test ./...` ต้องเขียวตลอด
> ผ่าน adversarial review รอบ 1 แล้ว — ลำดับ M1 ถูกแก้ให้รวม pipeline nil-guards (เดิมขัดแย้งกับ exit criteria ตัวเอง)

| Milestone | เนื้อหา | ขนาดโดยประมาณ |
|---|---|---|
| M1 | Embedder+chat optional + pipeline nil-guards + retrieval auto mode | กลาง |
| M2 | Two-phase indexing + EmbedWorker + writer lock | ใหญ่ |
| M3 | MCP server 4 tools + watcher refactor | **ใหญ่** (อย่าประเมินต่ำ: path guard, stdout hygiene, shutdown, integration tests) |
| M4 | Tier-0 live scan + polish (status/doctor/UX/docs) | เล็ก |

---

## M1 — Optional providers + Retrieval auto mode

### 1.1 Config: embedder และ chat เป็น optional
- `internal/config/config.go`
  - `Validate()` (~:247): อนุญาต `embedder.provider` ว่าง/`"none"` → ข้าม validate model; ยังห้าม `claude` เมื่อระบุ
  - **chat optional ด้วย** (`Validate` :255-264 รันใน `config.Load()` ทุก command): provider ว่าง/`"none"` → ผ่าน; `ask` เช็คเองระดับ command
  - helpers `cfg.HasEmbedder() bool`, `cfg.HasChat() bool`
  - `defaultConfig`: embedder+chat เป็น `none`/commented พร้อมคำแนะนำ `ney init`
  - `NewEmbedder`/`NewChatModel`: คืน `(nil, nil)` เมื่อไม่ได้ตั้งค่า (ตั้งแล้วสร้างไม่ได้ = ยัง error)
- `cmd/ney/app.go` (`initAppWithOptions`): nil embedder/chat = สถานะปกติ
- `cmd/ney/cmd_ask.go` + REPL ask path: `App.Chat == nil` → friendly error "no chat provider configured — run `ney init`"
- Tests: validate ทุก combination; factories none→nil,nil; config เก่าจริง (มี claude/hybrid:false) ต้อง load ผ่าน

### 1.2 Pipeline nil-guards (ย้ายมาจาก M2 — จำเป็นเพื่อ exit criteria ของ M1)
> ไม่ split phase ใน M1 — แค่ทำให้ nil embedder ไม่ panic: index ได้แบบ FTS-only
- `internal/index/pipeline.go`
  - `checkEmbedderConsistency` (:571): `ix.Embedder == nil` → return nil
  - `indexDocument`: `ix.Embedder == nil` → ข้าม `Embed` (:497) + vectorItems (:533-540) + `Vectors.Add` (:554-558); ยัง `Vectors.Delete(oldIDs)` ตามปกติ
  - `Index`/`IndexPath` (:166,:199): ข้าม `SetActiveEmbedder` เมื่อ nil
- จุดเสี่ยง panic ที่ review ยืนยัน: `sync.go:30` → `Index()` ผ่าน `syncWorkspaceIfKnown` เมื่อ cwd อยู่ใน workspace — test ครอบ
- Tests: index ด้วย nil embedder → chunks+FTS ครบ, vectors ว่าง, hash เขียน, re-run skip, ไม่ panic

### 1.3 Retriever auto mode
- `internal/search/retriever.go`
  - `Search()`: (1) keyword เสมอ (error → meta ไม่ fail); (2) semantic เมื่อ `Embedder != nil && Vectors.Count() > 0`, embed error → degrade + `meta.Degraded`; (3) สอง signal → RRF, เดียว → ตรง, ศูนย์ → ว่าง+meta
  - เพิ่ม `SearchMeta{SemanticUsed, KeywordUsed bool; EmbedCoverage float64; Degraded string}` — แก้ signature ตรงๆ (callers มีแค่ `cmd_search.go:45,54`, `cmd_ask.go:47,55` — review ยืนยันแล้ว REPL ผ่าน rootCmd)
  - `RetrieveOptions.Hybrid bool` → `Mode string` (`auto|semantic|keyword|hybrid`)
- `internal/config/config.go`: `retrieval.hybrid` รับ bool เก่า (true→hybrid, false→auto) + string ใหม่; default `auto`
  - ⚠️ **behavior change ที่ตั้งใจ** (spec §5 note): install เดิม `hybrid: false` → auto; บันทึก changelog + golden ranking test
- `cmd/ney/retrieve.go` (~:42), `cmd_search.go`, `cmd_ask.go`: ส่ง Mode, แสดง degradation note (stderr)
- Tests: ตาราง {embedder nil, vectors ว่าง, embed error, ปกติ} × {mode}; golden ranking hybrid vs auto

### 1.4 status/doctor semantics
- `cmd/ney/cmd_status.go` (~:70): `vectors < chunks` → `Embedding X/Y (Z%)`; `embedder: not configured (keyword-only)`
- `cmd/ney/cmd_doctor.go` (~:354): vector_parity สามเคส — orphan (`vec>chunks`) / in-progress (`vec<chunks` — ปัจจุบันผ่านพร้อมข้อความ "matches" ที่ผิด) / ok; embedder/chat not-configured = info
- Tests: ปรับ existing

**M1 exit criteria:** ติดตั้งสด config default ใหม่ (ไม่มี embedder/chat) → `ney index && ney search "x"` ทำงาน FTS-only + note, `ney ask` ได้ friendly error, cwd-sync ไม่ panic; repo tests เขียว

---

## M2 — Two-phase indexing + EmbedWorker + writer lock

### 2.1 แยก Phase A ใน pipeline (สำหรับกรณีมี embedder ด้วย)
- `internal/index/pipeline.go`
  - `indexDocument`: ตัด embed ออกจาก flow **ถาวร** (ทุกกรณี ไม่ใช่แค่ nil) — tx เหลือ delete-old→chunks→FTS→commit
  - ลบ/ย้าย handling `EmbedUnavailableError`/`EmbedError`/`maxEmbedFailures` (:33-50, :129-141) — unreachable หลัง split; logic backoff ไปอยู่ worker
  - `SetActiveEmbedder` ออกจาก pipeline → worker เขียนเมื่อ embed แรกสำเร็จ
  - stats เพิ่ม `ChunksPendingEmbed`; `cmd_index.go:110` เปลี่ยนข้อความเป็น "chunked X / embedded Y"

### 2.2 `VectorStore.IDs()`
- `internal/vectorstore/types.go` (interface — **ไม่ใช่ store.go**), `mem.go`, `hnsw.go`: `IDs() []string` ภายใต้ RLock; tests ทั้งสอง backend

### 2.3 EmbedWorker (`internal/index/embedworker.go`)
- struct: `{DB, Vectors, Embedder, BatchSize, Workspace string /*"" = all*/, OnProgress}`
- `Run(ctx)` loop ตาม spec §4.2 พร้อมข้อบังคับจาก review:
  - **latched Notify** — buffered chan(1)
  - **orphan pass สุดท้ายก่อน idle** (การันตี self-heal)
  - **orphan deletion แบบ batch + threshold** (< 50 → เลื่อน) — กัน HNSW full-rebuild ถี่ (`hnsw.go:205-222` delete ทุกครั้ง dirty graph)
  - pending: paginated `store.GetAllChunkIDs(afterID, limit)` / `GetChunkIDsByWorkspace` (มีแล้ว `db.go:437`) — **อ่าน rows จบก่อนยิง query ถัดไป** (SetMaxOpenConns(1): query ซ้อนขณะ rows เปิด = hang)
  - consistency check: parse `"model":"` substring แบบ `pipeline.go:580-585` — ห้ามใช้ `GetActiveEmbedder().Model` (Sscanf parse ไม่ได้จริง, `db.go:394-397`)
  - backoff 30s×2^n cap 5m; ctx cancel → Flush + return; `Status()` อ่านได้
- `cmd/ney/cmd_index.go`: Phase A → worker synchronous **scoped workspace นั้น** จน pending หมด (spinner+progress); `--no-embed` ข้าม
- `cmd/ney/sync.go`: Phase A + bounded embed ≤ 256 chunks; เหลือ → stderr note ("X chunks รอ embed — รัน `ney index`")
- `cmd/ney/cmd_watch.go`: worker เป็น goroutine + `Notify()` หลัง flush
- Tests: worker เติมครบ; orphan cleanup + batch threshold; race จำลอง (re-chunk ระหว่างรอบ) → converge; notify ระหว่างวิ่งไม่หาย; ล่ม→backoff; mismatch→blocked; scope ต่อ workspace ไม่แตะ workspace อื่น; end-state เทียบ flow เก่า (golden chunk/vector sets)

### 2.4 Writer lock (`internal/store/lock.go` หรือ `internal/lockfile`)
- `~/.ney/writer.lock`: pid + start-time, `O_EXCL` create, staleness check (pid ตาย → ยึดได้)
- ถือโดย: `index`, `watch`, `reset`, (M3: `mcp`); ขอไม่ได้ → error "another ney process (pid X) is writing — stop it first"
- read-only commands ไม่ถือ
- Tests: ชนกัน, stale pid, ปลดตอน exit/signal

**M2 exit criteria:** `ney index` (มี embedder) end-state เทียบเท่าเดิม; ไม่มี embedder → จบเร็ว + status โชว์ pending; สอง process เขียนพร้อมกันถูกกัน; search ระหว่าง embed ไม่ block

---

## M3 — MCP server + watcher refactor

### 3.0 Pre-flight
- **Re-verify** `modelcontextprotocol/go-sdk` ล่าสุด + สถานะ spec 2026-07-28/roots ก่อน pin (ข้อมูลใน spec มาจาก RC — spec §7.1 ⚠️)

### 3.1 Watcher refactor (prerequisite — review H4)
- `internal/watch/watcher.go`: เอา `signal.Notify` ออก (:110-112 → ย้ายไป `cmd_watch.go`), รับ ctx ภายนอกล้วน, รับ optional `Serialize func(func())` สำหรับ mutex ร่วมกับ Phase A ของ server, prune ticker เป็น option ปิดได้ (server รวมศูนย์เอง)
- `cmd_watch.go` ปรับตาม; tests เดิมต้องผ่าน

### 3.2 Dependency + skeleton
- `go get github.com/modelcontextprotocol/go-sdk@<verified>`
- ไฟล์ใหม่ `cmd/ney/cmd_mcp.go`: cobra cmd `mcp`, flags `--root` (repeatable)
  - acquire writer lock → เปิด app ครั้งเดียว → `mcp.NewServer` + 4 tools → `server.Run(ctx, &mcp.StdioTransport{})`
  - **stdout hygiene:** mcp path ห้ามผ่าน banner/spinner/output helpers (พิมพ์ stdout เสรี) — bypass ชัดเจน + test จับ stdout ปนเปื้อน
  - background: Phase A ทุก root (mutex เดียวกับ watcher Serialize) → EmbedWorker → watcher/root; shutdown: cancel → Flush → unlock
  - `--root`: basename ชน workspace เดิมที่ root ต่าง → **error แนะนำ** ห้าม silent merge (`UpsertWorkspace` ON CONFLICT จะ re-point เงียบ, `db.go:95`); ไม่ระบุ --root → ใช้ workspaces ใน DB

### 3.3 Tools (`cmd/ney/mcp_tools.go` — บาง, logic อยู่ internal)
- `search_documents{query, workspace?, path_prefix?, top_k?}` → stack เดียวกับ `cmd_search` (+tier-0 M4) → structured `{results[], index_status}`
- `read_document{path, offset_chars?, max_chars? (50000)}`
  - plain-text → อ่านไฟล์ตรง; pdf/docx → reassemble จาก chunks (`GetChunksByDocumentOrdered` ใหม่; `GetDocumentByPath` **มีแล้ว** `db.go:167`) — join ตรง, ประกาศ approximate/overlap-dup ใน description
  - ยังไม่ index → loader parse สด ≤ 20 MB, OCR timeout 10s
  - path guard: `EvalSymlinks`+`Clean`+prefix — tests: `../`, symlink ออกนอก root
- `list_workspaces{}` → counts + embed coverage (diff ต่อ workspace; cache ใน worker status ถ้าแพง)
- `index_status{}` → WorkerStatus + counts + phase A/watcher state
- structs พร้อม `jsonschema` tags

### 3.4 Integration tests (`cmd/ney/mcp_test.go`)
- in-process transport: temp dir + md/pdf ตัวอย่าง + mock embedder ช้า → `search_documents` ทันที (ได้ FTS บางส่วน + status in-progress), `read_document` (ทั้งสองเส้นทาง, offset, guard), `list_workspaces`, `index_status`, stdout สะอาด, graceful shutdown flush

**M3 exit criteria:** เพิ่ม `{"command":"ney","args":["mcp","--root","~/docs"]}` ใน Claude Code แล้วถาม-ตอบได้จริงตั้งแต่นาทีแรก; รัน `ney reset` ระหว่างนั้นถูกกันพร้อมข้อความชัด

---

## M4 — Tier-0 live scan + polish

### 4.1 `internal/scan`
- `Scan(ctx, root, query, opts) ([]Hit, truncated bool)` — filename token scoring + plain-text grep (spec §6); caps 10k files / 2s / 2MB; skip กติกาเดียวกับ indexer
- Tests: fixture — filename hit บน `.pdf`, content hit บน `.md`, caps, binary skip

### 4.2 Wire เข้า search paths (เกณฑ์ต่างกันต่อ path — spec §6.1)
- **MCP:** trigger จาก server state (Phase A ของ root ยังวิ่ง)
- **CLI `ney search`:** heuristic — ไม่มี workspace ครอบ path หรือ `documents == 0`
- merge dedup-by-path ท้ายผล, `source: "live-scan"`

### 4.3 Polish
- wording สุดท้ายของ degradation notes; README ส่วน MCP setup (Claude Code/Desktop/Cursor snippets); CLAUDE.md architecture; roadmap §4.2 เช็คถูก; changelog ระบุ hybrid→auto behavior change
- `ney doctor`: เช็ค "mcp readiness"

**M4 exit criteria:** folder ใหม่ไม่เคย index → ต่อ MCP → ถาม "ใบเสร็จ order-1233" → ได้ไฟล์จาก live-scan/FTS ภายในไม่กี่วินาที

---

## ความเสี่ยง & จุดต้องระวังตอนลงมือ

1. **stdout ปนเปื้อนใน `ney mcp`** — เสี่ยงสุด; route ทุก print ผ่าน helper ที่รู้ mode + test จับ
2. **SetMaxOpenConns(1)** สองแง่: (ก) Phase A ถือ tx สั้น → tool call หน่วง ms-level ยอมรับ v0.5; (ข) **query ซ้อนขณะ `sql.Rows` เปิด = hang แน่นอน** — บังคับใน code review ของ store methods ใหม่ทุกตัว
3. **`LastInsertId` constraint** (CLAUDE.md): SELECT id หลัง upsert เสมอ
4. **HNSW dirty-rebuild**: ทั้ง re-`Add` และ `Delete` trigger — worker embed เฉพาะ ID ใหม่ + orphan delete แบบ batch/threshold; test ครอบ
5. **viper bool→string ของ `retrieval.hybrid`**: decode-compat test กับ config เก่าจริง + ยอมรับว่าเป็น behavior change (changelog)
6. **read_document parse สด + OCR** — timeout 10s, size cap
7. **multi-process clobber** — writer lock ต้องมาก่อน (M2) mcp (M3) เสมอ อย่าสลับลำดับ

## ลำดับ commit ที่แนะนำ

M1 (3 commits: config+app+ask / pipeline nil-guards / retriever+status-doctor) → M2 (pipeline split / IDs() / worker / lock / CLI wiring) → M3 (watcher refactor / dep+skeleton / tools / tests) → M4 (scan / wiring / docs) — แต่ละ commit เขียว
