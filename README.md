# pink-agent

Telegram bot that runs Claude Code sessions in forum topics.

## Requirements

- [claude](https://docs.anthropic.com/en/docs/claude-code) CLI
- [pink-transcriber](https://github.com/pink-tools/pink-transcriber) (optional, for voice)

## Setup

See [ai-docs/SETUP.md](ai-docs/SETUP.md).

## Usage

```bash
pink-agent          # Start daemon
pink-agent stop     # Stop daemon
pink-agent status   # Check if running
pink-agent --help   # Show all commands
```

## Build from Source

```bash
git clone https://github.com/pink-tools/pink-agent.git
cd pink-agent
make build
```
