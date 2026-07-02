package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

type ProviderRecord struct {
	ID         int64
	Role       string
	Name       string
	Model      string
	Dimensions int
	Version    string
	CreatedAt  time.Time
}

type StoreStats struct {
	WorkspaceCount int
	DocumentCount  int
	ChunkCount     int
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
	if _, err := sqldb.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;"); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("set pragmas: %w", err)
	}
	db := &DB{db: sqldb}
	if err := db.migrate(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (d *DB) Close() error { return d.db.Close() }

func (d *DB) migrate() error {
	if _, err := d.db.Exec(schema); err != nil {
		return err
	}
	return d.BackfillFTS()
}

func (d *DB) Begin() (*sql.Tx, error) { return d.db.Begin() }

// Workspace

func (d *DB) UpsertWorkspace(name, rootPath string) (int64, error) {
	_, err := d.db.Exec(
		`INSERT INTO workspaces(name, root_path) VALUES(?, ?)
		 ON CONFLICT(name) DO UPDATE SET root_path=excluded.root_path`,
		name, rootPath,
	)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := d.db.QueryRow(`SELECT id FROM workspaces WHERE name=?`, name).Scan(&id); err != nil {
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
	_, err := d.db.Exec(
		`INSERT INTO documents(workspace_id, path, type, hash, size_bytes, indexed_at)
		 VALUES(?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		   workspace_id=excluded.workspace_id,
		   type=excluded.type,
		   hash=excluded.hash,
		   size_bytes=excluded.size_bytes,
		   indexed_at=excluded.indexed_at`,
		doc.WorkspaceID, doc.Path, doc.Type, doc.Hash, doc.SizeBytes, time.Now(),
	)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := d.db.QueryRow(`SELECT id FROM documents WHERE path=?`, doc.Path).Scan(&id); err != nil {
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

func (d *DB) GetChunksByIDs(ids []int64) ([]*Chunk, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
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
	placeholders := make([]string, len(chunkIDs))
	args := make([]any, len(chunkIDs))
	for i, id := range chunkIDs {
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
	defer rows.Close()
	out := make(map[int64]*DocWithWorkspace)
	for rows.Next() {
		var chunkID int64
		dw := &DocWithWorkspace{}
		if err := rows.Scan(&chunkID, &dw.ID, &dw.WorkspaceID, &dw.Path, &dw.Type, &dw.Hash, &dw.SizeBytes, &dw.IndexedAt, &dw.WorkspaceName); err != nil {
			return nil, err
		}
		out[chunkID] = dw
	}
	return out, rows.Err()
}

// Provider / IndexMeta

func (d *DB) SetActiveEmbedder(name, model string, dimensions int) error {
	val := fmt.Sprintf(`{"name":%q,"model":%q,"dimensions":%d}`, name, model, dimensions)
	return d.SetMeta("active_embedder", val)
}

func (d *DB) GetActiveEmbedder() (*ProviderRecord, error) {
	val, err := d.GetMeta("active_embedder")
	if err != nil || val == "" {
		return nil, err
	}
	// simple parse without json to avoid import cycle risk
	pr := &ProviderRecord{}
	// parse {"name":"x","model":"y","dimensions":N}
	var name, model string
	var dims int
	_, err = fmt.Sscanf(
		strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(val, `"`, ""), "{", ""), "}", ""),
		"name:%s,model:%s,dimensions:%d", &name, &model, &dims,
	)
	if err != nil {
		// fallback: just store raw
		pr.Name = val
		return pr, nil
	}
	pr.Name = strings.TrimSuffix(strings.TrimPrefix(name, ":"), ",")
	pr.Model = strings.TrimSuffix(strings.TrimPrefix(model, ":"), ",")
	pr.Dimensions = dims
	return pr, nil
}

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
	d.db.QueryRow(`SELECT COUNT(*) FROM workspaces`).Scan(&s.WorkspaceCount)
	d.db.QueryRow(`SELECT COUNT(*) FROM documents`).Scan(&s.DocumentCount)
	d.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&s.ChunkCount)
	return s, nil
}

// GetChunkDocumentIDs returns chunk IDs → document IDs for vector delete on reset
func (d *DB) GetChunkIDsByWorkspace(workspaceID int64) ([]int64, error) {
	rows, err := d.db.Query(
		`SELECT c.id FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.workspace_id=?`,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (d *DB) GetAllChunkIDs() ([]int64, error) {
	rows, err := d.db.Query(`SELECT id FROM chunks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	return ids, rows.Err()
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

// int64SliceToStrings converts chunk IDs to string IDs for VectorStore.Delete
func Int64SliceToStrings(ids []int64) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.FormatInt(id, 10)
	}
	return out
}
