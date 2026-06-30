# PRD — Ney (`ney`)

> **Product Requirements Document — MVP (v0.1)**
> Local-first AI knowledge engine ที่ index ไฟล์ในเครื่อง แล้วค้นหา/ถามคำถามด้วย semantic search

| Field | Value |
|---|---|
| Project name | **Ney** |
| CLI command | `ney` |
| Version | 0.1 (MVP) |
| Core language | Go |
| Interface | CLI เท่านั้น (Web UI / API เป็น future) |
| Status | Draft |

> ชื่อสั้น จำง่าย เน้นที่ตัว CLI เป็นหลัก — model เดียวกับ `git`, `uv`, `bun`, `mise` ที่คนจำคำสั่งมากกว่าชื่อเต็ม

---

## 0. Core Principles

หลักการกลางที่ใช้ตัดสินใจทุกครั้งที่มี feature request เข้ามา ป้องกันไม่ให้โปรเจกต์ "บวม" และทำให้ผู้ร่วมพัฒนาเข้าใจทิศทางเดียวกัน:

1. **CLI-first** — terminal คือ interface หลัก ทุกอย่างใช้งานผ่านคำสั่งได้
2. **Local-first** — ทำงานบนเครื่องผู้ใช้, รัน offline ได้เมื่อใช้ local model
3. **Library-first** — core logic เป็น Go package ที่นำไป import ได้ CLI เป็นแค่ชั้นบาง ๆ ที่ห่อมัน
4. **Provider-agnostic** — ไม่ผูกกับ vendor หรือ framework (เช่น LangChain) ตัวใดตัวหนึ่ง
5. **Plugin-friendly** — เพิ่ม loader / provider / vector store ใหม่ได้โดยไม่แตะ core
6. **Single binary** — distribute เป็น binary เดียว ไม่ต้องมี runtime ภายนอก
7. **Minimal dependencies** — ใช้ stdlib ก่อน, เพิ่ม dependency เมื่อจำเป็นจริง

> เมื่อมีคนเสนอให้ผูกกับ external DB หรือบริการเฉพาะราย ให้พิจารณาเทียบกับหลักการเหล่านี้ก่อน

---

## 1. Vision & Scope

### Vision
เครื่องมือที่ผู้ใช้ชี้ไปยังโฟลเดอร์ของตัวเอง แล้วค้นหาและถามคำถามกับองค์ความรู้ทั้งหมดได้ทันที โดยข้อมูลไม่ออกจากเครื่อง (ยกเว้นกรณีเลือกใช้ cloud provider เอง)

**Mindset:** นี่ไม่ใช่ "RAG app" หรือ "chat with PDF" แต่เป็น **knowledge engine แบบ CLI** ในตระกูลเดียวกับ `git` / `docker` / `gh` / `uv`

### สิ่งที่ MVP นี้ **เป็น**
- CLI tool ที่ index → search → ask ได้จริง end-to-end
- รองรับ 3 ฟอร์แมต: PDF, DOCX, Markdown
- รองรับ 4 provider: Claude, OpenAI, Gemini, Ollama (ดูข้อจำกัดใน §5)
- มี source citation ทุกคำตอบ

### สิ่งที่ MVP นี้ **ไม่ใช่** (Non-goals)
- ไม่มี Web UI / REST API / MCP server (เป็น roadmap)
- ไม่มี OCR, image search, file watcher (เป็น roadmap)
- ไม่มี hybrid search (semantic อย่างเดียวก่อน แต่เตรียม interface ไว้)
- ยังไม่มี reranker implementation (แต่มี interface ไว้รองรับ)
- ไม่ใช่ document management system

---

## 2. Target Users

- **Developer** — ค้นหาว่า logic อยู่ไฟล์ไหน, ถามว่า feature ทำงานยังไง โดยไม่ต้องไล่ grep
- **Knowledge worker / researcher** — ถามข้อมูลจากกอง PDF/DOCX/notes ที่สะสมไว้
- **คนที่ห่วงความเป็นส่วนตัว** — อยากได้ RAG ที่รันบนเครื่อง ไม่ส่งข้อมูลขึ้น cloud

ทั้งหมดเป็นผู้ใช้ระดับ technical ที่คุ้นเคยกับ terminal และยอมตั้งค่า config เอง

---

## 3. User Stories (MVP)

- ในฐานะผู้ใช้ ฉันสามารถ index โฟลเดอร์ได้ด้วยคำสั่งเดียว และเห็นจำนวนไฟล์/chunk ที่ทำเสร็จ
- ในฐานะผู้ใช้ ฉันสามารถค้นหาด้วย "ความหมาย" ไม่ใช่ keyword ตรงตัว แล้วได้ไฟล์ที่เกี่ยวข้องเรียงตามคะแนน
- ในฐานะผู้ใช้ ฉันสามารถถามคำถามเป็นประโยค แล้วได้คำตอบจาก LLM พร้อมรายการ source (ไฟล์ + ตำแหน่ง)
- ในฐานะผู้ใช้ ฉันสามารถเลือกได้ว่าจะใช้ provider ตัวไหนสำหรับ embedding และตัวไหนสำหรับ LLM ผ่านไฟล์ config
- ในฐานะผู้ใช้ ฉันสามารถ index หลายโฟลเดอร์เป็นคนละ workspace แล้วรู้ว่าผลลัพธ์มาจาก workspace ไหน
- ในฐานะผู้ใช้ ฉันสามารถ re-index ได้โดยระบบข้ามไฟล์ที่ไม่เปลี่ยน (เช็คด้วย hash)
- ในฐานะผู้ใช้ ฉันสามารถรัน `ney doctor` เพื่อเช็คว่าทุกอย่างตั้งค่าถูกต้องก่อนเริ่มใช้

---

## 4. CLI Commands (MVP)

```bash
ney index <path>         # index ไฟล์/โฟลเดอร์ (recursive) เป็น workspace
ney search "<query>"     # semantic search → คืนรายการไฟล์/chunk ที่เกี่ยวข้อง
ney ask "<question>"     # RAG: retrieve → (optional rerank) → LLM → ตอบ + sources
ney status               # สถิติ index ปัจจุบัน (ไฟล์, chunk, workspace, ขนาด DB)
ney config               # แสดง/แก้ไข config ปัจจุบัน
ney doctor               # เช็คความพร้อม: API key, Ollama, SQLite, index, models
ney models               # แสดง provider/model ที่ใช้ได้ + ollama models ที่ติดตั้ง
ney version              # แสดงเวอร์ชัน
ney reset                # ล้าง index (เลือก workspace ได้)
```

### พฤติกรรมแต่ละคำสั่ง

**`ney index <path>`**
- เดิน directory แบบ recursive, เก็บเฉพาะนามสกุลที่รองรับ (`.pdf`, `.docx`, `.md`)
- ผูกกับ workspace (default = ชื่อโฟลเดอร์, หรือกำหนดเองด้วย `--workspace`)
- คำนวณ hash ของไฟล์ → ถ้าไม่เปลี่ยนจากครั้งก่อน ให้ข้าม (incremental เบื้องต้น)
- flow: load → chunk → embed → เก็บ vector ใน VectorStore + metadata ใน SQLite
- output ตัวอย่าง:
  ```
  ✓ 423 files scanned (workspace: documents)
  ✓ 18,210 chunks embedded
  ✓ Index ready (~/.ney/index.db)
  ```

**`ney search "<query>"`**
- embed query → query VectorStore → คืน top-K chunk จัดกลุ่มตามไฟล์
- แสดง path + คะแนน + ตำแหน่ง (line/page) + snippet สั้นๆ
- ไม่เรียก LLM (เร็วและถูก)
- จำกัด scope ได้ด้วย `--workspace`

**`ney ask "<question>"`**
- embed question → retrieve top-K chunk → (optional) rerank → ประกอบ prompt → เรียก LLM
- คำตอบต้องตามด้วยบล็อก `Sources:` ที่อ้างอิงไฟล์ + ตำแหน่งที่ใช้จริง
- output ตัวอย่าง:
  ```
  Billing uses Stripe subscriptions. The webhook updates
  invoices, and failed payments retry every 3 days.

  Sources:
    billing.md (lines 12–40)
    stripe/webhook.ts (lines 88–120)
  ```

**`ney doctor`**
- เช็ค: config valid ไหม, API key ของ provider ที่เลือกมีไหม, Ollama daemon ตอบไหม, model ที่ระบุติดตั้งหรือยัง, SQLite เขียนได้ไหม, index มีข้อมูลไหม
- รายงานเป็น ✓ / ✗ ต่อรายการ พร้อมคำแนะนำวิธีแก้

**`ney models`**
- แสดง provider ที่ config รองรับ (Anthropic / OpenAI / Gemini / Ollama) และบทบาท (embed/chat)
- สำหรับ Ollama: list model ที่ติดตั้งในเครื่องจริง

### Flags ที่ควรมี
- `--workspace <name>` — เลือก/จำกัด workspace
- `--top-k <n>` (default 8) — จำนวน chunk ที่ดึง
- `--provider <name>` — override provider ชั่วคราว
- `--json` — **ทุก command รองรับ** เพื่อให้ Claude Code / Cursor / VS Code เรียกใช้ได้
- `--path <dir>` — จำกัด scope การค้นหาเฉพาะบางโฟลเดอร์

---

## 5. Provider Architecture ⚠️ จุดสำคัญ

ระบบแยกความรับผิดชอบเป็น 2 บทบาท และ provider แต่ละตัวรองรับไม่เท่ากัน:

| Provider | Embedding | Chat/LLM | หมายเหตุ |
|---|:---:|:---:|---|
| **Claude (Anthropic)** | ❌ | ✅ | **ไม่มี embedding API** ใช้เป็น LLM ตอบคำถามเท่านั้น |
| **OpenAI** | ✅ | ✅ | embedding: `text-embedding-3-small/large` |
| **Gemini (Google)** | ✅ | ✅ | embedding: `text-embedding-004` / `gemini-embedding` |
| **Ollama (local)** | ✅ | ✅ | embedding: `nomic-embed-text`, `bge-m3`; default ของ local-first |

> **ข้อควรระวังเชิงออกแบบ:** ถ้าผู้ใช้เลือก Claude เป็น chat provider จะ **ต้อง** เลือก embedder ตัวอื่นเสมอ (OpenAI/Gemini/Ollama) ระบบต้อง validate ตอนอ่าน config และแจ้ง error ที่ชัดเจนถ้าตั้ง Claude เป็น embedder (`ney doctor` ควรจับเคสนี้)

> **ข้อควรระวังเชิง consistency:** เปลี่ยน embedding model = vector space เปลี่ยน → vector เดิมใช้ร่วมกับ query ใหม่ไม่ได้ ระบบต้องบันทึก embedder + dimension ที่ใช้ (ตาราง `providers`/`index_meta`) และเตือนให้ re-index ถ้าผู้ใช้สลับ embedder

### Interfaces ใน Go (แนวทาง)

```go
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Dimensions() int
    ModelID() string
}

type ChatModel interface {
    Complete(ctx context.Context, prompt string, ctxChunks []Chunk) (string, error)
    ModelID() string
}
```

แต่ละ provider = หนึ่ง implementation; เลือกใช้งานผ่าน config (factory pattern)

---

## 6. Configuration

ไฟล์ config: `~/.ney/config.yaml` (อ่าน API key จาก env var ได้เพื่อไม่เก็บ secret ในไฟล์)

```yaml
# embedder: ใช้สร้าง vector (ห้ามเป็น claude)
embedder:
  provider: ollama          # openai | gemini | ollama
  model: bge-m3
  # endpoint: http://localhost:11434   # เฉพาะ ollama

# chat: ใช้ตอบคำถามใน `ney ask`
chat:
  provider: claude          # claude | openai | gemini | ollama
  model: claude-sonnet-4-6

# การดึง context
retrieval:
  top_k: 8
  max_context_chars: 12000
  rerank: false             # เปิดเมื่อมี reranker (ดู §8)

# การ chunk
chunking:
  strategy: markdown        # character | sentence | paragraph | markdown
  target_chars: 1200        # ~1000-1500 ตามเหมาะสม
  overlap_chars: 150

# ความเป็นส่วนตัว — ปิดตั้งแต่วันแรก
telemetry: false

# API keys อ่านจาก env (แนะนำ) หรือใส่ตรงนี้
# ANTHROPIC_API_KEY, OPENAI_API_KEY, GEMINI_API_KEY
```

> `telemetry: false` เป็น default ชัดเจน เพราะคำถามแรกของชุมชน open-source คือ "ส่งข้อมูลออกไหม"

---

## 7. Data Model (SQLite)

```
workspaces
  id            INTEGER PK
  name          TEXT UNIQUE   -- เช่น code, documents, notes
  root_path     TEXT
  created_at    TIMESTAMP

documents
  id            INTEGER PK
  workspace_id  INTEGER FK -> workspaces.id
  path          TEXT UNIQUE
  type          TEXT          -- pdf | docx | md
  hash          TEXT          -- เช็คว่าเปลี่ยนไหม
  size_bytes    INTEGER
  indexed_at    TIMESTAMP

chunks
  id            INTEGER PK
  document_id   INTEGER FK -> documents.id
  chunk_index   INTEGER
  content       TEXT
  start_pos     INTEGER       -- line (md/docx) หรือ page (pdf)
  end_pos       INTEGER

providers
  id            INTEGER PK
  role          TEXT          -- embedder | chat
  name          TEXT          -- ollama | openai | gemini | anthropic
  model         TEXT
  dimensions    INTEGER       -- เฉพาะ embedder
  version       TEXT
  created_at    TIMESTAMP

index_meta
  key           TEXT PK       -- เช่น schema_version, active_embedder
  value         TEXT
```

- **Vector** เก็บใน VectorStore (turbovec), key = `chunks.id`
- **SQLite** ใช้ driver `modernc.org/sqlite` (pure Go, ไม่ต้อง cgo → cross-compile ง่าย)
- ตาราง `workspaces` รองรับ `ney search --workspace code` ในอนาคต
- ตาราง `providers` บันทึกว่า index สร้างด้วย embedder/dimension อะไร เพื่อเตือนเมื่อต้อง re-index

---

## 8. Processing Pipeline & Interfaces

```
Index:  Files → Loader → Chunk → Embed → VectorStore + SQLite
Ask:    Question → Embed → Retriever → (optional) Reranker → LLM → Answer + Sources
```

### Loader (เดิมเรียก Extract)
ทุกแหล่งข้อมูลคือ "resource ที่ต้องโหลด" ไม่ใช่แค่ "extract text" — ตั้งชื่อให้รองรับอนาคต (Git Loader, Notion Loader, HTML Loader)

```go
type Loader interface {
    Load(ctx context.Context, path string) ([]Document, error)
    Supports(path string) bool
}
```

- **MarkdownLoader** — อ่าน text ตรง, เก็บ heading เป็น metadata, ตำแหน่ง = line number
- **PDFLoader** — ดึง text ต่อหน้า, ตำแหน่ง = page number (scan PDF ที่เป็นภาพ → text ว่าง → ข้ามก่อน, OCR เป็น roadmap)
- **DOCXLoader** — ดึง paragraph/heading จาก XML, ตำแหน่ง = paragraph index

### ChunkStrategy (interface ตั้งแต่แรก)
Go ไม่มี tokenizer กลาง และ token แต่ละ provider ไม่เท่ากัน MVP จึงใช้ **character-based** แล้วค่อยเพิ่ม tokenizer chunking ภายหลัง

```go
type ChunkStrategy interface {
    Chunk(doc Document) []Chunk
}
```

- MVP implement: `CharacterChunker`, `ParagraphChunker`, `MarkdownHeadingChunker`, (เริ่มต้น) `SentenceChunker`
- target ~1000–1500 ตัวอักษร, overlap ~150
- Future: `TokenizerChunker`

### VectorStore (สำคัญที่สุด — อย่า hardcode turbovec)
core ไม่รู้จัก turbovec ตรง ๆ รู้จักแค่ interface — ถ้า turbovec หยุดพัฒนา โปรเจกต์ไม่ตาย

```go
type VectorStore interface {
    Add(ctx context.Context, items []VectorItem) error
    Search(ctx context.Context, query []float32, k int) ([]SearchResult, error)
    Delete(ctx context.Context, ids []string) error
}
```

- MVP implementation เดียว: **TurbovecStore**
- Future: เปลี่ยนเป็น store อื่นได้โดยไม่แตะ pipeline

### Retriever / Reranker (เตรียม interface ไว้)
ยังไม่มี implementation reranker ใน MVP แต่วาง slot ไว้ให้ Jina / Cohere / BGE เสียบได้

```go
type Reranker interface {
    Rerank(ctx context.Context, query string, results []SearchResult) []SearchResult
}
```

- ถ้า `retrieval.rerank: false` → ข้าม, ส่ง retriever result ตรงเข้า LLM

### Embed
- batch หลาย chunk ต่อ request เพื่อความเร็ว
- จัดการ rate limit / retry สำหรับ cloud provider

---

## 9. Library Choices (Go)

| งาน | Library | เหตุผล |
|---|---|---|
| CLI | `spf13/cobra` + `viper` | มาตรฐาน subcommand + config |
| SQLite | `modernc.org/sqlite` | pure Go, ไม่ต้อง cgo |
| PDF | `pdfcpu` / `ledongthuc/pdf` | ดึง text ได้, ต้องเทียบคุณภาพ |
| DOCX | `nguyenthenguyen/docx` / unzip + parse XML เอง | DOCX = zip ของ XML |
| Markdown | parse บรรทัดตรง / `goldmark` ถ้าต้อง AST | เบา |
| HTTP client | `net/http` (stdlib) | เรียก provider API เอง |

> library PDF/DOCX ของ Go ยังไม่นิ่งเท่า Python — wrap ไว้หลัง `Loader` interface เพื่อสลับ library ได้ภายหลัง (ตรงกับหลักการ Minimal dependencies / Library-first)

---

## 10. Project Structure (เสนอ)

```
ney/
  cmd/ney/            # main + cobra commands (index, search, ask, doctor, models, version)
  internal/
    index/            # pipeline: scan, orchestrate
    loader/           # markdown.go, pdf.go, docx.go (Loader)
    chunk/            # character.go, paragraph.go, markdown.go (ChunkStrategy)
    embed/            # provider implementations (Embedder)
    chat/             # provider implementations (ChatModel)
    vectorstore/      # turbovec.go (VectorStore)
    rerank/           # interface + (future) implementations
    store/            # sqlite layer
    search/           # retriever logic
    config/           # โหลด/validate config
  go.mod
```

---

## 11. Success Criteria (MVP)

- `ney index` โฟลเดอร์ ~500 ไฟล์ผสม PDF/DOCX/MD สำเร็จโดยไม่ crash
- `ney search` คืนผลที่ "เกี่ยวข้องเชิงความหมาย" แม้ไม่มี keyword ตรงในไฟล์
- `ney ask` ตอบได้พร้อม source ที่อ้างอิงไฟล์จริงและตำแหน่งถูกต้อง
- `ney doctor` จับ config ผิด (เช่น Claude เป็น embedder) ได้
- สลับ provider ผ่าน config ได้โดยไม่แก้โค้ด
- ทุก command มี `--json`
- รันแบบ offline เต็มรูปแบบได้เมื่อใช้ Ollama ทั้ง embedder และ chat
- distribute เป็น single binary ต่อ OS/arch

---

## 12. Future Roadmap (หลัง MVP)

- Reranker implementations (Jina / Cohere / BGE)
- Tokenizer-based chunking
- Workspace-scoped search UX (`ney search --workspace code` เต็มรูปแบบ)
- Incremental indexing เต็มรูปแบบ + File Watcher
- Hybrid Search (keyword BM25 + semantic)
- OCR + Image Search
- Loader plugins: Git, Notion, Obsidian, Confluence, HTML, JSON
- Web UI Dashboard + REST API + MCP Server
- VS Code Extension
- Related documents / duplicate detection / multi-language tuning

---

## 13. Open Questions

- **turbovec** — เป็น Go library, binary แยก, หรือ service? มี Go client / API แบบไหน? (กระทบ `vectorstore/turbovec.go` โดยตรง — interface พร้อมแล้ว เหลือ implementation)
- รองรับ scan PDF ในอนาคตด้วย OCR engine ตัวไหน (Tesseract ผ่าน cgo จะกระทบ single-binary)
- กลยุทธ์เก็บ API key — env var อย่างเดียว หรือมี keyring integration
- default chunk strategy ต่อ format — markdown ใช้ heading, pdf/docx ใช้อะไรดีที่สุด
