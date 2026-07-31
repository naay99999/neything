# Roadmap — Ney (`ney`)

> Post-MVP roadmap สำหรับ implement ทีละ phase  
> MVP v0.1 ครบตาม [PRD](./PRD.md) แล้ว — เอกสารนี้เป็นแผนงานหลัง v0.1

| Field | Value |
|---|---|
| Current version | 0.1 (MVP) |
| Status | Active |
| Last updated | 2026-07-31 |

---

## 2026-07-31 — Layered context repositioning (breaking)

หลัง brainstorm session สรุปว่า ney ควรเป็น **personal context server สำหรับ AI** ไม่ใช่แค่ document search engine — AI client ทุกตัวควรรู้ว่าผู้ใช้เป็นใคร กำลังทำโปรเจกต์อะไรอยู่ ตั้งแต่ต้น session แล้วค่อยขุดลึกด้วย search เมื่อจำเป็น (layered context: L1 = profile + active projects, L2 = search เดิม)

**ถอดออก:**
- Loader ที่ไม่ใช่ md/txt ทั้งหมด: PDF (+ OCR path ทั้ง `pdftoppm`/`tesseract`), DOCX, HTML, JSON, Confluence XML, Notion export — เหลือแค่ Markdown (+ Obsidian wikilink metadata) และ `.txt`
- Chunk strategy `page` (มีไว้สำหรับ PDF อย่างเดียว)
- Config keys `loaders.ocr.*` (config เก่าที่ยังมี key นี้โหลดได้ปกติ — viper ข้าม key ที่ไม่รู้จัก เหมือน policy เดิม)
- Tool `list_workspaces` — แทนที่ด้วย `list_projects` (ดูด้านล่าง)
- `internal/discover`'s เดิม (deep-walk Home + iCloud หา document cluster) — เขียนใหม่ทั้งหมด (ดูด้านล่าง)

**เพิ่ม:**
- 4 MCP tool ใหม่ ครบเป็น 9 tools: `get_context` (L1 — profile + active projects + how-to, live scan, never fails), `list_projects` (L1.5 — per-project detail, แทน `list_workspaces`), `remember` (เขียน memory file ลง `~/.ney/memory`, ค้นเจอผ่าน `search_documents` ในไม่กี่วินาที), `update_profile` (แก้ section ใน `profile.md`)
- `internal/context` (package `neycontext`): `ScanRepos` (live git scan ของ `context.dev_roots`, ไม่มี DB table), `LoadProfile`/`UpdateProfile`, `Render` (L1 markdown), `WriteMemory` — stateless ทั้งหมด mirror ปรัชญา "diff, don't record" ของ Phase B
- `internal/discover` เขียนใหม่: จาก "หา document cluster" → "หา git repo ใต้ `context.dev_roots`" (wrapper บาง ๆ เหนือ `context.ScanRepos`)
- Config section ใหม่ `context.dev_roots` (default `~/workspace` ถ้ามี) และ `context.active_days` (default 14)
- Memory workspace (`~/.ney/memory`) ลงทะเบียนเป็น served root ภายใน `runMCP` เองทั้ง read-write และ read-only mode (ไม่ผ่าน `index_folder`) — index+watch เฉพาะ write mode
- Wizard reflow: `[1/4]` สแกน repo (แทนสแกน document folder) → เลือก+index → `[2/4]` bootstrap `profile.md` (2-3 คำถามสั้นๆ, ข้ามถ้ามีไฟล์อยู่แล้ว) → `[3/4]` register AI clients (เหมือนเดิม) → `[4/4]` embedder (เหมือนเดิม) — ตัด OCR step ออก

**Migration:** index เก่าที่มี chunk จาก pdf/docx/html/json/confluence/notion → รัน `ney reset` แล้ว re-index ใหม่ (acceptable ที่ single-user stage นี้); md content เดิมยังค้นได้ตามปกติจาก index เก่าที่ยังไม่ reset แต่จะไม่มีการ re-embed format ที่ถูกตัดออก

**Positioning ใหม่:** ney = personal context server สำหรับ AI — search ยังอยู่ แต่กลายเป็นกลไก recall ภายใน ไม่ใช่ product หลักอีกต่อไป

Spec: [`docs/superpowers/specs/2026-07-31-layered-context-design.md`](./superpowers/specs/2026-07-31-layered-context-design.md), plan: [`docs/superpowers/plans/2026-07-31-layered-context.md`](./superpowers/plans/2026-07-31-layered-context.md)

## 2026-07-19 — MCP-first refocus (breaking)

หลัง product review สรุปว่า differentiator จริงของ ney คือ **MCP server แบบ single-binary zero-config ที่ให้ AI client ค้น/อ่านเอกสาร local ได้อย่างปลอดภัย** จึงตัดฟีเจอร์ที่ซ้ำกับเครื่องมือที่ดีกว่าออก และเพิ่ม security:

**ถอดออก:**
- `ney ask` + chat providers ทั้งหมด (`internal/chat/`) — Claude ผ่าน MCP ตอบได้ดีกว่า local RAG chat; คนต้องการ RAG แบบ standalone ใช้ MCP client ใดก็ได้แทน
- REPL / interactive mode + startup banner — ซ้ำกับ AI client ที่ต่อผ่าน MCP; `ney` เปล่าๆ แสดง help ปกติ
- Git history loader (`loaders.git.recent_commits`) — `git log -S` และ agent ทำได้อยู่แล้ว

**เพิ่ม:**
- `internal/pathfilter` — deny list ในตัว (dotfiles + `*secret*`, `*credential*`, `*password*`, `*.key`, `*.pem`, `id_rsa*`, ...) บังคับใช้ทั้ง indexer, live scan, และ `read_document`; ผู้ใช้เพิ่ม pattern เองได้ผ่าน `index.exclude`
- Read-only fallback — `ney mcp` ตัวที่สอง (เช่น Claude Desktop + Claude Code พร้อมกัน) เสิร์ฟ search/read จาก index เดิมแทนที่จะตายเพราะ writer lock; `index_status` รายงาน `mode: "read-only"`
- `PRAGMA busy_timeout=5000` กัน SQLITE_BUSY ตอนสอง process เปิด DB พร้อมกัน

**ตัดจากแผน:** REST API (`ney serve`) และ Web UI — พาไปแข่งในตลาดที่แออัด (AnythingLLM, Open WebUI) โดยไม่หนุน positioning หลัก; VS Code extension ยังอยู่ในแผน

config เก่าที่ยังมี key `chat:`, `retrieval.max_context_chars`, `loaders.git` ยังโหลดได้ปกติ (viper ข้าม key ที่ไม่รู้จัก)

## 2026-07-19 (ภาค 2) — First-run setup wizard + index_folder

- **Setup wizard**: `ney` เปล่าๆ บนเครื่องใหม่ (หรือ `ney init`) → สแกน Home + iCloud Drive หาจุดที่เอกสารกระจุก (`internal/discover`, concentration heuristic) → เลือกโฟลเดอร์ → เสนอติดตั้ง OCR ผ่าน brew (tha+eng) → ลงทะเบียน Claude Desktop / Claude Code / Codex อัตโนมัติแบบถามรายตัว (backup .bak เสมอ, ไม่ทับไฟล์ที่ parse ไม่ได้) → embedder เป็น optional
- **Source of truth = ตาราง workspaces**: client ทุกตัวลงทะเบียนแบบไม่มี args (`ney mcp`) จึงเห็นชุดเดียวกันเสมอ
- **Tool ที่ 6 `index_folder`**: สั่ง AI ให้ index โฟลเดอร์เพิ่มได้กลาง session — เป็น workspace ถาวร, อ่านได้ทันที (dynamic rootSet), เปิด watcher ให้, จำกัด home+iCloud + secret deny เหมือนเดิม
- Spec: `docs/superpowers/specs/2026-07-19-first-run-setup-design.md`

---

## MVP v0.1 — Done ✓

สิ่งที่ ship แล้วตาม PRD §11:

- CLI ครบ: `index`, `search`, `ask`, `status`, `config`, `doctor`, `models`, `version`, `reset`
- Loaders: `.md`, `.pdf`, `.docx` *(pdf/docx removed 2026-07-31 — see md-only refocus above; current loaders: `.md`/`.markdown` + `.txt`)*
- Providers: Claude, OpenAI, Gemini, Ollama (+ LM Studio ใน config)
- Workspace + flags: `--workspace`, `--path`, `--top-k`, `--provider`, `--json`
- Hash-based skip บน re-index
- Source citations ใน `ney ask`
- Embedder consistency check + `ney doctor`
- Data: SQLite (`~/.ney/index.db`) + `BruteForceStore` (`~/.ney/vectors.bin`)

### สิ่งที่ PRD วาง slot ไว้แต่ยังไม่ implement

| Item | สถานะ |
|---|---|
| Reranker | Done — Cohere, Jina, Ollama/local |
| Hybrid search | Done — SQLite FTS5 + RRF |
| Tokenizer chunking | Done — `strategy: tokenizer` (~4 chars/token) |
| Incremental indexing เต็มรูปแบบ | Done — prune missing files, orphan vectors, rename-by-hash |
| File watcher | Done — `ney watch <path>` |
| TurbovecStore | Done — `HNSWStore` (pure Go) + `BruteForceStore` fallback |
| OCR, loaders เพิ่ม, Web UI, REST API, MCP, VS Code | OCR + loaders done (Phase 3) *then removed 2026-07-31 — md-only refocus*; MCP done (Phase 4.2); Web UI, REST API, VS Code ยังไม่มี |

---

## Implementation Phases

Implement ตามลำดับ phase — แต่ละ phase ควร merge เป็น milestone แยก (เช่น v0.2, v0.3)

---

### Phase 1 — Search Quality

**เป้าหมาย:** ปรับปรุงความแม่นยำของ `search` และ `ask` โดยไม่เปลี่ยน UX หลัก

#### 1.1 Reranker

- [x] Implement `Reranker` (Jina / Cohere / BGE — เลือกอย่างน้อย 1 ตัวก่อน)
- [x] Factory ใน `internal/config` (`NewReranker`)
- [x] Wire ใน `ney ask`: retrieve → rerank → trim context → LLM
- [x] รองรับ `retrieval.rerank: true` ใน config
- [x] อัปเดต `ney doctor` / `ney models`
- [x] Tests

**ไฟล์ที่เกี่ยวข้อง:** `internal/rerank/`, `cmd/ney/cmd_ask.go`, `internal/config/config.go`

#### 1.2 Hybrid Search (BM25 + Semantic)

- [x] ออกแบบ interface สำหรับ keyword search (SQLite FTS หรือ in-memory BM25)
- [x] รวม score semantic + BM25 (reciprocal rank fusion หรือ weighted)
- [x] Config: เปิด/ปิด hybrid, น้ำหนักแต่ละสignal
- [x] ใช้ใน `search` และ `ask`
- [x] Tests

#### 1.3 Tokenizer-based Chunking

- [x] `TokenizerChunker` ใน `internal/chunk/`
- [x] กำหนด tokenizer ต่อ provider หรือใช้ approximation (tiktoken-style)
- [x] Config option: `chunking.strategy: tokenizer`
- [x] เอกสาร: เปลี่ยน chunk strategy = ต้อง re-index

---

### Phase 2 — Index Reliability & Scale

**เป้าหมาย:** index สะท้อนไฟล์จริง + รองรับ corpus ใหญ่ขึ้น

#### 2.1 Incremental Indexing (เต็มรูปแบบ)

- [x] ลบ document/chunk/vector ของไฟล์ที่หายจาก workspace ตอน re-index
- [x] ลบ vector เก่าเมื่อ re-index ไฟล์ที่เปลี่ยน (chunk ID ใหม่)
- [x] จัดการ rename/move (optional: detect ด้วย hash + path diff)
- [x] รายงานใน output: `files_removed`, `vectors_pruned`
- [x] Tests

**ไฟล์ที่เกี่ยวข้อง:** `internal/index/pipeline.go`, `internal/vectorstore/`, `internal/store/`

#### 2.2 File Watcher

- [x] คำสั่งใหม่ เช่น `ney watch <path>` หรือ flag `--watch` บน index
- [x] Debounce + batch re-index
- [x] Graceful shutdown (SIGINT)
- [x] เอกสารการใช้งาน

#### 2.3 Vector Store Upgrade

- [x] ประเมิน backend: HNSW / Turbovec / อื่น ๆ (ดู Open Questions)
- [x] Implementation ใหม่ผ่าน `VectorStore` interface
- [x] Migration path จาก `vectors.bin` (หรือ `ney reset` + re-index)
- [x] Benchmark กับ corpus ~10k+ chunks
- [x] เก็บ `BruteForceStore` เป็น fallback สำหรับ index เล็ก (optional)

---

### Phase 3 — Content Coverage

**เป้าหมาย:** index แหล่งข้อมูลและรูปแบบไฟล์เพิ่ม

#### 3.1 OCR & Scanned PDFs — ~~removed 2026-07-31~~ (md-only refocus, PDF loader + OCR path deleted entirely)

- [x] ~~ตัดสินใจ OCR engine (external CLI: pdftoppm + tesseract)~~
- [x] ~~Fallback ใน PDF loader เมื่อ extract text ว่าง~~
- [x] ~~Config: เปิด/ปิด OCR~~
- [x] ~~Tests กับ PDF scan ตัวอย่าง~~

#### 3.2 Loader Plugins

Implement ทีละ loader ผ่าน `Loader` interface:

- [x] ~~HTML (`.html`, `.htm`)~~ — removed 2026-07-31
- [x] ~~JSON (structured docs)~~ — removed 2026-07-31
- [x] Git (repo history — recent commits via `git log`) — removed 2026-07-19 (MCP-first refocus, see above)
- [x] Obsidian vault (`.md` + wikilinks metadata) — kept
- [x] ~~Notion export~~ — removed 2026-07-31
- [x] ~~Confluence export~~ — removed 2026-07-31

ลงทะเบียนใน `loader/registry.go` + `supportedExts` ใน `internal/index/pipeline.go`. หลัง 2026-07-31 เหลือแค่ Markdown (+ Obsidian) และ `.txt`

#### 3.3 Default Chunk Strategy ต่อ Format

- [x] Auto-select: markdown → heading, pdf → page ~~(page strategy removed 2026-07-31 with the PDF loader)~~, docx → paragraph ~~(docx removed 2026-07-31)~~
- [x] Override ได้ใน config (`chunking.strategy: auto` + `by_format`)
- [x] อัปเดต default config + docs

---

### Phase 4 — Integrations & Ecosystem

**เป้าหมาย:** ให้ tools อื่น (IDE, agents, scripts) ใช้ Ney ได้ง่าย

#### 4.1 REST API

- [ ] HTTP server (local only by default, e.g. `127.0.0.1:7423`)
- [ ] Endpoints: `/search`, `/ask`, `/index`, `/status`, `/health`
- [ ] JSON request/response (สอดคล้องกับ `--json` output ที่มีอยู่)
- [ ] Auth optional (token / localhost-only)
- [ ] คำสั่ง `ney serve`

#### 4.2 MCP Server — Done ✓

Implemented per [`docs/superpowers/specs/2026-07-12-mcp-tiered-search-design.md`](./superpowers/specs/2026-07-12-mcp-tiered-search-design.md) (expanded scope beyond the original bullets below: tiered zero-setup search, two-phase indexing, optional providers).

- [x] Expose tools: `search_documents`, `read_document`, `list_workspaces`, `index_status`
- [x] ใช้ index จาก `~/.ney/` เดียวกับ CLI
- [x] คำสั่ง `ney mcp` (stdio transport)
- [x] เอกสาร integration กับ Cursor / Claude Code / Claude Desktop (README §MCP)

#### 4.3 Web UI Dashboard

- [ ] Browse workspaces, search, ask
- [ ] แสดง sources + snippet
- [ ] เรียก REST API ด้านหลัง
- [ ] Embed ใน binary หรือ serve static (ตัดสินใจตอน implement)

#### 4.4 VS Code Extension

- [ ] Command palette: search / ask against workspace
- [ ] ใช้ MCP หรือ REST API
- [ ] Publish แยก repo (optional)

---

### Phase 5 — Advanced Features

**เป้าหมาย:** คุณภาพชีวิตและ analytics — หลัง core นิ่ง

- [ ] Related documents (similarity จาก vector ใกล้เคียง)
- [ ] Duplicate / near-duplicate detection
- [ ] Multi-language tuning (embedder ต่อภาษา, chunking ปรับตาม locale)
- [ ] Keyring integration สำหรับ API keys (ดู Open Questions)

---

## Open Questions

ตัดสินใจก่อนเริ่ม phase ที่เกี่ยวข้อง:

| # | คำถาม | Phase | ตัวเลือก |
|---|---|---|---|
| 1 | Vector store backend | 2.3 | Turbovec, HNSW (e.g. usearch), brute-force ต่อ |
| 2 | OCR engine | 3.1 | Done — external CLI (pdftoppm + tesseract), optional; **removed 2026-07-31** (md-only refocus) |
| 3 | API key storage | 5 | env only (ปัจจุบัน), OS keyring |
| 4 | Hybrid search storage | 1.2 | SQLite FTS5, in-memory BM25 |
| 5 | Reranker ตัวแรก | 1.1 | Jina, Cohere, BGE-local |
| 6 | Web UI delivery | 4.3 | embedded static vs separate frontend repo |

---

## Milestone Suggestions

| Version | Bundle | Rationale |
|---|---|---|
| **v0.2** | Phase 1 ทั้งหมด | Done — rerank, hybrid, tokenizer chunking |
| **v0.3** | Phase 2 ทั้งหมด | Done — incremental index, watch, HNSW vector store |
| **v0.4** | Phase 3 ทั้งหมด | Done — OCR, loaders, per-format chunking; most of it (OCR/PDF/DOCX/HTML/JSON/Confluence/Notion) removed 2026-07-31, see dated entry above |
| **v0.5** | Phase 4.2 (MCP) done; REST API + UI ต่อ | เปิด ecosystem |
| **v1.0** | Phase 5 + polish | stable API, docs, release |

---

## Core Principles (จาก PRD §0)

ทุก phase ต้องผ่านหลักการเหล่านี้:

1. **CLI-first** — feature ใหม่ต้องใช้ผ่าน CLI ได้ก่อน
2. **Local-first** — offline ได้เมื่อใช้ local model
3. **Library-first** — logic อยู่ใน `internal/`, CLI เป็นชั้นบาง
4. **Provider-agnostic** — ไม่ผูก vendor
5. **Plugin-friendly** — loader / provider / vector store เสียบได้
6. **Single binary** — ระวัง cgo และ runtime ภายนอก
7. **Minimal dependencies** — stdlib ก่อน

---

## References

- [PRD — MVP v0.1](./PRD.md)
- [README — Roadmap summary](../README.md#roadmap)
- [CLAUDE.md — Architecture](../CLAUDE.md)
