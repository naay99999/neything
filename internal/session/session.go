// Package session defines the agent-neutral model of a coding-agent session
// and the Source interface adapters implement. Adapters must not redact;
// redaction happens once, when a brief is built.
package session

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ErrNotASession is returned by Source.Load when a transcript has no real
// user prompt (sidechain-only, meta-only, empty).
var ErrNotASession = errors.New("session: no user prompt")

// Ref locates one session transcript.
type Ref struct {
	Agent    string // "claude-code" | "codex"
	ID       string
	Path     string
	Modified time.Time
}

// FileChange is one file the agent modified, with the operations used.
type FileChange struct {
	Path  string
	Ops   []string // e.g. ["edit","write"]
	Count int
}

// CommandRun is one shell command the agent ran.
type CommandRun struct {
	Command string
	Failed  bool
}

// TailEntry is one step of the session's recent history.
type TailEntry struct {
	Kind string // user|assistant|command|file|failure
	Head string
	Body string
	At   time.Time
}

// Session is the distilled state of one transcript.
type Session struct {
	Agent, ID, Title, Branch, Cwd string
	Start, End                    time.Time
	FirstPrompt, LastPrompt       string
	PromptCount                   int
	LastMessage                   string
	Files                         []FileChange // first-touch order
	Commands                      []CommandRun // chronological
	Tail                          []TailEntry  // chronological, full loads only
}

// Source reads sessions of one agent.
type Source interface {
	Name() string
	// Recent lists sessions whose cwd is inside repoRoot and modified at or
	// after since, newest Modified first.
	Recent(repoRoot string, since time.Time) ([]Ref, error)
	// Load distils a session. full=false avoids reading whole large files.
	Load(ref Ref, full bool) (Session, error)
}

// Canon returns path cleaned and, best-effort, with symlinks resolved; when
// resolution fails it is just the cleaned path.
func Canon(path string) string {
	if path == "" {
		return ""
	}
	c := filepath.Clean(path)
	if r, err := filepath.EvalSymlinks(c); err == nil {
		return r
	}
	return c
}

// Within reports whether cwd is root or lies inside it, comparing both the
// literal and the symlink-resolved forms of each side.
func Within(cwd, root string) bool {
	if cwd == "" || root == "" {
		return false
	}
	in := func(c, r string) bool {
		if runtime.GOOS == "windows" { // NTFS paths are case-insensitive; drive letters vary in case
			c, r = strings.ToLower(c), strings.ToLower(r)
		}
		return c == r || strings.HasPrefix(c, r+string(filepath.Separator))
	}
	return in(filepath.Clean(cwd), filepath.Clean(root)) || in(Canon(cwd), Canon(root))
}
