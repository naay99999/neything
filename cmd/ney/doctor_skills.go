package main

import (
	"fmt"

	"github.com/naay99999/neything/internal/skills"
)

// skillChecks reports the ney-handoff skill's state in each target directory.
func skillChecks(home string) []checkResult {
	var res []checkResult
	for _, dir := range skills.Targets(home) {
		st := skills.Status(dir).State
		c := checkResult{
			Check:   "skill_" + skills.Name,
			OK:      st == skills.UpToDate,
			Message: fmt.Sprintf("%s skill (%s): %s", skills.Name, displayPath(dir), st),
		}
		if st == skills.Absent || st == skills.Outdated {
			c.Hint = "Run: ney skills sync"
		}
		res = append(res, c)
	}
	return res
}
