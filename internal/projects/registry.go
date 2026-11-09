package projects

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	neycontext "github.com/naay99999/neything/internal/context"
)

const registryFile = ".registry.json"

// Dir is where cards, the registry and index.md live.
func Dir(neyDir string) string { return filepath.Join(neyDir, "projects") }

// CardPath is the card file for name.
func CardPath(dir, name string) string { return filepath.Join(dir, name+".md") }

// Entry is one known repo.
type Entry struct {
	Path     string    `json:"path"`
	Remote   string    `json:"remote,omitempty"`
	Name     string    `json:"name"`
	LastSeen time.Time `json:"last_seen"`
}

// Registry lists the repos ney knows about, so the hook never scans the disk.
type Registry struct {
	Repos []Entry `json:"repos"`
}

// LoadRegistry reads the registry in dir. A missing file is an empty
// registry; a corrupt one is an empty registry plus the error.
func LoadRegistry(dir string) (Registry, error) {
	data, err := os.ReadFile(filepath.Join(dir, registryFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Registry{}, nil
	}
	if err != nil {
		return Registry{}, err
	}
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return Registry{}, err
	}
	return r, nil
}

// Save writes the registry atomically.
func (r Registry) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return neycontext.WriteFileAtomic(filepath.Join(dir, registryFile), append(data, '\n'), 0o600)
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (r Registry) index(path, remote string) int {
	if path != "" {
		for i, e := range r.Repos {
			if samePath(e.Path, path) {
				return i
			}
		}
	}
	if remote != "" {
		for i, e := range r.Repos {
			if e.Remote == remote {
				return i
			}
		}
	}
	return -1
}

// Lookup finds the entry for a repo: by path first, then by remote, so a
// moved or renamed checkout keeps its entry and card.
func (r Registry) Lookup(path, remote string) (Entry, bool) {
	if i := r.index(path, remote); i >= 0 {
		return r.Repos[i], true
	}
	return Entry{}, false
}

// Find looks an entry up by name (case-insensitive) or absolute path.
func (r Registry) Find(nameOrPath string) (Entry, bool) {
	for _, e := range r.Repos {
		if strings.EqualFold(e.Name, nameOrPath) {
			return e, true
		}
	}
	if filepath.IsAbs(nameOrPath) {
		return r.Lookup(nameOrPath, "")
	}
	return Entry{}, false
}

// Upsert records e and returns the stored entry. An existing entry keeps its
// name; its path and remote follow e when e has them; LastSeen only moves
// forward. A new entry gets a unique name from NameFor.
func (r *Registry) Upsert(e Entry) Entry { return r.UpsertKnowing(e, nil) }

// UpsertKnowing is Upsert for a registry that may have lost entries (e.g.
// rebuilt after corruption): a new entry adopts the name of the card whose
// path or remote matches it, and never takes the name of another repo's card.
func (r *Registry) UpsertKnowing(e Entry, cards []Card) Entry {
	if i := r.index(e.Path, e.Remote); i >= 0 {
		cur := &r.Repos[i]
		if e.Path != "" {
			cur.Path = e.Path
		}
		if e.Remote != "" {
			cur.Remote = e.Remote
		}
		if e.LastSeen.After(cur.LastSeen) {
			cur.LastSeen = e.LastSeen
		}
		return *cur
	}
	taken := map[string]bool{}
	for _, x := range r.Repos {
		taken[x.Name] = true
	}
	e.Name = ""
	for _, c := range cards {
		mine := (c.Path != "" && samePath(c.Path, e.Path)) || (e.Remote != "" && c.Remote == e.Remote)
		if mine && c.Name != "" && !taken[c.Name] {
			e.Name = c.Name
			break
		}
	}
	if e.Name == "" {
		for _, c := range cards {
			taken[c.Name] = true
		}
		e.Name = NameFor(e.Path, taken)
	}
	r.Repos = append(r.Repos, e)
	return e
}

// ErrLocked is returned by UpdateRegistry when another process holds the
// registry lock past the timeout.
var ErrLocked = errors.New("project registry is locked by another ney process")

const (
	lockFile  = ".registry.lock"
	lockStale = 10 * time.Second
)

// UpdateRegistry loads the registry, applies fn, and saves it, all under an
// exclusive lock file so concurrent ney processes (hooks, MCP servers) never
// lose each other's updates. A corrupt registry is backed up to .bak and
// rebuilt from empty. A lock older than 10s is treated as abandoned.
func UpdateRegistry(dir string, timeout time.Duration, fn func(*Registry)) (Registry, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Registry{}, err
	}
	lock := filepath.Join(dir, lockFile)
	deadline := time.Now().Add(timeout)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return Registry{}, err
		}
		if fi, serr := os.Stat(lock); serr == nil && time.Since(fi.ModTime()) > lockStale {
			_ = os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			return Registry{}, ErrLocked
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer os.Remove(lock)

	r, err := LoadRegistry(dir)
	if err != nil {
		path := filepath.Join(dir, registryFile)
		if data, rerr := os.ReadFile(path); rerr == nil {
			if werr := neycontext.WriteFileAtomic(path+".bak", data, 0o600); werr != nil {
				return Registry{}, werr
			}
		}
		r = Registry{}
	}
	fn(&r)
	return r, r.Save(dir)
}

// LoadCards reads every card in dir (unparseable ones are skipped). Callers
// use it only when a repo is new to the registry.
func LoadCards(dir string) []Card {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var cards []Card
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".md") || n == "index.md" {
			continue
		}
		if c, err := Load(filepath.Join(dir, n)); err == nil {
			c.Name = strings.TrimSuffix(n, ".md")
			cards = append(cards, c)
		}
	}
	return cards
}

func slug(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// NameFor derives a card name from a repo root: its basename, slugged. A
// taken name becomes "<base>-<parent>", then "-2", "-3", ...
func NameFor(root string, taken map[string]bool) string {
	root = filepath.Clean(root)
	base := slug(filepath.Base(root))
	if base == "" {
		base = "project"
	}
	if !taken[base] {
		return base
	}
	if p := slug(filepath.Base(filepath.Dir(root))); p != "" {
		base += "-" + p
	}
	if !taken[base] {
		return base
	}
	for n := 2; ; n++ {
		if c := base + "-" + itoa(n); !taken[c] {
			return c
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for ; n > 0; n /= 10 {
		d = append([]byte{byte('0' + n%10)}, d...)
	}
	return string(d)
}

// Closest returns up to n names most like q: prefix and substring matches
// first, then by edit distance.
func Closest(names []string, q string, n int) []string {
	q = strings.ToLower(q)
	type cand struct {
		name string
		rank int
		dist int
	}
	var cs []cand
	for _, name := range names {
		l := strings.ToLower(name)
		rank := 2
		switch {
		case strings.HasPrefix(l, q):
			rank = 0
		case strings.Contains(l, q):
			rank = 1
		}
		cs = append(cs, cand{name, rank, editDistance(l, q)})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].rank != cs[j].rank {
			return cs[i].rank < cs[j].rank
		}
		return cs[i].dist < cs[j].dist
	})
	var out []string
	for i := 0; i < len(cs) && i < n; i++ {
		out = append(out, cs[i].name)
	}
	return out
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
