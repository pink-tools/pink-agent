# Architecture

Telegram bot for remote Claude Code control via voice and text.

## Directory Structure

```
pink-agent/
├── src/pink_agent/
│   ├── daemon/               # Process supervision
│   │   ├── supervisor.py     # Supervisor (manages two agents)
│   │   └── singleton.py      # Ensure single instance
│   ├── claude/               # Claude Code execution
│   │   ├── agent.py          # Claude agent entry point
│   │   ├── processor.py      # Command processor (reads commands.jsonl)
│   │   ├── executor.py       # Execute Claude Code with session
│   │   ├── parser.py         # Parse JSON output from Claude
│   │   ├── output.py         # Format tool actions for Telegram
│   │   ├── sessions.py       # Session lifecycle (.session file)
│   │   └── compact.py        # Auto-compact when context grows
│   ├── telegram/             # Telegram bot
│   │   ├── agent.py          # Telegram agent entry point
│   │   ├── receiver.py       # Handle incoming messages
│   │   ├── sender.py         # Send responses to user
│   │   ├── commands.py       # Bot commands (/new, /compact, /restart)
│   │   ├── transcriber.py    # Voice transcription (calls pink-transcriber)
│   │   ├── files.py          # File attachment handling
│   │   └── output.py         # Format messages for Telegram
│   ├── queue/                # JSONL queue storage
│   │   ├── storage.py        # Read/write/delete with fcntl locking
│   │   └── watcher.py        # File monitoring (watchfiles library)
│   ├── cli/                  # CLI commands
│   │   └── send.py           # pink-agent send (bot → user messages)
│   └── config.py             # Configuration and environment
├── commands.jsonl            # User messages queue
├── responses.jsonl           # Bot responses queue
├── .session                  # Current Claude session ID
├── .attachments              # File paths from recent messages
├── .env                      # Bot token and user ID
└── pyproject.toml            # Package configuration
```

## Files

### `daemon/supervisor.py`
Main entry point (pink-agent command). Starts two subprocesses and monitors health. Handles SIGINT (shutdown) and SIGUSR1 (restart with git pull + uv sync). Restarts through caffeinate on macOS to prevent sleep.

### `daemon/singleton.py`
Ensures only one pink-agent instance runs. Finds all processes with project identifiers, walks up to root process (handles wrappers like caffeinate/uv), kills entire tree. Avoids killing own parent chain.

### `claude/agent.py`
Independent process that monitors commands.jsonl. Entry point: pink-claude-agent.

### `claude/processor.py`
Event-driven task that watches commands.jsonl. Processes ONE command at a time (sequential execution to prevent session conflicts). Deletes command BEFORE execution to prevent re-execution on restart. Spawns detached subprocess for each command (survives bot restarts). Auto-compacts when session exceeds 180k tokens.

### `claude/executor.py`
Executes Claude Code CLI with session resumption. Runs in user home directory with JSON output format. Parses token usage for auto-compact detection. Raises RuntimeError on heap overflow (session too large).

### `claude/sessions.py`
Manages Claude Code session lifecycle. Session ID stored in .session file (portable). First initialization runs claude -p with greeting, extracts session ID from ~/.claude/projects/. Subsequent commands use --resume with stored session ID.

### `claude/compact.py`
Triggered when session context exceeds threshold. Steps: run /summarize on current session, reset .session file, create new session with summary as first message, save new session ID. Takes 1-2 minutes.

### `claude/parser.py`
Parses Claude Code JSON output (array of events). Extracts tool_use from assistant events (Write, Edit, Bash, Read, etc.), tool_result from user events, final text responses, and token usage. Returns formatted text with token counter.

### `claude/output.py`
Formats tool actions for Telegram display. Each tool has custom formatter: Write shows file path + content, Edit shows old/new strings with line numbers, Read shows content for text files (truncated if large), Bash shows command + output. Cleans system reminders and line numbers.

### `telegram/agent.py`
Independent process that polls Telegram and monitors responses.jsonl. Entry point: pink-telegram-agent. Two asyncio tasks: receiver (Telegram → commands.jsonl) and sender (responses.jsonl → Telegram). Registers bot commands and sends greeting on startup.

### `telegram/receiver.py`
Handles incoming Telegram messages. Text messages go directly to commands.jsonl. Voice messages download to temp, transcribe via pink-transcriber, send transcription to user, add command to queue. Files download to temp and either attach to .attachments (no text) or append to next message (with text). Reply context extracted from quoted text or full message.

### `telegram/sender.py`
Watches responses.jsonl and sends to Telegram. Formats output with telegramify-markdown (MarkdownV2). Splits long messages into chunks (preserves code blocks). Retries indefinitely on failure. Sets thumbs up reaction when done. Fallback to plain text if Markdown parsing fails.

### `telegram/commands.py`
Bot command handlers. /new resets session (deletes .session file). /compact manually triggers auto-compact. /restart sends SIGUSR1 to parent supervisor. All commands check user authorization.

### `telegram/transcriber.py`
Calls external pink-transcriber CLI for voice-to-text. Checks service health on startup. 2-minute timeout for long voice messages. Returns transcribed text or raises RuntimeError.

### `telegram/files.py`
Manages file attachments. Downloads to /tmp/pink-agent/files/ with message_id prefix. Saves paths to .attachments file. Formats attachment list as prompt prefix. Clears attachments after use.

### `telegram/output.py`
Formats responses for Telegram. Converts markdown to MarkdownV2 using telegramify-markdown. Splits into chunks at newlines (avoids splitting code blocks). Sets/removes reactions (👀 progress, 👍 done).

### `queue/storage.py`
JSONL file operations with fcntl locking. Atomic read/append/delete operations. Two queues: commands.jsonl (message_id + content) and responses.jsonl (message_id + output). Files stored in project root.

### `queue/watcher.py`
Cross-platform file monitoring using watchfiles (Rust-based). Event-driven callback when file changes. High-performance alternative to polling. Integrates with asyncio event loop.

### `cli/send.py`
Standalone command for bot-to-user messages. Usage: pink-agent send "text" -f file.png. Uses Telegram Bot HTTP API directly (no bot instance). Loads .env from project root. Auto-detects MIME type (sendPhoto vs sendDocument).

### `config.py`
Loads .env, validates credentials. Sets logging level (DEBUG in verbose mode, INFO otherwise). Defines constants (token limits, timeouts, emojis). Returns Claude-specific env vars (MAX_THINKING_TOKENS, NODE_OPTIONS).

## Entry Points

```bash
# Main supervisor (starts both agents)
pink-agent

# Individual agents (called by supervisor)
pink-claude-agent
pink-telegram-agent

# CLI send command
pink-agent send "message"
pink-agent send -f file.png
pink-agent send "caption" -f file1.png -f file2.jpg

# Verbose mode
VERBOSE=1 pink-agent

# macOS (prevent sleep)
caffeinate -id pink-agent
```

## Key Concepts

**Dual-Process Architecture** — Two independent processes communicate through JSONL files. Telegram agent never blocks on Claude execution. Claude agent never blocks on Telegram polling. Either can crash without losing messages.

**Event-Driven Queues** — watchfiles library monitors JSONL files for changes. Callbacks triggered on file modification. No polling, no sleep loops. Low CPU usage.

**Detached Subprocess Execution** — Claude commands execute in spawn-mode multiprocesses. Survives bot restarts. Parent waits for completion but subprocess runs independently. Commands deleted before execution starts (prevents infinite loops on restart).

**Session Persistence** — Session ID stored in .session file (not ephemeral). Survives bot restarts. Single source of truth for current session. Auto-compact creates new session with summary.

**File-Based Communication** — commands.jsonl: user → Claude, responses.jsonl: Claude → user, .session: current Claude session, .attachments: pending file paths. All in project root (portable, survives restarts).

**Process Isolation** — Supervisor → two child processes. Each child has own asyncio loop and signal handlers. Supervisor monitors health and handles restart. Singleton ensures only one supervisor runs.

**Graceful Degradation** — Voice transcription fails → error message, bot continues. Claude execution fails → error response, queue continues. Telegram send fails → retry indefinitely. Markdown parsing fails → fallback to plain text.

**Zero Configuration** — One command to run: uv run pink-agent. Session auto-initializes on first message. Queues auto-create on startup. Temp directories created on demand.

**External Dependencies** — python-telegram-bot (Telegram API), watchfiles (Rust-based file monitoring), telegramify-markdown (MarkdownV2 conversion), psutil (process management), fcntl (file locking). Requires claude CLI, pink-transcriber service, uv package manager, and git for updates.
