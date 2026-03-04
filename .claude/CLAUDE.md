# pink-agent

## Context

You are running as Claude Code via `claude -p` (NDJSON protocol), controlled by pink-agent.

- pink-agent runs on user's Mac, creates Telegram bot in a Forum Mode supergroup
- Each forum topic = one project = one Claude session (this conversation)
- User sends voice/text/files via Telegram → input goes to your stdin as NDJSON
- Voice is transcribed to text automatically
- Your stdout streams back to the Telegram topic
- User creates topic in Telegram → bot auto-creates project + session
- User renames topic → bot updates project name
- User closes topic → bot stops session + cleans up
- `/stop` in topic → interrupts current Claude response

**Structure:**
- **Projects** — each project has one forum topic and one Claude session
- **Compaction** — when context fills up, session is compacted: summary extracted, fresh session started with context + summary + last user message

---

## Rules

**No Blocking Commands.** Never run commands that don't exit (dev servers, watchers, daemons). Give command to user, let them run.

---

## CLI

```bash
pink-agent                    # Start daemon
pink-agent stop               # Stop daemon
pink-agent status             # Check if running
pink-agent project list       # List projects
pink-agent store list         # List files in project store
pink-agent store get <path>   # Get file content
pink-agent store add <path> "content"        # Add file (fails if exists)
pink-agent store add --force <path> "content" # Overwrite file
pink-agent store delete <path>               # Delete file
pink-agent send "text"        # Send text to topic
pink-agent send -f <file>     # Send file to topic
```

`store` and `send` use env vars `PINK_PROJECT_ID` and `PINK_THREAD_ID` (set automatically). Fallback: `pink-agent store -p "Project Name" list`.

## Store

Each project has a file store at `store/<projectID>/`. `PROJECT.md` is the project context — auto-created on topic creation, injected into init prompts. Claude can read/write it via `pink-agent store` commands.

## Data

`/Users/pink-tools/pink-agent/`

---

Code: @CODE.md
Projects: @PROJECTS.md
MCPs: @MCP.md
