package loader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ocrMockRunner struct {
	t *testing.T
}

func (m *ocrMockRunner) LookPath(name string) (string, error) {
	if name == "pdftoppm" || name == "tesseract" {
		return "/usr/bin/" + name, nil
	}
	return "", os.ErrNotExist
}

func (m *ocrMockRunner) Run(name string, args ...string) (string, error) {
	if name == "pdftoppm" {
		prefix := args[len(args)-1]
		if err := os.WriteFile(prefix+"-1.png", []byte("fake"), 0600); err != nil {
			m.t.Fatal(err)
		}
		return "", nil
	}
	if name == "tesseract" {
		return "ocr extracted text from scan", nil
	}
	return "", nil
}

func TestOCRRunnerShouldOCR(t *testing.T) {
	r := NewOCRRunner(OCRConfig{Enabled: true, MinChars: 32}, nil)
	if !r.ShouldOCR("short") {
		t.Fatal("expected OCR for short text")
	}
	if r.ShouldOCR(strings.Repeat("x", 100)) {
		t.Fatal("expected skip for long text")
	}
}

func TestOCRToolsAvailable(t *testing.T) {
	ok, msg := NewOCRRunner(OCRConfig{
		Enabled:      true,
		PdftoppmCmd:  "missing-pdftoppm",
		TesseractCmd: "missing-tesseract",
	}, &ocrMockRunner{t: t}).ToolsAvailable()
	if ok {
		t.Fatal("expected missing tools")
	}
	if msg == "" {
		t.Fatal("expected message")
	}
}

func TestOCRRunnerExtractPDF(t *testing.T) {
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "scan.pdf")
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.4"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &ocrMockRunner{t: t}
	ocr := NewOCRRunner(OCRConfig{Enabled: true, MinChars: 32, Lang: "eng"}, runner)
	text, posMap, err := ocr.ExtractPDF(pdfPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "ocr extracted text") {
		t.Fatalf("unexpected text: %q", text)
	}
	if len(posMap) != 1 || posMap[0].Logical != 1 {
		t.Fatalf("unexpected pos map: %+v", posMap)
	}
}
