"""
Tool output formatters for Claude Code execution results.
"""

import re
from pink_agent.config import TOOL_EMOJIS


# Compiled regex patterns for performance
SYSTEM_REMINDER_PATTERN = re.compile(r'<system-reminder>.*?</system-reminder>', re.DOTALL)
MULTIPLE_NEWLINES_PATTERN = re.compile(r'\n\n\n+')
LINE_NUMBER_PATTERN = re.compile(r'^\s*(\d+)→(.*)$')


def clean_system_reminders(text: str) -> str:
    """
    Remove <system-reminder> tags from tool results.

    Claude Code adds system reminders to tool outputs, which are technical
    metadata not meant for end users.
    """
    cleaned = SYSTEM_REMINDER_PATTERN.sub('', text)
    cleaned = MULTIPLE_NEWLINES_PATTERN.sub('\n\n', cleaned)
    return cleaned.strip()


def clean_line_numbers(text: str) -> str:
    """
    Format line numbers from Read tool results.

    Claude Code formats output as "     1→content", we want "1: content".
    """
    lines = text.split('\n')
    cleaned = []
    for line in lines:
        match = LINE_NUMBER_PATTERN.match(line)
        if match:
            line_num = match.group(1)
            content = match.group(2)
            cleaned.append(f"{line_num}: {content}")
        else:
            cleaned.append(line)
    return '\n'.join(cleaned)


def _format_write(tool_input: dict, tool_result: str | None) -> str:
    """Format Write tool output."""
    file_path = tool_input.get('file_path', '')
    content = tool_input.get('content', '')
    emoji = TOOL_EMOJIS.get('Write', '📝')
    return f"{emoji} Created: {file_path}\n```\n{content}\n```"


def _format_edit(tool_input: dict, tool_result: str | None) -> str:
    """Format Edit tool output."""
    file_path = tool_input.get('file_path', '')
    old_string = tool_input.get('old_string', '')
    new_string = tool_input.get('new_string', '')

    # Try to extract line number from tool_result
    line_num = None
    if tool_result:
        for line in tool_result.split('\n'):
            match = LINE_NUMBER_PATTERN.match(line)
            if match and new_string.strip() in match.group(2):
                line_num = match.group(1)
                break

    emoji = TOOL_EMOJIS.get('Edit', '✏️')
    if line_num:
        return f"{emoji} Edited: {file_path}\n```\n{line_num}: - {old_string}\n{line_num}: + {new_string}\n```"
    else:
        return f"{emoji} Edited: {file_path}\n```\n- {old_string}\n+ {new_string}\n```"


def _format_read(tool_input: dict, tool_result: str | None) -> str:
    """Format Read tool output."""
    file_path = tool_input.get('file_path', '')
    emoji = TOOL_EMOJIS.get('Read', '👀')

    # Whitelist: text files to show content
    text_extensions = (
        '.txt', '.md', '.markdown',
        '.py', '.js', '.ts', '.jsx', '.tsx',
        '.html', '.css', '.scss', '.sass',
        '.json', '.yaml', '.yml', '.toml', '.ini', '.env',
        '.sh', '.bash', '.zsh',
        '.php', '.java', '.c', '.cpp', '.h', '.go', '.rs',
        '.xml', '.svg', '.sql',
        '.log', '.conf', '.config'
    )

    is_text_file = any(file_path.lower().endswith(ext) for ext in text_extensions)

    if not is_text_file:
        return f"{emoji} Read: {file_path}"

    if not isinstance(tool_result, str):
        return f"{emoji} Read: {file_path}"

    content = tool_result or ""
    content = clean_system_reminders(content)
    content = clean_line_numbers(content)

    # Truncate large content
    max_length = 2000
    if len(content) > max_length:
        truncated = content[:max_length]
        lines_count = content.count('\n')
        return f"{emoji} Read: {file_path}\n```\n{truncated}\n...\n(truncated, {lines_count} lines total)\n```"

    return f"{emoji} Read: {file_path}\n```\n{content}\n```"


def _format_bash(tool_input: dict, tool_result: str | None) -> str:
    """Format Bash tool output."""
    command = tool_input.get('command', '')
    emoji = TOOL_EMOJIS.get('Bash', '🔧')

    if not isinstance(tool_result, str):
        return f"{emoji} Bash: {command}"

    result = tool_result or ""
    result = clean_system_reminders(result)

    if result.strip():
        return f"{emoji} Bash: {command}\n```\n{result}\n```"
    else:
        return f"{emoji} Bash: {command}"


def _format_glob(tool_input: dict, tool_result: str | None) -> str:
    """Format Glob tool output."""
    pattern = tool_input.get('pattern', '')
    emoji = TOOL_EMOJIS.get('Glob', '🔍')
    return f"{emoji} Glob: {pattern}"


def _format_grep(tool_input: dict, tool_result: str | None) -> str:
    """Format Grep tool output."""
    pattern = tool_input.get('pattern', '')
    emoji = TOOL_EMOJIS.get('Grep', '🔍')
    return f"{emoji} Grep: {pattern}"


def _format_task(tool_input: dict, tool_result: str | None) -> str:
    """Format Task tool output."""
    description = tool_input.get('description', '')
    emoji = TOOL_EMOJIS.get('Task', '🤖')
    return f"{emoji} Agent: {description}"


# Tool formatter dispatch dictionary
TOOL_FORMATTERS = {
    'Write': _format_write,
    'Edit': _format_edit,
    'Read': _format_read,
    'Bash': _format_bash,
    'Glob': _format_glob,
    'Grep': _format_grep,
    'Task': _format_task,
}


def format_tool_action(tool_name: str, tool_input: dict, tool_result: str | None) -> str | None:
    """
    Format a single tool execution into a readable string.

    Uses a dispatch dictionary for clean, maintainable code.

    Args:
        tool_name: Name of the tool (Write, Edit, Bash, etc.)
        tool_input: Tool parameters from assistant event
        tool_result: Tool result from user event (optional)

    Returns:
        Formatted string or None if tool should be skipped
    """
    if tool_name == 'TodoWrite':
        return None

    formatter = TOOL_FORMATTERS.get(tool_name)
    if formatter:
        return formatter(tool_input, tool_result)

    return f"🔨 {tool_name}"
