package neycontext

import (
	"context"
	"sync"
	"time"
)

// defaultScanCacheTTL bounds how long a ScanCache serves a stale scan before
// re-forking git. get_context and list_projects each independently rescan
// the same dev_roots (and, within a scan, fork git 3x per repo) — a client
// that calls get_context and then list_projects moments later (a common
// session-start pattern) would otherwise pay for two full scans instead of
// one.
const defaultScanCacheTTL = 45 * time.Second

// ScanCache memoizes ScanRepos for a short TTL, keyed by the roots argument,
// so back-to-back calls (e.g. get_context immediately followed by
// list_projects) reuse one scan instead of re-forking git 3x per repo for
// each call. Safe for concurrent use.
//
// Only a long-running process (ney mcp) should hold a ScanCache. CLI
// one-shot commands must call ScanRepos directly — they run once and exit,
// so there's never a second call to amortize, and a cache would only risk
// serving a stale scan for a single-shot answer.
type ScanCache struct {
	// TTL overrides defaultScanCacheTTL when > 0.
	TTL time.Duration

	mu        sync.Mutex
	roots     []string
	projects  []Project
	fetchedAt time.Time
}

// Get returns a cached ScanRepos(ctx, roots) result if one was fetched for
// the identical roots slice within the TTL, otherwise performs a fresh scan
// and caches it. The returned slice is a defensive copy — callers may
// mutate it (e.g. to set Indexed) without corrupting the cache.
func (c *ScanCache) Get(ctx context.Context, roots []string) []Project {
	ttl := c.TTL
	if ttl <= 0 {
		ttl = defaultScanCacheTTL
	}

	c.mu.Lock()
	if c.projects != nil && time.Since(c.fetchedAt) < ttl && sameRoots(c.roots, roots) {
		cached := cloneProjects(c.projects)
		c.mu.Unlock()
		return cached
	}
	c.mu.Unlock()

	projects := ScanRepos(ctx, roots)

	c.mu.Lock()
	c.roots = append([]string(nil), roots...)
	c.projects = cloneProjects(projects)
	c.fetchedAt = time.Now()
	c.mu.Unlock()

	return projects
}

func cloneProjects(in []Project) []Project {
	if in == nil {
		return nil
	}
	return append([]Project(nil), in...)
}

func sameRoots(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
