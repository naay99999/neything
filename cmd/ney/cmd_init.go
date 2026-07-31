package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/naay99999/neything/internal/config"
	neycontext "github.com/naay99999/neything/internal/context"
	"github.com/naay99999/neything/internal/discover"
	"github.com/naay99999/neything/internal/lockfile"
	"github.com/naay99999/neything/internal/store"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Guided setup — discover repos, set up profile, connect AI clients",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSetupWizard(cmd.Context())
	},
}

const (
	defaultOllamaEndpoint   = "http://localhost:11434"
	defaultLMStudioEndpoint = "http://localhost:1234"
)

type wizardChoice struct {
	embedProvider, embedModel, embedEndpoint string
}

// setupCompleted reports whether this machine has been through setup: either
// the wizard finished once (meta key) or workspaces already exist (installs
// that predate the wizard). Errors count as "set up" so a broken DB can never
// trap the user in an unwanted wizard loop.
func setupCompleted() bool {
	db, err := store.Open(config.DBPath())
	if err != nil {
		return true
	}
	defer db.Close()
	if v, err := db.GetMeta("setup_completed"); err == nil && v != "" {
		return true
	}
	wss, err := db.ListWorkspaces()
	return err != nil || len(wss) > 0
}

// runSetupWizard walks the user through the full first-run flow. Every step
// is idempotent and skippable; setup_completed is recorded only at the end,
// so an interrupted run simply offers itself again next time.
func runSetupWizard(ctx context.Context) error {
	fmt.Println(Bold("Ney setup — ให้ AI ของคุณรู้จักโปรเจกต์และค้นเอกสารในเครื่องได้อย่างปลอดภัย"))
	fmt.Println(Dim("Enter = ค่าเริ่มต้น, Ctrl+C = ยกเลิก (รัน `ney init` ซ้ำได้เสมอ)"))
	fmt.Println()

	// Loaded early (and reloaded after writeSetupConfig) because step 1
	// needs cfg.Context.DevRoots to know where to scan for repos.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	selected, devRoot := stepRepos(ctx, cfg)

	stepProfile()

	stepClients()

	w := stepEmbedder()

	if err := writeSetupConfig(w, devRoot); err != nil {
		return err
	}
	cfg, err = config.Load()
	if err != nil {
		return err
	}
	if w != nil {
		testEmbedder(cfg)
		offerResetOnModelChange(ctx, cfg, w)
	}

	indexed := indexSelected(ctx, cfg, selected)

	if db, err := store.Open(config.DBPath()); err == nil {
		_ = db.SetMeta("setup_completed", time.Now().Format(time.RFC3339))
		db.Close()
	}

	fmt.Println()
	fmt.Println(Bold("✓ Setup เสร็จแล้ว"))
	if indexed > 0 {
		fmt.Printf("  index แล้ว %d repo — เปิด Claude/Codex แล้วถามหาไฟล์หรือถามความคืบหน้าโปรเจกต์ได้เลย\n", indexed)
	}
	fmt.Println(Dim("  เพิ่ม/แก้ทีหลัง: รัน `ney init` อีกครั้ง หรือบอก AI ว่า \"index โฟลเดอร์นี้ให้หน่อย\""))
	fmt.Println()
	fmt.Println(Dim("  Memory: บอก AI ว่า \"จำไว้ว่า...\" แล้วมันจะเซฟเป็นไฟล์ที่ " + displayPath(memoryDir()) + " — ค้นเจอผ่าน search_documents ได้ในไม่กี่วินาที (แก้ profile ได้ตรงๆ ที่ " + displayPath(filepath.Join(config.NeyDir(), "profile.md")) + " เมื่อไหร่ก็ได้)"))
	return nil
}

// --- step 1: repo discovery ------------------------------------------------------

// stepRepos scans cfg.Context.DevRoots for git repositories and lets the
// user pick which ones to index. If DevRoots is empty (no configured
// dev_roots and no ~/workspace), it prompts for a root to scan; when the
// user provides one, devRootToPersist is returned non-empty so the caller
// writes it into config.yaml (otherwise it would be re-asked every run).
func stepRepos(ctx context.Context, cfg *config.Config) (selected []string, devRootToPersist string) {
	fmt.Println(Bold("[1/4] สแกนหา repo ในเครื่อง (git repositories)"))

	roots := cfg.Context.DevRoots
	if len(roots) == 0 {
		fmt.Println(Dim("      ยังไม่ได้ตั้งค่าโฟลเดอร์เก็บโปรเจกต์ (context.dev_roots) และไม่พบ ~/workspace"))
		line := promptLine(Cyan("      พิมพ์ path โฟลเดอร์ที่เก็บ repo ต่างๆ (เช่น ~/code, Enter=ข้าม): "))
		if line = strings.TrimSpace(line); line != "" {
			abs, err := filepath.Abs(expandTilde(line))
			if err != nil {
				fmt.Println(Yellow("      ข้าม: path ไม่ถูกต้อง"))
			} else if fi, statErr := os.Stat(abs); statErr != nil || !fi.IsDir() {
				fmt.Println(Yellow("      ข้าม: ไม่พบโฟลเดอร์นี้"))
			} else {
				roots = []string{abs}
				devRootToPersist = abs
			}
		}
	}
	if len(roots) == 0 {
		fmt.Println(Dim("      ข้ามการสแกน repo — ตั้งค่า context.dev_roots ใน config.yaml แล้วรัน `ney init` ใหม่ได้ทีหลัง"))
		return nil, devRootToPersist
	}

	cands, err := discover.Discover(ctx, discover.Options{Roots: roots}, func(scanned int) {
		fmt.Fprintf(os.Stderr, "\r      สแกนแล้ว %d repo...", scanned)
	})
	fmt.Fprint(os.Stderr, "\r\033[K")
	if err != nil {
		fmt.Println(Yellow("      สแกน repo ไม่สำเร็จ: " + err.Error()))
		return nil, devRootToPersist
	}

	if len(cands) == 0 {
		fmt.Println("      ไม่พบ git repo ใน " + strings.Join(displayPaths(roots), ", ") + " — พิมพ์ path เองได้ด้านล่าง")
	} else {
		fmt.Println("      พบ repo:")
		for i, c := range cands {
			fmt.Printf("      [%d] %-24s %-40s %s\n", i+1, c.Name, Dim(displayPath(c.Path)),
				Dim(fmt.Sprintf("%d ไฟล์ · commit ล่าสุด %s", c.DocCount, relativeAge(c.LastCommit))))
		}
	}
	fmt.Println(Dim("      เลือกที่จะให้ AI ค้นได้ เช่น 1,3 / a=ทั้งหมด / พิมพ์ path เพิ่มเองก็ได้ / Enter=ข้าม"))
	line := promptLine(Cyan("      เลือก: "))

	indices, unknown := parseSelection(line, len(cands))
	for _, idx := range indices {
		selected = append(selected, cands[idx].Path)
	}
	for _, raw := range unknown {
		abs, err := filepath.Abs(expandTilde(raw))
		if err != nil {
			fmt.Println(Yellow("      ข้าม " + raw + ": path ไม่ถูกต้อง"))
			continue
		}
		fi, err := os.Stat(abs)
		if err != nil || !fi.IsDir() {
			fmt.Println(Yellow("      ข้าม " + raw + ": ไม่พบโฟลเดอร์นี้"))
			continue
		}
		if strings.HasPrefix(abs, "/Volumes/") {
			fmt.Println(Yellow("      หมายเหตุ: " + raw + " อยู่บน external drive — ถ้าถอดไดรฟ์ ไฟล์ชุดนี้จะหายจากผลค้นจนกว่าจะเสียบและ index ใหม่"))
		}
		selected = append(selected, abs)
	}
	return selected, devRootToPersist
}

// displayPaths applies displayPath to each entry, for compact log lines.
func displayPaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = displayPath(p)
	}
	return out
}

// relativeAge renders t relative to now as a short human string ("2h ago",
// "3d ago"), mirroring internal/context's render.go formatting for the
// wizard's plain-text repo list.
func relativeAge(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d/(30*24*time.Hour)))
	default:
		return fmt.Sprintf("%dy ago", int(d/(365*24*time.Hour)))
	}
}

// --- step 2: profile bootstrap ---------------------------------------------------

// stepProfile creates ~/.ney/profile.md from the embedded template (via
// neycontext.LoadProfile) and, on a fresh profile, asks 2-3 short questions
// to seed it. If a profile already exists, this step is a no-op note — the
// file is user-owned from then on (AI edits it via update_profile).
func stepProfile() {
	fmt.Println()
	fmt.Println(Bold("[2/4] ตั้งค่า profile (~/.ney/profile.md)"))

	profilePath := filepath.Join(config.NeyDir(), "profile.md")
	if _, err := os.Stat(profilePath); err == nil {
		fmt.Println(Dim("      พบ profile.md อยู่แล้ว — ข้าม (แก้เองได้ตรงๆ หรือให้ AI แก้ผ่าน update_profile)"))
		return
	}

	if _, _, err := neycontext.LoadProfile(profilePath); err != nil {
		fmt.Println(Yellow("      สร้าง profile.md ไม่สำเร็จ: " + err.Error()))
		return
	}

	fmt.Println(Dim("      ตอบสั้นๆ ได้เลย (Enter = ข้ามข้อนั้น) — แก้เพิ่มทีหลังได้ที่ " + displayPath(profilePath)))

	if ans := promptLine(Cyan("      คุณเป็นใคร ทำงานอะไร: ")); strings.TrimSpace(ans) != "" {
		_ = neycontext.UpdateProfile(profilePath, "Name & role", ans, false)
	}
	if ans := promptLine(Cyan("      ตอนนี้กำลังโฟกัสอะไรอยู่: ")); strings.TrimSpace(ans) != "" {
		_ = neycontext.UpdateProfile(profilePath, "Current focus", ans, false)
	}
	if ans := promptLine(Cyan("      สไตล์การทำงาน/สิ่งที่อยากให้ AI รู้ไว้: ")); strings.TrimSpace(ans) != "" {
		_ = neycontext.UpdateProfile(profilePath, "Working style", ans, false)
	}

	fmt.Println(Green("      ✓ บันทึก profile.md แล้ว"))
}

// parseSelection parses the folder-picker input: comma/space-separated
// numbers select candidates (1-based), "a"/"all" selects every candidate,
// and anything else is returned as a raw path for the caller to validate.
func parseSelection(line string, n int) (indices []int, paths []string) {
	seen := make(map[int]bool)
	for _, tok := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' }) {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if strings.EqualFold(tok, "a") || strings.EqualFold(tok, "all") {
			for i := 0; i < n; i++ {
				if !seen[i] {
					seen[i] = true
					indices = append(indices, i)
				}
			}
			continue
		}
		if num, err := strconv.Atoi(tok); err == nil {
			if num >= 1 && num <= n && !seen[num-1] {
				seen[num-1] = true
				indices = append(indices, num-1)
			}
			continue
		}
		paths = append(paths, tok)
	}
	return indices, paths
}

// --- step 3: AI clients ---------------------------------------------------------

func stepClients() {
	fmt.Println()
	fmt.Println(Bold("[3/4] เชื่อมกับ AI clients"))

	neyBin, err := os.Executable()
	if err == nil {
		neyBin, _ = filepath.EvalSymlinks(neyBin)
	}
	if neyBin == "" {
		neyBin = "ney"
	}

	for _, c := range detectClients(neyBin) {
		if !c.Detected {
			fmt.Printf("      ✗ %s — ไม่พบในเครื่อง (วิธีเพิ่มเองทีหลัง:)\n", c.Name)
			fmt.Println(Dim(indentLines(c.Manual, "        ")))
			continue
		}
		ans := strings.ToLower(promptDefault(fmt.Sprintf("      ✓ พบ %s — ลงทะเบียน ney ไหม? (y/n)", c.Name), "y"))
		if !strings.HasPrefix(ans, "y") {
			fmt.Println(Dim("        ข้าม"))
			continue
		}
		if err := c.Register(); err != nil {
			fmt.Println(Yellow("        ลงทะเบียนไม่สำเร็จ: " + err.Error()))
			continue
		}
		fmt.Println(Green("        ✓ ลงทะเบียนแล้ว (restart " + c.Name + " เพื่อให้มีผล)"))
	}
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// --- step 4: optional embedder --------------------------------------------------

func stepEmbedder() *wizardChoice {
	fmt.Println()
	fmt.Println(Bold("[4/4] Semantic search (optional)"))
	fmt.Println(Dim("      ค้นเชิงความหมายด้วย embedding model ในเครื่อง (Ollama/LM Studio) หรือ cloud"))
	fmt.Println(Dim("      ไม่ตั้งก็ใช้ได้เต็มรูปแบบ — ค้นแบบ keyword ทำงานในเครื่อง 100%"))
	ans := promptLine(Cyan("      ตั้งค่าเลยไหม? [Enter=ข้าม / y=ตั้งค่า] "))
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(ans)), "y") {
		return nil
	}

	ollamaModels := listOllamaModels(defaultOllamaEndpoint)
	lmModels := listOpenAICompatModels(defaultLMStudioEndpoint)

	fmt.Printf("      [1] Ollama on this machine%s\n", detectedNote(len(ollamaModels)))
	fmt.Printf("      [2] LM Studio / OpenAI-compatible server (local or remote)%s\n", detectedNote(len(lmModels)))
	fmt.Printf("      [3] Cloud APIs (OpenAI, Gemini)\n")

	def := "3"
	switch {
	case len(ollamaModels) > 0:
		def = "1"
	case len(lmModels) > 0:
		def = "2"
	}

	var w wizardChoice
	var err error
	switch promptDefault("      Choice", def) {
	case "1":
		err = wizardOllama(&w, ollamaModels)
	case "2":
		err = wizardLMStudio(&w, lmModels)
	case "3":
		err = wizardCloud(&w)
	default:
		err = fmt.Errorf("pick 1, 2, or 3")
	}
	if err != nil {
		fmt.Println(Yellow("      ข้าม semantic search: " + err.Error()))
		return nil
	}
	return &w
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

	embedModel, err := pickModels(models)
	if err != nil {
		return err
	}
	w.embedProvider, w.embedModel, w.embedEndpoint = "ollama", embedModel, endpoint
	return nil
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

	embedModel, err := pickModels(models)
	if err != nil {
		return err
	}
	w.embedProvider, w.embedModel, w.embedEndpoint = "lmstudio", embedModel, endpoint
	return nil
}

// pickModels lists the server's models and lets the user pick an embedding
// model, preselecting a sensible default by name.
func pickModels(models []string) (embedModel string, err error) {
	fmt.Println("\nModels on the server:")
	for i, m := range models {
		fmt.Printf("  [%d] %s\n", i+1, m)
	}

	embedDef := ""
	for _, m := range models {
		if looksLikeEmbedder(m) {
			embedDef = m
			break
		}
	}

	if embedDef == "" {
		fmt.Println(Yellow("⚠ No embedding model detected — load one (e.g. nomic-embed-text / bge-m3) and pick it below."))
	}
	embedModel = promptModel("Embedding model (number or name)", models, embedDef)
	if embedModel == "" {
		return "", fmt.Errorf("an embedding model is required")
	}
	return embedModel, nil
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
	return nil
}

func warnMissingKey(envVar string) {
	if os.Getenv(envVar) == "" {
		fmt.Println(Yellow(fmt.Sprintf("⚠ %s is not set — export it before indexing.", envVar)))
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

// --- config write + final indexing ---------------------------------------------

// writeSetupConfig writes ~/.ney/config.yaml from the wizard's choices,
// backing up any existing config first. embedder may be nil (keyword-only).
// devRoot, when non-empty, is a dev root the user typed during step 1
// (because context.dev_roots was unset and ~/workspace didn't exist) — it
// is persisted as context.dev_roots so the wizard doesn't ask again and
// get_context/list_projects pick it up.
func writeSetupConfig(w *wizardChoice, devRoot string) error {
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
	fmt.Fprintf(&b, "# embedder: creates vectors for semantic search (openai | gemini | ollama | lmstudio — never claude)\n")
	if w != nil {
		fmt.Fprintf(&b, "embedder:\n  provider: %s\n  model: %s\n", w.embedProvider, w.embedModel)
		if w.embedEndpoint != "" {
			fmt.Fprintf(&b, "  endpoint: %s\n", w.embedEndpoint)
		}
	} else {
		b.WriteString("embedder:\n  provider: none            # keyword-only — run `ney init` to enable semantic search\n")
	}
	b.WriteString(`
retrieval:
  top_k: 8
  rerank: false
  mode: auto                # auto | semantic | keyword | hybrid

# indexing — extra exclude patterns on top of the built-in secret/dotfile deny
index:
  exclude: []

chunking:
  strategy: markdown        # auto | character | sentence | paragraph | markdown | tokenizer
  target_chars: 1200
  overlap_chars: 150
`)
	if devRoot != "" {
		fmt.Fprintf(&b, "\n# layered context (get_context / list_projects): where to look for git repos\ncontext:\n  dev_roots: [%q]\n", devRoot)
	}
	b.WriteString("\n# privacy — off by default\ntelemetry: false\n")

	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o600); err != nil {
		return err
	}
	fmt.Println(Green("✓ Config written to " + displayPath(cfgPath)))
	return nil
}

// indexSelected indexes each chosen folder as a workspace (Phase A). Returns
// how many were indexed. A held writer lock (e.g. an AI client's `ney mcp`
// is running) skips indexing with instructions instead of failing setup.
func indexSelected(ctx context.Context, cfg *config.Config, folders []string) int {
	if len(folders) == 0 {
		return 0
	}
	fmt.Println()

	lock, err := lockfile.Acquire(config.NeyDir())
	if err != nil {
		fmt.Println(Yellow("ข้ามการ index: " + err.Error()))
		fmt.Println(Dim("ปิด AI client ที่เปิดอยู่ (ตัวที่รัน ney mcp) แล้วรัน `ney index <path>` หรือ `ney init` ใหม่"))
		return 0
	}
	defer lock.Release()

	app, err := initAppWithOptions(cfg, false)
	if err != nil {
		printCLIError(err)
		return 0
	}
	defer app.DB.Close()
	defer app.Vectors.Close()

	ix, err := newIndexer(app, cfg)
	if err != nil {
		printCLIError(err)
		return 0
	}

	indexed := 0
	for _, folder := range folders {
		resolved := resolveRootBestEffort(folder)
		name := workspaceNameFor(app.DB, resolved)
		fmt.Printf("Indexing %s (workspace: %s)...\n", displayPath(resolved), name)
		stats, err := ix.Index(ctx, resolved, name)
		if err != nil {
			fmt.Println(Yellow("  ข้าม " + displayPath(resolved) + ": " + err.Error()))
			continue
		}
		fmt.Printf("  ✓ %d ไฟล์, %d chunks\n", stats.FilesScanned, stats.ChunksCreated)
		indexed++
	}
	return indexed
}

// workspaceNameFor picks a workspace name for root: its basename, or
// basename-parentname when that name is already bound to a different path.
func workspaceNameFor(db *store.DB, resolvedRoot string) string {
	name := filepath.Base(resolvedRoot)
	existing, err := db.GetWorkspaceByName(name)
	if err == nil && existing != nil && resolveRootBestEffort(existing.RootPath) != resolvedRoot {
		name = name + "-" + filepath.Base(filepath.Dir(resolvedRoot))
	}
	return name
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
