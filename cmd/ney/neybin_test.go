package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// caskLayout builds the filesystem shape a Homebrew cask install produces:
// a stable symlink in bin/ pointing into a VERSIONED Caskroom directory that
// `brew upgrade` deletes. Returns (stablePath, versionedPath).
func caskLayout(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()

	versionedDir := filepath.Join(root, "Caskroom", "ney", "1.2.3")
	if err := os.MkdirAll(versionedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	versioned := filepath.Join(versionedDir, "ney")
	if err := os.WriteFile(versioned, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(binDir, "ney")
	if err := os.Symlink(versioned, stable); err != nil {
		t.Fatal(err)
	}
	return stable, versioned
}

// TestResolveNeyBinaryPrefersStablePathEntryOverCaskroomTarget is the
// regression guard for the Homebrew breakage: registering the versioned
// Caskroom path means every AI client breaks on the next `brew upgrade`.
func TestResolveNeyBinaryPrefersStablePathEntryOverCaskroomTarget(t *testing.T) {
	t.Setenv("NEY_BINARY", "")
	stable, versioned := caskLayout(t)

	r := binResolver{
		LookPath:   func(string) (string, error) { return stable, nil },
		Executable: func() (string, error) { return versioned, nil }, // Linux /proc/self/exe shape
		SameFile:   sameFile,
	}

	if got := r.resolve(); got != stable {
		t.Errorf("resolve() = %q, want the stable symlink %q (never the versioned target)", got, versioned)
	}
}

func TestResolveNeyBinaryFallsBackToExecutableWhenPathEntryIsADifferentBinary(t *testing.T) {
	t.Setenv("NEY_BINARY", "")
	_, versioned := caskLayout(t)

	other := filepath.Join(t.TempDir(), "ney")
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := binResolver{
		LookPath:   func(string) (string, error) { return other, nil },
		Executable: func() (string, error) { return versioned, nil },
		SameFile:   sameFile,
	}

	if got := r.resolve(); got != versioned {
		t.Errorf("resolve() = %q, want %q — an unrelated `ney` on PATH must not hijack the registration", got, versioned)
	}
}

func TestResolveNeyBinaryUsesPathEntryWhenExecutableIsUnavailable(t *testing.T) {
	t.Setenv("NEY_BINARY", "")
	stable, _ := caskLayout(t)

	r := binResolver{
		LookPath:   func(string) (string, error) { return stable, nil },
		Executable: func() (string, error) { return "", errors.New("unsupported") },
		SameFile:   sameFile,
	}

	if got := r.resolve(); got != stable {
		t.Errorf("resolve() = %q, want %q", got, stable)
	}
}

func TestResolveNeyBinaryHonorsEnvOverride(t *testing.T) {
	t.Setenv("NEY_BINARY", "/opt/custom/ney")

	r := binResolver{
		LookPath:   func(string) (string, error) { return "/usr/local/bin/ney", nil },
		Executable: func() (string, error) { return "/somewhere/else/ney", nil },
		SameFile:   func(string, string) bool { return true },
	}

	if got := r.resolve(); got != "/opt/custom/ney" {
		t.Errorf("resolve() = %q, want the NEY_BINARY override", got)
	}
}

func TestResolveNeyBinaryNeverReturnsEmpty(t *testing.T) {
	t.Setenv("NEY_BINARY", "")

	r := binResolver{
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
		Executable: func() (string, error) { return "", errors.New("unsupported") },
		SameFile:   func(string, string) bool { return false },
	}

	if got := r.resolve(); got != "ney" {
		t.Errorf("resolve() = %q, want the bare name %q as a last resort", got, "ney")
	}
}
