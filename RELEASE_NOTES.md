# pink-agent

Telegram bot that runs Claude Code sessions in forum topics.

## Binaries

- `pink-agent-darwin-arm64` - macOS ARM64
- `pink-agent-linux-amd64` - Linux x64
- `pink-agent-windows-amd64.exe` - Windows x64

## Features

- Telegram Forum Mode — each topic is a project with its own Claude session
- Voice input via pink-transcriber
- Session compaction when context fills up
- Per-project file store

## Requirements

- [claude](https://docs.anthropic.com/en/docs/claude-code) CLI
- [pink-transcriber](https://github.com/pink-tools/pink-transcriber) (optional, for voice)
