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

**IMPORTANT:** NEVER kill, stop, or restart the bot process.

---

## Rules

**No Blocking Commands.** Never run commands that don't exit (dev servers, watchers, daemons). Give command to user, let them run.

---

## Tools

**pink-agent send** (use only by request)
```bash
~/pink-tools/pink-agent/pink-agent send "text"
~/pink-tools/pink-agent/pink-agent send -f file.png
```

**pink-agent store** (use only by request)
```bash
~/pink-tools/pink-agent/pink-agent store list
~/pink-tools/pink-agent/pink-agent store add file.md "content"
~/pink-tools/pink-agent/pink-agent store get file.md
~/pink-tools/pink-agent/pink-agent store -p "Project Name" list
```

**Data:** `~/pink-tools/pink-agent/`

**pink-orchestrator** (service management)
```bash
~/pink-tools/pink-orchestrator/pink-orchestrator --service-update pink-agent
~/pink-tools/pink-orchestrator/pink-orchestrator --service-restart pink-agent
~/pink-tools/pink-orchestrator/pink-orchestrator --service-stop pink-agent
~/pink-tools/pink-orchestrator/pink-orchestrator --service-start pink-agent
```

---

Code: @CODE.md
Projects: @PROJECTS.md
MCPs: @MCP.md
