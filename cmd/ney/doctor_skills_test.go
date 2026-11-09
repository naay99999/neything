package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/naay99999/neything/internal/skills"
)

func withPromptInput(t *testing.T, input string, eof bool) {
	t.Helper()
	origR, origEOF := stdinPrompt, stdinEOF
	stdinPrompt, stdinEOF = bufio.NewReader(strings.NewReader(input)), eof
	t.Cleanup(func() { stdinPrompt, stdinEOF = origR, origEOF })
}

func TestStepSkillsEOFInstallsNothing(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	withPromptInput(t, "", true)
	captureStdout(t, stepSkills)
	for _, dir := range skills.Targets(home) {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%s installed on EOF", dir)
		}
	}
}

func TestStepSkillsYesInstalls(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	withPromptInput(t, "y\n", false)
	out := captureStdout(t, stepSkills)
	if !strings.Contains(out, "[3/3] Installing ney skills") {
		t.Errorf("no step title:\n%s", out)
	}
	for _, dir := range skills.Targets(home) {
		if s := skills.Status(dir); s.State != skills.UpToDate {
			t.Errorf("%s: %v", dir, s.State)
		}
	}
}

func TestStepSkillsMentionsOldHandoff(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".agents", "skills", "handoff"), 0o755); err != nil {
		t.Fatal(err)
	}
	withPromptInput(t, "n\n", false)
	out := captureStdout(t, stepSkills)
	const want = `You also have a "handoff" skill; ney-handoff replaces it. Remove ~/.agents/skills/handoff if you no longer need it.`
	if !strings.Contains(out, want) {
		t.Errorf("missing old-skill note:\n%s", out)
	}
}

func TestDoctorSkillModified(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if _, err := syncSkills(home, false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(skills.Targets(home)[1], "SKILL.md")
	if err := os.WriteFile(p, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := skillChecks(home)
	if len(res) != 2 || !res[0].OK || !res[1].OK ||
		res[1].Message != "ney-handoff skill ("+displayPath(skills.Targets(home)[1])+"): modified locally — not updated" {
		t.Fatalf("checks = %+v", res)
	}
}

func TestDoctorSkillAbsent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	res := skillChecks(home)
	if len(res) != 2 || !res[0].OK || res[0].Hint != "Run: ney skills sync" {
		t.Fatalf("checks = %+v", res)
	}
}

func TestDoctorSkillDeclinedOrEditedIsInformational(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	for _, c := range skillChecks(home) { // never installed (declined at init)
		if !c.OK {
			t.Errorf("absent skill fails doctor: %+v", c)
		}
	}
	if _, err := syncSkills(home, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skills.Targets(home)[0], "SKILL.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := skillChecks(home)[0]; !c.OK {
		t.Errorf("deliberately edited skill fails doctor: %+v", c)
	}
}
