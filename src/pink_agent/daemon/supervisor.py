#!/usr/bin/env python3
"""
Simple supervisor for pink-agent processes.
Manages two agents and handles graceful shutdown.
"""

import subprocess
import signal
import sys
import os
import time


class Supervisor:
    def __init__(self):
        self.claude_process = None
        self.telegram_process = None
        self.shutting_down = False

    def start(self):
        """Start both processes."""
        print("[Supervisor] Starting processes...")

        self.claude_process = subprocess.Popen(
            ["pink-claude-agent"],
            stdout=sys.stdout,
            stderr=sys.stderr
        )
        print(f"[Supervisor] Claude agent started (PID: {self.claude_process.pid})")

        self.telegram_process = subprocess.Popen(
            ["pink-telegram-agent"],
            stdout=sys.stdout,
            stderr=sys.stderr
        )
        print(f"[Supervisor] Telegram agent started (PID: {self.telegram_process.pid})")
        print("\n[Supervisor] Press Ctrl+C to stop\n")

    def shutdown(self, signum=None, frame=None):
        """Graceful shutdown."""
        if self.shutting_down:
            return

        self.shutting_down = True
        print("\n[Supervisor] Shutting down...")

        if self.telegram_process:
            print(f"[Supervisor] Stopping Telegram agent...")
            self.telegram_process.send_signal(signal.SIGINT)
            self.telegram_process.wait()
            print("[Supervisor] Telegram agent stopped")

        if self.claude_process:
            print(f"[Supervisor] Stopping Claude agent...")
            self.claude_process.send_signal(signal.SIGINT)
            self.claude_process.wait()
            print("[Supervisor] Claude agent stopped")

        print("\n[Supervisor] All processes stopped\n")
        sys.exit(0)

    def restart_handler(self, signum=None, frame=None):
        """Handle restart signal (SIGUSR1 on Unix, not available on Windows)."""
        if self.shutting_down:
            return

        print("\n[Supervisor] Restart requested...")
        self.shutting_down = True

        if self.telegram_process:
            print(f"[Supervisor] Stopping Telegram agent...")
            self.telegram_process.send_signal(signal.SIGINT)
            self.telegram_process.wait()

        if self.claude_process:
            print(f"[Supervisor] Stopping Claude agent...")
            self.claude_process.send_signal(signal.SIGINT)
            self.claude_process.wait()

        # Update code from git
        print("[Supervisor] Updating code (git pull)...")
        try:
            result = subprocess.run(
                ['git', 'pull'],
                cwd=os.path.dirname(os.path.dirname(os.path.dirname(__file__))),
                capture_output=True,
                text=True,
                timeout=30
            )
            if result.returncode == 0:
                print(f"[Supervisor] Git pull: {result.stdout.strip()}")
            else:
                print(f"[Supervisor] Git pull failed: {result.stderr.strip()}")
        except Exception as e:
            print(f"[Supervisor] Git pull error: {e}")

        # Update dependencies
        print("[Supervisor] Updating dependencies (uv sync)...")
        try:
            result = subprocess.run(
                ['uv', 'sync'],
                cwd=os.path.dirname(os.path.dirname(os.path.dirname(__file__))),
                capture_output=True,
                text=True,
                timeout=60
            )
            if result.returncode == 0:
                print("[Supervisor] Dependencies updated")
            else:
                print(f"[Supervisor] uv sync failed: {result.stderr.strip()}")
        except Exception as e:
            print(f"[Supervisor] uv sync error: {e}")

        print("[Supervisor] Restarting...\n")

        # On macOS, restart through caffeinate to prevent sleep
        if sys.platform == 'darwin':
            os.execv('/usr/bin/caffeinate', ['caffeinate', '-id', sys.executable] + sys.argv)
        else:
            os.execv(sys.executable, [sys.executable] + sys.argv)

    def run(self):
        """Main loop."""
        # Register signal handlers
        signal.signal(signal.SIGINT, self.shutdown)
        signal.signal(signal.SIGTERM, self.shutdown)

        # SIGUSR1 only available on Unix-like systems
        if hasattr(signal, 'SIGUSR1'):
            signal.signal(signal.SIGUSR1, self.restart_handler)

        # Start processes
        self.start()

        while not self.shutting_down:
            if self.claude_process.poll() is not None:
                print(f"\n[Supervisor] Claude agent died (exit code: {self.claude_process.returncode})")
                self.shutdown()
                break

            if self.telegram_process.poll() is not None:
                print(f"\n[Supervisor] Telegram agent died (exit code: {self.telegram_process.returncode})")
                self.shutdown()
                break

            time.sleep(0.5)


def main():
    """Entry point for pink-agent command."""
    # Check for subcommands
    if len(sys.argv) > 1 and sys.argv[1] == 'send':
        from pink_agent.cli.send import send_main
        send_main()
        return

    # Set process title for daemon (not for CLI commands)
    try:
        import setproctitle
        setproctitle.setproctitle('Pink Agent Supervisor')
    except ImportError:
        pass

    # Ensure only one instance of supervisor runs
    from pink_agent.daemon.singleton import ensure_single_instance
    ensure_single_instance('pink-agent')

    # Default: run supervisor
    supervisor = Supervisor()
    supervisor.run()


if __name__ == "__main__":
    main()
