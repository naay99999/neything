---
name: ney-handoff
description: Hand the current work off to the next agent session through ney. Use when the user is wrapping up or switching agents — "handoff", "hand off", "ส่งต่อ", "เดี๋ยวไปทำต่อใน codex/opencode", "leave a note for the next session".
---

# ney-handoff

Write a handoff note for the next agent session and save it in ney, so whichever agent opens
this repo next starts with it.

1. **Read the request.** Note the next agent if named, and what the next session will do. If
   the request does not say what the next session will do, ask exactly that one question and
   wait for the answer. Ask nothing else.
2. **Gather** from this conversation and the repo (`git status`, recent commits): what the next
   session must do, ordered next steps, current state (uncommitted changes, failing tests),
   pitfalls, decisions and why, what is done, which skills the next agent should use.
   - Do not copy what already exists as an artifact (specs, plans, ADRs, issues, commits,
     PRs, diffs): reference it by path, hash or URL.
   - Redact secrets (API keys, tokens, passwords) and personal data.
3. **Draft** the note in the language of the conversation, at most ~500 words, with these
   sections in this order, dropping any that would be empty:

   ```
   # Handoff → <next agent, or "next session">: <what it will do>
   ## Next session will
   ## Next steps
   ## State now
   ## Watch out
   ## Decisions
   ## Done
   ## Suggested skills
   ## References
   ```
4. **Show the draft** and ask whether to save it or what to change. Revise until the user
   approves.
5. **Save** with the ney MCP tool `write_handoff` (`cwd` = absolute working directory, `agent` =
   your agent name, `note` = the draft). Tell the user it is saved and how the next session gets
   it: shown automatically at session start in agents with ney's hook (Claude Code, Codex); in
   other agents, ask it to call ney's `get_handoff`.
6. **If ney's MCP tools are not available**, write the note to a file in the OS temp directory
   (`$TMPDIR`, else `/tmp`; `%TEMP%` on Windows), not the workspace, and give the user its path.
