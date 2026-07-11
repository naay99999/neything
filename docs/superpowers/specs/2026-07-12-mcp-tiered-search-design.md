# Design — MCP Server + Tiered Zero-Setup Search

> **Design Spec** — ขยาย Roadmap Phase 4.2 (MCP Server) และเพิ่มสถาปัตยกรรม tiered search ใหม่
> เป้าหมาย: ต่อ MCP แล้วถามได้ทันที ("ใบเสร็จ order-1233 อยู่ไหน ใครซื้อ กี่บาท") โดยไม่ต้องรอ index, ไม่ต้องมี embedder, ไม่ต้องมี API key

| Field | Value |
|---|---|
| Date | 2026-07-12 |
| Status | Draft — awaiting user review (ผ่าน adversarial review รอบ 1 แล้ว, แก้ตาม findings) |
| Supersedes | Roadmap §4.2 (ขยายรายละเอียด) |
| Target milestone | v0.5 |

---

## 1. ปัญหา

ทุกวันนี้ ney ต้องผ่าน 3 ด่านก่อนตอบคำถามแรกได้: (1) ตั้งค่า embedder (ลง Ollama/มี API key), (2) รัน `ney index` แล้วรอ embed จบ, (3) มี chat provider สำหรับ `ask` และถ้า embedder ล่ม การค้นหา**ทุกโหมด**พังทันที เพราะ `Retriever.Search` embed query ก่อนเสมอ (`internal/search/retriever.go:59,115`) แม้ FTS จะมีข้อมูลครบ

Insight หลัก: query จำนวนมากในโลกจริง ("order-1233", ชื่อคน, เลขที่เอกสาร) เป็น exact token ที่ keyword search ตอบได้แม่นกว่า semantic — เราไม่ควรเอา embedding มาเป็นด่านบังคับของ use case ที่ไม่ต้องใช้มัน

## 2. Vision

**ney = local document index ที่เสียบเข้ากับ AI client ตัวไหนก็ได้ผ่าน MCP** โดยชั้นความสามารถ degrade อย่างสง่างาม:

| Tier | พร้อมใช้เมื่อ | ต้องการ | ตอบ query แบบไหน |
|---|---|---|---|
| 0 — live scan | ทันที (ก่อน index เสร็จ) | ไม่มี | filename match + grep ไฟล์ text |
| 1 — FTS5 keyword | วินาที–นาที (CPU-only) | ไม่มี | exact token, keyword, bm25 |
| 2 — semantic vectors | เบื้องหลัง ค่อยๆ เต็ม | embedder (Ollama/cloud) | ความหมาย, ภาษาธรรมชาติ |

การถาม-ตอบ/สรุปเป็นหน้าที่ของ LLM ฝั่ง client (Claude ฯลฯ) — ney ส่งมอบ retrieval ที่ดีที่สุดเท่าที่ tier ที่พร้อมจะให้ได้ พร้อมบอกสถานะความพร้อมใน response ให้ client ตัดสินใจเอง

หลักการนี้สอดคล้อง Core Principles เดิมทุกข้อ (CLI-first: ทุกอย่างยังสั่งผ่าน CLI ได้; local-first: tier 0–1 ทำงาน offline สมบูรณ์แบบโดยไม่มี model เลย)

## 3. สถาปัตยกรรมรวม

```
                       ┌────────────────────────────────────┐
 MCP client ── stdio ──│ ney mcp                            │
 (Claude Code/Desktop) │  tools: search_documents,          │
                       │         read_document,             │
                       │         list_workspaces,           │
                       │         index_status               │
                       └──────┬─────────────────┬───────────┘
                              │                 │ background
                       ┌──────▼──────┐   ┌──────▼───────────┐
                       │ TieredSearch│   │ Phase A: scan+FTS │
                       │ 0 → 1 → 2   │   │ Phase B: embed    │
                       └──────┬──────┘   │ + fsnotify watch  │
                              │          └──────┬───────────┘
                       ┌──────▼─────────────────▼───────────┐
                       │ SQLite (chunks+FTS) + VectorStore  │
                       └────────────────────────────────────┘
```

## 4. Two-Phase Indexing

### 4.1 Phase A — parse + chunk + FTS (ไม่ต้องมี embedder)

`indexDocument` (`internal/index/pipeline.go:436`) ปัจจุบันทำ chunk→embed→insert ใน flow เดียว โดยเรียก `Embedder.Embed` **ภายใน write transaction** (`pipeline.go:497`) ซึ่งบล็อก SQLite connection เดียว (SetMaxOpenConns(1)) ตลอด network round-trip

แยกเป็น:

- **Phase A** = flow เดิมตัด embed ออก: upsert doc → tx { delete old chunks+FTS → insert chunks → insert FTS } → commit → `Vectors.Delete(oldIDs)` → update hash เร็วมากเพราะ CPU-only และ**แก้ปัญหา tx-holds-connection-during-embed ไปในตัว**
- **Phase B** = งาน embed แยกต่างหาก (ดู §4.2) — ไม่มี transaction ยาว

`Indexer` รับ `Embedder` เป็น optional (nil ได้) เมื่อ nil: ทำ Phase A อย่างเดียว จุดที่ต้อง nil-guard ใน pipeline ปัจจุบัน: `Embed` call (:497), `SetActiveEmbedder` (:166,:199), `checkEmbedderConsistency` (:571 — ข้ามเมื่อ nil; consistency เช็คตอน Phase B แทน) `SetActiveEmbedder` ย้ายไปเขียนโดย Phase B เท่านั้น

การจัดการ `EmbedUnavailableError`/`EmbedError`/`maxEmbedFailures` ใน walk ของ `Index` (:33-50, :129-141) จะ unreachable หลังแยก phase — logic นี้ย้ายไปอยู่ใน EmbedWorker (backoff, §4.2)

**hash-skip ยังถูกต้อง:** `indexFileIfNeeded` (`pipeline.go:312`, เช็ค hash ที่ :317) skip เมื่อ hash ตรง + มี chunk rows — ใช้ได้ต่อ เพราะสถานะ "embed หรือยัง" ไม่ได้อยู่ที่ document อีกต่อไป (ดู 4.2)

### 4.2 Phase B — progressive embedding แบบ diff-based (ไม่แก้ schema)

โปรเจกต์ไม่มี schema migration mechanism (`db.go:84` รันแต่ `CREATE IF NOT EXISTS`) — เราเลี่ยงการเพิ่ม column โดย**ไม่เก็บสถานะ embed เลย** แต่คำนวณจาก diff:

```
pending  = chunkIDs(SQLite) − vectorIDs(VectorStore)   → ต้อง embed
orphans  = vectorIDs(VectorStore) − chunkIDs(SQLite)   → ต้องลบ
```

หลักประกันความถูกต้อง: `chunks.id` เป็น `INTEGER PRIMARY KEY AUTOINCREMENT` (`schema.go:27`) — ID ไม่ถูก reuse ดังนั้น vector เก่าไม่มีทางถูก bind เข้า content ใหม่โดยเงียบ มีแต่กลายเป็น orphan

- เพิ่ม method `IDs() []string` เข้า `VectorStore` interface (`internal/vectorstore/types.go`; ทั้งสอง backend มี map ภายในภายใต้ RWMutex อยู่แล้ว)
- **EmbedWorker** (ไฟล์ใหม่ `internal/index/embedworker.go`): loop → คำนวณ pending (scope ได้ต่อ workspace, ดูด้านล่าง) → `SELECT content` เป็น batch → `Embed` (นอก tx) → `Vectors.Add` → `Flush` เป็นระยะ → วนจน pending ว่าง → **ทำ orphan pass สุดท้ายก่อน idle** → รอ signal
- **Notify ต้อง latched:** ใช้ buffered channel ขนาด 1 — signal ที่มาระหว่าง worker กำลังวิ่ง ต้องไม่หาย มิฉะนั้น stale vector จาก race (ด้านล่าง) จะค้างจนกว่าจะมี event ใหม่
- **Self-healing:** ถ้า watcher re-chunk เอกสารระหว่างที่ worker กำลัง embed chunk เก่า — chunk ID เก่าหายจาก SQLite → กลายเป็น orphan → ถูกลบใน orphan pass; chunk ID ใหม่เป็น pending → ถูก embed รอบถัดไป เงื่อนไขคือ latched notify + orphan-pass-before-idle ตามที่ระบุข้างบน (ไม่ใช่ property ที่ได้มาฟรี)
- **HNSW rebuild cost (ยอมรับใน v0.5):** `HNSWStore.Delete` **ทุกครั้ง** mark graph dirty → full O(N) rebuild ใน `Search` ถัดไป (`hnsw.go:205-222`, `:179-184`) — ไม่ใช่แค่ re-`Add` ดังนั้น orphan deletion ต้อง**batch และ defer**: ลบครั้งเดียวต่อรอบ worker (ไม่ลบทีละตัว) และเลื่อนได้ถ้า orphans น้อยกว่า threshold (เช่น < 50) เพื่อไม่ให้ search stall บ่อยระหว่าง watcher ทำงานถี่; cost ที่เหลือบันทึกเป็นข้อจำกัดที่ทราบใน docs (BruteForceStore ไม่มีปัญหานี้)
- **Pending scope:** `ney index <path>` embed เฉพาะ pending ของ workspace นั้น (diff กับ `GetChunkIDsByWorkspace`, `db.go:437`); `ney mcp` embed ทุก workspace
- embedder ล่มกลางทาง: exponential backoff (30s → cap 5m), log stderr, ไม่ crash
- model consistency: worker เช็ค `active_embedder` meta ก่อนเริ่มรอบ — **ต้อง parse แบบ substring เหมือน `pipeline.go:580-585`** เพราะ `GetActiveEmbedder` (`db.go:394-397`) parse `Model` ไม่ได้จริง (Sscanf `%s` กินทั้ง string); mismatch → หยุด + สถานะ `blocked_mismatch` ผ่าน `index_status` (ไม่ auto-reset)

### 4.3 จุด trigger

- `ney index` CLI: Phase A → รัน worker แบบ synchronous จน pending ของ workspace นั้นหมด (UX เดิม); `--no-embed` = Phase A อย่างเดียว
- `ney mcp` startup: Phase A ทุก root → spawn EmbedWorker (ทุก workspace) + watcher
- watcher event: Phase A แล้ว `worker.Notify()`
- `syncWorkspaceIfKnown` (process สั้น ไม่มี worker ค้าง): Phase A + **bounded embed** — embed pending ได้ไม่เกิน N chunks (default 256) ต่อการ sync; เหลือค้าง → แจ้งใน output ("X chunks รอ embed — รัน `ney index` หรือใช้ `ney mcp`")

## 5. Retrieval — auto mode

`Retriever.Search` เปลี่ยน semantics:

1. **FTS รันเสมอ** (เมื่อมี FTS rows)
2. **semantic รันเมื่อทำได้:** embedder != nil **และ** `Vectors.Count() > 0`; ถ้า embed query ล้มเหลว (endpoint ล่ม) → log + degrade เป็น FTS-only แทนที่จะ fail ทั้ง request (แก้พฤติกรรม `retriever.go:116` ที่ทำให้ embedder ล่ม = ค้นหาไม่ได้เลย)
3. ได้สอง signal → RRF fusion (โค้ดเดิม `fusion.go`); ได้ signal เดียว → ใช้ตรงๆ
4. ผลลัพธ์แนบ `SearchMeta{SemanticUsed, KeywordUsed, EmbedCoverage float64, Degraded string}` ให้ caller (CLI แสดง note ทาง stderr, MCP ใส่ใน structured output)

config `retrieval.hybrid` เดิมเปลี่ยนเป็น mode: `auto` (default ใหม่) / `semantic` / `keyword` / `hybrid`; รับค่า bool เก่าได้ (`true`→`hybrid`, `false`→`auto`)

> **Behavior change ที่ตั้งใจ:** install เดิมที่ config เขียน `hybrid: false` จะเปลี่ยนจาก semantic-only เป็น auto (semantic+FTS fused) — ranking เปลี่ยนสำหรับผู้ใช้ปัจจุบันทุกคน เรามองว่าเป็น improvement และบันทึกใน changelog ไม่เรียกว่า backward-compatible

## 6. Tier 0 — live scan (`internal/scan`)

ใช้เมื่อ index ของ scope ยังไม่พร้อม (นิยาม "พร้อม" ต่อ execution path — ดู §6.1):

- **filename match:** เดิน `filepath.WalkDir` (กติกา skip เดียวกับ indexer), แตก query เป็น token, ให้คะแนนชื่อไฟล์ที่มี token (case-insensitive substring) — จับเคส `order-1233.pdf` ได้แม้เป็น binary format
- **content grep:** เฉพาะไฟล์ plain-text (`.md .txt .html .json .csv` ฯลฯ ≤ 2 MB) — scan หา token, คืน matching line เป็น snippet; **ไม่แตะ PDF/DOCX** (parse สดแพงเกิน — filename match ครอบเคสนั้น)
- caps: หยุดที่ 10k ไฟล์ หรือ 2 วินาที แล้วคืนเท่าที่ได้ + flag `truncated`
- ผลลัพธ์ merge ท้ายรายการ FTS/semantic (ถ้ามี) แบบ dedup by path, label `source: "live-scan"`

Tier 0 เป็น stateless ล้วน ไม่เขียนอะไรลง DB

### 6.1 เกณฑ์ "index พร้อมหรือยัง"

- **ใน `ney mcp`:** ใช้ server state ตรงๆ — Phase A ของ root นั้นยังวิ่งอยู่ = ไม่พร้อม
- **ใน `ney search` (CLI, ไม่มี server state):** heuristic — ไม่มี workspace ครอบ path ที่ค้น หรือ workspace มี `documents == 0` = ไม่พร้อม → รัน tier 0 เสริม

## 7. MCP Server (`ney mcp`)

### 7.1 SDK และ transport

- **`github.com/modelcontextprotocol/go-sdk` v1.6.1** (GA, official, typed tool handlers + schema inference จาก struct tags)
- ⚠️ ข้อมูล MCP spec revision 2026-07-28 (roots deprecation) อ้างจาก release candidate — **ต้อง re-verify version/spec ตอนเริ่ม implement M3** ก่อน pin จริง
- stdio transport เท่านั้นใน milestone นี้; **stdout สงวนให้ protocol** — banner/spinner/log ทุกอย่างไป stderr (โค้ด CLI ปัจจุบันพิมพ์ stdout เสรีใน banner.go/output.go/spinner.go — คำสั่ง `mcp` ต้อง bypass path เหล่านั้นอย่างชัดเจน ไม่พึ่ง isatty)
- **ไม่ใช้ MCP roots เป็น mechanism** (มีแผน deprecate) — workspace scoping ผ่าน tool parameter; อ่าน `ListRoots` เป็น hint ตั้งต้นได้ถ้า client รองรับ แต่ไม่ load-bearing

### 7.2 Startup และ single-writer enforcement

```bash
ney mcp [--root <path>]...   # ไม่ระบุ --root = ใช้ workspaces ที่มีอยู่ใน DB
```

ลำดับ: acquire **writer lock** → เปิด DB/Vectors (+ Embedder ถ้า config มี) → register tools → `server.Run` ทันที (ตอบ query ได้เลยผ่าน tier ที่พร้อม) → background: Phase A ทุก root (mutex ภายใน process กัน overlap กับ watcher) → EmbedWorker → watcher; SIGINT/stdin ปิด → cancel → Flush → ปลด lock

**Writer lock (ใหม่ — จำเป็น):** vector store ทั้งสอง backend `Flush` โดยเขียนไฟล์ทั้งไฟล์จาก memory — สอง writer process พร้อมกัน (เช่น `ney mcp` ค้างอยู่ + ผู้ใช้รัน `ney index` หรือ `ney reset`) = last-writer-wins ทับงานกัน และ `reset` ระหว่าง mcp รันจะถูก flush ถัดไป resurrect vectors กลับมา จึงกำหนด:

- lock file `~/.ney/writer.lock` (flock/`O_EXCL`+pid+staleness check) ถือโดยทุก command ที่เขียน (index/watch/mcp/reset)
- command ที่ขอ lock ไม่ได้ → error ชัดเจน: "another ney process (pid X, `ney mcp`) is writing — stop it first" — ไม่รอเงียบๆ
- read-only commands (search/ask/status) ไม่ต้องถือ lock (SQLite WAL อ่านได้; vector file อ่าน snapshot ล่าสุด)

`--root` ที่ basename ชนกับ workspace เดิมที่ root_path ต่างกัน: **error พร้อมแนะนำ** ("workspace 'docs' already bound to ~/a/docs — use ney index --workspace to name it differently") — ห้าม silent re-point (`UpsertWorkspace` เป็น `ON CONFLICT(name) DO UPDATE SET root_path`, `db.go:95` ซึ่งจะ merge เงียบ)

### 7.3 Tools

**`search_documents`** `{query, workspace?, path_prefix?, top_k? (default 8)}`
→ tiered search ตาม §5–6; structured output: `results[]{path, workspace, score, snippet, location, source}` + `index_status` ย่อ (ให้ Claude รู้ว่าผลยัง partial)

**`read_document`** `{path, offset_chars? (0), max_chars? (default 50000)}`
→ คืน text ของเอกสาร สองเส้นทางตามชนิดไฟล์:
  - **plain-text formats (md/txt/html/json/…):** อ่านไฟล์ตรงจาก filesystem — เร็ว, exact
  - **binary formats (pdf/docx):** ประกอบจาก chunk rows ใน SQLite (เรียง `chunk_index`) — **approximate**: `start_pos/end_pos` เป็น line/page ไม่ใช่ char offset จึงตัด overlap แม่นๆ ไม่ได้ ใช้ join ตรง + ข้อความอาจซ้ำช่วง overlap (~150 chars ต่อรอยต่อ) ระบุใน tool description ให้ client ทราบ; ยังไม่ถูก index → loader parse สด (≤ 20 MB, ถ้า OCR เปิดใช้ timeout 10s)
→ แนบ `{total_chars, truncated, next_offset}` ให้ client เรียกต่อได้
→ **Path guard:** อ่านได้เฉพาะใต้ root ของ workspace ที่รู้จักหรือ `--root` (resolve symlink ด้วย `filepath.EvalSymlinks` + `Clean` ก่อนเทียบ prefix) — MCP tool ห้ามเป็น arbitrary file reader

**`list_workspaces`** `{}` → `[]{name, root_path, documents, chunks, embed_coverage}` — coverage คำนวณจาก diff ต่อ workspace; ที่ corpus ใหญ่ (>100k chunks) ให้ cache ผล diff ไว้ใน worker status แทนการคำนวณสดต่อ call

**`index_status`** `{}` → per-workspace: files/chunks/vectors, `embedding: {configured, model, progress, state: idle|running|backoff|blocked_mismatch}`, phase A running?, watcher active? — เป็น polling tool ราคาถูกแทน progress notifications

### 7.4 Concurrency ภายใน process

SQLite conn เดียว serialize ทุก query; Phase A ถือ tx ช่วงสั้น (CPU-only) — tool call ระหว่าง index โฟลเดอร์ใหญ่จะหน่วงเป็นช่วง ms-level ยอมรับใน v0.5 (บันทึกใน docs) EmbedWorker ไม่ถือ tx; VectorStore มี RWMutex ของตัวเอง ข้อบังคับโค้ดใหม่: **ห้าม query ซ้อนขณะ `sql.Rows` ยังเปิดอยู่** บน connection เดียว (hang ทันที) — helper ทุกตัวต้องอ่าน rows จบก่อนยิง query ถัดไป

**Watcher refactor (จำเป็นก่อน embed เข้า mcp):** `watch.Watcher.Run` ปัจจุบันติดตั้ง `signal.Notify` เอง + self-cancel + prune ticker ต่อ instance (`watcher.go:110-112,129-137`) — ต้อง refactor ให้รับ ctx ภายนอกอย่างเดียว (signal handling ย้ายไป `cmd_watch.go`), รับ serialization function จากภายนอก (mutex เดียวกับ Phase A ของ server), และรวม prune ticker เป็นของ server ไม่ใช่ต่อ root

## 8. Config & CLI changes

- `embedder` **optional**: ไม่ตั้ง (หรือ `provider: none`) = tier 0–1 mode; `config.Validate` (`config.go:247`) เลิกบังคับ; `initAppWithOptions` ไม่ fail เมื่อไม่ได้ตั้งค่า (ตั้งค่าแล้วสร้างไม่ได้ = ยัง error เหมือนเดิม)
- `chat` **optional เช่นกัน** (จำเป็นต่อ zero-config เพราะ `Validate` รันใน `config.Load()` ทุก command, `config.go:241,255-264`): ไม่ตั้ง = `ask`/REPL-ask แจ้ง "no chat provider configured — run `ney init`" ระดับ command ไม่ใช่ fail ตอน load
- default config ใหม่: ไม่มี embedder + ไม่มี chat = **ติดตั้งแล้วใช้ (ผ่าน MCP/search) ได้ทันที zero-config**; `ney init` เป็นทาง upgrade
- `ney status`: `vectors < chunks` → "embedding in progress (X/Y, Z%)" (`cmd_status.go:70`); แสดง "embedder: not configured (keyword-only)" เมื่อไม่มี
- `ney doctor`: แก้ vector_parity (`cmd_doctor.go:354`) ให้แยกสามเคสชัด — orphan (`vec > chunks`) / in-progress (`vec < chunks`) / ok — ปัจจุบันเคส `vec < chunks` ผ่านพร้อมข้อความผิด ("matches") ; embedder-not-configured = info ไม่ใช่ fail
- `ney index --no-embed`; `ney search` แสดง degradation note; ข้อความสรุปของ `cmd_index` (:110 "chunks embedded") เปลี่ยนเป็นแยก "chunked X / embedded Y"
- `ask`/REPL/chat layer: logic ไม่แตะใน milestone นี้ นอกจากจุด optional-chat ข้างต้น

## 9. Error handling หลัก

| สถานการณ์ | พฤติกรรม |
|---|---|
| ไม่มี embedder | search = FTS(+tier 0), `index_status` บอกชัด, ไม่มี error |
| embedder ตั้งไว้แต่ล่ม | search degrade เป็น FTS + `degraded` note; EmbedWorker backoff; ไม่ fail request |
| embedder model เปลี่ยน | EmbedWorker หยุด, สถานะ `blocked_mismatch`, แนะนำ `ney reset` |
| `ney reset`/`index` ระหว่าง `ney mcp` รัน | writer lock ปฏิเสธพร้อมข้อความบอก pid/command ที่ถืออยู่ |
| query ก่อน Phase A จบ | tier 0 + FTS บางส่วน + `index_status` แนบใน response |
| `read_document` นอก root | error ชัดเจน "path outside indexed workspaces" |
| MCP client ตาย/stdin ปิด | graceful shutdown: หยุด watcher/worker, `Vectors.Flush`, ปลด lock, ปิด DB |

## 10. Testing

- unit: EmbedWorker diff logic (pending/orphan, race กับ re-chunk, latched notify, orphan-pass-before-idle, per-workspace scope), Retriever auto mode ทุก combination, tier-0 scanner (caps, binary skip), `read_document` (สองเส้นทาง, path guard/symlink traversal), config validate (embedder+chat optional, bool→mode compat กับ config เก่าจริง), writer lock (ชน, stale pid)
- integration: `ney mcp` in-process transport — 4 tools จริงกับ temp DB + mock embedder ช้า; scenario "ถามก่อน index เสร็จ"; stdout-สะอาด
- regression: `go test ./...` เดิมเขียว; end-state ของ `ney index` (มี embedder) เทียบ flow เก่า — chunk/vector sets เหมือนกัน; **golden test ranking** ครอบ behavior change hybrid→auto (§5)

## 11. Out of scope (milestone นี้)

- ลบ/ลดชั้น chat (`ask`, `internal/chat`) — ตัดสินใจหลัง MCP พิสูจน์ตัวเอง
- HTTP transport / REST API (Phase 4.1), Web UI, summarize command
- OAuth/auth ใดๆ (stdio local เท่านั้น)
- migration mechanism ของ SQLite schema (design นี้จงใจไม่ต้องใช้)
- แก้ต้นทุน HNSW delete-rebuild เชิงโครงสร้าง (บันทึกเป็น known limitation; brute-force ไม่กระทบ)

## 12. การตัดสินใจที่ปิดแล้ว (พร้อมเหตุผล)

1. **Official go-sdk แทน mark3labs/mcp-go** — GA + typed handlers; **re-verify version ตอนเริ่ม M3**
2. **ไม่พึ่ง MCP roots** — มีแผน deprecate; ใช้ tool param (verify พร้อมข้อ 1)
3. **Diff-based pending แทน embedded column** — เลี่ยงการสร้าง migration mechanism; AUTOINCREMENT การันตี ID ไม่ reuse; cost แค่ `IDs()` บน interface
4. **`read_document`: text อ่านไฟล์ตรง / binary ประกอบจาก chunks (approximate)** — reuse งาน parse, PDF อ่านเป็น text ได้ทันที, ความซ้ำช่วง overlap ยอมรับและประกาศ
5. **tier 0 ไม่ parse PDF/DOCX สด** — filename match ครอบเคสหลัก, กัน latency ระเบิด
6. **stdio เท่านั้น** — ครอบ Claude Code/Desktop/Cursor ครบ; HTTP ค่อยตามใน 4.1
7. **Writer lock file** — ราคาถูกที่สุดที่กัน multi-process clobber ได้จริง; ทางเลือก (merge-on-flush, per-shard files) แพงเกิน v0.5
