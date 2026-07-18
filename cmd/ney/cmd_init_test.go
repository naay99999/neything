package main

import "testing"

func TestNormalizeEndpoint(t *testing.T) {
	cases := map[string]string{
		"192.168.1.150:1234":      "http://192.168.1.150:1234",
		"http://localhost:1234/":  "http://localhost:1234",
		"https://api.example.com": "https://api.example.com",
		"  http://host:1234/  ":   "http://host:1234",
		"":                        "",
	}
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
