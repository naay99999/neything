package main

import (
	"fmt"
	"strings"

	"github.com/naay99999/neything/internal/config"
	"github.com/naay99999/neything/internal/store"
)

var neyLogo = []string{
	"███╗   ██╗ ███████╗ ██╗   ██╗",
	"████╗  ██║ ██╔════╝ ╚██╗ ██╔╝",
	"██╔██╗ ██║ █████╗    ╚████╔╝ ",
	"██║╚██╗██║ ██╔══╝     ╚██╔╝  ",
	"██║ ╚████║ ███████╗    ██║   ",
	"╚═╝  ╚═══╝ ╚══════╝    ╚═╝   ",
}

// cyan → green, one 256-color shade per logo row
var logoGradient = []int{51, 50, 49, 48, 47, 46}

type bannerState struct {
	chunkCount int
	configOK   bool
}

// printBanner renders the startup screen: logo on the left, live status
// (models, index size, data dir) on the right. Falls back to a plain
// one-liner when stdout isn't an interactive terminal.
func printBanner() bannerState {
	info, state := bannerInfo()

	if !colorEnabled {
		fmt.Printf("ney %s — local-first AI knowledge engine\n", Version)
		return state
	}

	fmt.Println()
	for i, row := range neyLogo {
		fmt.Printf(" \033[38;5;%dm%s\033[0m", logoGradient[i], row)
		if i < len(info) && info[i] != "" {
			fmt.Print("   ", info[i])
		}
		fmt.Println()
	}
	fmt.Println()
	fmt.Println(Dim(" Ask a question directly, or type a command — ? for help · exit to leave"))
	fmt.Println()
	return state
}

func bannerInfo() ([]string, bannerState) {
	lines := make([]string, len(neyLogo))
	state := bannerState{}

	lines[0] = Bold("ney "+Version) + Dim(" — local-first AI knowledge engine")

	cfg, err := config.Load()
	if err != nil {
		lines[2] = Yellow("config error — fix with: init (or config edit)")
		lines[3] = Dim(strings.SplitN(err.Error(), "\n", 2)[0])
		return lines, state
	}
	state.configOK = true

	label := func(s string) string { return Dim(fmt.Sprintf("%-6s", s)) }
	lines[2] = label("chat") + " " + cfg.Chat.Model + Dim(" ("+cfg.Chat.Provider+")")
	lines[3] = label("embed") + " " + cfg.Embedder.Model + Dim(" ("+cfg.Embedder.Provider+")")
	lines[5] = label("data") + " " + displayPath(config.NeyDir())

	if db, dbErr := store.Open(config.DBPath()); dbErr == nil {
		if s, sErr := db.Stats(); sErr == nil && s != nil {
			state.chunkCount = s.ChunkCount
			if s.ChunkCount > 0 {
				lines[4] = label("index") + fmt.Sprintf(" %d workspace(s) · %d files · %d chunks", s.WorkspaceCount, s.DocumentCount, s.ChunkCount)
			} else {
				lines[4] = label("index") + " " + Yellow("empty")
			}
		}
		db.Close()
	}
	return lines, state
}

// runOnboarding shows a numbered getting-started menu when the index is
// empty, instead of dropping new users at a silent prompt.
func runOnboarding(state bannerState) {
	def := "2"
	if !state.configOK {
		def = "1"
	}
	fmt.Println(Bold(" Nothing indexed yet — let's get you started:"))
	fmt.Println("   [1] Set up providers " + Dim("(detects Ollama / LM Studio, writes config)"))
	fmt.Println("   [2] Index a folder " + Dim("(make your notes searchable)"))
	fmt.Println("   [3] Skip for now")
	switch promptDefault(" Choice", def) {
	case "1":
		if err := dispatchLine("init"); err != nil {
			printCLIError(err)
		}
	case "2":
		if err := dispatchLine("index"); err != nil {
			printCLIError(err)
		}
	}
	fmt.Println()
}
