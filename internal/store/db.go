package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	db *sql.DB
}

type Workspace struct {
	ID        int64
	Name      string
	RootPath  string
	CreatedAt time.Time
}

type Document struct {
	ID          int64
	WorkspaceID int64
	Path        string
	Type        string
	Hash        string
	SizeBytes   int64
	IndexedAt   time.Time
}

type Chunk struct {
	ID         int64
	DocumentID int64
	ChunkIndex int
	Content    string
	StartPos   int
	EndPos     int
}

type StoreStats struct {
	WorkspaceCount int
	DocumentCount  int
	ChunkCount     int
}

// dbFileMode is the permission index.db and its WAL sidecars are held at.
// The database stores the full text of every indexed document (and -wal /
// -shm hold recently written pages of that same text), so the files are
// owner-only even though ~/.ney is already 0700: the directory mode is a
// single layer that a restore from backup, an rsync, a container
// bind-mount or a cloud-sync client can loosen without anyone noticing.
// SQLite itself creates these files with 0644 under the common umask 022.
const dbFileMode os.FileMode = 0o600

// tightenDBPerms narrows the database file and its WAL sidecars to 0600.
// Deliberately best-effort and non-fatal: the -wal/-shm files don't exist
// until SQLite actually enters WAL mode (so a chmod on them legitimately
// hits ENOENT on a fresh open), and no chmod failure — read-only mount,
// mode-less filesystem — should ever prevent ney from starting. A chmod is
// a filesystem call, not a query, so it's safe to make here regardless of
// the SetMaxOpenConns(1) / open-sql.Rows constraints.
func tightenDBPerms(dbPath string) {
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		_ = os.Chmod(p, dbFileMode)
	}
}

func Open(dbPath string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	sqldb, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqldb.SetMaxOpenConns(1)
	// busy_timeout: with WAL, a second process can open/read while a writer
	// holds a write transaction (e.g. read-only `ney mcp` starting up while a
	// read-write one is indexing) — wait briefly instead of failing SQLITE_BUSY.
	// synchronous=NORMAL is safe (not just fast) under WAL: durability is only
	// at risk on an OS crash, not a process crash, and a checkpoint still
	// fsyncs. cache_size/temp_store trade memory for fewer spills to disk
	// during indexing's chunk/FTS writes.
	if _, err := sqldb.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA synchronous=NORMAL; PRAGMA cache_size=-64000; PRAGMA temp_store=MEMORY;"); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}
	// The pragma Exec is what actually opens the connection and creates the
	// db file (sql.Open alone is lazy), so this is the first moment the file
	// exists to chmod.
	tightenDBPerms(dbPath)
	db := &DB{db: sqldb}
	if err := db.migrate(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// Again after migrate: its schema writes are the first WAL traffic, so
	// -wal/-shm are guaranteed to exist by now even if they didn't above.
	// They live as long as this connection does (MaxOpenConns(1) with an
	// idle conn that the pool never reaps), so one pass here is enough.
	tightenDBPerms(dbPath)
	return db, nil
}

func (d *DB) Close() error { return d.db.Close() }

func (d *DB) migrate() error {
	if _, err := d.db.Exec(schema); err != nil {
		return err
	}
	if err := d.BackfillFTS(); err != nil {
		return err
	}
	return d.migrateChunkerVersion()
}

// chunkerVersion identifies the chunk-boundary algorithm the rows in `chunks`
// were produced by. Bump it whenever a change to internal/chunk makes the
// stored chunks wrong or wasteful, and every existing index rebuilds itself
// on its next indexing pass.
//
// v2: the character chunker used to emit ~OverlapChars extra chunks per
// document — shrinking suffixes of the tail, up to 95% of all rows on a real
// corpus.
const chunkerVersion = "2"

// migrateChunkerVersion forces a re-chunk of every document when the stored
// chunks came from an older chunker.
//
// It blanks documents.hash rather than deleting chunk rows. The indexer skips
// a file whose hash still matches, and this class of change never alters file
// contents — so without this, `ney index` would report every file as
// "unchanged" and keep serving the stale chunks forever, with no signal to
// the user. Blanking the hash makes the next pass treat each document as
// modified and replace its chunks + FTS rows atomically, which also means
// search keeps working on the old rows until each document's turn comes,
// instead of going empty the moment the DB is opened.
//
// Runs at Open, like BackfillFTS, and is safe under SetMaxOpenConns(1): every
// statement here is an Exec or a QueryRow, never a query left open. A
// read-only `ney mcp` may be the one to run it; that is fine, because it does
// both halves together and the next writer sees the blank hashes and rebuilds.
func (d *DB) migrateChunkerVersion() error {
	current, err := d.GetMeta("chunker_version")
	if err != nil {
		return err
	}
	if current == chunkerVersion {
		return nil
	}
	res, err := d.db.Exec(`UPDATE documents SET hash=''`)
	if err != nil {
		return err
	}
	// Only flag a rebuild when there was something to rebuild. A fresh install
	// takes this branch too (no version stamped yet) with zero documents, and
	// must not go on to announce a rebuild and VACUUM an empty database.
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		if err := d.SetMeta(metaChunkerRebuildPending, "1"); err != nil {
			return err
		}
	}
	return d.SetMeta("chunker_version", chunkerVersion)
}

// metaChunkerRebuildPending is set while a chunker-version rebuild is still
// working through the corpus, and cleared by FinishChunkerRebuildIfDone.
const metaChunkerRebuildPending = "chunker_rebuild_pending"

// FinishChunkerRebuildIfDone reclaims the space freed by a chunker-version
// rebuild, once one is pending and every document has been re-chunked.
//
// Deleting the superseded chunk rows only moves their pages onto SQLite's
// freelist — the file itself never shrinks, so an upgrade would otherwise
// leave index.db permanently sized for an index several times larger than the
// one it now holds (262 MB for 91 MB of data, on the corpus that surfaced the
// chunker bug). VACUUM is the only way to give that back.
//
// "Done" is a blank hash existing nowhere in the DB, which is global rather
// than per-workspace on purpose: the migration blanks every workspace's
// hashes at once, so a single workspace finishing its pass is not the end of
// the rebuild.
//
// Best-effort and non-fatal. VACUUM rewrites the whole database and needs a
// moment plus scratch space, and a concurrently reading process can make it
// return SQLITE_BUSY; none of that should fail an indexing run that already
// succeeded. It is skipped entirely when no rebuild is pending, which is
// every run after the first.
func (d *DB) FinishChunkerRebuildIfDone() (vacuumed bool, err error) {
	pending, err := d.GetMeta(metaChunkerRebuildPending)
	if err != nil || pending == "" {
		return false, err
	}
	var stale int
	if err := d.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM documents WHERE hash='' LIMIT 1)`).Scan(&stale); err != nil {
		return false, err
	}
	if stale == 1 {
		return false, nil // still working through the corpus
	}
	// Merge the FTS5 index first. Replacing every document's chunks leaves the
	// old terms behind as tombstones spread across many segments, which VACUUM
	// cannot compact because to SQLite they are live rows in the shadow
	// tables. Without this the rebuilt index stays ~50% larger than one built
	// from scratch.
	if _, err := d.db.Exec(`INSERT INTO chunks_fts(chunks_fts) VALUES('optimize')`); err != nil {
		return false, err
	}
	if _, err := d.db.Exec(`VACUUM`); err != nil {
		// Leave the flag set so a later run retries.
		return false, err
	}
	return true, d.SetMeta(metaChunkerRebuildPending, "")
}

func (d *DB) Begin() (*sql.Tx, error) { return d.db.Begin() }

// Workspace

func (d *DB) UpsertWorkspace(name, rootPath string) (int64, error) {
	var id int64
	err := d.db.QueryRow(
		`INSERT INTO workspaces(name, root_path) VALUES(?, ?)
		 ON CONFLICT(name) DO UPDATE SET root_path=excluded.root_path
		 RETURNING id`,
		name, rootPath,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (d *DB) GetWorkspaceByName(name string) (*Workspace, error) {
	ws := &Workspace{}
	err := d.db.QueryRow(`SELECT id, name, root_path, created_at FROM workspaces WHERE name=?`, name).
		Scan(&ws.ID, &ws.Name, &ws.RootPath, &ws.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return ws, err
}

func (d *DB) ListWorkspaces() ([]*Workspace, error) {
	rows, err := d.db.Query(`SELECT id, name, root_path, created_at FROM workspaces ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Workspace
	for rows.Next() {
		ws := &Workspace{}
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.RootPath, &ws.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ws)
	}
	return out, rows.Err()
}

func (d *DB) DeleteWorkspace(id int64) error {
	_, err := d.db.Exec(`DELETE FROM workspaces WHERE id=?`, id)
	return err
}

// Document

func (d *DB) UpsertDocument(doc *Document) (int64, error) {
	var id int64
	err := d.db.QueryRow(
		`INSERT INTO documents(workspace_id, path, type, hash, size_bytes, indexed_at)
		 VALUES(?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   workspace_id=excluded.workspace_id,
		   type=excluded.type,
		   hash=excluded.hash,
		   size_bytes=excluded.size_bytes,
		   indexed_at=excluded.indexed_at
		 RETURNING id`,
		doc.WorkspaceID, doc.Path, doc.Type, doc.Hash, doc.SizeBytes, time.Now(),
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (d *DB) GetDocumentByPath(path string) (*Document, error) {
	doc := &Document{}
	err := d.db.QueryRow(
		`SELECT id, workspace_id, path, type, hash, size_bytes, indexed_at FROM documents WHERE path=?`, path,
	).Scan(&doc.ID, &doc.WorkspaceID, &doc.Path, &doc.Type, &doc.Hash, &doc.SizeBytes, &doc.IndexedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return doc, err
}

// GetDocumentsByPaths batch-fetches document rows keyed by path, batching
// IN(...) lists at idBatchSize. Used by the indexer's rename-detection pass
// to replace a GetDocumentByPath call per (missing-document, candidate-path)
// pair with one query per batch of candidates.
func (d *DB) GetDocumentsByPaths(paths []string) (map[string]*Document, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make(map[string]*Document, len(paths))
	for start := 0; start < len(paths); start += idBatchSize {
		end := start + idBatchSize
		if end > len(paths) {
			end = len(paths)
		}
		batch := paths[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, p := range batch {
			placeholders[i] = "?"
			args[i] = p
		}
		rows, err := d.db.Query(
			`SELECT id, workspace_id, path, type, hash, size_bytes, indexed_at FROM documents WHERE path IN (`+
				strings.Join(placeholders, ",")+`)`,
			args...,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			doc := &Document{}
			if err := rows.Scan(&doc.ID, &doc.WorkspaceID, &doc.Path, &doc.Type, &doc.Hash, &doc.SizeBytes, &doc.IndexedAt); err != nil {
				rows.Close()
				return nil, err
			}
			out[doc.Path] = doc
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		// Close explicitly (not deferred) before the next batch's Query —
		// SetMaxOpenConns(1) means an open sql.Rows would block it.
		rows.Close()
	}
	return out, nil
}

func (d *DB) GetDocumentsByWorkspace(workspaceID int64) ([]*Document, error) {
	rows, err := d.db.Query(
		`SELECT id, workspace_id, path, type, hash, size_bytes, indexed_at FROM documents WHERE workspace_id=?`,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Document
	for rows.Next() {
		doc := &Document{}
		if err := rows.Scan(&doc.ID, &doc.WorkspaceID, &doc.Path, &doc.Type, &doc.Hash, &doc.SizeBytes, &doc.IndexedAt); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

func (d *DB) DeleteDocument(id int64) error {
	_, err := d.db.Exec(`DELETE FROM documents WHERE id=?`, id)
	return err
}

// DeleteDocumentWithCleanup removes a document, its FTS rows, and cascading chunks
// in one transaction. Returns deleted chunk IDs for vector store cleanup.
func (d *DB) DeleteDocumentWithCleanup(docID int64) ([]int64, error) {
	chunkIDs, err := d.GetChunkIDsByDocument(docID)
	if err != nil {
		return nil, err
	}
	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	if err := d.DeleteChunkFTS(tx, chunkIDs); err != nil {
		tx.Rollback()
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM documents WHERE id=?`, docID); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return chunkIDs, nil
}

// UpdateDocumentHash records a document's content hash. Indexing sets the
// hash only after its chunks are committed, so a failed run can't leave a
// chunk-less document that later runs skip as "unchanged".
func (d *DB) UpdateDocumentHash(docID int64, hash string) error {
	_, err := d.db.Exec(`UPDATE documents SET hash=? WHERE id=?`, hash, docID)
	return err
}

func (d *DB) UpdateDocumentPath(docID int64, newPath string) error {
	_, err := d.db.Exec(
		`UPDATE documents SET path=?, indexed_at=CURRENT_TIMESTAMP WHERE id=?`,
		newPath, docID,
	)
	return err
}

func (d *DB) GetDocumentByHashInWorkspace(workspaceID int64, hash string) (*Document, error) {
	doc := &Document{}
	err := d.db.QueryRow(
		`SELECT id, workspace_id, path, type, hash, size_bytes, indexed_at FROM documents WHERE workspace_id=? AND hash=? LIMIT 1`,
		workspaceID, hash,
	).Scan(&doc.ID, &doc.WorkspaceID, &doc.Path, &doc.Type, &doc.Hash, &doc.SizeBytes, &doc.IndexedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// Chunk

func (d *DB) InsertChunks(tx *sql.Tx, chunks []*Chunk) error {
	stmt, err := tx.Prepare(
		`INSERT INTO chunks(document_id, chunk_index, content, start_pos, end_pos) VALUES(?,?,?,?,?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range chunks {
		res, err := stmt.Exec(c.DocumentID, c.ChunkIndex, c.Content, c.StartPos, c.EndPos)
		if err != nil {
			return err
		}
		c.ID, _ = res.LastInsertId()
	}
	return nil
}

// idBatchSize keeps IN(...) lists well under SQLite's bound-variable limit
// for large ID slices — mirrors ftsDeleteBatch in fts.go.
const idBatchSize = 500

func (d *DB) GetChunksByIDs(ids []int64) ([]*Chunk, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var out []*Chunk
	for start := 0; start < len(ids); start += idBatchSize {
		end := start + idBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args[i] = id
		}
		rows, err := d.db.Query(
			`SELECT id, document_id, chunk_index, content, start_pos, end_pos FROM chunks WHERE id IN (`+
				strings.Join(placeholders, ",")+`)`,
			args...,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			c := &Chunk{}
			if err := rows.Scan(&c.ID, &c.DocumentID, &c.ChunkIndex, &c.Content, &c.StartPos, &c.EndPos); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, c)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// GetChunksByDocumentOrdered returns every chunk row for docID ordered by
// chunk_index. Like every query method here it fully drains its sql.Rows
// (via defer) before returning, safe to call again immediately
// (SetMaxOpenConns(1)).
func (d *DB) GetChunksByDocumentOrdered(docID int64) ([]*Chunk, error) {
	rows, err := d.db.Query(
		`SELECT id, document_id, chunk_index, content, start_pos, end_pos FROM chunks WHERE document_id=? ORDER BY chunk_index`,
		docID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Chunk
	for rows.Next() {
		c := &Chunk{}
		if err := rows.Scan(&c.ID, &c.DocumentID, &c.ChunkIndex, &c.Content, &c.StartPos, &c.EndPos); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (d *DB) DeleteChunksByDocument(tx *sql.Tx, docID int64) ([]int64, error) {
	oldIDs, err := d.GetChunkIDsByDocumentInTx(tx, docID)
	if err != nil {
		return nil, err
	}
	if err := d.DeleteChunkFTS(tx, oldIDs); err != nil {
		return nil, err
	}
	_, err = tx.Exec(`DELETE FROM chunks WHERE document_id=?`, docID)
	return oldIDs, err
}

func (d *DB) GetChunkIDsByDocumentInTx(tx *sql.Tx, docID int64) ([]int64, error) {
	rows, err := tx.Query(`SELECT id FROM chunks WHERE document_id=?`, docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

type DocWithWorkspace struct {
	Document
	WorkspaceName string
}

func (d *DB) GetDocumentsByChunkIDs(chunkIDs []int64) (map[int64]*DocWithWorkspace, error) {
	if len(chunkIDs) == 0 {
		return nil, nil
	}
	out := make(map[int64]*DocWithWorkspace, len(chunkIDs))
	for start := 0; start < len(chunkIDs); start += idBatchSize {
		end := start + idBatchSize
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}
		batch := chunkIDs[start:end]
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))
		for i, id := range batch {
			placeholders[i] = "?"
			args[i] = id
		}
		rows, err := d.db.Query(
			`SELECT c.id, d.id, d.workspace_id, d.path, d.type, d.hash, d.size_bytes, d.indexed_at, w.name
			 FROM chunks c
			 JOIN documents d ON d.id = c.document_id
			 JOIN workspaces w ON w.id = d.workspace_id
			 WHERE c.id IN (`+strings.Join(placeholders, ",")+`)`,
			args...,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var chunkID int64
			dw := &DocWithWorkspace{}
			if err := rows.Scan(&chunkID, &dw.ID, &dw.WorkspaceID, &dw.Path, &dw.Type, &dw.Hash, &dw.SizeBytes, &dw.IndexedAt, &dw.WorkspaceName); err != nil {
				rows.Close()
				return nil, err
			}
			out[chunkID] = dw
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// IndexMeta

func (d *DB) GetMeta(key string) (string, error) {
	var val string
	err := d.db.QueryRow(`SELECT value FROM index_meta WHERE key=?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

func (d *DB) SetMeta(key, value string) error {
	_, err := d.db.Exec(
		`INSERT INTO index_meta(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value,
	)
	return err
}

// Stats

func (d *DB) Stats() (*StoreStats, error) {
	s := &StoreStats{}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&s.WorkspaceCount); err != nil {
		return nil, err
	}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM documents`).Scan(&s.DocumentCount); err != nil {
		return nil, err
	}
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&s.ChunkCount); err != nil {
		return nil, err
	}
	return s, nil
}

// CountChunks returns the total number of chunk rows across all workspaces.
// It's a single QueryRow (no open sql.Rows), so it's always safe to call
// even with SetMaxOpenConns(1). Used by the retriever to compute embedding
// coverage (vectors indexed / chunks total).
func (d *DB) CountChunks() (int, error) {
	var n int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&n)
	return n, err
}

func (d *DB) DeleteAllData() error {
	if err := d.ClearFTS(); err != nil {
		return err
	}
	_, err := d.db.Exec(`DELETE FROM workspaces`)
	if err != nil {
		return err
	}
	_, err = d.db.Exec(`DELETE FROM index_meta`)
	return err
}
