package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type OCRConfig struct {
	Enabled      bool
	Lang         string
	TesseractCmd string
	PdftoppmCmd  string
	MinChars     int
}

type OCRRunner struct {
	cfg    OCRConfig
	runner commandRunner
}

func NewOCRRunner(cfg OCRConfig, runner commandRunner) *OCRRunner {
	if runner == nil {
		runner = execRunner{}
	}
	if cfg.Lang == "" {
		cfg.Lang = "eng"
	}
	if cfg.TesseractCmd == "" {
		cfg.TesseractCmd = "tesseract"
	}
	if cfg.PdftoppmCmd == "" {
		cfg.PdftoppmCmd = "pdftoppm"
	}
	if cfg.MinChars == 0 {
		cfg.MinChars = 32
	}
	return &OCRRunner{cfg: cfg, runner: runner}
}

func (o *OCRRunner) Enabled() bool {
	return o != nil && o.cfg.Enabled
}

func (o *OCRRunner) ToolsAvailable() (bool, string) {
	if o == nil || !o.cfg.Enabled {
		return true, ""
	}
	if _, err := o.runner.LookPath(o.cfg.PdftoppmCmd); err != nil {
		return false, fmt.Sprintf("%s not found on PATH", o.cfg.PdftoppmCmd)
	}
	if _, err := o.runner.LookPath(o.cfg.TesseractCmd); err != nil {
		return false, fmt.Sprintf("%s not found on PATH", o.cfg.TesseractCmd)
	}
	return true, ""
}

func (o *OCRRunner) ShouldOCR(text string) bool {
	if o == nil || !o.cfg.Enabled {
		return false
	}
	return len(strings.TrimSpace(text)) < o.cfg.MinChars
}

func (o *OCRRunner) ExtractPDF(pdfPath string) (string, []PositionEntry, error) {
	ok, msg := o.ToolsAvailable()
	if !ok {
		return "", nil, fmt.Errorf("ocr tools unavailable: %s", msg)
	}

	tmpDir, err := os.MkdirTemp("", "ney-ocr-*")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(tmpDir)

	prefix := filepath.Join(tmpDir, "page")
	if _, err := o.runner.Run(o.cfg.PdftoppmCmd, "-png", "-r", "300", pdfPath, prefix); err != nil {
		return "", nil, fmt.Errorf("pdftoppm: %w", err)
	}

	matches, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return "", nil, err
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		matches, _ = filepath.Glob(prefix + "*.png")
		sort.Strings(matches)
	}

	var sb strings.Builder
	var posMap []PositionEntry
	for i, img := range matches {
		text, err := o.runner.Run(o.cfg.TesseractCmd, img, "stdout", "-l", o.cfg.Lang)
		if err != nil {
			continue
		}
		normalized := strings.Join(strings.Fields(text), " ")
		if normalized == "" {
			continue
		}
		posMap = append(posMap, PositionEntry{ByteOffset: sb.Len(), Logical: i + 1})
		sb.WriteString(normalized)
		sb.WriteString("\n")
	}
	return sb.String(), posMap, nil
}

func OCRToolsAvailable(cfg OCRConfig) (bool, string) {
	return NewOCRRunner(cfg, nil).ToolsAvailable()
}
