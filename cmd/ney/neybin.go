package main

import (
	"os"
	"os/exec"
)

// binResolver holds the three OS calls resolveNeyBinary makes, as fields, so
// tests can substitute a fake filesystem — exec.LookPath and os.Executable are
// process-global and otherwise untestable.
type binResolver struct {
	LookPath   func(file string) (string, error)
	Executable func() (string, error)
	SameFile   func(a, b string) bool
}

var defaultBinResolver = binResolver{
	LookPath:   exec.LookPath,
	Executable: os.Executable,
	SameFile:   sameFile,
}

// sameFile reports whether a and b are the same file on disk. os.Stat (not
// Lstat) on both sides is required: one of the two paths is typically a
// symlink and the point is to compare the files they name.
func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

// resolve returns the path to record in AI-client MCP configs.
//
// It deliberately never calls filepath.EvalSymlinks. A Homebrew cask installs
// /opt/homebrew/bin/ney as a symlink into a VERSIONED Caskroom directory that
// `brew upgrade` deletes — so the symlink is the stable path and its target is
// the unstable one. Resolving it would write a path that dies on the next
// upgrade, silently breaking every registered client.
//
// Order: explicit override → a `ney` on PATH that is this same binary → this
// process's own path, unresolved → the bare name.
func (r binResolver) resolve() string {
	if p := os.Getenv("NEY_BINARY"); p != "" {
		return p
	}

	self, selfErr := r.Executable()

	// A `ney` on PATH is the stable, upgrade-surviving entry
	// (/opt/homebrew/bin/ney, /usr/local/bin/ney, ~/go/bin/ney). Only trust it
	// when it names this same binary, so a different ney elsewhere on PATH
	// can't hijack the registration.
	//
	// On Linux os.Executable() reads /proc/self/exe, which the kernel fully
	// resolves — so for a symlinked install `self` is already the versioned
	// path and this branch is the only thing that recovers the stable one.
	if p, err := r.LookPath("ney"); err == nil && p != "" {
		if selfErr != nil || r.SameFile(p, self) {
			return p
		}
	}

	if selfErr == nil && self != "" {
		return self
	}
	return "ney"
}

// resolveNeyBinary returns the ney binary path to record in AI-client configs.
func resolveNeyBinary() string { return defaultBinResolver.resolve() }
