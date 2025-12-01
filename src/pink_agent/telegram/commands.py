"""
Telegram bot command handlers (/start, /new, /compact, /restart).
"""

import os
import signal
from telegram import Update
from telegram.ext import ContextTypes

from pink_agent.config import (
    logger,
    TELEGRAM_USER_ID,
    MSG_READY,
    MSG_NEW_SESSION,
    MSG_COMPACT_STARTING,
    MSG_COMPACT_SUCCESS,
    MSG_COMPACT_FAILED,
    MSG_NO_SESSION,
    get_restart_message,
)


def is_authorized(update: Update) -> bool:
    """Check if the user is authorized to use the bot."""
    return update.effective_user.id == TELEGRAM_USER_ID


async def start(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Handle /start command."""
    if not is_authorized(update):
        return

    await update.message.reply_text(MSG_READY)


async def new(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Handle /new command."""
    if not is_authorized(update):
        return

    try:
        from pink_agent.claude.sessions import reset_session
        reset_session()
        await update.message.reply_text(MSG_NEW_SESSION)
    except Exception as e:
        logger.error(f"[Telegram] Failed to reset session: {e}")
        await update.message.reply_text(f"Failed to reset session: {e}")


async def compact(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Handle /compact command - manually trigger auto-compact."""
    if not is_authorized(update):
        return

    try:
        from pink_agent.claude.sessions import read_session_id
        from pink_agent.claude.compact import perform_auto_compact

        session_id = read_session_id()
        if not session_id:
            await update.message.reply_text(MSG_NO_SESSION)
            return

        await update.message.reply_text(MSG_COMPACT_STARTING)

        new_session_id = perform_auto_compact(session_id)

        await update.message.reply_text(MSG_COMPACT_SUCCESS.format(session_id=f"{new_session_id[:8]}..."))
    except Exception as e:
        logger.error(f"[Telegram] Compact failed: {e}")
        await update.message.reply_text(MSG_COMPACT_FAILED.format(error=str(e)))


async def restart(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Handle /restart command."""
    if not is_authorized(update):
        return

    bot_name = context.bot_data.get('bot_name', 'Bot')
    await update.message.reply_text(get_restart_message(bot_name))

    parent_pid = os.getppid()
    os.kill(parent_pid, signal.SIGUSR1)
