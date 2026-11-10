package main

import (
	"bytes"
	"context"
	"github.com/naay99999/neything/internal/skills"
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
	var want string
	for _, name := range []string{"ney-handoff", "ney-resume"} {
		for _, root := range []string{".agents", ".claude"} {
			want += "installed: " + filepath.Join("~", root, "skills", name) + "\n"
		}
	}
	if err != nil || out != want {
		t.Fatalf("err=%v out=%q want %q", err, out, want)
	}
	if out, _ := execSkills(t, "sync"); strings.Count(out, "up to date: ") != 4 {
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

// A user who installed ney's skills before ney-resume existed gets it on the
// installer's update path, but only in the folders where ney's skills live.
func TestSkillsSyncInstalledOnlyAddsNewSkillWhereOptedIn(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	if _, err := skills.Sync(filepath.Join(home, ".claude", "skills", "ney-handoff"), "0.12.0", false); err != nil {
		t.Fatal(err)
	}
	if _, err := execSkills(t, "sync", "--installed-only", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if s := skills.Status(filepath.Join(home, ".claude", "skills", "ney-resume")); s.State != skills.UpToDate {
		t.Errorf("ney-resume not added next to ney-handoff: %v", s.State)
	}
	for _, name := range []string{"ney-handoff", "ney-resume"} {
		if _, err := os.Stat(filepath.Join(home, ".agents", "skills", name)); !os.IsNotExist(err) {
			t.Errorf("%s installed in a folder the user never opted into", name)
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
