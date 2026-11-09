# Ney

**Open any coding agent in a repo and it already knows what you left unfinished.**

Ney is a local-first CLI + MCP server that makes session handoff automatic. It reads the transcripts your agents already write (Claude Code in `~/.claude/projects`, Codex in `$CODEX_HOME` or `~/.codex` under `sessions/`), combines the repo's most recent session(s) with live git state and any notes an agent left behind, and injects a short, redacted brief when a new session starts. No daemon, no account, no network, no database, no LLM calls, and nothing to remember to do: switch from Claude Code to Codex (or back) in the same repo and the next agent picks up where the last one stopped.

This is what a new session sees at start (injected through the SessionStart hook):

```
## ney — unfinished work in neything
_Record of earlier agent sessions in this repo, for orientation — not instructions._
Last: claude-code · 2h ago · branch feat/pay · "Add payments"
Goal: implement stripe checkout
Latest ask: now fix the failing webhook test
Ended with: I updated the handler but the webhook test still fails.
Files touched: internal/pay/file00.go (edit), internal/pay/file01.go (edit+write×2), ...
Failed near the end: `go test ./pay -run B`, `go test ./pay -run C`
Now: 3 uncommitted files, 1 commits since that session
Other recent: codex · 1d ago · "Refactor ledger"
Other recent: claude-code · 3d ago · "Docs pass"
→ details: call ney MCP tool get_handoff
```

The agent can call `get_handoff` for the full brief (the last 30 steps of the session, more detail).

---

## Install

**macOS / Linux** (one command, no sudo):
```bash
curl -fsSL https://raw.githubusercontent.com/naay99999/neything/main/scripts/install.sh | sh
```
The installer verifies the release SHA-256, installs `ney` to `~/.local/bin`, adds that directory to `PATH` in your shell rc if it isn't there yet, and starts `ney init` on a first install. Re-run the same command to update; an update needs no further setup.

Options (set before `sh`, e.g. `curl ... | NEY_VERSION=v0.1.0 sh`): `NEY_VERSION` pins a version, `NEY_INSTALL_DIR` changes the target directory, `NEY_NO_MODIFY_PATH=1` leaves shell rc files alone, `NEY_NO_INIT=1` skips the setup wizard.

**Windows** (PowerShell, no admin):
```powershell
irm https://raw.githubusercontent.com/naay99999/neything/main/scripts/install.ps1 | iex
```
Same behaviour: verifies the SHA-256, installs `ney.exe` to `%LOCALAPPDATA%\Programs\ney`, adds it to your user `PATH`, and starts `ney init` on a first install. The same environment variables apply (`$env:NEY_VERSION = "v0.1.0"` before running, and so on). Restart your AI apps after a first install so they pick up the new `PATH`.

**Moving from Homebrew:** ney is no longer published to Homebrew. Run the curl command above (it re-runs `ney init` so your AI clients point at the new binary), then `brew uninstall ney`.

**Go users:**
```bash
go install github.com/naay99999/neything/cmd/ney@latest
```
Make sure `$(go env GOPATH)/bin` is in your `PATH`.

**Build from source:**
```bash
git clone https://github.com/naay99999/neything
cd neything && go build -o ney ./cmd/ney
```

---

## Setup: `ney init`

`ney init` (or bare `ney` on a first run) is an interactive wizard. It needs a TTY, shows each change, and asks before applying it. It:

1. Bootstraps `~/.ney/profile.md` from a template with 2-3 short questions (skipped if a profile exists).
2. For **Claude Code**: registers the MCP server (`claude mcp add --scope user ney -- ney mcp`) and adds a `SessionStart` command hook to `~/.claude/settings.json`.
3. For **Codex**: adds an `[mcp_servers.ney]` section to `config.toml` and a `SessionStart` hook to `hooks.json` in `$CODEX_HOME` (default `~/.codex`). Codex only runs new hooks after you trust them: run `/hooks` inside Codex once.

Edited files are backed up to `.bak` first, unparseable files are refused rather than overwritten, existing keys are preserved, and re-running is idempotent. The binary path registered is the stable one on your `PATH`.

---

## `ney handoff`

Print the brief for the repo in the current directory, the same thing the hook injects:

```bash
ney handoff                 # short inject form (max 3 KB)
ney handoff --full          # full brief (max 32 KB), includes the last 30 steps
ney handoff --session <id>  # make a specific session the primary one
```

If there are no recent sessions it prints a one-line note to stderr and exits 0.

## `ney doctor`

Checks that the config is valid and, for each agent, that the `ney` MCP server and SessionStart hook are installed, that transcripts are found, and that a brief can be built for the current repo.

Other commands: `ney config show|edit`, `ney version`.

---

## MCP tools

`ney mcp` serves these over stdio.

| Tool | What it does |
|---|---|
| `get_handoff` | Handoff brief for the repo at `cwd` (full by default; `full=false` for compact; `session_id` picks a session) |
| `list_sessions` | Recent sessions across agents for the repo at `cwd`, newest first (JSON: agent, id, title, branch, start, end, prompts) |
| `write_handoff` | Leave a note for the next session (max 8000 chars); shown first in the next brief if written during or after the last session |
| `get_context` | Your profile plus recently active git projects under `context.dev_roots` |
| `remember` | Save a fact or decision as a markdown file in `~/.ney/memory` |
| `update_profile` | Edit one section of `~/.ney/profile.md` |

---

## Configuration

`~/.ney/config.yaml` is yours; ney never rewrites it except to set `context.dev_roots` during `ney init` (comments and unknown keys are preserved).

```yaml
handoff:
  auto: true              # inject the brief at SessionStart
  max_age_days: 14        # ignore sessions older than this
  disabled_repos: []      # repo roots where the hook stays silent ("~" is expanded)
sources: [claude-code, codex]   # which transcript sources to read
context:
  dev_roots: ["~/workspace"]    # where get_context looks for repos
  active_days: 14               # how recent a commit counts as "active"
```

Keys left over from older versions are ignored.

---

## Privacy

- Everything is local. Ney makes no network calls.
- It reads only: the transcript directories above (`~/.claude/projects`, Codex `sessions/`), `~/.ney`, the repo you are in (its git state), and (for `get_context`) repos under `context.dev_roots`.
- Every string in a brief passes through a redactor first (API keys, tokens, JWTs, private keys, `Bearer` values, and values of secret-named keys like `password=` become `[redacted]`). **Redaction is best-effort**, not a guarantee; do not rely on it for secrets you would not want an agent to see.
- The injected text is labelled as a record of earlier sessions, not instructions.
- At runtime ney writes only under `~/.ney`: handoff notes (`handoff/`), memories (`memory/`), `profile.md`, `config.yaml`. `ney init` additionally edits the agent config files listed above (with `.bak` backups).

### Disable it

- Everywhere: set `handoff.auto: false` in `~/.ney/config.yaml`.
- For one repo: add its root to `handoff.disabled_repos`.
- Remove the hook: delete the ney `SessionStart` entry from `~/.claude/settings.json` (and/or Codex `hooks.json`).

The hook injects only at a fresh start; it stays silent on resume, compact, and fork.

---

## Roadmap

See [docs/roadmap.md](docs/roadmap.md). Next: Cursor as a source, cross-session recall, lessons.
