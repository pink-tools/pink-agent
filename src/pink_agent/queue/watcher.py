"""
Cross-platform file monitoring for JSONL queues.

Event-driven monitoring using watchfiles library (Rust-based, works on macOS, Linux, Windows).
"""

import asyncio
from pathlib import Path
from watchfiles import awatch


class QueueMonitor:
    """
    Monitor JSONL queue file for modifications using watchfiles.

    High-performance cross-platform file monitoring integrated with asyncio event loop.
    Based on Rust notify library for minimal overhead.
    """

    def __init__(self, file_path: Path):
        """
        Initialize queue monitor.

        Args:
            file_path: Path to JSONL file to monitor
        """
        self.file_path = file_path
        self._watcher_task = None
        self._stop_event = None
        self._initialized = False
        self._callback = None

    def start(self, callback):
        """
        Start monitoring file changes.

        Args:
            callback: Async function to call when file changes
        """
        if self._initialized:
            return

        self._callback = callback
        self._stop_event = asyncio.Event()

        # Ensure file exists
        self.file_path.touch(exist_ok=True)

        # Start watcher task
        self._watcher_task = asyncio.create_task(self._watch_loop())

        self._initialized = True

    async def _watch_loop(self):
        """Watch file for changes and call callback."""
        try:
            async for changes in awatch(
                self.file_path.parent,
                stop_event=self._stop_event
            ):
                # Filter changes to only our file
                for change_type, path in changes:
                    if Path(path) == self.file_path:
                        await self._callback()
                        break  # Only call callback once per batch
        except asyncio.CancelledError:
            pass

    def shutdown(self):
        """Stop monitoring."""
        if self._stop_event and self._initialized:
            self._stop_event.set()

        if self._watcher_task and not self._watcher_task.done():
            self._watcher_task.cancel()

    def cleanup(self):
        """Clean up resources (synchronous)."""
        self.shutdown()
        self._initialized = False

    async def cleanup_async(self):
        """Clean up resources (async - waits for watcher task to finish)."""
        self.shutdown()

        if self._watcher_task:
            try:
                await asyncio.wait_for(self._watcher_task, timeout=1.0)
            except (asyncio.TimeoutError, asyncio.CancelledError):
                pass

        self._initialized = False
