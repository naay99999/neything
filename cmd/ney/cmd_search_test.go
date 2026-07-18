package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/naay99999/neything/internal/search"
	"github.com/naay99999/neything/internal/store"
)

// withSearchFlags saves/restores the global CLI flags liveScanRoot reads, so
// tests can set them without leaking state into other tests in this package.
func withSearchFlags(t *testing.T, workspace, path string, all bool) {
	t.Helper()
	origWS, origPath, origAll := flagWorkspace, flagPath, flagAll
	flagWorkspace, flagPath, flagAll = workspace, path, all
	t.Cleanup(func() { flagWorkspace, flagPath, flagAll = origWS, origPath, origAll })
}

func TestLiveScanRootNoCoveringWorkspace(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	scope := t.TempDir()
	withSearchFlags(t, "", scope, false)

	got := liveScanRoot(db, scope)
	resolved, _ := filepath.Abs(scope)
	if got != resolved {
		t.Fatalf("expected liveScanRoot to return the scope path %q when no workspace covers it, got %q", resolved, got)
	}
}

func TestLiveScanRootEmptyWorkspaceStillScans(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertWorkspace("empty-ws", resolvedRoot); err != nil {
		t.Fatal(err)
	}

	withSearchFlags(t, "", resolvedRoot, false)
	got := liveScanRoot(db, resolvedRoot)
	if got != resolvedRoot {
		t.Fatalf("expected liveScanRoot to return the workspace root (0 documents indexed), got %q", got)
	}
}

func TestLiveScanRootIndexedWorkspaceSkipsScan(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	wsID, err := db.UpsertWorkspace("indexed-ws", resolvedRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpsertDocument(&store.Document{
		WorkspaceID: wsID,
		Path:        filepath.Join(resolvedRoot, "note.md"),
		Type:        "md",
		Hash:        "abc123",
		SizeBytes:   10,
	}); err != nil {
		t.Fatal(err)
	}

	withSearchFlags(t, "", resolvedRoot, false)
	got := liveScanRoot(db, resolvedRoot)
	if got != "" {
		t.Fatalf("expected liveScanRoot to return \"\" for an already-indexed workspace, got %q", got)
	}
}

func TestLiveScanRootAllFlagDisablesScan(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	withSearchFlags(t, "", "", true)
	if got := liveScanRoot(db, ""); got != "" {
		t.Fatalf("expected --all to disable live scan entirely, got %q", got)
	}
}

func TestAppendLiveScanDedupesAndTagsSource(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	// Already "indexed" (one document row present) so notes.md — matched via
	// GroupByFile below — is not duplicated by the scan.
	indexedPath := filepath.Join(resolvedRoot, "notes.md")
	if err := os.WriteFile(indexedPath, []byte("widget notes here"), 0644); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(resolvedRoot, "widget-fresh.md")
	if err := os.WriteFile(newPath, []byte("brand new widget file, not indexed yet"), 0644); err != nil {
		t.Fatal(err)
	}

	withSearchFlags(t, "", resolvedRoot, false)

	existingGroups := []search.FileGroup{
		{DocPath: indexedPath, DocType: "md", Source: "index", Chunks: []search.EnrichedResult{{DocPath: indexedPath, Content: "widget notes"}}},
	}

	// No workspace registered at all -> liveScanRoot falls back to scanning
	// resolvedRoot directly (the "no covering workspace" branch).
	got := appendLiveScan(context.Background(), db, existingGroups, "widget", nil)

	if len(got) != 2 {
		t.Fatalf("expected 2 groups (1 index + 1 new live-scan hit), got %d: %+v", len(got), got)
	}
	var liveScanGroup *search.FileGroup
	for i := range got {
		if got[i].DocPath == newPath {
			liveScanGroup = &got[i]
		}
		if got[i].DocPath == indexedPath && got[i].Source != "index" {
			t.Fatalf("expected the pre-existing index group to be untouched, got source=%q", got[i].Source)
		}
	}
	if liveScanGroup == nil {
		t.Fatalf("expected a live-scan hit for %q, got %+v", newPath, got)
	}
	if liveScanGroup.Source != "live-scan" {
		t.Fatalf("expected source=live-scan for the new hit, got %q", liveScanGroup.Source)
	}
}
