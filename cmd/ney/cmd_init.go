package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
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

// isInteractive is a var so tests can drive the wizard's TTY guard.
var isInteractive = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) }

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
	// Every step of the wizard is a prompt, so there is no useful
	// non-interactive behaviour. Refusing outright also means an EOF stdin
	// can never accept a default that writes to an AI client's config.
	if !isInteractive() {
		return fmt.Errorf("`ney init` is interactive — run it from a terminal (stdin is not a TTY).\n" +
			"To index a folder non-interactively, use: ney index <path>")
	}

	fmt.Println(Bold("Ney setup — let your AI know your projects and search your local documents, safely"))
	fmt.Println(Dim("Enter = accept the default, Ctrl+C = cancel (you can rerun `ney init` any time)"))
	fmt.Println()

	// Loaded early because step 1 needs cfg.Context.DevRoots to know where to
	// scan for repos.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	selected, devRoot := stepRepos(ctx, cfg)

	stepProfile()

	stepClients()

	// The only config.yaml write the wizard makes. Non-fatal: the user has
	// already picked their repos, and a failed config write shouldn't throw
	// that away.
	if devRoot != "" {
		if err := config.SetDevRoots([]string{devRoot}); err != nil {
			fmt.Println(Yellow("  Could not save that folder to config.yaml: " + err.Error()))
		} else if cfg, err = config.Load(); err != nil {
			return err
		}
	}

	indexed := indexSelected(ctx, cfg, selected)

	if db, err := store.Open(config.DBPath()); err == nil {
		_ = db.SetMeta("setup_completed", time.Now().Format(time.RFC3339))
		db.Close()
	}

	fmt.Println()
	fmt.Println(Bold("✓ Setup complete"))
	if indexed > 0 {
		fmt.Printf("  Indexed %d folder(s) — open Claude or Codex and ask about your files, or ask what you were working on.\n", indexed)
	}
	fmt.Println(Dim("  Add or change folders later: rerun `ney init`, or just tell your AI \"index this folder\"."))
	fmt.Println()
	fmt.Println(Dim("  Memory: tell your AI \"remember that ...\" and it saves a file under " + displayPath(memoryDir()) +
		" — searchable via search_documents within seconds. Edit your profile any time at " +
		displayPath(filepath.Join(config.NeyDir(), "profile.md")) + "."))
	return nil
}

// --- step 1: repo discovery ------------------------------------------------------

// stepRepos scans cfg.Context.DevRoots for git repositories and lets the
// user pick which ones to index. If DevRoots is empty (no configured
// dev_roots and no ~/workspace), it prompts for a root to scan; when the
// user provides one, devRootToPersist is returned non-empty so the caller
// writes it into config.yaml (otherwise it would be re-asked every run).
func stepRepos(ctx context.Context, cfg *config.Config) (selected []string, devRootToPersist string) {
	fmt.Println(Bold("[1/3] Scanning for git repositories on this machine"))

	roots := cfg.Context.DevRoots
	if len(roots) == 0 {
		fmt.Println(Dim("      No project folder configured (context.dev_roots), and no ~/workspace found."))
		line := promptLine(Cyan("      Path to the folder holding your repos (e.g. ~/code, Enter to skip): "))
		if line = strings.TrimSpace(line); line != "" {
			abs, err := filepath.Abs(expandTilde(line))
			if err != nil {
				fmt.Println(Yellow("      Skipped: not a valid path"))
			} else if fi, statErr := os.Stat(abs); statErr != nil || !fi.IsDir() {
				fmt.Println(Yellow("      Skipped: no such folder"))
			} else {
				roots = []string{abs}
				devRootToPersist = abs
			}
		}
	}
	if len(roots) == 0 {
		fmt.Println(Dim("      Skipping the repo scan — set context.dev_roots in config.yaml and rerun `ney init` later."))
		return nil, devRootToPersist
	}

	cands, err := discover.Discover(ctx, discover.Options{Roots: roots}, func(scanned int) {
		fmt.Fprintf(os.Stderr, "\r      scanned %d repo(s)...", scanned)
	})
	fmt.Fprint(os.Stderr, "\r\033[K")
	if err != nil {
		fmt.Println(Yellow("      Repo scan failed: " + err.Error()))
		return nil, devRootToPersist
	}

	if len(cands) == 0 {
		fmt.Println("      No git repos found in " + strings.Join(displayPaths(roots), ", ") + " — you can type a path below.")
	} else {
		fmt.Println("      Found:")
		for i, c := range cands {
			fmt.Printf("      [%d] %-24s %-40s %s\n", i+1, c.Name, Dim(displayPath(c.Path)),
				Dim(fmt.Sprintf("%d files · last commit %s", c.DocCount, relativeAge(c.LastCommit))))
		}
	}
	fmt.Println(Dim("      Pick what your AI may search, e.g. 1,3 / a=all / type a path to add one / Enter=skip"))
	line := promptLine(Cyan("      Select: "))

	indices, unknown := parseSelection(line, len(cands))
	for _, idx := range indices {
		selected = append(selected, cands[idx].Path)
	}
	for _, raw := range unknown {
		abs, err := filepath.Abs(expandTilde(raw))
		if err != nil {
			fmt.Println(Yellow("      Skipped " + raw + ": not a valid path"))
			continue
		}
		fi, err := os.Stat(abs)
		if err != nil || !fi.IsDir() {
			fmt.Println(Yellow("      Skipped " + raw + ": no such folder"))
			continue
		}
		if strings.HasPrefix(abs, "/Volumes/") {
			fmt.Println(Yellow("      Note: " + raw + " is on an external drive — unplug it and these files drop out of search results until it's reconnected and re-indexed."))
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
	fmt.Println(Bold("[2/3] Setting up your profile (~/.ney/profile.md)"))

	profilePath := filepath.Join(config.NeyDir(), "profile.md")
	if _, err := os.Stat(profilePath); err == nil {
		fmt.Println(Dim("      profile.md already exists — skipping (edit it directly, or let your AI update it via update_profile)"))
		return
	}

	if _, _, err := neycontext.LoadProfile(profilePath); err != nil {
		fmt.Println(Yellow("      Could not create profile.md: " + err.Error()))
		return
	}

	fmt.Println(Dim("      Short answers are fine (Enter skips a question) — edit later at " + displayPath(profilePath)))

	if ans := promptLine(Cyan("      Who are you, and what do you do? ")); strings.TrimSpace(ans) != "" {
		_ = neycontext.UpdateProfile(profilePath, "Name & role", ans, false)
	}
	if ans := promptLine(Cyan("      What are you focused on right now? ")); strings.TrimSpace(ans) != "" {
		_ = neycontext.UpdateProfile(profilePath, "Current focus", ans, false)
	}
	if ans := promptLine(Cyan("      Working style / anything your AI should know? ")); strings.TrimSpace(ans) != "" {
		_ = neycontext.UpdateProfile(profilePath, "Working style", ans, false)
	}

	fmt.Println(Green("      ✓ Saved profile.md"))
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
	fmt.Println(Bold("[3/3] Connecting your AI clients"))

	neyBin := resolveNeyBinary()

	for _, c := range detectClients(neyBin) {
		if !c.Detected {
			fmt.Printf("      ✗ %s — not found (to add it yourself later:)\n", c.Name)
			fmt.Println(Dim(indentLines(c.Manual, "        ")))
			continue
		}
		ans := strings.ToLower(promptDefault(fmt.Sprintf("      ✓ Found %s — register ney with it? (y/n)", c.Name), "y"))
		if !strings.HasPrefix(ans, "y") {
			fmt.Println(Dim("        Skipped"))
			continue
		}
		if err := c.Register(); err != nil {
			fmt.Println(Yellow("        Registration failed: " + err.Error()))
			continue
		}
		fmt.Println(Green("        ✓ Registered (restart " + c.Name + " to apply)"))
	}
}

func indentLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = prefix + lines[i]
	}
	return strings.Join(lines, "\n")
}

// --- prompt helpers -------------------------------------------------------------

// promptDefault asks question, falling back to def when the user just presses
// Enter. At EOF it returns "" rather than def: an exhausted stdin is not
// consent, and this function gates writes into AI-client config files.
func promptDefault(question, def string) string {
	label := question
	if def != "" {
		label += " [" + def + "]"
	}
	ans := promptLine(Cyan(label + ": "))
	if stdinEOF {
		return ""
	}
	if ans == "" {
		return def
	}
	return ans
}

// --- config write + final indexing ---------------------------------------------

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

	app, err := initApp(cfg)
	if err != nil {
		printCLIError(err)
		return 0
	}
	defer app.DB.Close()

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
