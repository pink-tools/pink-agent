# pink-agent

## Context

You are running as Claude Code in a PTY, controlled via pink-agent.

- pink-agent runs on user's Mac, creates Telegram bot + tunnel
- User sends voice/text/files via Telegram → input goes to your PTY
- Voice is transcribed to text automatically

**Structure:**
- **Projects** — group sessions by topic
- **Sessions** — Claude Code instances, persist via --resume
- **Store** — per-project file storage

---

## Rules

**No Blocking Commands.** Never run commands that don't exit (dev servers, watchers, daemons). Give command to user, let them run.

---

## CLI Commands

### send (use only by request)
```bash
/Users/pink-tools/pink-agent/pink-agent send "text message"
/Users/pink-tools/pink-agent/pink-agent send -f path/to/file
```

### store (use only by request)
```bash
/Users/pink-tools/pink-agent/pink-agent store list
/Users/pink-tools/pink-agent/pink-agent store get <path>
/Users/pink-tools/pink-agent/pink-agent store add <path> "content"
/Users/pink-tools/pink-agent/pink-agent store add --force <path> "content"  # overwrite existing
/Users/pink-tools/pink-agent/pink-agent store -p "Project Name" list
/Users/pink-tools/pink-agent/pink-agent store -p "Project Name" get <path>
/Users/pink-tools/pink-agent/pink-agent store -p "Project Name" add <path> "content"
```

### session
```bash
/Users/pink-tools/pink-agent/pink-agent session list
/Users/pink-tools/pink-agent/pink-agent session new [name] [prompt]
/Users/pink-tools/pink-agent/pink-agent session switch <session-id>
```

### project
```bash
/Users/pink-tools/pink-agent/pink-agent project list
```

### tokens
```bash
/Users/pink-tools/pink-agent/pink-agent tokens
```
Returns current context token count.

---

## Data

`/Users/pink-tools/pink-agent/`

---

Code: @CODE.md
Projects: @PROJECTS.md
MCPs: @MCP.md
