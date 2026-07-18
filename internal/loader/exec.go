package loader

import (
	"fmt"
	"os/exec"
	"strings"
)

// commandRunner abstracts external tool invocation (pdftoppm, tesseract) so
// tests can stub it out.
type commandRunner interface {
	LookPath(name string) (string, error)
	Run(name string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (execRunner) Run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %v: %w: %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
