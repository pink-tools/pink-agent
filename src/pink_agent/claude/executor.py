"""
Claude Code executor - main orchestration.
"""

import os
import subprocess
from pathlib import Path

from pink_agent.config import logger, get_claude_env
from pink_agent.claude.sessions import ensure_session
from pink_agent.claude.parser import parse_json_output


def execute_claude(prompt: str) -> tuple[str, int, str]:
    """
    Execute Claude Code with the given prompt using session resumption.

    Args:
        prompt: User prompt to send to Claude

    Returns:
        Tuple of (result_text, session_context_size, session_id)

    Raises:
        RuntimeError: If execution fails
    """
    try:
        session_id = ensure_session()
        home_dir = str(Path.home())
        env = get_claude_env()

        cmd = ['claude', '-p', prompt, '--resume', session_id, '--dangerously-skip-permissions', '--output-format=json']

        mcp_config = os.getenv('MCP_CONFIG')
        if mcp_config:
            cmd.extend(['--mcp-config', mcp_config])

        result = subprocess.run(
            cmd,
            cwd=home_dir,
            env=env,
            capture_output=True,
            text=True
        )

        if result.returncode != 0:
            error_msg = result.stderr.strip() if result.stderr else "Unknown error"

            if "heap out of memory" in error_msg.lower() or "heap limit" in error_msg.lower():
                raise RuntimeError(
                    "Claude Code ran out of memory. Session context is too large.\n\n"
                    "Use /newsession to start fresh."
                )

            if len(error_msg) > 500:
                error_msg = error_msg[:500] + "...\n(error truncated)"

            raise RuntimeError(f"Claude Code failed: {error_msg}")

        json_output = result.stdout.strip()

        if not json_output:
            logger.warning("[Claude] Empty output")
            return ("Done", 0, session_id)

        parsed = parse_json_output(json_output, session_id)

        if parsed is None:
            logger.warning("[Claude] JSON parse failed, returning raw output")
            return (json_output if json_output else "Done", 0, session_id)

        result_text, input_tokens, cache_read, cache_creation = parsed
        session_context_size = input_tokens + cache_creation + cache_read

        return (result_text if result_text else "Done", session_context_size, session_id)

    except FileNotFoundError:
        raise RuntimeError("Claude Code binary not found. Is it installed?")
    except Exception as e:
        raise RuntimeError(f"Execution failed: {e}")
