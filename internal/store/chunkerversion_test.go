package store

import (
	"path/filepath"
	"testing"
)

// TestChunkerVersionBlanksStaleHashes: a change to chunk boundaries does not
// change any file's contents, so the indexer's hash-based skip would keep the
// stale chunks forever. Opening a DB stamped with an older chunker version
// must blank documents.hash so the next indexing pass re-chunks everything.
func TestChunkerVersionBlanksStaleHashes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "index.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	wsID, err := db.UpsertWorkspace("ws", "/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertDocument(&Document{
		WorkspaceID: wsID, Path: "/tmp/ws/a.md", Type: "md", Hash: "deadbeef", SizeBytes: 10,
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate an index written by an older build.
	if err := db.SetMeta("chunker_version", "1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc, err := db.GetDocumentByPath("/tmp/ws/a.md")
	if err != nil || doc == nil {
		t.Fatalf("document missing after reopen: %v", err)
	}
	if doc.Hash != "" {
		t.Fatalf("expected the stale hash to be blanked so the next pass re-chunks, got %q", doc.Hash)
	}
	got, err := db.GetMeta("chunker_version")
	if err != nil {
		t.Fatal(err)
	}
	if got != chunkerVersion {
		t.Fatalf("chunker_version = %q, want %q", got, chunkerVersion)
	}
}

// TestChunkerVersionIsNoOpWhenCurrent: the common case — every subsequent Open
// must leave hashes alone, or the indexer would re-chunk the whole corpus on
// every single run.
func TestChunkerVersionIsNoOpWhenCurrent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "index.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	wsID, err := db.UpsertWorkspace("ws", "/tmp/ws")
	if err != nil {
		t.Fatal(err)
	}
	docID, err := db.UpsertDocument(&Document{
		WorkspaceID: wsID, Path: "/tmp/ws/a.md", Type: "md", Hash: "", SizeBytes: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateDocumentHash(docID, "cafebabe"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	doc, err := db.GetDocumentByPath("/tmp/ws/a.md")
	if err != nil || doc == nil {
		t.Fatalf("document missing after reopen: %v", err)
	}
	if doc.Hash != "cafebabe" {
		t.Fatalf("hash was rewritten on an up-to-date index: got %q", doc.Hash)
	}
}
