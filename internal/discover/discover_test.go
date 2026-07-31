package discover

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func discoverIn(t *testing.T, root string) []Candidate {
	t.Helper()
	cands, err := Discover(context.Background(), Options{Roots: []string{root}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cands
}

func paths(cands []Candidate) map[string]int {
	m := make(map[string]int, len(cands))
	for _, c := range cands {
		m[c.Path] = c.DocCount
	}
	return m
}

func TestDiscoverSkipsJunkAndSecrets(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "notes", "a.md"))
	write(t, filepath.Join(root, "notes", "b.md"))
	// None of these may be counted or surfaced:
	write(t, filepath.Join(root, "node_modules", "pkg", "readme.md"))
	write(t, filepath.Join(root, "Library", "x.md"))
	write(t, filepath.Join(root, ".hidden", "y.md"))
	write(t, filepath.Join(root, "secrets", "z.md"))
	write(t, filepath.Join(root, "MyApp.app", "help.html"))
	write(t, filepath.Join(root, "notes", "id_rsa"))     // excluded file
	write(t, filepath.Join(root, "notes", "photo.jpg")) // unsupported ext

	got := paths(discoverIn(t, root))
	want := filepath.Join(root, "notes")
	if got[want] != 2 {
		t.Fatalf("expected notes with 2 docs, got %v", got)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 candidate, got %v", got)
	}
}

func TestDiscoverConcentrationPromotesChild(t *testing.T) {
	root := t.TempDir()
	// 9 of 10 docs under workspace live in projA → projA is the candidate.
	for i := 0; i < 9; i++ {
		write(t, filepath.Join(root, "workspace", "projA", "docs", "f"+string(rune('a'+i))+".md"))
	}
	write(t, filepath.Join(root, "workspace", "stray.md"))

	got := paths(discoverIn(t, root))
	// projA/docs holds 100% of projA's docs, so the descent continues to docs.
	want := filepath.Join(root, "workspace", "projA", "docs")
	if got[want] != 9 {
		t.Fatalf("expected %s with 9 docs, got %v", want, got)
	}
	if _, ok := got[filepath.Join(root, "workspace")]; ok {
		t.Fatalf("parent workspace should not be a candidate when a child dominates: %v", got)
	}
}

func TestDiscoverNoDominantChildKeepsParent(t *testing.T) {
	root := t.TempDir()
	// docsA 60% / docsB 40% — neither dominates (>=80%), so the parent is
	// the candidate: indexing ~/big covers both clusters in one pick.
	for i := 0; i < 6; i++ {
		write(t, filepath.Join(root, "big", "docsA", "f"+string(rune('a'+i))+".md"))
	}
	for i := 0; i < 4; i++ {
		write(t, filepath.Join(root, "big", "docsB", "g"+string(rune('a'+i))+".md"))
	}

	got := paths(discoverIn(t, root))
	if got[filepath.Join(root, "big")] != 10 || len(got) != 1 {
		t.Fatalf("expected single candidate big=10, got %v", got)
	}
}

func TestDiscoverSurfacesSecondaryCluster(t *testing.T) {
	root := t.TempDir()
	// docsA dominates (80%) so the walk descends into it — but docsB still
	// holds 20% of everything and must not silently disappear.
	for i := 0; i < 8; i++ {
		write(t, filepath.Join(root, "big", "docsA", "f"+string(rune('a'+i))+".md"))
	}
	write(t, filepath.Join(root, "big", "docsB", "g1.md"))
	write(t, filepath.Join(root, "big", "docsB", "g2.md"))

	got := paths(discoverIn(t, root))
	if got[filepath.Join(root, "big", "docsA")] != 8 {
		t.Fatalf("expected dominant docsA=8, got %v", got)
	}
	if got[filepath.Join(root, "big", "docsB")] != 2 {
		t.Fatalf("expected secondary cluster docsB=2, got %v", got)
	}
}

func TestDiscoverNeverEmitsWalkRoot(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.md"))
	write(t, filepath.Join(root, "sub", "b.md"))

	got := paths(discoverIn(t, root))
	if _, ok := got[root]; ok {
		t.Fatalf("walk root must never be a candidate: %v", got)
	}
}

func TestDiscoverByExtCounts(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "d", "one.md"))
	write(t, filepath.Join(root, "d", "two.txt"))
	write(t, filepath.Join(root, "d", "three.txt"))

	cands := discoverIn(t, root)
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %+v", cands)
	}
	if cands[0].ByExt[".txt"] != 2 || cands[0].ByExt[".md"] != 1 {
		t.Fatalf("wrong ByExt: %+v", cands[0].ByExt)
	}
}

func TestDiscoverContextCancel(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "d", "one.md"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Discover(ctx, Options{Roots: []string{root}}, nil); err == nil {
		t.Fatal("expected ctx cancellation error")
	}
}
