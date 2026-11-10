package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func markerOf(t *testing.T, dir string) marker {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil {
		t.Fatal(err)
	}
	var m marker
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// ageInstall makes dir look like an install by an older ney: SKILL.md has
// other content and the marker records that content's hash.
func ageInstall(t *testing.T, dir string) {
	t.Helper()
	old := []byte("---\nname: ney-handoff\n---\nold\n")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), old, 0o644); err != nil {
		t.Fatal(err)
	}
	m := markerOf(t, dir)
	sum := sha256.Sum256(old)
	m.Files["SKILL.md"] = hex.EncodeToString(sum[:])
	m.Version = "0.0.1"
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, markerFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedSkills(t *testing.T) {
	if !reflect.DeepEqual(Names, []string{"ney-handoff", "ney-resume"}) {
		t.Fatalf("Names = %v", Names)
	}
	for _, name := range Names {
		f := Files(name)
		var files []string
		for k := range f {
			files = append(files, k)
		}
		if len(f) != 2 || f["SKILL.md"] == nil || f["agents/openai.yaml"] == nil {
			t.Fatalf("%s files = %v", name, files)
		}
		if !strings.HasPrefix(string(f["SKILL.md"]), "---\nname: "+name+"\n") {
			t.Errorf("%s SKILL.md frontmatter:\n%s", name, f["SKILL.md"][:80])
		}
		if !strings.Contains(string(f["agents/openai.yaml"]), "allow_implicit_invocation: true") {
			t.Errorf("%s openai.yaml must allow implicit invocation", name)
		}
	}
	resume := string(Files("ney-resume")["SKILL.md"])
	for _, want := range []string{"get_handoff", "ทำต่อ", "pick up"} {
		if !strings.Contains(resume, want) {
			t.Errorf("ney-resume SKILL.md lacks %q", want)
		}
	}
	if Files("nope") != nil {
		t.Error("unknown skill has files")
	}
}

func TestSyncUnknownSkillDirFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "other")
	if _, err := Sync(dir, "1", false); err == nil {
		t.Fatal("synced a directory that names no embedded skill")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("wrote an unknown skill dir")
	}
}

func TestSyncResumeAndMarkerNamesSkill(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ney-resume")
	if r, err := Sync(dir, "1", false); err != nil || r.State != Installed {
		t.Fatalf("sync = %+v, %v", r, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(got) != string(Files("ney-resume")["SKILL.md"]) {
		t.Error("ney-resume SKILL.md not written")
	}
	if m := markerOf(t, dir); m.Skill != "ney-resume" {
		t.Errorf("marker skill = %q", m.Skill)
	}
	// A ney-handoff marker inside the ney-resume dir is not ours.
	other := filepath.Join(t.TempDir(), "ney-handoff")
	if _, err := Sync(other, "1", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(other, markerFile))
	if err := os.WriteFile(filepath.Join(dir, markerFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Status(dir); s.State != Unmanaged {
		t.Errorf("foreign marker: %v, want Unmanaged", s.State)
	}
}

func TestSyncInstallsAndIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ney-handoff")
	r, err := Sync(dir, "1.2.3", false)
	if err != nil || r.State != Installed || r.Dir != dir {
		t.Fatalf("first sync = %+v, %v", r, err)
	}
	for name, want := range Files("ney-handoff") {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil || string(got) != string(want) {
			t.Errorf("%s differs from embedded: %v", name, err)
		}
		if fi, _ := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o644 {
			t.Errorf("%s mode = %v", name, fi.Mode().Perm())
		}
	}
	m := markerOf(t, dir)
	if m.Skill != "ney-handoff" || m.Version != "1.2.3" || len(m.Files) != 2 || len(m.Files["SKILL.md"]) != 64 {
		t.Errorf("marker = %+v", m)
	}
	if r, err := Sync(dir, "1.2.3", false); err != nil || r.State != UpToDate {
		t.Errorf("second sync = %+v, %v", r, err)
	}
}

func TestSyncUpdatesUnmodified(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ney-handoff")
	if _, err := Sync(dir, "1", false); err != nil {
		t.Fatal(err)
	}
	ageInstall(t, dir)
	if s := Status(dir); s.State != Outdated {
		t.Errorf("Status of an old install = %v", s.State)
	}
	r, err := Sync(dir, "2", false)
	if err != nil || r.State != Updated {
		t.Fatalf("sync = %+v, %v", r, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(got) != string(Files("ney-handoff")["SKILL.md"]) {
		t.Error("SKILL.md not updated")
	}
	if markerOf(t, dir).Version != "2" {
		t.Error("marker version not updated")
	}
}

func TestSyncSkipsModified(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ney-handoff")
	if _, err := Sync(dir, "1", false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "SKILL.md")
	edited := append(Files("ney-handoff")["SKILL.md"], '!')
	if err := os.WriteFile(p, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err := Sync(dir, "2", false); err != nil || r.State != Modified {
		t.Fatalf("sync = %+v, %v", r, err)
	}
	if got, _ := os.ReadFile(p); string(got) != string(edited) {
		t.Error("user edit overwritten")
	}
	if s := Status(dir); s.State != Modified {
		t.Errorf("Status = %v", s.State)
	}
}

func TestSyncSkipsUnmanaged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ney-handoff")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := []byte("my own skill")
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), mine, 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err := Sync(dir, "1", false); err != nil || r.State != Unmanaged {
		t.Fatalf("sync = %+v, %v", r, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(got) != string(mine) {
		t.Error("unmanaged skill overwritten")
	}
	if _, err := os.Stat(filepath.Join(dir, markerFile)); !os.IsNotExist(err) {
		t.Error("marker written into an unmanaged dir")
	}
}

func TestSyncInstalledOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ney-handoff")
	if r, err := Sync(dir, "1", true); err != nil || r.State != Absent {
		t.Fatalf("sync = %+v, %v", r, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("installed-only created the dir")
	}
}

func TestStatusFresh(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ney-handoff")
	if s := Status(dir); s.State != Absent {
		t.Errorf("absent: %v", s.State)
	}
	if _, err := Sync(dir, "1", false); err != nil {
		t.Fatal(err)
	}
	if s := Status(dir); s.State != UpToDate {
		t.Errorf("fresh: %v", s.State)
	}
}

func TestTargets(t *testing.T) {
	want := []string{
		filepath.Join("/h", ".agents", "skills", "ney-handoff"), filepath.Join("/h", ".claude", "skills", "ney-handoff"),
		filepath.Join("/h", ".agents", "skills", "ney-resume"), filepath.Join("/h", ".claude", "skills", "ney-resume"),
	}
	if got := Targets("/h"); !reflect.DeepEqual(got, want) {
		t.Errorf("Targets = %v", got)
	}
}

func TestSyncMarkerMissingEntryCountsAsModified(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ney-handoff")
	if _, err := Sync(dir, "1", false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "agents", "openai.yaml")
	if err := os.WriteFile(p, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := markerOf(t, dir)
	delete(m.Files, "agents/openai.yaml") // hand-edited / damaged marker
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, markerFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if r, err := Sync(dir, "2", false); err != nil || r.State != Modified {
		t.Fatalf("sync = %+v, %v", r, err)
	}
	if got, _ := os.ReadFile(p); string(got) != "mine" {
		t.Error("file not covered by the marker was overwritten")
	}
}

func TestStatusRejectsNonLocalMarkerPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ney-handoff")
	if _, err := Sync(dir, "1", false); err != nil {
		t.Fatal(err)
	}
	m := markerOf(t, dir)
	m.Files["../outside"] = "x"
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, markerFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if s := Status(dir); s.State != Unmanaged {
		t.Errorf("marker with ../ path: %v, want Unmanaged", s.State)
	}
}
