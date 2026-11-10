---
name: ney-resume
description: Pick up work an earlier agent session handed off through ney. Use when the user wants to continue or resume — "resume", "/ney-resume", "/ney-resume <project>", "pick up where we left off", "continue from the handoff", "ทำต่อจากเมื่อกี้", "รับ handoff", "ต่องานเดิม".
---

# ney-resume

Let the user choose a saved handoff note, then get ready to continue that work.

1. **List the notes.** Call the ney MCP tool `list_handoffs` with `cwd` = the absolute working
   directory. If the user named a project (`/ney-resume portfolio`, "resume portfolio"), pass it
   as `project`. If the tool says there are no notes for that name, show its suggestions and ask
   which project they meant. If there are no notes at all, say so and stop.
2. **Show the list and let the user pick.** One line per note, numbered as `n`, newest first:
   project · title · agent · when written; mark the notes of the current repo. Ask which one to
   open and wait. Do this even when there is only one note.
3. **Open the chosen note.** Call `get_handoff` with that note's `cwd` and `note_id`. It returns
   the full note, the recent sessions in that repo and what changed in git since the note.
4. **Brief the user** in the language of the conversation, in a few lines: what the work is,
   where it stopped, the next step, anything to watch out for, and what changed since (new
   commits, uncommitted changes, steps that already look done). Read the files or artifacts the
   note references before relying on them; do not paste the note back.
5. **If the note's repo is not the one this session is in**, say so: the user can open a new
   session in that repo (recommended), or continue here using its absolute path.
6. **Ask before acting.** Propose the first next step and wait for the user to confirm or
   redirect. Do not start changing anything on your own.
7. **If ney's MCP tools are not available**, run `ney handoff --full` in the repo and use its
   output instead; it shows only the latest note of that repo.
