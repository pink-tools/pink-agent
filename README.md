# pink-agent

Telegram bot that runs Claude Code sessions in forum topics. Each topic is an independent project with its own Claude session. Supports text, voice, photos, files, and documents.

## Features

- **Forum topics as projects** — create topic = start Claude session, close topic = cleanup
- **Voice input** — send voice message, auto-transcribed via pink-transcriber
- **File support** — photos, documents, video, audio sent directly to Claude
- **Message batching** — groups rapid messages into single Claude input (2s window)
- **Session resume** — reconnects to existing session on restart
- **Context assembly** — collects context from all installed pink-tools services
- **Per-project file store** — persistent files accessible to Claude via CLI
- **MCP support** — optional MCP server config passed to Claude sessions

## Requirements

- [claude](https://docs.anthropic.com/en/docs/claude-code) CLI
- [pink-transcriber](https://github.com/pink-tools/pink-transcriber) (optional, for voice)

## Install

Download binary from [Releases](https://github.com/pink-tools/pink-agent/releases), or via pink-orchestrator:

```bash
pink-orchestrator --service-download pink-agent
```

## Setup

See [ai-docs/SETUP.md](ai-docs/SETUP.md).

## Usage

```bash
pink-agent                                          # Start daemon
pink-agent stop                                     # Stop daemon
pink-agent status                                   # Check if running

pink-agent project list                             # List projects
pink-agent project create "Name" ["Prompt"] [--dir] # Create project + topic
pink-agent project delete [id-or-name]              # Delete project

pink-agent store list|get|add|delete                # Manage project files
pink-agent send "text"                              # Send text to topic
pink-agent send -f <file>                           # Send file to topic

pink-agent session list <dir>                       # List sessions for directory
pink-agent session attach <id> <dir>                # Attach existing session to new topic
pink-agent refresh                                  # Restart session with fresh context
```

## Configuration

`.env` file in `~/pink-tools/pink-agent/`:

| Variable | Required | Description |
|----------|----------|-------------|
| `TELEGRAM_BOT_TOKEN` | Yes | From BotFather |
| `TELEGRAM_GROUP_ID` | Yes | Forum-enabled supergroup ID |

## Build from Source

```bash
git clone https://github.com/pink-tools/pink-agent.git
cd pink-agent
make build
```
