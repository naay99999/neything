package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFTSSearchFindsKeyword(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wsID, err := db.UpsertWorkspace("test", dir)
	if err != nil {
		t.Fatal(err)
	}
	doc := &Document{WorkspaceID: wsID, Path: "/tmp/a.md", Type: "md", Hash: "abc", SizeBytes: 10}
	docID, err := db.UpsertDocument(doc)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	chunks := []*Chunk{
		{DocumentID: docID, ChunkIndex: 0, Content: "billing uses stripe subscriptions", StartPos: 1, EndPos: 10},
		{DocumentID: docID, ChunkIndex: 1, Content: "unrelated content about cats", StartPos: 11, EndPos: 20},
	}
	if err := db.InsertChunks(tx, chunks); err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if err := db.UpsertChunkFTS(tx, c.ID, c.Content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	results, err := db.SearchFTS("stripe billing", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected FTS hits")
	}
	if results[0].ChunkID != chunks[0].ID {
		t.Fatalf("expected first chunk, got chunk %d", results[0].ChunkID)
	}
}

func TestBackfillFTS(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// create db with chunks but without fts backfill by manual setup
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	wsID, _ := db.UpsertWorkspace("test", dir)
	docID, _ := db.UpsertDocument(&Document{WorkspaceID: wsID, Path: "/x.md", Type: "md", Hash: "h", SizeBytes: 1})
	tx, _ := db.Begin()
	chunks := []*Chunk{{DocumentID: docID, ChunkIndex: 0, Content: "hello world", StartPos: 1, EndPos: 2}}
	_ = db.InsertChunks(tx, chunks)
	tx.Commit()

	if err := db.BackfillFTS(); err != nil {
		t.Fatal(err)
	}

	results, err := db.SearchFTS("hello", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 hit after backfill, got %d", len(results))
	}
	db.Close()
}
