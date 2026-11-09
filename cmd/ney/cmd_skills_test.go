package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func execSkills(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetAllFlags(rootCmd)
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(append([]string{"skills"}, args...))
	t.Cleanup(func() { rootCmd.SetOut(nil); rootCmd.SetErr(nil); rootCmd.SetArgs(nil) })
	err := rootCmd.Execute()
	return out.String(), err
}

func TestSkillsSyncCommand(t *testing.T) {
	setHome(t, t.TempDir())
	out, err := execSkills(t, "sync")
	want := "installed: " + filepath.Join("~", ".agents", "skills", "ney-handoff") + "\ninstalled: " +
		filepath.Join("~", ".claude", "skills", "ney-handoff") + "\n"
	if err != nil || out != want {
		t.Fatalf("err=%v out=%q want %q", err, out, want)
	}
	if out, _ := execSkills(t, "sync"); strings.Count(out, "up to date: ") != 2 {
		t.Errorf("second run:\n%s", out)
	}
}

func TestSkillsSyncInstalledOnly(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	out, err := execSkills(t, "sync", "--installed-only", "--quiet")
	if err != nil || out != "" {
		t.Fatalf("err=%v out=%q", err, out)
	}
	for _, d := range []string{".agents", ".claude"} {
		if _, err := os.Stat(filepath.Join(home, d, "skills", "ney-handoff")); !os.IsNotExist(err) {
			t.Errorf("%s skill installed unasked", d)
		}
	}
}

func TestWriteHandoffDescriptionHint(t *testing.T) {
	env := newMCPTestEnv(t)
	res, err := env.cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	const hint = "If the user asked for a handoff: ask what the next session will do if they have not said, show the draft, and save only after they approve."
	for _, tool := range res.Tools {
		if tool.Name == "write_handoff" {
			if !strings.Contains(tool.Description, hint) {
				t.Errorf("description:\n%s", tool.Description)
			}
			return
		}
	}
	t.Fatal("write_handoff missing")
}
