// Package handoff builds the short "what was left unfinished" brief handed to
// the next agent session. It is pure apart from the notes files, and Build is
// the single redaction chokepoint.
package handoff

import (
	"time"
	"unicode/utf8"

	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/redact"
	"github.com/naay99999/neything/internal/session"
)

// maxFieldBytes bounds every string before redaction; adapters don't cap, and
// a transcript field can be megabytes.
const maxFieldBytes = 8 << 10

// Brief is the distilled, already-redacted handoff state for one repo.
type Brief struct {
	RepoName string
	Primary  *session.Session  // nil when no session in window
	Others   []session.Session // at most 2
	Note     *Note             // newest note, only if written at/after Primary.Start (or Primary nil)
	Git      neycontext.GitState
	GitOK    bool
	Now      time.Time
}

// Build assembles a Brief. sessions are newest End first. Every string is
// redacted and then trimmed to 8 KiB here, and nowhere else.
func Build(repoName string, sessions []session.Session, notes []Note, git neycontext.GitState, gitOK bool, now time.Time) Brief {
	b := Brief{RepoName: scrub(repoName), Git: git, GitOK: gitOK, Now: now}
	if len(sessions) > 0 {
		p := scrubSession(sessions[0])
		b.Primary = &p
		for i := 1; i < len(sessions) && i < 3; i++ {
			b.Others = append(b.Others, scrubSession(sessions[i]))
		}
	}
	var newest *Note
	for i := range notes {
		if newest == nil || notes[i].At.After(newest.At) {
			newest = &notes[i]
		}
	}
	if newest != nil && (b.Primary == nil || !newest.At.Before(b.Primary.Start)) {
		n := Note{Agent: scrub(newest.Agent), At: newest.At, Body: scrub(newest.Body)}
		b.Note = &n
	}
	return b
}

// Empty reports whether there is nothing worth handing over.
func (b Brief) Empty() bool { return b.Primary == nil && b.Note == nil }

// scrub redacts before the final trim: a secret straddling the 8 KiB cut would
// otherwise leave an unmatchable fragment. A 2x window is redacted first.
func scrub(s string) string {
	return trimBytes(redact.String(trimBytes(s, 2*maxFieldBytes)), maxFieldBytes)
}

// trimBytes keeps at most max bytes of s, cut on a UTF-8 boundary.
func trimBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func scrubSession(s session.Session) session.Session {
	out := s
	out.Agent, out.ID, out.Title = scrub(s.Agent), scrub(s.ID), scrub(s.Title)
	out.Branch, out.Cwd = scrub(s.Branch), scrub(s.Cwd)
	out.FirstPrompt, out.LastPrompt = scrub(s.FirstPrompt), scrub(s.LastPrompt)
	out.LastMessage = scrub(s.LastMessage)
	out.Files = make([]session.FileChange, len(s.Files))
	for i, f := range s.Files {
		nf := session.FileChange{Path: scrub(f.Path), Count: f.Count}
		for _, op := range f.Ops {
			nf.Ops = append(nf.Ops, scrub(op))
		}
		out.Files[i] = nf
	}
	out.Commands = make([]session.CommandRun, len(s.Commands))
	for i, c := range s.Commands {
		out.Commands[i] = session.CommandRun{Command: scrub(c.Command), Failed: c.Failed}
	}
	out.Tail = make([]session.TailEntry, len(s.Tail))
	for i, t := range s.Tail {
		out.Tail[i] = session.TailEntry{Kind: scrub(t.Kind), Head: scrub(t.Head), Body: scrub(t.Body), At: t.At}
	}
	return out
}
