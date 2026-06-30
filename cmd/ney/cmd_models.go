package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/naay/ney/internal/config"
	"github.com/spf13/cobra"
)

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List available providers and models",
	RunE:  runModels,
}

type modelEntry struct {
	Provider string `json:"provider"`
	Role     string `json:"role"`
	Model    string `json:"model"`
	Active   bool   `json:"active"`
}

func runModels(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	entries := []modelEntry{
		{Provider: cfg.Embedder.Provider, Role: "embedder", Model: cfg.Embedder.Model, Active: true},
		{Provider: cfg.Chat.Provider, Role: "chat", Model: cfg.Chat.Model, Active: true},
	}

	// if Ollama configured, list installed models
	var ollamaModels []string
	if cfg.Embedder.Provider == "ollama" || cfg.Chat.Provider == "ollama" {
		ollamaModels = listOllamaModels(cfg.Embedder.Endpoint)
	}

	if flagJSON {
		type out struct {
			Configured []modelEntry `json:"configured"`
			Ollama     []string     `json:"ollama_installed"`
		}
		PrintJSON(out{Configured: entries, Ollama: ollamaModels})
		return nil
	}

	PrintTable(
		[]string{"Provider", "Role", "Model"},
		[][]string{
			{entries[0].Provider, "embedder", entries[0].Model},
			{entries[1].Provider, "chat", entries[1].Model},
		},
	)

	if len(ollamaModels) > 0 {
		fmt.Println("\nOllama installed models:")
		for _, m := range ollamaModels {
			fmt.Printf("  - %s\n", m)
		}
	} else if cfg.Embedder.Provider == "ollama" || cfg.Chat.Provider == "ollama" {
		fmt.Println("\n[Ollama offline or no models installed]")
	}
	return nil
}

func listOllamaModels(endpoint string) []string {
	if endpoint == "" {
		endpoint = "http://localhost:11434"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(endpoint + "/api/tags")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	names := make([]string, len(out.Models))
	for i, m := range out.Models {
		names[i] = m.Name
	}
	return names
}
