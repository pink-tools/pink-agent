"""
Claude Code session management.

Manages Claude session lifecycle:
- Session ID stored in .session file (portable, in project root)
- New sessions initialized via claude -p command
- Session discovery from ~/.claude/projects/ (only for first initialization)
"""

import os
import subprocess
from pathlib import Path

from pink_agent.config import logger, get_claude_env

# Session file in project root (portable)
PROJECT_ROOT = Path(__file__).parent.parent.parent.parent
SESSION_FILE = PROJECT_ROOT / ".session"


def read_session_id() -> str:
    """Read current session ID from file."""
    if SESSION_FILE.exists():
        return SESSION_FILE.read_text().strip()
    return ""


def write_session_id(session_id: str) -> None:
    """Write session ID to file."""
    SESSION_FILE.write_text(session_id)
    logger.debug(f"Session ID saved: {session_id}")


def extract_session_id() -> str:
    """
    Extract session ID from Claude Code session files.

    Finds the latest session file in ~/.claude/projects/ for home directory.
    Used ONLY during first session initialization.

    Returns:
        Session ID from most recent session file
    """
    projects_dir = Path.home() / ".claude" / "projects"

    if not projects_dir.exists():
        raise RuntimeError(f"Claude projects directory not found: {projects_dir}")

    session_dir = _find_session_directory(projects_dir)
    session_files = list(session_dir.glob("*.jsonl"))

    if not session_files:
        raise RuntimeError(f"No session files found in {session_dir}")

    latest_session = max(session_files, key=lambda f: f.stat().st_mtime)
    session_id = latest_session.stem

    logger.debug(f"Extracted session ID: {session_id}")
    return session_id


def _find_session_directory(projects_dir: Path) -> Path:
    """Find the session directory for home directory."""
    home_dir = str(Path.home())
    possible_names = [
        home_dir.replace("/", "_")[1:],
        Path.home().name,
        "home"
    ]

    for name in possible_names:
        candidate = projects_dir / name
        if candidate.exists():
            return candidate

    subdirs = [d for d in projects_dir.iterdir() if d.is_dir()]
    if subdirs:
        return max(subdirs, key=lambda d: d.stat().st_mtime)

    raise RuntimeError(f"No session directories found in {projects_dir}")


def initialize_session() -> str:
    """Initialize a new Claude Code session."""
    logger.debug("Initializing new Claude Code session...")

    env = get_claude_env()

    result = subprocess.run(
        ['claude', '-p', 'Hello, you are a new Pink Agent session. Read the configuration:\n\n~/.claude/CLAUDE.md', '--dangerously-skip-permissions'],
        cwd=str(Path.home()),
        env=env,
        capture_output=True,
        text=True
    )

    if result.returncode != 0:
        raise RuntimeError(f"Failed to initialize session: {result.stderr}")

    session_id = extract_session_id()
    write_session_id(session_id)

    logger.debug(f"New session initialized: {session_id}")
    return session_id


def ensure_session() -> str:
    """Ensure a valid session exists, creating one if necessary."""
    session_id = read_session_id()

    if not session_id:
        session_id = initialize_session()

    return session_id


def reset_session() -> None:
    """Reset the current session by deleting the session file."""
    if SESSION_FILE.exists():
        SESSION_FILE.unlink()
        logger.debug("Session reset - next command will create new session")
    else:
        logger.warning("No session file to reset")
