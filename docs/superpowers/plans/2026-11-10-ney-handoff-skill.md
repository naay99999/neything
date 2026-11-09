# ney-handoff Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** ney ships and installs a model-invocable `ney-handoff` skill, and the session-start brief shows a fresh handoff note intact (fenced, most important sections first) instead of flattening it.

**Architecture:** A new `internal/skills` package embeds the skill files and syncs them into agent skill directories under an ownership marker. `internal/handoff` changes how a note renders in the inject and full briefs. `cmd/ney` adds `ney skills sync`, an init step, doctor lines and the `write_handoff` fallback hint; the installers sync on update.

**Tech Stack:** Go (`embed`, `crypto/sha256`, `encoding/json`), cobra, POSIX sh, PowerShell.

**Spec:** `docs/superpowers/specs/2026-11-10-ney-handoff-skill-design.md`

## Global Constraints

- Skill name `ney-handoff`; `SKILL.md` and `agents/openai.yaml` text is binding copy from the spec's "The skill" section — copy it verbatim.
- Targets: `<home>/.agents/skills/ney-handoff/` and `<home>/.claude/skills/ney-handoff/`; real files, no symlinks; dirs 0755, files 0644; writes via `neycontext.WriteFileAtomic`.
- Marker file `.ney-managed`: `{"skill":"ney-handoff","version":"<Version>","files":{"SKILL.md":"<sha256 hex>","agents/openai.yaml":"<sha256 hex>"}}`.
- ney never deletes anything in a skill directory and never touches a directory without its marker or with a locally modified file.
- Inject brief: note body budget 1800 bytes, cut at the last line break that fits, then `… (full note: get_handoff)`; whole output ≤ 3072 bytes incl. projects section (≤ 600). Full brief: note clipped at 8000 runes.
- With a note, the inject brief omits `Goal`, `Latest ask`, `Ended with`, `Files touched`, `Failed near the end`; keeps `Last`, `Now`, `Other recent`.
- Note header line (exact): `Handoff note from <agent> · <rel> (full: get_handoff):`
- `write_handoff` description gains (exact): `If the user asked for a handoff: ask what the next session will do if they have not said, show the draft, and save only after they approve.`
- `ney mcp` and `ney hook` never touch skill files.
- Init step titles: `[1/3] Setting up your profile (~/.ney/profile.md)`, `[2/3] Connecting your AI clients`, `[3/3] Installing ney skills`.
- Commit after each task (Conventional Commits) on the feature branch.

## Review Focus

1. A user who edited their installed `SKILL.md` then updates ney: the edit survives and doctor says "modified locally". → Task 1 `TestSyncSkipsModified`, Task 4 `TestDoctorSkillModified`.
2. A pre-existing `~/.claude/skills/ney-handoff` the user made by hand (no marker): untouched. → Task 1 `TestSyncSkipsUnmanaged`.
3. A note that itself contains ```` ``` ```` fences and `## ` headings: it stays inside the brief's fence and cannot add brief sections. → Task 2 `TestInjectNoteFenceHoldsBackticks`.
4. A note with one very long line (no line breaks) over budget: the cut still lands ≤ 1800 bytes without splitting a UTF-8 rune. → Task 2 `TestInjectNoteLongSingleLine`.
5. `install.sh` update on a machine where skills were never installed: nothing is installed. → Task 5 sandbox test.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/skills/skills.go` (create) | embed FS, `Targets`, `Sync`, `Status`, `Result` |
| `internal/skills/ney-handoff/SKILL.md`, `agents/openai.yaml` (create) | binding copy |
| `internal/handoff/render.go` (modify) | note rendering, inject + full |
| `cmd/ney/cmd_skills.go` (create) | `ney skills sync` |
| `cmd/ney/cmd_init.go` (modify) | step 3, renumbering |
| `cmd/ney/doctor_skills.go` (create) | doctor lines |
| `cmd/ney/mcp_handoff.go` (modify) | fallback hint |
| `scripts/install.sh`, `scripts/install.ps1`, `.github/workflows/ci.yml` (modify) | sync on update + CI assertion |
| `README.md`, `CLAUDE.md`, `AGENTS.md` (modify) | docs |

---

### Task 1: `internal/skills` — embed and sync

**Files:** Create `internal/skills/skills.go`, `internal/skills/skills_test.go`, `internal/skills/ney-handoff/SKILL.md`, `internal/skills/ney-handoff/agents/openai.yaml`

**Interfaces:**
- Produces:
  ```go
  const Name = "ney-handoff"
  type State string
  const (
      Installed  State = "installed"
      Updated    State = "updated"
      UpToDate   State = "up to date"
      Outdated   State = "outdated"               // Status only: managed, unmodified, differs from embedded
      Modified   State = "modified locally — not updated"
      Unmanaged  State = "not managed by ney — skipped"
      Absent     State = "not installed"           // Status only
  )
  type Result struct { Dir string; State State }
  func Targets(home string) []string                      // [<home>/.agents/skills/ney-handoff, <home>/.claude/skills/ney-handoff]
  func Sync(dir, version string, installedOnly bool) (Result, error) // installedOnly: Absent dir → Result{State: Absent}, nothing written
  func Status(dir string) Result                          // read-only; never errors (unreadable → Unmanaged)
  func Files() map[string][]byte                          // embedded relative path → content
  ```
- Consumes: `neycontext.WriteFileAtomic`.

- [ ] **Step 1: Write failing tests** (temp dirs as `dir`):
  - `TestEmbeddedSkill`: `Files()` has exactly `SKILL.md` and `agents/openai.yaml`; `SKILL.md` starts with `"---\nname: ney-handoff\n"`; `openai.yaml` contains `allow_implicit_invocation: true`.
  - `TestSyncInstallsAndIsIdempotent`: first `Sync` → `Installed`, both files equal embedded, `.ney-managed` JSON has `skill:"ney-handoff"`, `version:"1.2.3"`, two sha256 hex values; second `Sync` → `UpToDate`; file mode 0644 (skip on Windows).
  - `TestSyncUpdatesUnmodified`: install, then overwrite `SKILL.md` *and* rewrite the marker hash to match the new bytes (simulating an older ney version) → `Sync` restores embedded content, `Updated`.
  - `TestSyncSkipsModified`: install, append one byte to `SKILL.md` → `Sync` = `Modified`, file unchanged; `Status` = `Modified`.
  - `TestSyncSkipsUnmanaged`: dir exists with a user `SKILL.md`, no marker → `Unmanaged`, untouched.
  - `TestSyncInstalledOnly`: absent dir + `installedOnly=true` → `Absent`, dir still absent.
  - `TestStatusOutdated`: marker+files from an "old" content (as in `TestSyncUpdatesUnmodified` before syncing) → `Outdated`; fresh install → `UpToDate`; absent → `Absent`.
  - `TestTargets`: `Targets("/h")` equals `[filepath.Join("/h",".agents","skills","ney-handoff"), filepath.Join("/h",".claude","skills","ney-handoff")]`.
- [ ] **Step 2:** `go test ./internal/skills` → FAIL (package missing).
- [ ] **Step 3:** Create the two skill files with the spec's text verbatim; implement with `//go:embed ney-handoff` on an `embed.FS` (note: `embed` skips files starting with `.`; neither file does). `Modified` means: any file listed in the marker whose current sha256 ≠ marker hash (missing file counts as modified).
- [ ] **Step 4:** `go test ./internal/skills` → PASS.
- [ ] **Step 5:** Commit `feat(skills): embed ney-handoff and sync it under an ownership marker`.

### Task 2: Note rendering in the briefs

**Files:** Modify `internal/handoff/render.go`; Test `internal/handoff/note_render_test.go` (create); update `internal/handoff/testdata` goldens only if a note appears in them.

**Interfaces:**
- Produces: `const noteInjectBudget = 1800`, `const fullNoteMax = 8000` (was 4000); `func clipLines(s string, max int) (string, bool)` — longest prefix ≤ max bytes ending at a `\n` (or, if the first line alone exceeds max, at a rune boundary), reports whether it cut.
- Inject with `b.Note != nil`: after `Last:` (when `Primary` exists) write `Handoff note from <inline(agent)> · <rel> (full: get_handoff):\n` + `fenced(body)` where body = `clipLines(note, 1800)` + (if cut) `"\n… (full note: get_handoff)"`; skip Goal / Latest ask / Ended with / Files touched / Failed near the end; then `Now:`, `Other recent` as before. The existing step ladder still runs; when it reaches the last step and the output is still over budget, shrink the note budget by 300-byte steps (min 300) before falling back to `trimBytes`.
- Full: unchanged structure; `clipRunes(n.Body, fullNoteMax)`.

- [ ] **Step 1: Write failing tests:**
  - `TestInjectStructuredNote`: Primary session + a note with body `"# Handoff → codex: release\n## Next session will\nship v0.12\n## Next steps\n1. tag\n"` → output contains `"Handoff note from claude-code · "` and `"(full: get_handoff):\n```\n# Handoff → codex: release\n## Next session will\nship v0.12\n"` (fence of 3 backticks), contains `"Last: "` and `"Now: "`, and does NOT contain `"Goal:"`, `"Ended with:"`, `"Files touched:"`.
  - `TestInjectNoteBudget`: note of 40 lines × 80 bytes → the fenced body is ≤ 1800 bytes + marker, ends with `"… (full note: get_handoff)"`, and the cut is at a line end (the line before the marker is one of the original full lines).
  - `TestInjectNoteLongSingleLine`: one line of 5000 × `"ก"` → body ≤ 1800 bytes, valid UTF-8 (`utf8.ValidString`).
  - `TestInjectNoteFenceHoldsBackticks`: body contains "```go\nx\n```" and "\n## Forged\n" → output's fence is ≥ 4 backticks and the text `"\n## Forged"` appears only between the opening and closing fence lines.
  - `TestInjectNoteWithProjectsUnderCap`: max-size note (8000 chars) + huge Primary + 60 project names → `len(out) ≤ 3072`, note header present, `## ney — projects` present.
  - `TestFullNoteKeeps8000`: note of 7900 runes → `RenderFull` contains all of it.
  - `TestInjectWithoutNoteUnchanged`: existing goldens still pass (no change needed to `testdata`).
- [ ] **Step 2:** `go test ./internal/handoff` → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test ./internal/handoff ./cmd/ney` → PASS.
- [ ] **Step 5:** Commit `feat(handoff): show a handoff note intact in the brief`.

### Task 3: `ney skills sync` and the `write_handoff` hint

**Files:** Create `cmd/ney/cmd_skills.go`, `cmd/ney/cmd_skills_test.go`; Modify `cmd/ney/mcp_handoff.go`, `cmd/ney/mcp_handoff_test.go`

**Interfaces:**
- Consumes: Task 1.
- Produces: command `ney skills sync [--quiet] [--installed-only]`; helper `func syncSkills(home string, installedOnly bool) ([]skills.Result, error)` (home from `os.UserHomeDir`, version `Version`). Output: one line per target, `"<State>: <dir with ~>"`; `--quiet` prints nothing unless an error (then exit non-zero with the error on stderr).
- [ ] **Step 1: Write failing tests:** `TestSkillsSyncCommand` (fresh `setHome` → two lines starting `installed: ` with `~/.agents/skills/ney-handoff` and `~/.claude/skills/ney-handoff`; second run → `up to date: `); `TestSkillsSyncInstalledOnly` (fresh home, `--installed-only --quiet` → no output, neither dir exists); `TestWriteHandoffDescriptionHint` (list tools via the in-memory MCP env; `write_handoff` description contains the exact hint from Global Constraints).
- [ ] **Step 2:** `go test ./cmd/ney -run 'SkillsSync|WriteHandoffDescription'` → FAIL.
- [ ] **Step 3:** Implement; append the hint sentence (with a leading space) to the existing description string.
- [ ] **Step 4:** `go test ./cmd/ney` → PASS.
- [ ] **Step 5:** Commit `feat(cli): ney skills sync; write_handoff hints the handoff workflow`.

### Task 4: Init step and doctor lines

**Files:** Modify `cmd/ney/cmd_init.go`, `cmd/ney/cmd_doctor.go`; Create `cmd/ney/doctor_skills.go`; Tests `cmd/ney/cmd_init_test.go`, `cmd/ney/doctor_skills_test.go`

**Interfaces:**
- Consumes: Tasks 1, 3.
- Produces: `func stepSkills()` — prints `[3/3] Installing ney skills`, lists `Targets` (with `~`), `promptDefault("        Apply? (y/n)", "y")`; on "y" calls `syncSkills(home, false)` and prints each result; else `Skipped`. If `<home>/.agents/skills/handoff` or `<home>/.claude/skills/handoff` exists, prints exactly: `You also have a "handoff" skill; ney-handoff replaces it. Remove ~/.agents/skills/handoff if you no longer need it.` `func skillChecks(home string) []checkResult` — per target: `Status`; OK for `UpToDate`; message `"ney-handoff skill (<dir with ~>): <state>"`; hint `"Run: ney skills sync"` for `Absent`/`Outdated`.
- [ ] **Step 1: Write failing tests:** `TestStepSkillsEOFInstallsNothing` (`stdinEOF = true` with cleanup; `stepSkills()` → neither dir exists); `TestStepSkillsYesInstalls` (swap `stdinPrompt = bufio.NewReader(strings.NewReader("y\n"))` and reset `stdinEOF`, restoring both in `t.Cleanup`; both dirs installed); `TestStepSkillsMentionsOldHandoff` (create `~/.agents/skills/handoff`, capture stdout → contains the exact sentence); `TestDoctorSkillModified` (install, edit `SKILL.md` → a check whose message ends `modified locally — not updated`, `OK=false`); `TestDoctorSkillAbsent` (→ hint `Run: ney skills sync`). Update the step-title assertions if any test checks `[1/2]`/`[2/2]`.
- [ ] **Step 2:** `go test ./cmd/ney -run 'StepSkills|DoctorSkill'` → FAIL.
- [ ] **Step 3:** Implement; call `stepSkills()` after `stepClients()` in `runSetupWizard`; rename the two existing titles to `[1/3]`/`[2/3]`; append `skillChecks(home)` in `runDoctor` after the project checks.
- [ ] **Step 4:** `go test ./cmd/ney` → PASS.
- [ ] **Step 5:** Commit `feat(init): install ney skills; doctor reports skill state`.

### Task 5: Installers sync on update; docs

**Files:** Modify `scripts/install.sh`, `scripts/install.ps1`, `.github/workflows/ci.yml`, `README.md`, `CLAUDE.md`, `AGENTS.md`

**Interfaces:**
- Consumes: Task 3's `ney skills sync --installed-only --quiet`.
- [ ] **Step 1: Write the check** (scratchpad, not committed): build `ney` from the tree, set a temp `HOME`, and run `ney skills sync --installed-only --quiet` exactly as the installer line will: (a) with nothing installed, `$HOME/.agents/skills/ney-handoff` stays absent; (b) after `ney skills sync` and an "old" marker (hash rewritten as in Task 1), the call restores the embedded content.
- [ ] **Step 2:** Run it → (a) and (b) pass; `grep -c "skills sync" scripts/install.sh scripts/install.ps1` → 0 each (installers not wired yet: RED).
- [ ] **Step 3:** `install.sh`: just before `say "Updated. ..."`, add `"$TARGET" skills sync --installed-only --quiet || true`. `install.ps1`: before `Write-Host "Updated. ..."`, add `& $target skills sync --installed-only --quiet 2>$null | Out-Null`. `ci.yml` `installer-windows`: after the update run, assert `-not (Test-Path "$env:USERPROFILE\.agents\skills\ney-handoff")`.
- [ ] **Step 4:** `grep -c "skills sync"` → 1 in each installer; `sh -n scripts/install.sh && bash -n scripts/install.sh` clean.
- [ ] **Step 5:** Docs:
  - README: new section "Handoff skill" under `ney handoff` — trigger phrases, the ask-then-draft-then-save flow, where it is installed, `ney skills sync`, editing it keeps your edits; Uninstall gains removing the two `ney-handoff` dirs; Setup lists step 3.
  - CLAUDE.md/AGENTS.md: CLI list adds `skills sync`; package map row `internal/skills`; key constraint: skills are installed only by `init`/`skills sync`/installers, never by `mcp`/`hook`, and ney never overwrites a modified or unmarked skill dir. `cmp CLAUDE.md AGENTS.md`.
- [ ] **Step 6:** `gofmt -l .` empty, `go vet ./...`, `GOOS=windows go vet ./...`, `go test ./...` PASS.
- [ ] **Step 7:** Commit `feat(install): keep ney skills in sync on update; docs`.
