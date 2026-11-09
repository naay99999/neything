package projects

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 10, 4, 0, 0, 0, time.UTC)

func TestNameCollision(t *testing.T) {
	var r Registry
	a := r.Upsert(Entry{Path: "/a/portfolio", LastSeen: t0})
	b := r.Upsert(Entry{Path: "/b/portfolio", LastSeen: t0})
	c := r.Upsert(Entry{Path: "/c/b/portfolio", LastSeen: t0})
	if a.Name != "portfolio" || b.Name != "portfolio-b" || c.Name != "portfolio-b-2" {
		t.Fatalf("names = %q %q %q", a.Name, b.Name, c.Name)
	}
	again := r.Upsert(Entry{Path: "/b/portfolio", LastSeen: t0.Add(time.Hour)})
	if again.Name != "portfolio-b" || len(r.Repos) != 3 || !again.LastSeen.Equal(t0.Add(time.Hour)) {
		t.Fatalf("re-upsert = %+v, %d repos", again, len(r.Repos))
	}
	if got := NameFor("/x/My Project.v2", nil); got != "my-project-v2" {
		t.Errorf("NameFor = %q", got)
	}
}

func TestLookupByRemoteAfterMove(t *testing.T) {
	var r Registry
	r.Upsert(Entry{Path: "/old/x", Remote: "github.com/u/x", LastSeen: t0})
	e := r.Upsert(Entry{Path: "/new/x", Remote: "github.com/u/x", LastSeen: t0.Add(time.Hour)})
	if len(r.Repos) != 1 || e.Path != "/new/x" || e.Name != "x" {
		t.Fatalf("after move: %+v (%d repos)", e, len(r.Repos))
	}
	if older := r.Upsert(Entry{Path: "/new/x", LastSeen: t0}); !older.LastSeen.Equal(t0.Add(time.Hour)) {
		t.Errorf("LastSeen went backwards: %v", older.LastSeen)
	}
}

func TestLookupWindowsCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("case-insensitive paths are Windows-only")
	}
	r := Registry{Repos: []Entry{{Path: `c:\repo\x`, Name: "x"}}}
	if e, ok := r.Lookup(`C:\Repo\X`, ""); !ok || e.Name != "x" {
		t.Fatalf("Lookup = %+v %v", e, ok)
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if r, err := LoadRegistry(dir); err != nil || len(r.Repos) != 0 {
		t.Fatalf("missing file: %+v %v", r, err)
	}
	var r Registry
	r.Upsert(Entry{Path: "/a/x", Remote: "h/u/x", LastSeen: t0})
	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRegistry(dir)
	if err != nil || !reflect.DeepEqual(got, r) {
		t.Fatalf("round trip: %+v %v, want %+v", got, err, r)
	}
	if err := os.WriteFile(filepath.Join(dir, ".registry.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadRegistry(dir); err == nil || len(got.Repos) != 0 {
		t.Errorf("corrupt: %+v %v", got, err)
	}
}

func TestFind(t *testing.T) {
	r := Registry{Repos: []Entry{{Path: "/a/neything", Name: "neything"}}}
	if e, ok := r.Find("NeyThing"); !ok || e.Path != "/a/neything" {
		t.Errorf("by name: %+v %v", e, ok)
	}
	if e, ok := r.Find("/a/neything"); !ok || e.Name != "neything" {
		t.Errorf("by path: %+v %v", e, ok)
	}
	if _, ok := r.Find("nope"); ok {
		t.Error("found a missing name")
	}
}

func TestClosest(t *testing.T) {
	names := []string{"neything", "portfolio", "suan-ops"}
	if got := Closest(names, "neyth", 3); len(got) == 0 || got[0] != "neything" {
		t.Errorf("Closest(neyth) = %v", got)
	}
	if got := Closest(names, "portfolo", 1); !reflect.DeepEqual(got, []string{"portfolio"}) {
		t.Errorf("Closest(portfolo) = %v", got)
	}
}

func TestTOCLineAndIndex(t *testing.T) {
	home, _ := os.UserHomeDir()
	now := t0
	carded := Item{Name: "neything", Path: filepath.Join(home, "w", "neything"), HasCard: true,
		Summary: "Automatic session handoff. Reads transcripts.", Activity: now.Add(-2 * time.Hour)}
	bare := Item{Name: "portfolio", Path: "/srv/portfolio", Activity: now.Add(-3 * 24 * time.Hour)}
	if got, want := TOCLine(carded, now), "neything · "+filepath.Join("~", "w", "neything")+" · Automatic session handoff. · 2h ago"; got != want {
		t.Errorf("TOCLine = %q, want %q", got, want)
	}
	if got, want := TOCLine(bare, now), "portfolio · /srv/portfolio · (no card yet) · 3d ago"; got != want {
		t.Errorf("TOCLine = %q, want %q", got, want)
	}
	dir := t.TempDir()
	if err := WriteIndex(dir, []Item{bare, carded}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "index.md"))
	want := "# Projects\n- [[neything]] — Automatic session handoff. · " + filepath.Join("~", "w", "neything") +
		"\n- [[portfolio]] — (no card yet) · /srv/portfolio\n"
	if string(b) != want {
		t.Errorf("index.md =\n%s\nwant\n%s", b, want)
	}
	if got := FirstSentence(strings.Repeat("a", 130)); len([]rune(got)) != 120 || !strings.HasSuffix(got, "…") {
		t.Errorf("FirstSentence long = %q", got)
	}
}
