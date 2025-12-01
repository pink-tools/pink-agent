#!/usr/bin/env python3
"""
Pink Claude Agent - Claude Code execution agent.

Entry point for the Claude Code agent daemon.
Runs independently from Telegram agent.
"""

# Set process title early
try:
    import setproctitle
    setproctitle.setproctitle('Pink Claude Agent')
except ImportError:
    pass

import asyncio
from pink_agent.config import logger
from pink_agent.queue.storage import ensure_queue_files, COMMANDS_QUEUE
from pink_agent.queue.watcher import QueueMonitor
from pink_agent.claude.processor import command_processor_task


def main() -> None:
    """Start the Claude Code worker."""
    import sys
    from pink_agent.config import VERBOSE_MODE

    if VERBOSE_MODE:
        logger.debug("[Claude] Development mode enabled - verbose logging active")

    logger.info("[Claude] Starting...")

    ensure_queue_files()
    monitor = QueueMonitor(COMMANDS_QUEUE)

    try:
        asyncio.run(command_processor_task(monitor=monitor))
    except KeyboardInterrupt:
        pass
    finally:
        monitor.cleanup()


if __name__ == "__main__":
    main()
