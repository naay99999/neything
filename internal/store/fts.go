package store

import (
	"database/sql"
	"fmt"
	"strings"
)

type FTSResult struct {
	ChunkID int64
	Score   float32
}

func (d *DB) UpsertChunkFTS(tx *sql.Tx, chunkID int64, content string) error {
	_, err := tx.Exec(`INSERT INTO chunks_fts(rowid, content) VALUES(?, ?)`, chunkID, content)
	return err
}

// UpsertChunksFTS inserts FTS rows for multiple chunks using a single
// prepared statement, instead of the implicit prepare/exec/close per call
// that looping UpsertChunkFTS would do. Used by the indexer when writing all
// of a document's chunks in one transaction; UpsertChunkFTS itself stays for
// call sites that only ever have one chunk at a time (e.g. BackfillFTS's
// row-at-a-time scan).
func (d *DB) UpsertChunksFTS(tx *sql.Tx, chunks []*Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`INSERT INTO chunks_fts(rowid, content) VALUES(?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range chunks {
		if _, err := stmt.Exec(c.ID, c.Content); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) DeleteChunkFTS(tx *sql.Tx, chunkIDs []int64) error {
	return d.deleteChunkFTSDirectTx(tx, chunkIDs)
}

// ftsDeleteBatch keeps IN(...) lists well under SQLite's bound-variable limit.
const ftsDeleteBatch = 500

func (d *DB) deleteChunkFTSDirectTx(tx *sql.Tx, chunkIDs []int64) error {
	if len(chunkIDs) == 0 {
		return nil
	}
	exec := func(query string, args ...any) error {
		if tx != nil {
			_, err := tx.Exec(query, args...)
			return err
		}
		_, err := d.db.Exec(query, args...)
		return err
	}
	for start := 0; start < len(chunkIDs); start += ftsDeleteBatch {
		end := start + ftsDeleteBatch
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
		if err := exec(`DELETE FROM chunks_fts WHERE rowid IN (`+strings.Join(placeholders, ",")+`)`, args...); err != nil {
			return err
		}
	}
	return nil
}

// ChunkExistsForDocument reports whether docID has at least one chunk row,
// without materializing every chunk ID — used by the indexer's no-op fast
// path (an unchanged file whose hash already matches) to avoid paying
// GetChunkIDsByDocument's full row scan just to check len(ids) > 0.
func (d *DB) ChunkExistsForDocument(docID int64) (bool, error) {
	var exists int
	err := d.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM chunks WHERE document_id=? LIMIT 1)`, docID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

func (d *DB) GetChunkIDsByDocument(docID int64) ([]int64, error) {
	rows, err := d.db.Query(`SELECT id FROM chunks WHERE document_id=?`, docID)
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

func (d *DB) SearchFTS(query string, limit int) ([]FTSResult, error) {
	ftsQuery := sanitizeFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	rows, err := d.db.Query(
		`SELECT rowid, bm25(chunks_fts) FROM chunks_fts WHERE chunks_fts MATCH ? ORDER BY bm25(chunks_fts) LIMIT ?`,
		ftsQuery, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []FTSResult
	for rows.Next() {
		var id int64
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			return nil, err
		}
		// bm25 returns negative scores; lower is better — invert for descending sort
		results = append(results, FTSResult{ChunkID: id, Score: float32(-score)})
	}
	return results, rows.Err()
}

func (d *DB) BackfillFTS() error {
	var ftsCount int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM chunks_fts`).Scan(&ftsCount); err != nil {
		return err
	}
	if ftsCount > 0 {
		return nil
	}

	var chunkCount int
	if err := d.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&chunkCount); err != nil {
		return err
	}
	if chunkCount == 0 {
		return nil
	}

	tx, err := d.db.Begin()
	if err != nil {
		return err
	}

	rows, err := tx.Query(`SELECT id, content FROM chunks`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var content string
		if err := rows.Scan(&id, &content); err != nil {
			tx.Rollback()
			return err
		}
		if err := d.UpsertChunkFTS(tx, id, content); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := rows.Err(); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (d *DB) ClearFTS() error {
	_, err := d.db.Exec(`DELETE FROM chunks_fts`)
	return err
}

func sanitizeFTSQuery(query string) string {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.ReplaceAll(f, `"`, "")
		if f == "" {
			continue
		}
		quoted = append(quoted, fmt.Sprintf(`"%s"`, f))
	}
	if len(quoted) == 0 {
		return ""
	}
	return strings.Join(quoted, " OR ")
}
