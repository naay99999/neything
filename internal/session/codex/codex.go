// Package codex reads Codex CLI rollout files
// ($CODEX_HOME/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl) into
// session.Session values.
package codex

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/session"
)

const (
	agentName     = "codex"
	tailWindow    = 256 << 10 // initial bytes read from the end of a large file
	maxTailWindow = 4 << 20   // the window doubles up to this size
	tailKeep      = 30        // Tail entries kept on a full load
	metaMaxLines  = 64        // Recent gives up looking for a cwd after this many lines
	headMax       = 120       // runes in a TailEntry.Head
	bodyMax       = 2000      // runes in a TailEntry.Body
)

// injectedPrefixes mark user-role messages that Codex injects as context
// rather than the human typing them.
var injectedPrefixes = []string{
	"<environment_context>", "<user_instructions>", "# AGENTS.md instructions for",
}

// shellTools are the function_call names whose arguments carry a command.
var shellTools = map[string]bool{
	"shell": true, "local_shell": true, "shell_command": true,
	"exec_command": true, "container.exec": true,
}

var (
	exitCodeRE = regexp.MustCompile(`(?m)^Exit code: [1-9]`)
	uuidRE     = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type source struct{ home string }

// New returns a Source reading rollouts under codexHome (see Home).
func New(codexHome string) session.Source { return source{home: codexHome} }

func (source) Name() string { return agentName }

// Home returns the Codex home directory: $CODEX_HOME when it is set and
// exists, else ~/.codex. ok is false when that directory does not exist.
func Home() (string, bool) {
	if h := os.Getenv("CODEX_HOME"); h != "" && isDir(h) {
		return h, true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	h := filepath.Join(home, ".codex")
	return h, isDir(h)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// Recent lists rollouts whose session cwd is repoRoot or inside it, modified
// at or after since, newest first. Only date directories that can hold such
// files are visited (with a day of slack either side for time zones).
func (s source) Recent(repoRoot string, since time.Time) ([]session.Ref, error) {
	root := filepath.Clean(repoRoot)
	base := filepath.Join(s.home, "sessions")
	var refs []session.Ref
	for _, y := range subdirs(base) {
		for _, m := range subdirs(filepath.Join(base, y)) {
			for _, d := range subdirs(filepath.Join(base, y, m)) {
				day, err := time.Parse("2006/01/02", y+"/"+m+"/"+d)
				if err != nil || day.Add(48*time.Hour).Before(since) {
					continue
				}
				dir := filepath.Join(base, y, m, d)
				files, err := os.ReadDir(dir)
				if err != nil {
					continue
				}
				for _, f := range files {
					name := f.Name()
					if f.IsDir() || !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
						continue
					}
					info, err := f.Info()
					if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(since) {
						continue
					}
					p := filepath.Join(dir, name)
					cwd := metaCwd(p)
					if !session.Within(cwd, root) {
						continue
					}
					refs = append(refs, session.Ref{
						Agent: agentName, ID: idFromName(name), Path: p, Modified: info.ModTime(),
					})
				}
			}
		}
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].Modified.After(refs[j].Modified) })
	return refs, nil
}

// subdirs lists the names of directories under dir that are all digits.
func subdirs(dir string) []string {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		if !e.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(e.Name()); err == nil {
			out = append(out, e.Name())
		}
	}
	return out
}

// idFromName extracts the session UUID from rollout-<timestamp>-<uuid>.jsonl;
// when the name has no UUID suffix the whole stem is used.
func idFromName(name string) string {
	stem := strings.TrimSuffix(strings.TrimPrefix(name, "rollout-"), ".jsonl")
	if m := uuidRE.FindString(stem); m != "" {
		return m
	}
	return stem
}

// metaCwd returns the cwd from the first session_meta (or turn_context) line,
// reading at most metaMaxLines lines; "" when none is found.
func metaCwd(path string) string {
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
			Type    string `json:"type"`
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &e) == nil && (e.Type == "session_meta" || e.Type == "turn_context") && e.Payload.Cwd != "" {
			cwd = e.Payload.Cwd
			stop.stopped = true
		} else if n >= metaMaxLines {
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

// Load distils one rollout. With full=false a large file is read from its
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
// maxTailWindow, until the tail yields a LastMessage and a later prompt. When
// the head may overlap the window, or the window covers the file, one clean
// pass over the whole file is used instead.
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

// acc accumulates a Session over rollout lines.
type acc struct {
	s       session.Session
	full    bool
	tail    []session.TailEntry
	fileIdx map[string]int
	pending map[string]int // function_call call_id -> index in s.Commands

	haltAtPrompt bool        // head pass: stop after the first real prompt
	halted       bool        // set once that prompt has been seen
	stop         *stopReader // head pass reader to cut off
}

func newAcc(full bool) *acc {
	return &acc{full: full, fileIdx: map[string]int{}, pending: map[string]int{}}
}

func (a *acc) read(r io.Reader) error { return session.Lines(r, a.handle) }

type line struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type payload struct {
	Type string `json:"type"`
	// session_meta / turn_context
	Cwd string `json:"cwd"`
	Git struct {
		Branch string `json:"branch"`
	} `json:"git"`
	// response_item message
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	// function_call / custom_tool_call
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Input     string `json:"input"`
	CallID    string `json:"call_id"`
	// function_call_output
	Output json.RawMessage `json:"output"`
	// event_msg task_complete
	LastAgentMessage string `json:"last_agent_message"`
}

func (a *acc) handle(raw []byte) {
	if a.halted && a.haltAtPrompt {
		return
	}
	var l line
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	switch l.Type {
	case "session_meta", "turn_context", "response_item", "event_msg":
	default:
		return
	}
	var p payload
	if json.Unmarshal(l.Payload, &p) != nil {
		return
	}
	var at time.Time
	if t, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
		at = t
		if a.s.Start.IsZero() {
			a.s.Start = t
		}
		a.s.End = t
	}
	switch l.Type {
	case "session_meta", "turn_context":
		if a.s.Cwd == "" {
			a.s.Cwd = p.Cwd
		}
		if p.Git.Branch != "" {
			a.s.Branch = p.Git.Branch
		}
	case "response_item":
		a.responseItem(p, at)
	case "event_msg":
		// user_message / agent_message duplicate response_item messages and
		// are ignored; task_complete carries the final assistant text.
		if p.Type == "task_complete" && strings.TrimSpace(p.LastAgentMessage) != "" {
			a.s.LastMessage = p.LastAgentMessage
		}
	}
}

func (a *acc) responseItem(p payload, at time.Time) {
	switch p.Type {
	case "message":
		var texts []string
		for _, c := range p.Content {
			if (c.Type == "input_text" || c.Type == "output_text") && strings.TrimSpace(c.Text) != "" {
				texts = append(texts, c.Text)
			}
		}
		text := strings.TrimSpace(strings.Join(texts, "\n"))
		switch p.Role {
		case "user":
			a.prompt(text, at)
		case "assistant":
			if text != "" {
				a.s.LastMessage = text
				a.addTail("assistant", text, text, at)
			}
		}
	case "function_call":
		switch {
		case shellTools[p.Name]:
			a.command(p, at)
		case p.Name == "apply_patch":
			var in struct {
				Input string `json:"input"`
			}
			_ = json.Unmarshal([]byte(p.Arguments), &in)
			a.patch(in.Input, at)
		}
	case "custom_tool_call":
		if p.Name == "apply_patch" {
			a.patch(p.Input, at)
		}
	case "function_call_output":
		i, ok := a.pending[p.CallID]
		if !ok {
			return
		}
		delete(a.pending, p.CallID)
		out := outputString(p.Output)
		if failed(out) {
			a.s.Commands[i].Failed = true
			a.addTail("failure", a.s.Commands[i].Command, outputText(out), at)
		}
	}
}

func (a *acc) prompt(text string, at time.Time) {
	if text == "" {
		return
	}
	for _, pre := range injectedPrefixes {
		if strings.HasPrefix(text, pre) {
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

func (a *acc) command(p payload, at time.Time) {
	cmd := commandText(p.Arguments)
	if cmd == "" {
		return
	}
	a.s.Commands = append(a.s.Commands, session.CommandRun{Command: cmd})
	if p.CallID != "" {
		a.pending[p.CallID] = len(a.s.Commands) - 1
	}
	a.addTail("command", cmd, cmd, at)
}

// commandText extracts the shell command from a function_call's JSON
// arguments: "command" as an argv array (a [sh|bash|zsh, -lc|-c, X] wrapper
// yields X, anything else is space-joined) or string, or "cmd" as a string.
func commandText(arguments string) string {
	var in struct {
		Command json.RawMessage `json:"command"`
		Cmd     string          `json:"cmd"`
	}
	if json.Unmarshal([]byte(arguments), &in) != nil {
		return ""
	}
	if len(in.Command) > 0 {
		var argv []string
		if json.Unmarshal(in.Command, &argv) == nil && len(argv) > 0 {
			if len(argv) == 3 && isShell(argv[0]) && (argv[1] == "-lc" || argv[1] == "-c") {
				return argv[2]
			}
			return strings.Join(argv, " ")
		}
		var s string
		if json.Unmarshal(in.Command, &s) == nil && s != "" {
			return s
		}
	}
	return in.Cmd
}

func isShell(s string) bool { return s == "sh" || s == "bash" || s == "zsh" }

// outputString returns a tool output value as text: a JSON string is
// unquoted, anything else is kept raw.
func outputString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// outputText returns the human text of a shell output: the "output" field of
// the JSON envelope when there is one, else the text itself.
func outputText(out string) string {
	var env struct {
		Output string `json:"output"`
	}
	if json.Unmarshal([]byte(out), &env) == nil && env.Output != "" {
		return env.Output
	}
	return out
}

// failed reports whether a shell output records a non-zero exit: JSON
// metadata.exit_code != 0, or a line "Exit code: N" with N > 0.
func failed(out string) bool {
	var env struct {
		Metadata struct {
			ExitCode *int `json:"exit_code"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(out), &env) == nil && env.Metadata.ExitCode != nil {
		return *env.Metadata.ExitCode != 0
	}
	return exitCodeRE.MatchString(out)
}

// patch records the files an apply_patch envelope touches.
func (a *acc) patch(input string, at time.Time) {
	for _, l := range strings.Split(input, "\n") {
		switch {
		case strings.HasPrefix(l, "*** Update File: "):
			a.touch(strings.TrimSpace(strings.TrimPrefix(l, "*** Update File: ")), "edit", at)
		case strings.HasPrefix(l, "*** Add File: "):
			a.touch(strings.TrimSpace(strings.TrimPrefix(l, "*** Add File: ")), "write", at)
		case strings.HasPrefix(l, "*** Delete File: "):
			a.touch(strings.TrimSpace(strings.TrimPrefix(l, "*** Delete File: ")), "delete", at)
		}
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
	known := false
	for _, o := range fc.Ops {
		known = known || o == op
	}
	if !known {
		fc.Ops = append(fc.Ops, op)
	}
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
