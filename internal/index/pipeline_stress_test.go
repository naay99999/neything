package index

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexerStress500Files(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	ix, db, dir := setupIndexer(t)
	defer db.Close()

	root := filepath.Join(dir, "stress")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 500; i++ {
		content := strings.Repeat(fmt.Sprintf("document %d topic alpha beta gamma\n", i), 40)
		path := filepath.Join(root, fmt.Sprintf("doc-%04d.md", i))
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	stats, err := ix.Index(context.Background(), root, "stress")
	if err != nil {
		t.Fatal(err)
	}
	if stats.FilesScanned < 500 {
		t.Fatalf("expected >= 500 files scanned, got %d", stats.FilesScanned)
	}
	if stats.ChunksCreated == 0 {
		t.Fatal("expected chunks created")
	}
}
