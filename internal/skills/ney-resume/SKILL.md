---
name: ney-resume
description: Pick up the work an earlier agent session handed off through ney. Use when the user starts a session wanting to continue — "resume", "pick up where we left off", "continue from the handoff", "ทำต่อจากเมื่อกี้", "รับ handoff", "ต่องานเดิม".
---

# ney-resume

Load what the previous agent session left for this repo and get ready to continue it.

1. **Load the handoff.** Call the ney MCP tool `get_handoff` with `cwd` = the absolute working
   directory (`full` defaults to true). It returns the latest handoff note, if any, plus the
   recent sessions in this repo (any agent) and the live git state.
2. **If there is nothing**, say so plainly, name the directory you looked in (the user may have
   opened the wrong folder), and stop.
3. **Check what changed since.** Compare the note or last session with the live git state and
   the repo now (`git status`, `git log` since then): new commits, uncommitted changes, steps
   that already look done.
4. **Brief the user** in the language of the conversation, in a few lines: what the work is,
   where it stopped, the next step, anything to watch out for, and what changed since. Read the
   files or artifacts the note references before relying on them; do not paste the note back.
5. **Ask before acting.** Propose the first next step and wait for the user to confirm or
   redirect. Do not start changing anything on your own.
6. **If ney's MCP tools are not available**, run `ney handoff --full` in the working directory
   and use its output instead.
