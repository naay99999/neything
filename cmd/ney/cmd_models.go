package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/config"
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
	}
	if cfg.Retrieval.Rerank {
		entries = append(entries, modelEntry{
			Provider: cfg.Reranker.Provider,
			Role:     "reranker",
			Model:    cfg.Reranker.Model,
			Active:   true,
		})
	}

	// if a local server is configured, list what it has available
	var ollamaModels []string
	if cfg.Embedder.Provider == "ollama" {
		ollamaModels = listOllamaModels(cfg.Embedder.Endpoint)
	}
	var lmModels []string
	lmConfigured := cfg.Embedder.Provider == "lmstudio"
	if lmConfigured {
		lmModels = listOpenAICompatModels(cfg.Embedder.Endpoint)
	}

	if flagJSON {
		type out struct {
			Configured []modelEntry `json:"configured"`
			Ollama     []string     `json:"ollama_installed"`
			LMStudio   []string     `json:"lmstudio_available"`
		}
		PrintJSON(out{Configured: entries, Ollama: ollamaModels, LMStudio: lmModels})
		return nil
	}

	PrintTable(
		[]string{"Provider", "Role", "Model"},
		func() [][]string {
			rows := make([][]string, len(entries))
			for i, e := range entries {
				rows[i] = []string{e.Provider, e.Role, e.Model}
			}
			return rows
		}(),
	)

	if len(ollamaModels) > 0 {
		fmt.Println("\nOllama installed models:")
		for _, m := range ollamaModels {
			fmt.Printf("  - %s\n", m)
		}
	} else if cfg.Embedder.Provider == "ollama" {
		fmt.Println("\n[Ollama offline or no models installed]")
	}
	if len(lmModels) > 0 {
		fmt.Println("\nModels available on the OpenAI-compatible server:")
		for _, m := range lmModels {
			fmt.Printf("  - %s\n", m)
		}
	} else if lmConfigured {
		fmt.Println("\n[LM Studio / OpenAI-compatible server offline or no models loaded]")
	}
	return nil
}

// listOpenAICompatModels queries an OpenAI-compatible /v1/models endpoint
// (LM Studio, vLLM, llama.cpp server, …) and returns the model ids.
func listOpenAICompatModels(endpoint string) []string {
	if endpoint == "" {
		endpoint = "http://localhost:1234"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(strings.TrimRight(endpoint, "/") + "/v1/models")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	names := make([]string, len(out.Data))
	for i, m := range out.Data {
		names[i] = m.ID
	}
	return names
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
