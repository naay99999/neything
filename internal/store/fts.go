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

func (d *DB) DeleteChunkFTS(tx *sql.Tx, chunkIDs []int64) error {
	return d.deleteChunkFTSDirectTx(tx, chunkIDs)
}

func (d *DB) deleteChunkFTSDirect(chunkIDs []int64) error {
	return d.deleteChunkFTSDirectTx(nil, chunkIDs)
}

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
	for _, id := range chunkIDs {
		if err := exec(`DELETE FROM chunks_fts WHERE rowid=?`, id); err != nil {
			return err
		}
	}
	return nil
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
