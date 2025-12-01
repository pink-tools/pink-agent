"""
Outgoing Telegram messages and reactions.

Handles all outgoing communication to user:
- Send messages (with Markdown formatting)
- Send errors
- Set/remove reactions (progress indicators)
- Split long messages into chunks
"""

from telegram import ReactionTypeEmoji
from telegram.ext import ContextTypes
from telegramify_markdown import markdownify

from pink_agent.config import TELEGRAM_USER_ID, logger, MAX_MESSAGE_LENGTH


def format_for_telegram(text: str) -> str:
    """Convert markdown text to Telegram MarkdownV2 format."""
    return markdownify(text)


def split_into_chunks(text: str, max_length: int = MAX_MESSAGE_LENGTH) -> list:
    """
    Split text into chunks, preserving code blocks.

    If forced to split inside code block, closes ``` at end and reopens at start of next chunk.
    """
    chunks = []
    remaining = text

    while remaining:
        if len(remaining) <= max_length:
            chunks.append(remaining)
            break

        split_pos = max_length

        # Try double newline first, then single newline (but only outside code blocks)
        double_newline = remaining.rfind('\n\n', 0, max_length)
        if double_newline != -1:
            backticks_count = remaining[:double_newline].count('```')
            if backticks_count % 2 == 0:
                split_pos = double_newline
            else:
                double_newline = -1

        if double_newline == -1:
            newline = remaining.rfind('\n', 0, max_length)
            if newline != -1:
                backticks_count = remaining[:newline].count('```')
                if backticks_count % 2 == 0:
                    split_pos = newline

        backticks_before_split = remaining[:split_pos].count('```')
        inside_code_block = backticks_before_split % 2 == 1

        chunk = remaining[:split_pos].rstrip()

        if inside_code_block:
            chunk += '\n```'

        chunks.append(chunk)

        remaining = remaining[split_pos:].lstrip()

        if inside_code_block and remaining:
            remaining = '```\n' + remaining

    return chunks


async def send_error(context: ContextTypes.DEFAULT_TYPE, error: str, reply_to_message_id: int | None = None) -> None:
    """Send error message to Telegram."""
    error_text = str(error)
    if len(error_text) > 1000:
        error_text = error_text[:1000] + "...\n(error truncated)"

    try:
        await context.bot.send_message(
            chat_id=TELEGRAM_USER_ID,
            text=f"❌ Error: {error_text}",
            reply_to_message_id=reply_to_message_id
        )
    except Exception:
        await context.bot.send_message(
            chat_id=TELEGRAM_USER_ID,
            text=f"❌ Error: {error_text}"
        )


async def set_reaction(context: ContextTypes.DEFAULT_TYPE, message_id: int, emoji: str) -> None:
    """Set a reaction on a message."""
    try:
        await context.bot.set_message_reaction(
            chat_id=TELEGRAM_USER_ID,
            message_id=message_id,
            reaction=[ReactionTypeEmoji(emoji)],
            is_big=False
        )
    except Exception as e:
        logger.warning(f"Failed to set reaction: {e}")


async def remove_reaction(context: ContextTypes.DEFAULT_TYPE, message_id: int) -> None:
    """Set done reaction (thumbs up) on a message, replacing progress reaction."""
    try:
        await context.bot.set_message_reaction(
            chat_id=TELEGRAM_USER_ID,
            message_id=message_id,
            reaction=[ReactionTypeEmoji("👍")]
        )
    except Exception as e:
        logger.warning(f"Failed to set done reaction: {e}")
