package loader

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type GitHistoryLoader struct {
	RecentCommits int
	runner        commandRunner
}

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

func NewGitHistoryLoader(recentCommits int) *GitHistoryLoader {
	return &GitHistoryLoader{
		RecentCommits: recentCommits,
		runner:        execRunner{},
	}
}

func (g *GitHistoryLoader) Supports(_ string) bool {
	return false
}

func (g *GitHistoryLoader) Load(_ context.Context, _ string) ([]Document, error) {
	return nil, fmt.Errorf("git loader is invoked directly, not via file path")
}

func (g *GitHistoryLoader) LoadRepo(ctx context.Context, repoPath string) ([]Document, error) {
	if g.RecentCommits <= 0 {
		return nil, nil
	}
	if _, err := g.runner.LookPath("git"); err != nil {
		return nil, nil
	}
	if _, err := g.runner.Run("git", "-C", repoPath, "rev-parse", "--git-dir"); err != nil {
		return nil, nil
	}

	logOut, err := g.runner.Run("git", "-C", repoPath,
		"log", "-n", strconv.Itoa(g.RecentCommits),
		`--pretty=format:%H%x09%h%x09%an%x09%ad%x09%s`,
		"--date=short",
	)
	if err != nil {
		return nil, err
	}

	var docs []Document
	for _, line := range strings.Split(strings.TrimSpace(logOut), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 5)
		if len(parts) < 5 {
			continue
		}
		fullHash, shortHash, author, date, subject := parts[0], parts[1], parts[2], parts[3], parts[4]

		statOut, err := g.runner.Run("git", "-C", repoPath, "show", "--name-status", "--pretty=format:", fullHash)
		if err != nil {
			statOut = ""
		}
		changed := strings.TrimSpace(statOut)
		content := fmt.Sprintf("Commit: %s\nAuthor: %s\nDate: %s\n\n%s", shortHash, author, date, subject)
		if changed != "" {
			content += "\n\nChanged files:\n" + changed
		}

		docs = append(docs, Document{
			Path:     fmt.Sprintf("git:commit:%s", shortHash),
			Type:     "git",
			Content:  content,
			Hash:     fullHash,
			Metadata: map[string]string{"author": author, "date": date, "short_hash": shortHash},
		})
	}
	return docs, nil
}

func GitAvailable(runner commandRunner) bool {
	if runner == nil {
		runner = execRunner{}
	}
	_, err := runner.LookPath("git")
	return err == nil
}
