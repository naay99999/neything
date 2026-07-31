package neycontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProfile_CreatesTemplateWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.md")

	content, created, err := LoadProfile(path)
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if !created {
		t.Errorf("created = false, want true")
	}
	if content != profileTemplate {
		t.Errorf("content mismatch:\n%s", content)
	}
	if !strings.Contains(content, "## Name & role") {
		t.Errorf("template missing Name & role section")
	}
	if !strings.Contains(content, "## Current focus") {
		t.Errorf("template missing Current focus section")
	}
	if !strings.Contains(content, "## Working style") {
		t.Errorf("template missing Working style section")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 0600", perm)
	}

	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(onDisk) != content {
		t.Errorf("on-disk content differs from returned content")
	}
}

func TestLoadProfile_ReadsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.md")
	want := "# Profile\n\n## Name & role\nJane, engineer\n"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	content, created, err := LoadProfile(path)
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if created {
		t.Errorf("created = true, want false (file already existed)")
	}
	if content != want {
		t.Errorf("content = %q, want %q", content, want)
	}
}

func TestUpdateProfile_ReplaceExistingSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.md")
	initial := "# Profile\n\n## Current focus\nold focus text\n\n## Working style\nasync\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := UpdateProfile(path, "Current focus", "new focus text", false); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "old focus text") {
		t.Errorf("old body still present:\n%s", s)
	}
	if !strings.Contains(s, "new focus text") {
		t.Errorf("new body missing:\n%s", s)
	}
	if !strings.Contains(s, "## Working style\nasync") {
		t.Errorf("unrelated section was disturbed:\n%s", s)
	}
}

func TestUpdateProfile_AppendMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.md")
	initial := "# Profile\n\n## Current focus\nfirst fact\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := UpdateProfile(path, "Current focus", "second fact", true); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "first fact") {
		t.Errorf("original content lost on append:\n%s", s)
	}
	if !strings.Contains(s, "second fact") {
		t.Errorf("appended content missing:\n%s", s)
	}
}

func TestUpdateProfile_SectionMissingAppendsNewAtEOF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.md")
	initial := "# Profile\n\n## Current focus\nsomething\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := UpdateProfile(path, "Goals", "ship layered context", false); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "## Goals") {
		t.Errorf("new section header missing:\n%s", s)
	}
	if !strings.Contains(s, "ship layered context") {
		t.Errorf("new section body missing:\n%s", s)
	}
	// The new section should come after the existing one.
	if strings.Index(s, "## Goals") < strings.Index(s, "## Current focus") {
		t.Errorf("new section not appended at EOF:\n%s", s)
	}
}

func TestUpdateProfile_CreatesFileIfMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "profile.md")

	if err := UpdateProfile(path, "Current focus", "brand new", false); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "brand new") {
		t.Errorf("content missing: %s", got)
	}
}

func TestUpdateProfile_CaseInsensitiveSectionMatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.md")
	initial := "## Current Focus\nold\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := UpdateProfile(path, "current focus", "new", false); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	got, _ := os.ReadFile(path)
	s := string(got)
	if strings.Count(s, "## ") != 1 {
		t.Errorf("expected exactly one section header (case-insensitive match should reuse it):\n%s", s)
	}
	if !strings.Contains(s, "new") || strings.Contains(s, "old") {
		t.Errorf("body not replaced correctly:\n%s", s)
	}
}

func TestUpdateProfile_NoLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.md")

	if err := UpdateProfile(path, "Focus", "hello", false); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 file in dir, got %d: %v", len(entries), entries)
	}
	if entries[0].Name() != "profile.md" {
		t.Errorf("unexpected file left behind: %s", entries[0].Name())
	}
}
