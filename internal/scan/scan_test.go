package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanFilenameHitOnPDF(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "invoice-order-1233.pdf"), "%PDF-1.4 binary, never grepped\n")
	writeFile(t, filepath.Join(root, "unrelated.txt"), "nothing interesting here\n")

	hits, truncated, err := Scan(context.Background(), root, "order-1233", Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if truncated {
		t.Fatal("did not expect truncation for a tiny fixture")
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly 1 hit, got %d: %+v", len(hits), hits)
	}
	h := hits[0]
	if !strings.HasSuffix(h.Path, "invoice-order-1233.pdf") {
		t.Fatalf("expected hit on the pdf, got %q", h.Path)
	}
	if h.MatchedIn != "filename" {
		t.Fatalf("expected matched_in=filename, got %q", h.MatchedIn)
	}
	if h.Score != 1.0 {
		t.Fatalf("expected score 1.0 (both tokens matched in filename), got %v", h.Score)
	}
}

func TestScanContentHitInMarkdown(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "notes.md"), "# Billing\n\nInvoice order-5555 was paid by Alice.\nNothing else matters here.\n")
	writeFile(t, filepath.Join(root, "other.md"), "Completely unrelated content.\n")

	hits, _, err := Scan(context.Background(), root, "order-5555", Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly 1 hit, got %d: %+v", len(hits), hits)
	}
	h := hits[0]
	if !strings.HasSuffix(h.Path, "notes.md") {
		t.Fatalf("expected hit on notes.md, got %q", h.Path)
	}
	if h.MatchedIn != "content" {
		t.Fatalf("expected matched_in=content (filename doesn't contain the token), got %q", h.MatchedIn)
	}
	if !strings.Contains(h.Snippet, "order-5555") {
		t.Fatalf("expected snippet to include the matching line, got %q", h.Snippet)
	}
}

func TestScanSkipsDotDirs(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".git", "secret.md"), "widget widget widget\n")
	writeFile(t, filepath.Join(root, "visible.md"), "nothing related\n")

	hits, _, err := Scan(context.Background(), root, "widget", Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected the .git dir to be skipped entirely, got hits: %+v", hits)
	}
}

func TestScanSkipsDotfiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".widget-notes.md"), "widget widget widget\n")

	hits, _, err := Scan(context.Background(), root, "widget", Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected the dotfile to be skipped, got hits: %+v", hits)
	}
}

func TestScanLargeFileSkipsContentButMatchesFilename(t *testing.T) {
	root := t.TempDir()

	// >2MB of filler that never contains the query token, so a content hit
	// would only happen if the size cap were *not* respected.
	filler := strings.Repeat("x", (2*1024*1024)+1024)
	writeFile(t, filepath.Join(root, "order-1233-archive.log"), filler)

	hits, _, err := Scan(context.Background(), root, "order-1233", Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly 1 hit (filename match), got %d: %+v", len(hits), hits)
	}
	h := hits[0]
	if h.MatchedIn != "filename" {
		t.Fatalf("expected matched_in=filename (content skipped for size), got %q", h.MatchedIn)
	}
	if h.Snippet != "" {
		t.Fatalf("expected no snippet since content was never read, got %q", h.Snippet)
	}
}

func TestScanRespectsMaxFilesCap(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		writeFile(t, filepath.Join(root, "widget-"+string(rune('a'+i))+".md"), "widget content\n")
	}

	hits, truncated, err := Scan(context.Background(), root, "widget", Options{MaxFiles: 3})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncated=true when MaxFiles is exceeded")
	}
	if len(hits) > 3 {
		t.Fatalf("expected at most 3 hits (MaxFiles=3), got %d", len(hits))
	}
}

func TestScanRespectsMaxHitsCap(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		writeFile(t, filepath.Join(root, "widget-"+string(rune('a'+i))+".md"), "widget content\n")
	}

	hits, truncated, err := Scan(context.Background(), root, "widget", Options{MaxHits: 2})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected exactly 2 hits (MaxHits=2), got %d", len(hits))
	}
	if !truncated {
		t.Fatal("expected truncated=true when MaxHits caps the result list")
	}
}

func TestScanContextCancelReturnsPromptly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "widget.md"), "widget content\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	var err error
	go func() {
		_, _, err = Scan(ctx, root, "widget", Options{})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Scan did not return promptly after ctx was cancelled")
	}
	if err == nil {
		t.Fatal("expected a context-cancelled error")
	}
}

func TestScanNoTokensReturnsEmpty(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "widget.md"), "widget content\n")

	hits, truncated, err := Scan(context.Background(), root, "!!! . ,,", Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if truncated {
		t.Fatal("did not expect truncation")
	}
	if len(hits) != 0 {
		t.Fatalf("expected no hits for a punctuation-only query, got %+v", hits)
	}
}

func TestScanBothFilenameAndContent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "widget-report.md"), "This report discusses widget sales figures.\n")

	hits, _, err := Scan(context.Background(), root, "widget", Options{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected exactly 1 hit, got %d", len(hits))
	}
	if hits[0].MatchedIn != "both" {
		t.Fatalf("expected matched_in=both, got %q", hits[0].MatchedIn)
	}
}
