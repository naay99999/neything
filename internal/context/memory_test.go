package neycontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteMemory_FilenameAndFrontmatter(t *testing.T) {
	dir := t.TempDir()
	date := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)

	path, err := WriteMemory(dir, Memory{
		Title:   "Ney Pivot Decision",
		Content: "Decided to reposition ney as a personal context server.",
		Project: "neything",
		Tags:    []string{"decision", "roadmap"},
		Date:    date,
	})
	if err != nil {
		t.Fatalf("WriteMemory: %v", err)
	}

	wantBase := "2026-07-31-ney-pivot-decision.md"
	if filepath.Base(path) != wantBase {
		t.Errorf("filename = %q, want %q", filepath.Base(path), wantBase)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("dir = %q, want %q", filepath.Dir(path), dir)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(data)

	if !strings.HasPrefix(s, "---\n") {
		t.Errorf("missing frontmatter delimiter:\n%s", s)
	}
	if !strings.Contains(s, "date: 2026-07-31\n") {
		t.Errorf("missing date field:\n%s", s)
	}
	if !strings.Contains(s, "project: neything\n") {
		t.Errorf("missing project field:\n%s", s)
	}
	if !strings.Contains(s, "tags: [decision, roadmap]\n") {
		t.Errorf("missing tags field:\n%s", s)
	}
	if !strings.Contains(s, "Decided to reposition ney") {
		t.Errorf("missing body content:\n%s", s)
	}
}

func TestWriteMemory_OmitsOptionalFrontmatterFields(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteMemory(dir, Memory{
		Title:   "no extras",
		Content: "just a note",
		Date:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("WriteMemory: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "project:") {
		t.Errorf("unexpected project field:\n%s", s)
	}
	if strings.Contains(s, "tags:") {
		t.Errorf("unexpected tags field:\n%s", s)
	}
}

func TestWriteMemory_DefaultsDateToNow(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteMemory(dir, Memory{Title: "no date given", Content: "x"})
	if err != nil {
		t.Fatalf("WriteMemory: %v", err)
	}
	wantPrefix := time.Now().Format("2006-01-02")
	if !strings.HasPrefix(filepath.Base(path), wantPrefix) {
		t.Errorf("filename %q doesn't start with today's date %q", filepath.Base(path), wantPrefix)
	}
}

func TestWriteMemory_SlugSanitization(t *testing.T) {
	dir := t.TempDir()
	date := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		title string
	}{
		{"path separators", "notes/on/../etc/passwd"},
		{"dotdot traversal", "../../etc/passwd"},
		{"thai text", "การตัดสินใจ"},
		{"spaces and punctuation", "Hello, World! It's a Test."},
		{"empty title", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			subDir := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "_"))
			path, err := WriteMemory(subDir, Memory{Title: c.title, Content: "body", Date: date})
			if err != nil {
				t.Fatalf("WriteMemory: %v", err)
			}
			// Must land exactly inside subDir, never escape it.
			if filepath.Dir(path) != subDir {
				t.Fatalf("path escaped target dir: %q not in %q", path, subDir)
			}
			base := filepath.Base(path)
			if strings.ContainsAny(base, "/\\") {
				t.Errorf("filename contains path separators: %q", base)
			}
			if strings.Contains(base, "..") {
				t.Errorf("filename contains '..': %q", base)
			}
			if !strings.HasPrefix(base, "2026-07-31-") {
				t.Errorf("filename missing date prefix: %q", base)
			}
			if !strings.HasSuffix(base, ".md") {
				t.Errorf("filename missing .md suffix: %q", base)
			}
		})
	}
}

func TestWriteMemory_CollisionSuffix(t *testing.T) {
	dir := t.TempDir()
	date := time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC)

	p1, err := WriteMemory(dir, Memory{Title: "same title", Content: "first", Date: date})
	if err != nil {
		t.Fatalf("WriteMemory 1: %v", err)
	}
	p2, err := WriteMemory(dir, Memory{Title: "same title", Content: "second", Date: date})
	if err != nil {
		t.Fatalf("WriteMemory 2: %v", err)
	}
	p3, err := WriteMemory(dir, Memory{Title: "same title", Content: "third", Date: date})
	if err != nil {
		t.Fatalf("WriteMemory 3: %v", err)
	}

	if p1 == p2 || p2 == p3 || p1 == p3 {
		t.Fatalf("expected distinct paths, got %q, %q, %q", p1, p2, p3)
	}
	if !strings.HasSuffix(p1, "2026-07-31-same-title.md") {
		t.Errorf("first path unexpected: %q", p1)
	}
	if !strings.HasSuffix(p2, "2026-07-31-same-title-2.md") {
		t.Errorf("second path unexpected: %q", p2)
	}
	if !strings.HasSuffix(p3, "2026-07-31-same-title-3.md") {
		t.Errorf("third path unexpected: %q", p3)
	}

	// All three files must actually exist with their own content.
	for i, p := range []string{p1, p2, p3} {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		_ = data
	}
}

func TestWriteMemory_SlugCapLength(t *testing.T) {
	dir := t.TempDir()
	longTitle := strings.Repeat("word ", 30) // way over 60 chars once slugified
	path, err := WriteMemory(dir, Memory{Title: longTitle, Content: "x", Date: time.Now()})
	if err != nil {
		t.Fatalf("WriteMemory: %v", err)
	}
	base := filepath.Base(path)
	// base = "YYYY-MM-DD-" (11 chars) + slug (<=60) + ".md" (3 chars)
	slug := strings.TrimSuffix(strings.TrimPrefix(base, base[:11]), ".md")
	if len(slug) > 60 {
		t.Errorf("slug length = %d, want <= 60: %q", len(slug), slug)
	}
}

func TestWriteMemory_NoLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := WriteMemory(dir, Memory{Title: "clean write", Content: "x", Date: time.Now()}); err != nil {
		t.Fatalf("WriteMemory: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 file, got %d: %v", len(entries), entries)
	}
	if strings.HasPrefix(entries[0].Name(), ".tmp-") {
		t.Errorf("leftover temp file: %s", entries[0].Name())
	}
}

func TestWriteMemory_CreatesDirIfMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "memory")
	path, err := WriteMemory(dir, Memory{Title: "x", Content: "y", Date: time.Now()})
	if err != nil {
		t.Fatalf("WriteMemory: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("dir perm = %o, want 0700", perm)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello World":          "hello-world",
		"already-a-slug":       "already-a-slug",
		"Multiple   Spaces":    "multiple-spaces",
		"--leading-trailing--": "leading-trailing",
		"":                     "memory",
		"!!!":                  "memory",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
