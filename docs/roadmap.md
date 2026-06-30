# Roadmap — Ney (`ney`)

> Post-MVP roadmap สำหรับ implement ทีละ phase  
> MVP v0.1 ครบตาม [PRD](./PRD.md) แล้ว — เอกสารนี้เป็นแผนงานหลัง v0.1

| Field | Value |
|---|---|
| Current version | 0.1 (MVP) |
| Status | Active |
| Last updated | 2026-07-01 |

---

## MVP v0.1 — Done ✓

สิ่งที่ ship แล้วตาม PRD §11:

- CLI ครบ: `index`, `search`, `ask`, `status`, `config`, `doctor`, `models`, `version`, `reset`
- Loaders: `.md`, `.pdf`, `.docx`
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
| OCR, loaders เพิ่ม, Web UI, REST API, MCP, VS Code | OCR + loaders done (Phase 3); Web UI, REST API, MCP, VS Code ยังไม่มี |

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

#### 3.1 OCR & Scanned PDFs

- [x] ตัดสินใจ OCR engine (external CLI: pdftoppm + tesseract)
- [x] Fallback ใน PDF loader เมื่อ extract text ว่าง
- [x] Config: เปิด/ปิด OCR
- [x] Tests กับ PDF scan ตัวอย่าง

#### 3.2 Loader Plugins

Implement ทีละ loader ผ่าน `Loader` interface:

- [x] HTML (`.html`, `.htm`)
- [x] JSON (structured docs)
- [x] Git (repo history — recent commits via `git log`)
- [x] Obsidian vault (`.md` + wikilinks metadata)
- [x] Notion export
- [x] Confluence export

ลงทะเบียนใน `loader/registry.go` + `supportedExts` ใน `internal/index/pipeline.go`

#### 3.3 Default Chunk Strategy ต่อ Format

- [x] Auto-select: markdown → heading, pdf → page, docx → paragraph
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

#### 4.2 MCP Server

- [ ] Expose tools: `search`, `ask`, `status`
- [ ] ใช้ index จาก `~/.ney/` เดียวกับ CLI
- [ ] คำสั่ง `ney mcp` (stdio transport)
- [ ] เอกสาร integration กับ Cursor / Claude Code

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
| 2 | OCR engine | 3.1 | Done — external CLI (pdftoppm + tesseract), optional |
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
| **v0.4** | Phase 3 ทั้งหมด | Done — OCR, loaders, per-format chunking |
| **v0.5** | Phase 4 (API + MCP ก่อน UI) | เปิด ecosystem |
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
