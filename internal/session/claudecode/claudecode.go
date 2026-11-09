// Package claudecode reads Claude Code transcripts
// (~/.claude/projects/<slug>/<session-id>.jsonl) into session.Session values.
package claudecode

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/naay99999/neything/internal/session"
)

const (
	agentName     = "claude-code"
	tailWindow    = 256 << 10 // initial bytes read from the end of a large file
	maxTailWindow = 4 << 20   // the window doubles up to this size
	tailKeep      = 30        // Tail entries kept on a full load
	cwdMaxLines   = 64        // Recent gives up on a file after this many lines
	headMax       = 120       // runes in a TailEntry.Head
	bodyMax       = 2000      // runes in a TailEntry.Body
)

// notPromptPrefixes mark user entries that are harness chatter, not prompts.
var notPromptPrefixes = []string{
	"<command-name>", "<command-message>", "<local-command-stdout>",
	"<local-command-caveat>", "<system-reminder>",
	"[Request interrupted", "<bash-input>", "<bash-stdout>", "<bash-stderr>",
	"<task-notification>",
}

type source struct{ dir string }

// New returns a Source reading transcripts under projectsDir
// (normally ~/.claude/projects).
func New(projectsDir string) session.Source { return source{dir: projectsDir} }

func (source) Name() string { return agentName }

// Slug is the directory name Claude Code uses for a project path: every rune
// outside [A-Za-z0-9] becomes one '-'.
func Slug(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// Recent lists transcripts for sessions whose first cwd is repoRoot or inside
// it, modified at or after since, newest first.
func (s source) Recent(repoRoot string, since time.Time) ([]session.Ref, error) {
	root := filepath.Clean(repoRoot)
	slugs := []string{Slug(root)}
	if r := session.Canon(root); r != root {
		slugs = append(slugs, Slug(r))
	}
	dirs, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var refs []session.Ref
	for _, d := range dirs {
		if !d.IsDir() || (!matchesSlug(d.Name(), slugs)) {
			continue
		}
		dirPath := filepath.Join(s.dir, d.Name())
		files, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(since) {
				continue
			}
			p := filepath.Join(dirPath, f.Name())
			cwd := firstCwd(p)
			if !session.Within(cwd, root) {
				continue
			}
			refs = append(refs, session.Ref{
				Agent:    agentName,
				ID:       strings.TrimSuffix(f.Name(), ".jsonl"),
				Path:     p,
				Modified: info.ModTime(),
			})
		}
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].Modified.After(refs[j].Modified) })
	return refs, nil
}

// firstCwd returns the first cwd in the file, reading at most cwdMaxLines
// lines; "" when none is found.
func firstCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var cwd string
	n := 0
	stop := &stopReader{r: f}
	_ = session.Lines(stop, func(line []byte) {
		if stop.stopped {
			return
		}
		n++
		var e struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(line, &e) == nil && e.Cwd != "" {
			cwd = e.Cwd
			stop.stopped = true
		} else if n >= cwdMaxLines {
			stop.stopped = true
		}
	})
	return cwd
}

// stopReader reports EOF once stopped is set, letting a Lines callback end
// the read early. n counts bytes handed out (an upper bound on bytes used).
type stopReader struct {
	r       io.Reader
	stopped bool
	n       int64
}

func (s *stopReader) Read(p []byte) (int, error) {
	if s.stopped {
		return 0, io.EOF
	}
	n, err := s.r.Read(p)
	s.n += int64(n)
	return n, err
}

// Load distils one transcript. With full=false a large file is read from its
// start only up to the first real prompt, then from its last tailWindow
// bytes, so cost does not grow with file size.
func (s source) Load(ref session.Ref, full bool) (session.Session, error) {
	f, err := os.Open(ref.Path)
	if err != nil {
		return session.Session{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return session.Session{}, err
	}
	if !fi.Mode().IsRegular() {
		return session.Session{}, session.ErrNotASession
	}

	var a *acc
	if full || fi.Size() <= tailWindow {
		a = newAcc(full)
		if err := a.read(f); err != nil {
			return session.Session{}, err
		}
	} else if a, err = loadPartial(f, fi.Size()); err != nil {
		return session.Session{}, err
	}
	if a.s.PromptCount == 0 {
		return session.Session{}, session.ErrNotASession
	}
	a.s.Agent = agentName
	a.s.ID = ref.ID
	if full && len(a.tail) > tailKeep {
		a.tail = a.tail[len(a.tail)-tailKeep:]
	}
	if full {
		a.s.Tail = a.tail
	}
	return a.s, nil
}

// loadPartial reads a large file's head (up to the first real prompt) plus
// its tail. The tail window starts at tailWindow and doubles, up to
// maxTailWindow, while the parse found no assistant text or no prompt after the first: a huge trailing
// line (multi-MB tool_result) otherwise hides LastMessage, Title and End.
// When the head may overlap the window, or the window covers the file, one
// clean pass over the whole file is used instead.
func loadPartial(f *os.File, size int64) (*acc, error) {
	for window := int64(tailWindow); ; window *= 2 {
		if window >= size {
			return wholeFile(f)
		}
		a := newAcc(false)
		a.haltAtPrompt = true
		head := &stopReader{r: f}
		a.stop = head
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		if err := session.Lines(head, a.handle); err != nil {
			return nil, err
		}
		a.haltAtPrompt, a.stop = false, nil
		if !a.halted || head.n > size-window {
			return wholeFile(f)
		}
		tr, err := session.TailReader(f, window)
		if err != nil {
			return nil, err
		}
		if err := a.read(tr); err != nil {
			return nil, err
		}
		// The head pass counted the first prompt; a second one means the tail
		// found the real latest ask.
		if (a.s.LastMessage != "" && a.s.PromptCount > 1) || window >= maxTailWindow {
			return a, nil
		}
	}
}

func wholeFile(f *os.File) (*acc, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	a := newAcc(false)
	return a, a.read(f)
}

// acc accumulates a Session over transcript lines.
type acc struct {
	s       session.Session
	full    bool
	tail    []session.TailEntry
	fileIdx map[string]int
	pending map[string]int // tool_use id -> index in s.Commands

	haltAtPrompt bool        // head pass: stop after the first real prompt
	halted       bool        // set once that prompt has been seen
	stop         *stopReader // head pass reader to cut off
}

func newAcc(full bool) *acc {
	return &acc{full: full, fileIdx: map[string]int{}, pending: map[string]int{}}
}

func (a *acc) read(r io.Reader) error { return session.Lines(r, a.handle) }

type entry struct {
	Type        string `json:"type"`
	Cwd         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	Timestamp   string `json:"timestamp"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	AITitle     string `json:"aiTitle"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func (a *acc) handle(line []byte) {
	if a.halted && a.haltAtPrompt {
		return
	}
	var e entry
	if json.Unmarshal(line, &e) != nil {
		return
	}
	if e.Type == "ai-title" {
		if e.AITitle != "" {
			a.s.Title = e.AITitle
		}
		return
	}
	if (e.Type != "user" && e.Type != "assistant") || e.IsSidechain {
		return
	}
	var at time.Time
	if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
		at = t
		if a.s.Start.IsZero() {
			a.s.Start = t
		}
		a.s.End = t
	}
	if a.s.Cwd == "" {
		a.s.Cwd = e.Cwd
	}
	if e.GitBranch != "" {
		a.s.Branch = e.GitBranch
	}
	blocks, text := parseContent(e.Message.Content)
	if e.Type == "user" {
		a.user(e, blocks, text, at)
	} else {
		a.assistant(blocks, at)
	}
}

// parseContent returns the blocks of a content value and the joined text of
// its text blocks (a bare string counts as one text block).
func parseContent(raw json.RawMessage) ([]block, string) {
	if len(raw) == 0 {
		return nil, ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return nil, s
	}
	var bs []block
	if json.Unmarshal(raw, &bs) != nil {
		return nil, ""
	}
	var texts []string
	for _, b := range bs {
		if b.Type == "text" && b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	return bs, strings.Join(texts, "\n")
}

func (a *acc) user(e entry, blocks []block, text string, at time.Time) {
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		i, ok := a.pending[b.ToolUseID]
		if !ok {
			continue
		}
		delete(a.pending, b.ToolUseID)
		if b.IsError {
			a.s.Commands[i].Failed = true
			a.addTail("failure", a.s.Commands[i].Command, resultText(b.Content), at)
		}
	}
	text = strings.TrimSpace(text)
	if e.IsMeta || text == "" {
		return
	}
	for _, p := range notPromptPrefixes {
		if strings.HasPrefix(text, p) {
			return
		}
	}
	a.s.PromptCount++
	if a.s.FirstPrompt == "" {
		a.s.FirstPrompt = text
	}
	a.s.LastPrompt = text
	a.addTail("user", text, text, at)
	if a.haltAtPrompt {
		a.halted = true
		a.stop.stopped = true
	}
}

func (a *acc) assistant(blocks []block, at time.Time) {
	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				texts = append(texts, b.Text)
			}
		case "tool_use":
			a.toolUse(b, at)
		}
	}
	if len(texts) > 0 {
		msg := strings.Join(texts, "\n")
		a.s.LastMessage = msg
		a.addTail("assistant", msg, msg, at)
	}
}

func (a *acc) toolUse(b block, at time.Time) {
	var in struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
	}
	_ = json.Unmarshal(b.Input, &in)
	switch b.Name {
	case "Edit", "MultiEdit":
		a.touch(in.FilePath, "edit", at)
	case "Write":
		a.touch(in.FilePath, "write", at)
	case "NotebookEdit":
		a.touch(in.NotebookPath, "edit", at)
	case "Bash":
		if in.Command == "" {
			return
		}
		a.s.Commands = append(a.s.Commands, session.CommandRun{Command: in.Command})
		if b.ID != "" {
			a.pending[b.ID] = len(a.s.Commands) - 1
		}
		a.addTail("command", in.Command, in.Command, at)
	}
}

func (a *acc) touch(path, op string, at time.Time) {
	if path == "" {
		return
	}
	i, ok := a.fileIdx[path]
	if !ok {
		i = len(a.s.Files)
		a.fileIdx[path] = i
		a.s.Files = append(a.s.Files, session.FileChange{Path: path})
	}
	fc := &a.s.Files[i]
	fc.Count++
	for _, o := range fc.Ops {
		if o == op {
			a.addTail("file", path, op, at)
			return
		}
	}
	fc.Ops = append(fc.Ops, op)
	a.addTail("file", path, op, at)
}

func (a *acc) addTail(kind, head, body string, at time.Time) {
	if !a.full {
		return
	}
	a.tail = append(a.tail, session.TailEntry{
		Kind: kind, Head: clip(firstLine(head), headMax), Body: clip(body, bodyMax), At: at,
	})
	if len(a.tail) >= 4*tailKeep { // bound memory on long sessions
		a.tail = append([]session.TailEntry(nil), a.tail[len(a.tail)-tailKeep:]...)
	}
}

// resultText extracts the text of a tool_result content value (string or
// array of text blocks).
func resultText(raw json.RawMessage) string {
	_, text := parseContent(raw)
	return text
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func clip(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func matchesSlug(name string, slugs []string) bool {
	if runtime.GOOS == "windows" { // drive letter / folder case may differ from the transcript dir
		name = strings.ToLower(name)
	}
	for _, slug := range slugs {
		if runtime.GOOS == "windows" {
			slug = strings.ToLower(slug)
		}
		if name == slug || strings.HasPrefix(name, slug+"-") {
			return true
		}
	}
	return false
}
