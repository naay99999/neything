package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/store"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system readiness",
	RunE:  runDoctor,
}

type checkResult struct {
	Check   string `json:"check"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

func runDoctor(cmd *cobra.Command, args []string) error {
	var results []checkResult

	// 1. Config valid
	cfg, err := config.Load()
	if err != nil {
		results = append(results, checkResult{
			Check:   "config_valid",
			OK:      false,
			Message: fmt.Sprintf("Config error: %v", err),
			Hint:    "Edit ~/.ney/config.yaml",
		})
		printDoctorResults(results)
		return nil
	}
	results = append(results, checkResult{
		Check:   "config_valid",
		OK:      true,
		Message: fmt.Sprintf("Config file valid (%s)", config.ConfigPath()),
	})

	// 2. Embedder is not Claude
	if cfg.Embedder.Provider == "claude" {
		results = append(results, checkResult{
			Check:   "embedder_not_claude",
			OK:      false,
			Message: "Claude cannot be used as embedder",
			Hint:    "Set embedder.provider to: openai, gemini, or ollama",
		})
	} else {
		results = append(results, checkResult{
			Check:   "embedder_not_claude",
			OK:      true,
			Message: fmt.Sprintf("Embedder provider is %q (not Claude)", cfg.Embedder.Provider),
		})
	}

	// 3. API keys
	type apiCheck struct {
		envVar   string
		provider string
	}
	needed := map[string]bool{}
	if cfg.Embedder.Provider == "openai" || cfg.Chat.Provider == "openai" {
		needed["OPENAI_API_KEY"] = true
	}
	if cfg.Chat.Provider == "claude" {
		needed["ANTHROPIC_API_KEY"] = true
	}
	if cfg.Embedder.Provider == "gemini" || cfg.Chat.Provider == "gemini" {
		needed["GEMINI_API_KEY"] = true
	}
	for envVar := range needed {
		if os.Getenv(envVar) != "" {
			results = append(results, checkResult{
				Check:   "api_key_" + strings.ToLower(strings.TrimSuffix(envVar, "_API_KEY")),
				OK:      true,
				Message: envVar + " is set",
			})
		} else {
			results = append(results, checkResult{
				Check:   "api_key_" + strings.ToLower(strings.TrimSuffix(envVar, "_API_KEY")),
				OK:      false,
				Message: envVar + " not set",
				Hint:    fmt.Sprintf("export %s=<your-key>", envVar),
			})
		}
	}

	// 4 & 5. Ollama reachable + model installed
	ollamaNeeded := cfg.Embedder.Provider == "ollama" || cfg.Chat.Provider == "ollama"
	if ollamaNeeded {
		endpoint := cfg.Embedder.Endpoint
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get(endpoint + "/")
		if err != nil {
			results = append(results, checkResult{
				Check:   "ollama_reachable",
				OK:      false,
				Message: "Ollama not reachable at " + endpoint,
				Hint:    "Run: ollama serve",
			})
		} else {
			resp.Body.Close()
			results = append(results, checkResult{
				Check:   "ollama_reachable",
				OK:      true,
				Message: "Ollama daemon reachable at " + endpoint,
			})

			// check model installed
			installed := listOllamaModels(endpoint)
			installedSet := map[string]bool{}
			for _, m := range installed {
				installedSet[m] = true
				// also check short name without tag
				if i := strings.Index(m, ":"); i >= 0 {
					installedSet[m[:i]] = true
				}
			}
			checkModel := func(model, role string) {
				shortName := model
				if i := strings.Index(model, ":"); i >= 0 {
					shortName = model[:i]
				}
				if installedSet[model] || installedSet[shortName] {
					results = append(results, checkResult{
						Check:   "ollama_model_" + role,
						OK:      true,
						Message: fmt.Sprintf("Ollama %s model %q installed", role, model),
					})
				} else {
					results = append(results, checkResult{
						Check:   "ollama_model_" + role,
						OK:      false,
						Message: fmt.Sprintf("Ollama %s model %q not found", role, model),
						Hint:    "Run: ollama pull " + model,
					})
				}
			}
			if cfg.Embedder.Provider == "ollama" {
				checkModel(cfg.Embedder.Model, "embedder")
			}
			if cfg.Chat.Provider == "ollama" {
				checkModel(cfg.Chat.Model, "chat")
			}
		}
	}

	// 6. SQLite writable
	db, err := store.Open(config.DBPath())
	if err != nil {
		results = append(results, checkResult{
			Check:   "sqlite_writable",
			OK:      false,
			Message: fmt.Sprintf("SQLite not writable: %v", err),
			Hint:    "Check permissions on " + config.NeyDir(),
		})
	} else {
		db.Close()
		results = append(results, checkResult{
			Check:   "sqlite_writable",
			OK:      true,
			Message: fmt.Sprintf("SQLite DB writable (%s)", config.DBPath()),
		})

		// 7. Index has data
		db2, _ := store.Open(config.DBPath())
		if db2 != nil {
			defer db2.Close()
			stats, _ := db2.Stats()
			if stats != nil && stats.ChunkCount > 0 {
				results = append(results, checkResult{
					Check:   "index_has_data",
					OK:      true,
					Message: fmt.Sprintf("Index contains %d chunks across %d files", stats.ChunkCount, stats.DocumentCount),
				})

				// 8. Embedder consistency
				active, err := db2.GetActiveEmbedder()
				if err == nil && active != nil {
					var storedModel string
					val := active.Name
					if i := strings.Index(val, `"model":"`); i >= 0 {
						rest := val[i+9:]
						if j := strings.Index(rest, `"`); j >= 0 {
							storedModel = rest[:j]
						}
					}
					if storedModel == "" {
						// raw JSON fallback — try to unmarshal
						var m map[string]any
						if json.Unmarshal([]byte(val), &m) == nil {
							if s, ok := m["model"].(string); ok {
								storedModel = s
							}
						}
					}
					if storedModel != "" && storedModel != cfg.Embedder.Model {
						results = append(results, checkResult{
							Check:   "embedder_consistent",
							OK:      false,
							Message: fmt.Sprintf("Embedder mismatch: index built with %q, config says %q", storedModel, cfg.Embedder.Model),
							Hint:    "Run: ney reset && ney index <path>",
						})
					} else {
						results = append(results, checkResult{
							Check:   "embedder_consistent",
							OK:      true,
							Message: fmt.Sprintf("Embedder consistent (%s)", cfg.Embedder.Model),
						})
					}
				}
			} else {
				results = append(results, checkResult{
					Check:   "index_has_data",
					OK:      false,
					Message: "Index is empty",
					Hint:    "Run: ney index <path>",
				})
			}
		}
	}

	printDoctorResults(results)
	return nil
}

func printDoctorResults(results []checkResult) {
	if flagJSON {
		PrintJSON(results)
		return
	}
	allOK := true
	for _, r := range results {
		mark := CheckMark(r.OK)
		fmt.Printf("%s %s\n", mark, r.Message)
		if !r.OK && r.Hint != "" {
			fmt.Printf("    → %s\n", r.Hint)
		}
		if !r.OK {
			allOK = false
		}
	}
	if allOK {
		fmt.Println("\nAll checks passed. Ney is ready!")
	}
}
