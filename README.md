# Pink Agent

Control Claude Code from your phone. Send voice or text messages via Telegram — your computer does the work.

Build an entire website lying on your pillow, just talking to your phone!

## Quick Start

```bash
git clone https://github.com/pink-tools/pink-agent
cd pink-agent
uv sync
cp .env.example .env
# Add your Telegram bot token and user ID to .env
uv run pink-agent
```

## Requirements

- Python 3.12+
- [uv](https://docs.astral.sh/uv/)
- [Claude Code](https://claude.ai/code) CLI installed
- For voice messages: [pink-transcriber-mps](https://github.com/pink-tools/pink-transcriber-mps) or [pink-transcriber-cpu](https://github.com/pink-tools/pink-transcriber-cpu) or [pink-transcriber-cuda](https://github.com/pink-tools/pink-transcriber-cuda)

## Setup

1. Create a Telegram bot via [@BotFather](https://t.me/BotFather), get the token
2. Get your user ID from [@userinfobot](https://t.me/userinfobot)
3. Add both to `.env`:

```bash
TELEGRAM_BOT_TOKEN=your_token
TELEGRAM_USER_ID=your_id
```

## Usage

```bash
# Run (macOS - prevents sleep)
caffeinate -id uv run pink-agent

# Run (Linux)
uv run pink-agent

# Verbose mode
VERBOSE=1 uv run pink-agent
```

**Telegram commands:**
- `/new` — start fresh conversation
- `/compact` — compress context manually
- `/restart` — pull updates and restart

**Send files to yourself from Claude:**
```bash
pink-agent send "Here's your screenshot" -f screenshot.png
```

## How It Works

Two processes run in parallel:
- One talks to Telegram
- One runs Claude Code

If one crashes, the other keeps working. Messages are saved to disk — nothing gets lost.

## License

MIT
