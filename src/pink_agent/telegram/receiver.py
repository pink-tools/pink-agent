"""
Incoming Telegram message handlers.

Handles all incoming messages from user:
- Text messages (immediate processing)
- Voice messages (transcription via pink-transcriber)
- File attachments (photos, documents, videos, audio)
"""

import os
import tempfile
from pathlib import Path
from telegram import Update
from telegram.ext import ContextTypes

from pink_agent.config import logger, TELEGRAM_USER_ID
from pink_agent.queue.storage import append_command, append_response
from pink_agent.telegram.output import set_reaction, remove_reaction
from pink_agent.telegram.files import (
    get_file_path,
    save_attachments,
    get_attachments,
    clear_attachments,
    format_attachments_prefix,
)


def is_authorized(update: Update) -> bool:
    """Check if the user is authorized to use the bot."""
    return update.effective_user.id == TELEGRAM_USER_ID


def get_reply_context(update: Update) -> str:
    """
    Extract reply context from message if it's a reply.

    Returns formatted reply context or empty string.
    Priority: quote (selected text) > full message text
    """
    if not update.message or not update.message.reply_to_message:
        return ""

    replied_msg = update.message.reply_to_message

    # Check if user selected specific text (quote)
    if update.message.quote and update.message.quote.text:
        quote_text = update.message.quote.text.strip()
        return f'[Reply: "{quote_text}"]\n\n'

    # Otherwise use full message text
    if replied_msg.text:
        replied_text = replied_msg.text.strip()
        return f'[Reply: "{replied_text}"]\n\n'

    # Reply to non-text message (photo, file, etc) - just indicate it's a reply
    if replied_msg.caption:
        caption_text = replied_msg.caption.strip()
        return f'[Reply to message with caption: "{caption_text}"]\n\n'

    return '[Reply to previous message]\n\n'


async def handle_file_message(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Handle incoming file attachments (photo, document, video, audio)."""
    if not is_authorized(update):
        return

    if update.message is None:
        return

    message = update.message
    message_id = message.message_id

    logger.debug(f"[Telegram] File message received")
    await set_reaction(context, message_id, "👀")

    files_to_download = []

    # Collect all files from message
    if message.photo:
        photo = message.photo[-1]
        file_name = f"photo_{photo.file_unique_id}.jpg"
        files_to_download.append((photo.file_id, file_name))

    if message.document:
        doc = message.document
        file_name = doc.file_name or f"document_{doc.file_unique_id}"
        files_to_download.append((doc.file_id, file_name))

    if message.video:
        video = message.video
        file_name = f"video_{video.file_unique_id}.mp4"
        files_to_download.append((video.file_id, file_name))

    if message.audio:
        audio = message.audio
        file_name = audio.file_name or f"audio_{audio.file_unique_id}.mp3"
        files_to_download.append((audio.file_id, file_name))

    if not files_to_download:
        return

    try:
        downloaded_paths = []

        for file_id, file_name in files_to_download:
            file_path = get_file_path(message_id, file_name)

            logger.debug(f"[Telegram] Downloading {file_name}...")
            file = await context.bot.get_file(file_id)
            await file.download_to_drive(file_path)

            downloaded_paths.append(str(file_path))
            logger.debug(f"[Telegram] Saved to {file_path}")

        # Check if message has caption or text
        user_text = message.caption or message.text

        if user_text:
            # Message with attachments - process immediately
            existing = get_attachments()
            all_paths = existing + downloaded_paths

            # Add reply context if present
            reply_context = get_reply_context(update)
            prompt = reply_context + format_attachments_prefix(all_paths) + "\n\n" + user_text
            clear_attachments()

            append_command(message_id, prompt)
        else:
            # Files only - save for next message
            existing = get_attachments()
            all_paths = existing + downloaded_paths
            save_attachments(all_paths)

            # Send confirmation
            count = len(downloaded_paths)
            file_word = "file" if count == 1 else "files"
            await message.reply_text(f"✅ {count} {file_word} saved")

        # Remove progress reaction
        await remove_reaction(context, message_id)

    except Exception as e:
        logger.error(f"[Telegram] File download error: {e}")
        append_response(message_id, f"❌ File download error: {e}")


async def handle_text_message(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Handle incoming text messages."""
    if not is_authorized(update):
        return

    # Ignore edited messages (update.message is None for edits)
    if update.message is None:
        return

    message_id = update.message.message_id
    text = update.message.text

    logger.debug(f"[Telegram] Text message received: {text}")
    await set_reaction(context, message_id, "👀")

    # Build prompt with reply context and attachments
    reply_context = get_reply_context(update)

    # Check for attachments from previous messages
    attachments = get_attachments()
    if attachments:
        prompt = reply_context + format_attachments_prefix(attachments) + "\n\n" + text
        clear_attachments()
    else:
        prompt = reply_context + text

    append_command(message_id, prompt)


async def handle_voice_message(update: Update, context: ContextTypes.DEFAULT_TYPE) -> None:
    """Handle incoming voice messages."""
    if not is_authorized(update):
        return

    voice = update.message.voice
    message_id = update.message.message_id

    logger.debug(f"[Telegram] Voice message received ({voice.duration}s)")
    await set_reaction(context, message_id, "👀")

    # Use system temp directory for voice files (auto-cleaned by OS)
    ogg_dir = Path(tempfile.gettempdir()) / "pink-agent"
    ogg_dir.mkdir(parents=True, exist_ok=True)
    ogg_path = ogg_dir / f"{message_id}.ogg"

    try:
        logger.debug(f"[Telegram] Downloading voice...")
        file = await context.bot.get_file(voice.file_id)
        await file.download_to_drive(ogg_path)

        logger.debug(f"[Telegram] Transcribing via pink-transcriber...")
        from pink_agent.telegram.transcriber import transcribe
        transcribed_text = transcribe(str(ogg_path))
        os.remove(ogg_path)

        logger.debug(f"[Telegram] Transcription: {transcribed_text}")

        voice_prefix = "[Voice input - if anything sounds unclear or nonsensical, please ask for clarification]"

        # Build prompt with reply context and attachments
        reply_context = get_reply_context(update)

        # Check for attachments from previous messages
        attachments = get_attachments()
        if attachments:
            prompt = f"{reply_context}{voice_prefix}\n\n{format_attachments_prefix(attachments)}\n\n{transcribed_text}"
            clear_attachments()
        else:
            prompt = f"{reply_context}{voice_prefix}\n\n{transcribed_text}"

        # Add command to queue first (before Telegram send that might fail)
        append_command(message_id, prompt)

        # Send transcription directly (not via responses.jsonl)
        # Split into chunks if too long for Telegram
        from pink_agent.telegram.output import split_into_chunks
        transcription_message = f"🎤 {transcribed_text}"

        try:
            chunks = split_into_chunks(transcription_message)
            for chunk in chunks:
                await update.message.reply_text(chunk)
        except Exception as send_error:
            logger.warning(f"[Telegram] Failed to send transcription: {send_error}")

    except Exception as e:
        logger.error(f"[Telegram] Voice processing error: {e}")
        append_response(message_id, f"❌ Voice processing error: {e}")
