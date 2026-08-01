package store

// CountDocumentsByWorkspace returns the number of document rows belonging to
// a workspace via SELECT COUNT(*), the counting counterpart of
// GetDocumentsByWorkspace (still used where the document rows themselves are
// needed, not just their count). A single QueryRow call — no open sql.Rows —
// so it's always safe under SetMaxOpenConns(1).
func (d *DB) CountDocumentsByWorkspace(workspaceID int64) (int, error) {
	var n int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM documents WHERE workspace_id=?`, workspaceID).Scan(&n)
	return n, err
}

// CountChunksByWorkspace returns the number of chunk rows belonging to a
// workspace's documents via SELECT COUNT(*). A single QueryRow call — no
// open sql.Rows — so it's always safe under SetMaxOpenConns(1).
func (d *DB) CountChunksByWorkspace(workspaceID int64) (int, error) {
	var n int
	err := d.db.QueryRow(
		`SELECT COUNT(*) FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.workspace_id=?`,
		workspaceID,
	).Scan(&n)
	return n, err
}
