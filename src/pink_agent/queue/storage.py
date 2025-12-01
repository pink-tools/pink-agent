"""
JSONL queue storage operations with fcntl locking.

Provides atomic operations for commands.jsonl and responses.jsonl queues.
Files are stored in project root (outside package).
"""

import json
import fcntl
from pathlib import Path
from typing import Optional, Dict, Any

# Queue file paths (project root)
PROJECT_ROOT = Path(__file__).parent.parent.parent.parent
COMMANDS_QUEUE = PROJECT_ROOT / "commands.jsonl"
RESPONSES_QUEUE = PROJECT_ROOT / "responses.jsonl"


def ensure_queue_files() -> None:
    """Create queue files if they don't exist."""
    COMMANDS_QUEUE.touch(exist_ok=True)
    RESPONSES_QUEUE.touch(exist_ok=True)


def append_command(message_id: int, content: str) -> None:
    """Append new command to queue (no status field)."""
    entry = {
        "message_id": message_id,
        "content": content
    }
    _append_entry(COMMANDS_QUEUE, entry)


def read_first_command() -> Optional[Dict[str, Any]]:
    """Read first command from queue (shared lock)."""
    return _read_first_entry(COMMANDS_QUEUE)


def delete_first_command() -> None:
    """Delete first command from queue (exclusive lock)."""
    _delete_first_entry(COMMANDS_QUEUE)


def append_response(message_id: int, output: str) -> None:
    """Append new response to queue (no status field)."""
    entry = {
        "message_id": message_id,
        "output": output
    }
    _append_entry(RESPONSES_QUEUE, entry)


def read_first_response() -> Optional[Dict[str, Any]]:
    """Read first response from queue (shared lock)."""
    return _read_first_entry(RESPONSES_QUEUE)


def delete_first_response() -> None:
    """Delete first response from queue (exclusive lock)."""
    _delete_first_entry(RESPONSES_QUEUE)


def clear_commands() -> None:
    """Clear all commands from queue (called on bot startup)."""
    if COMMANDS_QUEUE.exists():
        COMMANDS_QUEUE.write_text("")


def reset_interrupted_responses() -> None:
    """No-op - responses don't have status anymore."""
    pass


def _append_entry(file_path: Path, entry: Dict[str, Any]) -> None:
    """Append JSON entry to file with exclusive lock."""
    with open(file_path, 'a') as f:
        fcntl.flock(f, fcntl.LOCK_EX)
        try:
            f.write(json.dumps(entry) + '\n')
            f.flush()
        finally:
            fcntl.flock(f, fcntl.LOCK_UN)


def _read_first_entry(file_path: Path) -> Optional[Dict[str, Any]]:
    """Read first entry from file (shared lock)."""
    if not file_path.exists():
        return None

    with open(file_path, 'r') as f:
        fcntl.flock(f, fcntl.LOCK_SH)
        try:
            for line in f:
                line = line.strip()
                if not line:
                    continue
                return json.loads(line)
        finally:
            fcntl.flock(f, fcntl.LOCK_UN)

    return None


def _delete_first_entry(file_path: Path) -> None:
    """Delete first entry from file (exclusive lock)."""
    if not file_path.exists():
        return

    with open(file_path, 'r+') as f:
        fcntl.flock(f, fcntl.LOCK_EX)
        try:
            lines = f.readlines()
            f.seek(0)
            f.truncate()

            # Skip first line, write rest
            for line in lines[1:]:
                line = line.strip()
                if line:
                    f.write(line + '\n')

            f.flush()
        finally:
            fcntl.flock(f, fcntl.LOCK_UN)
