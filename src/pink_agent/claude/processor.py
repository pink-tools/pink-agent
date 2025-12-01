"""
Command processor task - sequential execution of queued commands.

CRITICAL: Only ONE command is processed at a time to prevent
Claude Code session conflicts.

IMPORTANT: Commands are deleted IMMEDIATELY after reading to prevent
infinite loops when bot is killed/restarted. A detached subprocess
handles execution and writes results independently.
"""

import asyncio
import multiprocessing

# CRITICAL: Use 'spawn' instead of 'fork' to avoid inheriting
# the Application polling loop into subprocess (causes Telegram API conflicts)
try:
    multiprocessing.set_start_method('spawn')
except RuntimeError:
    pass  # Already set

from pink_agent.config import (
    logger,
    AUTO_COMPACT_THRESHOLD,
    MSG_AUTO_COMPACT_NOTIFICATION,
    MSG_COMPACT_SUCCESS,
    MSG_COMPACT_FAILED,
)
from pink_agent.queue.storage import read_first_command, delete_first_command, append_response, COMMANDS_QUEUE
from pink_agent.queue.watcher import QueueMonitor
from pink_agent.claude.executor import execute_claude
from pink_agent.claude.compact import perform_auto_compact


def _execute_command_subprocess(message_id: int, content: str) -> None:
    """
    Execute command in subprocess (runs independently).

    Survives bot restarts - continues execution even if main bot is killed.
    """
    try:
        result, session_context_size, session_id = execute_claude(content)

        append_response(message_id, result)
        logger.debug(f"[Claude] Response queued")

        if session_context_size > AUTO_COMPACT_THRESHOLD:
            logger.warning(f"[Claude] Context exceeded threshold: {session_context_size}/{AUTO_COMPACT_THRESHOLD}")

            append_response(message_id, MSG_AUTO_COMPACT_NOTIFICATION.format(threshold=AUTO_COMPACT_THRESHOLD//1000))

            try:
                new_session_id = perform_auto_compact(session_id)
                append_response(message_id, MSG_COMPACT_SUCCESS.format(session_id=f"{new_session_id[:8]}..."))
                logger.debug(f"[Claude] Auto-compact completed")
            except Exception as e:
                logger.error(f"[Claude] Auto-compact failed: {e}")
                append_response(message_id, MSG_COMPACT_FAILED.format(error=str(e)))

    except Exception as e:
        error_msg = str(e)
        logger.error(f"[Claude] Execution failed: {error_msg}")
        append_response(message_id, f"❌ Error: {error_msg}")


async def command_processor_task(monitor=None):
    """
    Event-driven command processor.

    Processes commands ONE AT A TIME in detached subprocesses.

    CRITICAL: Commands are deleted BEFORE execution starts to prevent
    infinite loops when bot is killed during execution.
    """
    import signal
    from pink_agent.config import VERBOSE_MODE

    # Log selector info in dev mode
    if VERBOSE_MODE:
        loop = asyncio.get_running_loop()
        selector = loop._selector if hasattr(loop, '_selector') else None
        if selector:
            logger.debug(f"[Claude] Using selector: {selector.__class__.__name__}")

    logger.info("[Claude] Command processor started")
    if monitor is None:
        monitor = QueueMonitor(COMMANDS_QUEUE)

    stop_event = asyncio.Event()
    loop = asyncio.get_running_loop()
    current_process = None

    async def process_command():
        """Process a single command from queue."""
        nonlocal current_process

        try:
            command = read_first_command()

            if command is None:
                return

            message_id = command["message_id"]
            content = command["content"]

            logger.debug(f"[Claude] Executing command")

            delete_first_command()

            current_process = multiprocessing.Process(
                target=_execute_command_subprocess,
                args=(message_id, content),
                daemon=False
            )
            current_process.start()

            await loop.run_in_executor(None, current_process.join)
            current_process = None

        except Exception as e:
            logger.error(f"Command processor error: {e}")

    def signal_handler():
        """Handle shutdown signals."""
        if current_process and current_process.is_alive():
            current_process.terminate()
        monitor.shutdown()
        stop_event.set()

    loop.add_signal_handler(signal.SIGINT, signal_handler)
    loop.add_signal_handler(signal.SIGTERM, signal_handler)

    # Start monitoring with callback
    monitor.start(process_command)

    # Process any existing commands
    await process_command()

    # Wait for shutdown
    await stop_event.wait()
