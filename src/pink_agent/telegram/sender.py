"""
Response processor task - send processed responses to Telegram.

Monitors responses.jsonl and sends responses back to user via Telegram.
"""

import asyncio
from telegram.ext import Application
from telegram import ReactionTypeEmoji
from telegram.error import BadRequest

from pink_agent.config import logger, TELEGRAM_USER_ID, MAX_MESSAGE_LENGTH
from pink_agent.queue.storage import read_first_response, delete_first_response, RESPONSES_QUEUE
from pink_agent.queue.watcher import QueueMonitor
from pink_agent.telegram.output import format_for_telegram, split_into_chunks


async def send_message_with_markdown_fallback(application: Application, chat_id: int, text: str,
                                                parse_mode: str = None, reply_to_message_id: int = None):
    """
    Send message with Markdown formatting, fallback to plain text if parsing fails.

    Args:
        application: Telegram application
        chat_id: Chat ID to send to
        text: Message text
        parse_mode: Parse mode (MarkdownV2 or None)
        reply_to_message_id: Optional message ID to reply to

    Raises:
        Exception: If send fails for non-parsing reasons
    """
    try:
        # Try to send with specified parse_mode
        await application.bot.send_message(
            chat_id=chat_id,
            text=text,
            reply_to_message_id=reply_to_message_id,
            parse_mode=parse_mode
        )
    except BadRequest as e:
        # Check if it's a Markdown parsing error
        if "can't parse entities" in str(e).lower() or "can't find end" in str(e).lower():
            logger.warning(f"Markdown parsing failed, retrying with plain text: {e}")
            # Retry with plain text (no formatting)
            await application.bot.send_message(
                chat_id=chat_id,
                text=text,
                reply_to_message_id=reply_to_message_id,
                parse_mode=None
            )
        else:
            # Other BadRequest errors - re-raise
            raise


async def response_sender_task(application: Application, monitor=None):
    """
    Event-driven response sender.

    Monitors responses.jsonl for responses and sends them to Telegram.

    Flow:
    1. Read first response from responses.jsonl
    2. Send to Telegram (reply to original message)
    3. Set ✅ reaction
    4. Delete response only after successful send (retry on failure)

    Args:
        application: Telegram application
        monitor: QueueMonitor instance (optional, for shutdown support)
    """
    logger.info("[Telegram] Response sender started")
    if monitor is None:
        monitor = QueueMonitor(RESPONSES_QUEUE)

    async def process_response():
        """Process a single response from queue."""
        try:
            response = read_first_response()

            if response is None:
                return

            message_id = response["message_id"]
            output = response["output"]

            try:
                if output.startswith("❌ Error:"):
                    telegram_text = output
                    parse_mode = None
                else:
                    telegram_text = format_for_telegram(output)
                    parse_mode = 'MarkdownV2'

                if len(telegram_text) <= MAX_MESSAGE_LENGTH:
                    await send_message_with_markdown_fallback(
                        application=application,
                        chat_id=TELEGRAM_USER_ID,
                        text=telegram_text,
                        reply_to_message_id=message_id,
                        parse_mode=parse_mode
                    )
                else:
                    chunks = split_into_chunks(telegram_text, max_length=MAX_MESSAGE_LENGTH)
                    for i, chunk in enumerate(chunks):
                        if i == 0:
                            await send_message_with_markdown_fallback(
                                application=application,
                                chat_id=TELEGRAM_USER_ID,
                                text=chunk,
                                reply_to_message_id=message_id,
                                parse_mode=parse_mode
                            )
                        else:
                            # Continuation chunks don't reply to original
                            if parse_mode == 'MarkdownV2':
                                continuation_prefix = f"\\(continued {i+1}/{len(chunks)}\\)\n\n"
                            else:
                                continuation_prefix = f"(continued {i+1}/{len(chunks)})\n\n"

                            await send_message_with_markdown_fallback(
                                application=application,
                                chat_id=TELEGRAM_USER_ID,
                                text=continuation_prefix + chunk,
                                parse_mode=parse_mode
                            )

                # Set done reaction (replaces progress reaction, may fail if message deleted)
                try:
                    await application.bot.set_message_reaction(
                        chat_id=TELEGRAM_USER_ID,
                        message_id=message_id,
                        reaction=[ReactionTypeEmoji("👍")]
                    )
                except Exception:
                    pass

                delete_first_response()

                if output.startswith("🎤"):
                    logger.debug(f"[Telegram] Transcription sent to user")
                else:
                    logger.debug(f"[Telegram] Response sent to user")

            except Exception as e:
                logger.error(f"[Telegram] Send failed: {e}")

        except Exception as e:
            logger.error(f"Response processor error: {e}")

    # Start monitoring with callback
    monitor.start(process_response)

    # Process any existing responses
    await process_response()

    # Wait indefinitely (will be cancelled on shutdown)
    try:
        await asyncio.Event().wait()
    except asyncio.CancelledError:
        raise
