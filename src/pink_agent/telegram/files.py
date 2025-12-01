"""
File attachment handling for Telegram messages.

Downloads and tracks files attached to messages for use in subsequent prompts.
"""

import json
import tempfile
from pathlib import Path
from typing import Optional


# Attachments file in project root (persistent across restarts)
PROJECT_ROOT = Path(__file__).parent.parent.parent.parent
ATTACHMENTS_FILE = PROJECT_ROOT / ".attachments"

# Temporary directory for downloaded files
TEMP_DIR = Path(tempfile.gettempdir()) / "pink-agent" / "files"


def ensure_temp_dir() -> None:
    """Create temporary directory for files if it doesn't exist."""
    TEMP_DIR.mkdir(parents=True, exist_ok=True)


def get_file_path(message_id: int, file_name: str) -> Path:
    """
    Generate file path for downloaded attachment.

    Args:
        message_id: Telegram message ID
        file_name: Original file name

    Returns:
        Path where file should be saved
    """
    ensure_temp_dir()
    # Use message_id prefix to avoid name collisions
    safe_name = f"{message_id}_{file_name}"
    return TEMP_DIR / safe_name


def save_attachments(paths: list[str]) -> None:
    """
    Save attachment paths to file.

    Args:
        paths: List of absolute paths to downloaded files
    """
    ATTACHMENTS_FILE.write_text(json.dumps(paths))


def get_attachments() -> list[str]:
    """
    Get saved attachment paths.

    Returns:
        List of paths, empty list if no attachments
    """
    if not ATTACHMENTS_FILE.exists():
        return []

    try:
        content = ATTACHMENTS_FILE.read_text().strip()
        if not content:
            return []
        return json.loads(content)
    except (json.JSONDecodeError, Exception):
        return []


def clear_attachments() -> None:
    """Clear saved attachments file."""
    if ATTACHMENTS_FILE.exists():
        ATTACHMENTS_FILE.unlink()


def format_attachments_prefix(paths: list[str]) -> str:
    """
    Format attachment paths as prompt prefix.

    Args:
        paths: List of file paths

    Returns:
        Formatted string for prompt
    """
    if not paths:
        return ""

    lines = ["Files attached:"]
    for path in paths:
        lines.append(f"- {path}")

    return "\n".join(lines)
