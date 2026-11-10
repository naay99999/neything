// Package skills embeds the agent skills ney ships (ney-handoff, ney-resume)
// and installs them into agent skill directories. ney only ever updates a directory it
// installed itself and whose files the user has not edited; it never deletes.
package skills

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	neycontext "github.com/naay99999/neything/internal/context"
)

// Names are the skills ney ships; each is also its directory name.
var Names = []string{"ney-handoff", "ney-resume"}

const markerFile = ".ney-managed"

//go:embed ney-handoff ney-resume
var embedded embed.FS

// State is what Sync did, or what Status found, for one directory.
type State string

const (
	Installed State = "installed"
	Updated   State = "updated"
	UpToDate  State = "up to date"
	Outdated  State = "outdated"
	Modified  State = "modified locally — not updated"
	Unmanaged State = "not managed by ney — skipped"
	Absent    State = "not installed"
)

// Result is the outcome for one target directory.
type Result struct {
	Dir   string
	State State
}

type marker struct {
	Skill   string            `json:"skill"`
	Version string            `json:"version"`
	Files   map[string]string `json:"files"`
}

// Targets are the directories ney installs its skills into: for each skill,
// the shared ~/.agents/skills (Codex and others) and Claude Code's
// ~/.claude/skills. A target's base name is its skill's name.
func Targets(home string) []string {
	var out []string
	for _, name := range Names {
		out = append(out,
			filepath.Join(home, ".agents", "skills", name),
			filepath.Join(home, ".claude", "skills", name))
	}
	return out
}

// Files returns skill name's embedded files by slash-separated relative
// path, or nil for a name ney does not ship.
func Files(name string) map[string][]byte {
	known := false
	for _, n := range Names {
		known = known || n == name
	}
	if !known {
		return nil
	}
	out := map[string][]byte{}
	_ = fs.WalkDir(embedded, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := embedded.ReadFile(p)
		if err != nil {
			return err
		}
		rel := p[len(name)+1:]
		out[rel] = b
		return nil
	})
	return out
}

func hash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func readMarker(dir, name string) (marker, bool) {
	b, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil {
		return marker{}, false
	}
	var m marker
	if json.Unmarshal(b, &m) != nil || m.Skill != name || m.Files == nil {
		return marker{}, false
	}
	for rel := range m.Files {
		if !filepath.IsLocal(filepath.FromSlash(rel)) {
			return marker{}, false // a damaged marker is not ours to trust
		}
	}
	return m, true
}

// inspect classifies dir, the target of skill name, without writing anything.
func inspect(dir, name string) State {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return Absent
	}
	m, ok := readMarker(dir, name)
	if !ok {
		return Unmanaged
	}
	for rel, want := range m.Files {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil || hash(b) != want {
			return Modified
		}
	}
	files := Files(name)
	// Every file Sync would write must be vouched for by the marker; one that
	// exists but is not listed may be the user's, so it blocks the update.
	for rel := range files {
		if _, listed := m.Files[rel]; !listed {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
				return Modified
			}
		}
	}
	if len(files) != len(m.Files) {
		return Outdated
	}
	for rel, b := range files {
		if m.Files[rel] != hash(b) {
			return Outdated
		}
	}
	return UpToDate
}

// Status reports dir's state without writing anything.
func Status(dir string) Result {
	return Result{Dir: dir, State: inspect(dir, filepath.Base(dir))}
}

// Sync installs or updates the skill named by dir's base name in dir. With
// installedOnly, an absent dir is left absent. A modified or unmanaged dir is
// never written.
func Sync(dir, version string, installedOnly bool) (Result, error) {
	name := filepath.Base(dir)
	files := Files(name)
	if files == nil {
		return Result{Dir: dir}, fmt.Errorf("no skill named %q", name)
	}
	st := inspect(dir, name)
	switch st {
	case Modified, Unmanaged, UpToDate:
		return Result{Dir: dir, State: st}, nil
	case Absent:
		if installedOnly {
			return Result{Dir: dir, State: Absent}, nil
		}
	}
	names := make([]string, 0, len(files))
	for rel := range files {
		names = append(names, rel)
	}
	sort.Strings(names)
	m := marker{Skill: name, Version: version, Files: map[string]string{}}
	for _, rel := range names {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return Result{Dir: dir}, err
		}
		if err := neycontext.WriteFileAtomic(p, files[rel], 0o644); err != nil {
			return Result{Dir: dir}, err
		}
		m.Files[path.Clean(rel)] = hash(files[rel])
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Result{Dir: dir}, err
	}
	if err := neycontext.WriteFileAtomic(filepath.Join(dir, markerFile), append(b, '\n'), 0o644); err != nil {
		return Result{Dir: dir}, err
	}
	if st == Absent {
		return Result{Dir: dir, State: Installed}, nil
	}
	return Result{Dir: dir, State: Updated}, nil
}
