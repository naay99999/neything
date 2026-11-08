package session

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func collect(t *testing.T, s string) []string {
	t.Helper()
	var got []string
	if err := Lines(strings.NewReader(s), func(b []byte) { got = append(got, string(b)) }); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestLinesSkipsPartialFinalLine(t *testing.T) {
	got := collect(t, "a\nbb\n\ncc")
	if strings.Join(got, "|") != "a|bb|" {
		t.Fatalf("got %q", got)
	}
}

func TestLinesSkipsOversize(t *testing.T) {
	big := strings.Repeat("x", maxLine+1)
	got := collect(t, "a\n"+big+"\nb\n")
	if strings.Join(got, "|") != "a|b" {
		t.Fatalf("got %d lines", len(got))
	}
	ok := strings.Repeat("y", 2<<20)
	got = collect(t, ok+"\n")
	if len(got) != 1 || len(got[0]) != len(ok) {
		t.Fatal("2 MiB line not delivered intact")
	}
}

func TestTailReader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("aaaa\nbbbb\ncccc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	read := func(n int64) string {
		r, err := TailReader(f, n)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		return string(b)
	}
	if got := read(100); got != "aaaa\nbbbb\ncccc\n" {
		t.Fatalf("whole: %q", got)
	}
	if got := read(8); got != "cccc\n" { // window "bb\ncccc\n" -> after first newline
		t.Fatalf("tail: %q", got)
	}
	if got := read(3); got != "" {
		t.Fatalf("no newline in window: %q", got)
	}
}
