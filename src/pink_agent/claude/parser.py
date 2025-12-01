"""
Claude Code JSON output parser.
"""

import json
from typing import Optional, TypedDict

from pink_agent.config import logger, MAX_SESSION_CONTEXT
from pink_agent.claude.output import format_tool_action


class ToolInfo(TypedDict):
    """Tool execution information."""
    name: str
    input: dict
    result: str | None


def _extract_tools_and_text_from_events(events: list) -> tuple[dict[str, ToolInfo], list[str], dict | None]:
    """
    Extract tools, text blocks, and usage from Claude Code events.

    Args:
        events: List of events from Claude Code JSON output

    Returns:
        Tuple of (tools_dict, text_blocks, last_usage)
    """
    tools: dict[str, ToolInfo] = {}
    text_blocks = []
    last_usage = None

    # Pass 1: Collect tool_use from assistant events
    for event in events:
        if event.get('type') == 'assistant':
            msg = event.get('message', {})
            content = msg.get('content', [])
            usage = msg.get('usage', {})

            if usage:
                last_usage = usage

            for block in content:
                block_type = block.get('type')

                if block_type == 'tool_use':
                    tool_id = block.get('id')
                    tools[tool_id] = {
                        'name': block.get('name'),
                        'input': block.get('input', {}),
                        'result': None
                    }

                elif block_type == 'text':
                    text_blocks.append(block.get('text', ''))

    # Pass 2: Collect tool_result from user events
    for event in events:
        if event.get('type') == 'user':
            msg = event.get('message', {})
            content = msg.get('content', [])

            for block in content:
                if block.get('type') == 'tool_result':
                    tool_id = block.get('tool_use_id')
                    if tool_id in tools:
                        tools[tool_id]['result'] = block.get('content', '')

    return tools, text_blocks, last_usage


def _format_tool_actions(tools: dict[str, ToolInfo]) -> list[str]:
    """
    Format tool executions into readable strings.

    Args:
        tools: Dictionary of tool information by tool_id

    Returns:
        List of formatted action strings
    """
    actions = []
    for tool_id, tool in tools.items():
        action = format_tool_action(
            tool['name'],
            tool['input'],
            tool['result']
        )
        if action:
            actions.append(action)
    return actions


def _build_output_text(actions: list[str], text_blocks: list[str], usage: dict | None, session_id: str) -> str:
    """
    Build final formatted output text.

    Args:
        actions: List of formatted tool actions
        text_blocks: List of text responses from assistant
        usage: Token usage information
        session_id: Current session ID for display

    Returns:
        Formatted output string
    """
    result_parts = []

    # Token counter (first line)
    if usage and session_id:
        input_tokens = usage.get('input_tokens', 0)
        cache_creation = usage.get('cache_creation_input_tokens', 0)
        cache_read = usage.get('cache_read_input_tokens', 0)
        session_context = input_tokens + cache_creation + cache_read

        short_session_id = session_id[:8]
        result_parts.append(f"{session_context} / {MAX_SESSION_CONTEXT} | {short_session_id}")

    # Actions
    if actions:
        result_parts.append("")
        for i, action in enumerate(actions):
            result_parts.append(action)
            if i < len(actions) - 1:
                result_parts.append("")

    # Final text
    if text_blocks:
        result_parts.append("")
        result_parts.append('\n\n'.join(text_blocks))

    return '\n'.join(result_parts)


def parse_json_output(json_output: str, session_id: str = "") -> Optional[tuple[str, int, int, int]]:
    """
    Parse Claude Code JSON output to extract ALL tool calls and final text.

    Claude Code with --output-format=json returns an array of events:
    - assistant events contain tool_use (Write, Edit, Bash, etc.) and text
    - user events contain tool_result (execution results)

    This function extracts:
    - ALL tool executions (Write, Edit, Bash, Read, etc.)
    - ALL text responses
    - Token usage for auto-compact

    Args:
        json_output: JSON string from Claude Code stdout
        session_id: Current session ID (optional, for display)

    Returns:
        Tuple of (formatted_result, input_tokens, cache_read, cache_creation), or None if parsing failed
    """
    try:
        events = json.loads(json_output)

        if not isinstance(events, list):
            logger.warning(f"Expected JSON array, got: {type(events)}")
            return None

        tools, text_blocks, last_usage = _extract_tools_and_text_from_events(events)
        actions = _format_tool_actions(tools)
        full_result = _build_output_text(actions, text_blocks, last_usage, session_id)

        if last_usage:
            input_tokens = last_usage.get('input_tokens', 0)
            cache_creation = last_usage.get('cache_creation_input_tokens', 0)
            cache_read = last_usage.get('cache_read_input_tokens', 0)

            return (full_result, input_tokens, cache_read, cache_creation)

        logger.warning("[Claude] No usage found in events")
        return (full_result, 0, 0, 0)

    except json.JSONDecodeError as e:
        logger.error(f"[Claude] JSON parse error: {e}")
        return None
    except Exception as e:
        logger.error(f"[Claude] Parse error: {e}")
        return None
