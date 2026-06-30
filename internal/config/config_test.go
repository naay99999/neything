package config

import "testing"

func TestValidateRejectsClaudeEmbedder(t *testing.T) {
	cfg := &Config{
		Embedder: EmbedderConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
		Chat:     ChatConfig{Provider: "openai", Model: "gpt-4o"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for claude embedder")
	}
}

func TestValidateAcceptsValidProviders(t *testing.T) {
	cfg := &Config{
		Embedder: EmbedderConfig{Provider: "ollama", Model: "bge-m3"},
		Chat:     ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateRequiresModels(t *testing.T) {
	cfg := &Config{
		Embedder: EmbedderConfig{Provider: "ollama", Model: ""},
		Chat:     ChatConfig{Provider: "claude", Model: "claude-sonnet-4-6"},
	}
	if err := Validate(cfg); err == nil {
		t.Fatal("expected error for empty embedder model")
	}
}
