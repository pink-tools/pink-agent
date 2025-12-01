"""
Auto-compact functionality for Claude Code session management.
"""

import subprocess
from pathlib import Path

from pink_agent.config import logger, CLAUDE_EXECUTION_TIMEOUT, get_claude_env
from pink_agent.claude.parser import parse_json_output
from pink_agent.claude.sessions import reset_session, extract_session_id, write_session_id


def perform_auto_compact(current_session_id: str) -> str:
    """
    Perform auto-compact when cache_read exceeds threshold.

    Steps:
    1. Run /summarize command on current session
    2. Extract summary from result
    3. Reset .session file (so new session will be created)
    4. Create new session with summary as first message
    5. Extract and save new session_id

    Args:
        current_session_id: Current session ID to summarize

    Returns:
        New session ID after compaction

    Raises:
        RuntimeError: If auto-compact fails
    """
    logger.debug("[Claude] Auto-compact triggered")

    home_dir = str(Path.home())
    env = get_claude_env()

    try:
        logger.debug("[Claude] Running /summarize...")
        result = subprocess.run(
            ['claude', '-p', '/summarize', '--resume', current_session_id, '--dangerously-skip-permissions', '--output-format=json'],
            cwd=home_dir,
            env=env,
            capture_output=True,
            text=True,
            timeout=CLAUDE_EXECUTION_TIMEOUT
        )

        if result.returncode != 0:
            raise RuntimeError(f"Summarize failed: {result.stderr}")

        parsed = parse_json_output(result.stdout.strip(), current_session_id)
        if parsed is None:
            raise RuntimeError("Failed to parse summarize output")

        summary_text, _, _, _ = parsed
        summary_lines = summary_text.split('\n')
        if len(summary_lines) > 2 and '/' in summary_lines[0]:
            summary = '\n'.join(summary_lines[2:])
        else:
            summary = summary_text

        logger.debug("[Claude] Resetting session...")
        reset_session()

        logger.debug("[Claude] Creating new session with summary...")
        takeover_prompt = f"""Previous session summary:

{summary}

This is a session takeover/handoff. Read the listed files to load context, then acknowledge that you're ready. DO NOT make any changes (edit files, create files, commit, etc) - just read and report that you've loaded the context."""

        result = subprocess.run(
            ['claude', '-p', takeover_prompt, '--dangerously-skip-permissions'],
            cwd=home_dir,
            env=env,
            capture_output=True,
            text=True,
            timeout=120
        )

        if result.returncode != 0:
            raise RuntimeError(f"New session creation failed: {result.stderr}")

        new_session_id = extract_session_id()
        write_session_id(new_session_id)

        return new_session_id

    except subprocess.TimeoutExpired:
        raise RuntimeError("Auto-compact timeout (>2 minutes)")
    except Exception as e:
        raise RuntimeError(f"Auto-compact failed: {e}")
