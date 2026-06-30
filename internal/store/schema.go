package store

const schema = `
CREATE TABLE IF NOT EXISTS index_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workspaces (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT    NOT NULL UNIQUE,
	root_path  TEXT    NOT NULL,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS documents (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
	path         TEXT    NOT NULL UNIQUE,
	type         TEXT    NOT NULL,
	hash         TEXT    NOT NULL,
	size_bytes   INTEGER NOT NULL DEFAULT 0,
	indexed_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS chunks (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
	chunk_index INTEGER NOT NULL,
	content     TEXT    NOT NULL,
	start_pos   INTEGER NOT NULL DEFAULT 0,
	end_pos     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS providers (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	role       TEXT NOT NULL,
	name       TEXT NOT NULL,
	model      TEXT NOT NULL,
	dimensions INTEGER NOT NULL DEFAULT 0,
	version    TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_documents_workspace ON documents(workspace_id);
CREATE INDEX IF NOT EXISTS idx_chunks_document ON chunks(document_id);

CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
	content,
	tokenize='unicode61 remove_diacritics 2'
);
`
