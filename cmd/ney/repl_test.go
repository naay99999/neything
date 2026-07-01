package main

import "testing"

func TestNearestCommand(t *testing.T) {
	known := map[string]bool{"search": true, "status": true, "ask": true, "index": true, "init": true}
	cases := map[string]string{
		"serch":   "search",
		"statuss": "status",
		"sta":     "status", // prefix match
		"xyzzyqq": "",       // nothing close
	}
	for in, want := range cases {
		if got := nearestCommand(in, known); got != want {
			t.Errorf("nearestCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEditDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"serch", "search", 1},
		{"ask", "ask", 0},
		{"exit", "index", 5},
		{"", "abc", 3},
	}
	for _, c := range cases {
		if got := editDistance(c.a, c.b); got != c.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestHandleMetaCommandExitWords(t *testing.T) {
	for _, line := range []string{"exit", "quit", "q", "leave", ":quit", ":exit", "/exit", "/quit", "EXIT"} {
		handled, exit := handleMetaCommand(line)
		if !handled || !exit {
			t.Errorf("handleMetaCommand(%q) = (%v, %v), want (true, true)", line, handled, exit)
		}
	}
	if handled, _ := handleMetaCommand("what is love"); handled {
		t.Error("questions must not be treated as meta commands")
	}
}

func TestNormalizeEndpoint(t *testing.T) {
	cases := map[string]string{
		"192.168.1.150:1234":      "http://192.168.1.150:1234",
		"http://localhost:1234/":  "http://localhost:1234",
		"https://api.example.com": "https://api.example.com",
		"  http://host:1234/  ":   "", // whitespace handled below
		"":                        "",
	}
	cases["  http://host:1234/  "] = "http://host:1234"
	for in, want := range cases {
		if got := normalizeEndpoint(in); got != want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLooksLikeEmbedder(t *testing.T) {
	if !looksLikeEmbedder("text-embedding-nomic-embed-text-v1.5") {
		t.Error("nomic embed model should look like an embedder")
	}
	if looksLikeEmbedder("google/gemma-4-e4b") {
		t.Error("gemma should not look like an embedder")
	}
}
