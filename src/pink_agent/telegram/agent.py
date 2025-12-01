#!/usr/bin/env python3
"""
Pink Telegram Agent - Telegram agent daemon.

Entry point for the Telegram agent.
Runs two asyncio tasks:
1. Receiver: Telegram polling → commands.jsonl
2. Sender: responses.jsonl → Telegram (with infinite retry)
"""

# Set process title early
try:
    import setproctitle
    setproctitle.setproctitle('Pink Telegram Agent')
except ImportError:
    pass

import asyncio
from telegram import Update, BotCommand
from telegram.ext import Application, CommandHandler, MessageHandler, filters

from pink_agent.config import logger, TELEGRAM_BOT_TOKEN, validate_config, get_bot_started_message, TELEGRAM_USER_ID
from pink_agent.telegram.commands import start, new, compact, restart
from pink_agent.telegram.receiver import handle_text_message, handle_voice_message, handle_file_message
from pink_agent.telegram.transcriber import check_service
from pink_agent.queue.storage import ensure_queue_files, clear_commands, reset_interrupted_responses, COMMANDS_QUEUE, RESPONSES_QUEUE
from pink_agent.telegram.sender import response_sender_task
from pink_agent.queue.watcher import QueueMonitor


def create_application() -> Application:
    """Create and configure the Telegram agent application."""
    validate_config()

    application = Application.builder().token(TELEGRAM_BOT_TOKEN).build()

    # Register handlers
    application.add_handler(CommandHandler("start", start))
    application.add_handler(CommandHandler("new", new))
    application.add_handler(CommandHandler("compact", compact))
    application.add_handler(CommandHandler("restart", restart))
    application.add_handler(MessageHandler(filters.VOICE, handle_voice_message))
    application.add_handler(MessageHandler(filters.PHOTO | filters.Document.ALL | filters.VIDEO | filters.AUDIO, handle_file_message))
    application.add_handler(MessageHandler(filters.TEXT & ~filters.COMMAND, handle_text_message))

    return application


async def post_init(application: Application) -> None:
    """Called after the agent is initialized."""
    from telegram.error import TimedOut, NetworkError

    logger.info("[Telegram] Agent ready")

    ensure_queue_files()
    clear_commands()
    reset_interrupted_responses()

    response_monitor = QueueMonitor(RESPONSES_QUEUE)
    application.bot_data['response_monitor'] = response_monitor

    response_task = asyncio.create_task(response_sender_task(application, monitor=response_monitor))
    application.bot_data['response_task'] = response_task

    # Get bot name
    bot_info = await application.bot.get_me()
    bot_name = bot_info.first_name
    application.bot_data['bot_name'] = bot_name

    for attempt in range(3):
        try:
            await application.bot.set_my_commands([
                BotCommand("new", "New session"),
                BotCommand("compact", "Continue with summary"),
                BotCommand("restart", f"Restart {bot_name}")
            ])
            break
        except (TimedOut, NetworkError) as e:
            if attempt == 2:
                logger.error(f"[Telegram] Could not register commands: {e}")

    for attempt in range(3):
        try:
            await application.bot.send_message(
                chat_id=TELEGRAM_USER_ID,
                text=get_bot_started_message(bot_name)
            )
            break
        except (TimedOut, NetworkError) as e:
            if attempt == 2:
                logger.error(f"[Telegram] Could not send greeting: {e}")


async def post_shutdown(application: Application) -> None:
    """Called when the agent is shutting down."""
    import sys

    response_monitor = application.bot_data.get('response_monitor')
    response_task = application.bot_data.get('response_task')

    if response_monitor:
        response_monitor.shutdown()

    if response_task:
        response_task.cancel()
        await asyncio.gather(response_task, return_exceptions=True)

    if response_monitor:
        await response_monitor.cleanup_async()


async def run_agent():
    """Run the agent with proper async shutdown."""
    import signal as signal_module
    from pink_agent.config import VERBOSE_MODE

    application = create_application()
    stop_event = asyncio.Event()

    # Setup signal handlers
    loop = asyncio.get_running_loop()

    # Log selector info in dev mode
    if VERBOSE_MODE:
        selector = loop._selector if hasattr(loop, '_selector') else None
        if selector:
            logger.debug(f"[Telegram] Using selector: {selector.__class__.__name__}")

    for sig in (signal_module.SIGINT, signal_module.SIGTERM):
        loop.add_signal_handler(sig, stop_event.set)

    # Initialize
    await application.initialize()
    await post_init(application)

    # Start polling
    await application.start()
    await application.updater.start_polling(allowed_updates=Update.ALL_TYPES)

    # Run until stopped
    try:
        await stop_event.wait()
    finally:
        # Cleanup
        await post_shutdown(application)
        await application.updater.stop()
        await application.stop()
        await application.shutdown()


def main() -> None:
    """Start the Telegram agent."""
    import sys
    from pink_agent.config import VERBOSE_MODE

    if VERBOSE_MODE:
        logger.debug("[Telegram] Development mode enabled - verbose logging active")

    logger.info("[Telegram] Starting...")

    if not check_service():
        logger.warning("[Telegram] pink-transcriber service is not available - voice messages will fail until service starts")

    asyncio.run(run_agent())

    sys.exit(0)


if __name__ == "__main__":
    main()
