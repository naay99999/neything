package projects

import (
	"testing"
	"time"
)

func TestStaleness(t *testing.T) {
	now := t0
	cases := []struct {
		name    string
		updated time.Time
		commits int
		changed []string
		known   bool
		state   State
		reason  string
	}{
		{"fresh", now.Add(-24 * time.Hour), 3, nil, true, Fresh, ""},
		{"commits", now, 30, nil, true, Stale, "30 commits since it was written"},
		{"files", now, 1, []string{"go.mod"}, true, Stale, "go.mod changed"},
		{"all", now.Add(-75 * 24 * time.Hour), 42, []string{"README.md", "go.mod"}, true, Stale,
			"42 commits since it was written; README.md, go.mod changed; written 75 days ago"},
		{"unknown commit uses age only", now, 99, []string{"go.mod"}, false, Fresh, ""},
		{"unknown commit, old", now.Add(-61 * 24 * time.Hour), 0, nil, false, Stale, "written 61 days ago"},
	}
	for _, c := range cases {
		st, reason := Staleness(c.updated, c.commits, c.changed, c.known, now, 30, 60)
		if st != c.state || reason != c.reason {
			t.Errorf("%s: (%v, %q), want (%v, %q)", c.name, st, reason, c.state, c.reason)
		}
	}
}
