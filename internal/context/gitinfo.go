package neycontext

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RepoRemote returns the repo's origin URL normalised to host/owner/repo, or
// "" when there is no origin or git fails.
func RepoRemote(ctx context.Context, root string) string {
	out, err := gitCmd(ctx, root, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return normaliseRemote(strings.TrimSpace(string(out)))
}

// normaliseRemote maps the many spellings of one remote (scp-style, https
// with credentials, ssh://, trailing .git or /) to "host/path".
func normaliseRemote(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	scheme := false
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		scheme = true
	}
	if i := strings.LastIndex(u, "@"); i >= 0 {
		if j := strings.Index(u, "/"); j < 0 || i < j {
			u = u[i+1:]
		}
	}
	if !scheme { // scp-style host:path
		if i := strings.Index(u, ":"); i >= 0 {
			u = u[:i] + "/" + u[i+1:]
		}
	}
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	host, rest, _ := strings.Cut(u, "/")
	if rest == "" {
		return strings.ToLower(host)
	}
	return strings.ToLower(host) + "/" + rest
}

// HeadCommit returns HEAD's full SHA, or "" when git fails.
func HeadCommit(ctx context.Context, root string) string {
	out, err := gitCmd(ctx, root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// LastCommit returns HEAD's subject and commit time.
func LastCommit(ctx context.Context, root string) (subject string, at time.Time, err error) {
	out, err := gitCmd(ctx, root, "log", "-1", "--format=%ct%x00%s").Output()
	if err != nil {
		return "", time.Time{}, err
	}
	ts, subj, _ := strings.Cut(strings.TrimRight(string(out), "\n"), "\x00")
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return "", time.Time{}, err
	}
	return subj, time.Unix(sec, 0), nil
}

// CardDrift reports how far HEAD has moved since commit since: the number of
// commits, and which of files changed. known is false (with a nil error)
// when since is empty or not a commit in this repo, e.g. rebased away. Two git
// calls at most: it runs inside the session-start hook.
func CardDrift(ctx context.Context, root, since string, files []string) (commits int, changed []string, known bool, err error) {
	if since == "" {
		return 0, nil, false, nil
	}
	out, err := gitCmd(ctx, root, "rev-list", "--count", since+"..HEAD").Output()
	if err != nil {
		return 0, nil, false, nil // unknown commit (or no HEAD): only the age rule applies
	}
	if commits, err = strconv.Atoi(strings.TrimSpace(string(out))); err != nil {
		return 0, nil, true, err
	}
	args := append([]string{"diff", "--name-only", since, "HEAD", "--"}, files...)
	out, err = gitCmd(ctx, root, args...).Output()
	if err != nil {
		return commits, nil, true, err
	}
	seen := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" && !seen[l] {
			seen[l] = true
			changed = append(changed, l)
		}
	}
	sort.Strings(changed)
	return commits, changed, true, nil
}
