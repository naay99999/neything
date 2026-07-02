package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/store"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactive setup — detect your LLM servers and write the config",
	RunE:  runInit,
}

const (
	defaultOllamaEndpoint   = "http://localhost:11434"
	defaultLMStudioEndpoint = "http://localhost:1234"
)

type wizardChoice struct {
	embedProvider, embedModel, embedEndpoint string
	chatProvider, chatModel, chatEndpoint    string
}

func runInit(cmd *cobra.Command, args []string) error {
	fmt.Println(Bold("Let's set up ney — where do your models run?"))
	fmt.Println(Dim("Enter accepts the [default]; Ctrl+C aborts."))
	fmt.Println()

	ollamaModels := listOllamaModels(defaultOllamaEndpoint)
	lmModels := listOpenAICompatModels(defaultLMStudioEndpoint)

	fmt.Printf("  [1] Ollama on this machine%s\n", detectedNote(len(ollamaModels)))
	fmt.Printf("  [2] LM Studio / OpenAI-compatible server (local or remote)%s\n", detectedNote(len(lmModels)))
	fmt.Printf("  [3] Cloud APIs (OpenAI, Gemini, Claude)\n")

	def := "3"
	switch {
	case len(ollamaModels) > 0:
		def = "1"
	case len(lmModels) > 0:
		def = "2"
	}

	var w wizardChoice
	var err error
	switch promptDefault("Choice", def) {
	case "1":
		err = wizardOllama(&w, ollamaModels)
	case "2":
		err = wizardLMStudio(&w, lmModels)
	case "3":
		err = wizardCloud(&w)
	default:
		return fmt.Errorf("setup aborted: pick 1, 2, or 3")
	}
	if err != nil {
		return err
	}

	if err := writeWizardConfig(&w); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	testEmbedder(cfg)
	offerResetOnModelChange(cmd.Context(), cfg, &w)

	fmt.Println()
	fmt.Println(Bold("Next steps:"))
	fmt.Println("  ney index ~/path/to/your/notes")
	fmt.Println("  ney ask \"your question\"")
	fmt.Println(Dim("  (ney doctor checks everything again at any time)"))
	return nil
}

func detectedNote(n int) string {
	if n == 0 {
		return ""
	}
	return Green(fmt.Sprintf("   ← detected, %d model(s)", n))
}

func wizardOllama(w *wizardChoice, models []string) error {
	endpoint := defaultOllamaEndpoint
	if len(models) == 0 {
		endpoint = normalizeEndpoint(promptDefault("Ollama URL", defaultOllamaEndpoint))
		models = listOllamaModels(endpoint)
		if len(models) == 0 {
			return fmt.Errorf("no Ollama server (or no models) at %s — run `ollama serve` and `ollama pull bge-m3`, then rerun ney init", endpoint)
		}
	}

	embedModel, chatModel, err := pickModels(models)
	if err != nil {
		return err
	}
	w.embedProvider, w.embedModel, w.embedEndpoint = "ollama", embedModel, endpoint
	w.chatProvider, w.chatModel, w.chatEndpoint = "ollama", chatModel, endpoint
	return maybeCloudChat(w)
}

func wizardLMStudio(w *wizardChoice, localModels []string) error {
	def := defaultLMStudioEndpoint
	endpoint := normalizeEndpoint(promptDefault("Server URL (e.g. http://192.168.1.150:1234)", def))

	models := localModels
	if endpoint != defaultLMStudioEndpoint || len(models) == 0 {
		sp := startSpinner("checking " + endpoint)
		models = listOpenAICompatModels(endpoint)
		sp.Stop()
	}
	if len(models) == 0 {
		return fmt.Errorf("no OpenAI-compatible server responding at %s/v1/models — is LM Studio running with its server enabled?", endpoint)
	}
	fmt.Printf("%s reachable, %d model(s)\n", Green("✓ "+endpoint), len(models))

	embedModel, chatModel, err := pickModels(models)
	if err != nil {
		return err
	}
	w.embedProvider, w.embedModel, w.embedEndpoint = "lmstudio", embedModel, endpoint
	w.chatProvider, w.chatModel, w.chatEndpoint = "lmstudio", chatModel, endpoint
	return maybeCloudChat(w)
}

// pickModels lists the server's models and lets the user pick an embedding
// and a chat model, preselecting sensible defaults by name.
func pickModels(models []string) (embedModel, chatModel string, err error) {
	fmt.Println("\nModels on the server:")
	for i, m := range models {
		fmt.Printf("  [%d] %s\n", i+1, m)
	}

	embedDef, chatDef := "", ""
	for _, m := range models {
		if looksLikeEmbedder(m) {
			if embedDef == "" {
				embedDef = m
			}
		} else if chatDef == "" {
			chatDef = m
		}
	}

	if embedDef == "" {
		fmt.Println(Yellow("⚠ No embedding model detected — load one (e.g. nomic-embed-text / bge-m3) and pick it below."))
	}
	embedModel = promptModel("Embedding model (number or name)", models, embedDef)
	if embedModel == "" {
		return "", "", fmt.Errorf("setup aborted: an embedding model is required")
	}
	if chatDef == "" {
		chatDef = models[0]
	}
	chatModel = promptModel("Chat model (number or name)", models, chatDef)
	if chatModel == "" {
		return "", "", fmt.Errorf("setup aborted: a chat model is required")
	}
	return embedModel, chatModel, nil
}

// maybeCloudChat lets local-server users route only chat to a cloud provider
// (privacy-friendly mix: local embeddings, cloud answers).
func maybeCloudChat(w *wizardChoice) error {
	ans := strings.ToLower(promptDefault("Use the same server for chat answers? (y/n)", "y"))
	if ans == "" || strings.HasPrefix(ans, "y") {
		return nil
	}
	return pickCloudChat(w)
}

func wizardCloud(w *wizardChoice) error {
	fmt.Println("\nEmbedding provider (Claude has no embedding API):")
	fmt.Println("  [1] OpenAI  (text-embedding-3-small)")
	fmt.Println("  [2] Gemini  (text-embedding-004)")
	switch promptDefault("Choice", "1") {
	case "2":
		w.embedProvider, w.embedModel = "gemini", "text-embedding-004"
		warnMissingKey("GEMINI_API_KEY")
	default:
		w.embedProvider, w.embedModel = "openai", "text-embedding-3-small"
		warnMissingKey("OPENAI_API_KEY")
	}
	return pickCloudChat(w)
}

func pickCloudChat(w *wizardChoice) error {
	fmt.Println("\nChat provider:")
	fmt.Println("  [1] Claude  (claude-sonnet-4-6)")
	fmt.Println("  [2] OpenAI  (gpt-4o)")
	fmt.Println("  [3] Gemini  (gemini-2.0-flash)")
	switch promptDefault("Choice", "1") {
	case "2":
		w.chatProvider, w.chatModel, w.chatEndpoint = "openai", "gpt-4o", ""
		warnMissingKey("OPENAI_API_KEY")
	case "3":
		w.chatProvider, w.chatModel, w.chatEndpoint = "gemini", "gemini-2.0-flash", ""
		warnMissingKey("GEMINI_API_KEY")
	default:
		w.chatProvider, w.chatModel, w.chatEndpoint = "claude", "claude-sonnet-4-6", ""
		warnMissingKey("ANTHROPIC_API_KEY")
	}
	return nil
}

func warnMissingKey(envVar string) {
	if os.Getenv(envVar) == "" {
		fmt.Println(Yellow(fmt.Sprintf("⚠ %s is not set — export it before indexing/asking.", envVar)))
	}
}

func looksLikeEmbedder(model string) bool {
	m := strings.ToLower(model)
	for _, marker := range []string{"embed", "bge", "minilm", "e5-", "gte-"} {
		if strings.Contains(m, marker) {
			return true
		}
	}
	return false
}

func promptDefault(question, def string) string {
	label := question
	if def != "" {
		label += " [" + def + "]"
	}
	ans := promptLine(Cyan(label + ": "))
	if ans == "" {
		return def
	}
	return ans
}

func promptModel(question string, models []string, def string) string {
	ans := promptDefault(question, def)
	if n, err := strconv.Atoi(ans); err == nil && n >= 1 && n <= len(models) {
		return models[n-1]
	}
	return ans
}

func normalizeEndpoint(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s != "" && !strings.Contains(s, "://") {
		s = "http://" + s
	}
	return s
}

func writeWizardConfig(w *wizardChoice) error {
	if err := os.MkdirAll(config.NeyDir(), 0o700); err != nil {
		return err
	}
	cfgPath := config.ConfigPath()
	if data, err := os.ReadFile(cfgPath); err == nil {
		if err := os.WriteFile(cfgPath+".bak", data, 0o600); err != nil {
			return fmt.Errorf("backup existing config: %w", err)
		}
		fmt.Println(Dim("(previous config saved as config.yaml.bak)"))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Ney configuration — generated by `ney init` on %s\n\n", time.Now().Format("2006-01-02"))
	fmt.Fprintf(&b, "# embedder: creates vectors (openai | gemini | ollama | lmstudio — never claude)\n")
	fmt.Fprintf(&b, "embedder:\n  provider: %s\n  model: %s\n", w.embedProvider, w.embedModel)
	if w.embedEndpoint != "" {
		fmt.Fprintf(&b, "  endpoint: %s\n", w.embedEndpoint)
	}
	fmt.Fprintf(&b, "\n# chat: answers questions in `ney ask` (claude | openai | gemini | ollama | lmstudio)\n")
	fmt.Fprintf(&b, "chat:\n  provider: %s\n  model: %s\n", w.chatProvider, w.chatModel)
	if w.chatEndpoint != "" {
		fmt.Fprintf(&b, "  endpoint: %s\n", w.chatEndpoint)
	}
	b.WriteString(`
retrieval:
  top_k: 8
  max_context_chars: 12000
  rerank: false
  hybrid: false

chunking:
  strategy: markdown        # auto | character | sentence | paragraph | markdown | tokenizer | page
  target_chars: 1200
  overlap_chars: 150

# privacy — off by default
telemetry: false
`)

	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o600); err != nil {
		return err
	}
	fmt.Println(Green("✓ Config written to " + displayPath(cfgPath)))
	return nil
}

func testEmbedder(cfg *config.Config) {
	sp := startSpinner("testing embedder")
	emb, err := config.NewEmbedder(cfg)
	var dims int
	if err == nil {
		var vecs [][]float32
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		vecs, err = emb.Embed(ctx, []string{"ney setup probe"})
		if err == nil && len(vecs) > 0 {
			dims = len(vecs[0])
		}
	}
	sp.Stop()
	if err != nil {
		printCLIError(fmt.Errorf("embedder test failed: %w", err))
		fmt.Println(Yellow("Config was written anyway — fix the issue and check with: ney doctor"))
		return
	}
	fmt.Println(Green(fmt.Sprintf("✓ Embedder works (%d dimensions)", dims)))
}

// offerResetOnModelChange catches the switch-embedder trap: an existing index
// built with a different model would make the next `ney index` refuse to run.
func offerResetOnModelChange(ctx context.Context, cfg *config.Config, w *wizardChoice) {
	db, err := store.Open(config.DBPath())
	if err != nil {
		return
	}
	defer db.Close()
	stats, err := db.Stats()
	if err != nil || stats == nil || stats.ChunkCount == 0 {
		return
	}
	active, err := db.GetActiveEmbedder()
	if err != nil || active == nil {
		return
	}
	if !strings.Contains(active.Name, w.embedModel) {
		fmt.Println(Yellow("⚠ Your existing index was built with a different embedding model."))
		ans := strings.ToLower(promptDefault("Reset the index now (re-index afterwards)? (y/n)", "y"))
		if ans == "" || strings.HasPrefix(ans, "y") {
			if err := db.DeleteAllData(); err != nil {
				printCLIError(err)
				return
			}
			os.Remove(config.VectorsPath())
			os.Remove(config.HNSWPath())
			os.Remove(config.HNSWPath() + ".graph")
			fmt.Println(Green("✓ Index cleared — run: ney index <path>"))
		} else {
			fmt.Println(Dim("Left as-is. `ney index` will refuse until you run: ney reset"))
		}
	}
}
