"""
Configuration and environment variables for Pink Agent bot.
"""

import os
import logging
from dotenv import load_dotenv

# Load environment variables
load_dotenv()

# Telegram credentials
TELEGRAM_BOT_TOKEN = os.getenv("TELEGRAM_BOT_TOKEN")
TELEGRAM_USER_ID = int(os.getenv("TELEGRAM_USER_ID"))

# Verbose mode (for logging and singleton)
VERBOSE_MODE = os.getenv('VERBOSE') == '1'

# Singleton identifiers (for process detection)
SINGLETON_IDENTIFIERS = ['pink-agent', 'pink_agent', 'Pink Agent', 'Pink Claude Agent', 'Pink Telegram Agent']

# Logging configuration
# Production: startup/shutdown and errors only (INFO+)
# Verbose: full verbose logging (DEBUG)
log_level = logging.DEBUG if VERBOSE_MODE else logging.INFO
logging.basicConfig(
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s' if VERBOSE_MODE else '%(asctime)s - %(message)s',
    datefmt='%Y-%m-%d %H:%M:%S',
    level=log_level
)

# Silence noisy third-party loggers (always)
logging.getLogger('httpx').setLevel(logging.WARNING)
logging.getLogger('telegram').setLevel(logging.WARNING)
logging.getLogger('httpcore').setLevel(logging.WARNING)
logging.getLogger('watchfiles').setLevel(logging.WARNING)

# asyncio: silence in all modes (we log selector info ourselves per-process)
logging.getLogger('asyncio').setLevel(logging.WARNING)

logger = logging.getLogger(__name__)

# Claude Code session settings
AUTO_COMPACT_THRESHOLD = 180000  # tokens - trigger auto-compact when session context exceeds this
MAX_SESSION_CONTEXT = 200000  # tokens - hard limit for Claude Code context

# Claude Code execution settings
MAX_THINKING_TOKENS = 31999  # Maximum thinking tokens for Claude
NODE_HEAP_SIZE_MB = 8192  # Node.js heap size in MB (8GB)
CLAUDE_EXECUTION_TIMEOUT = 120  # Timeout in seconds for Claude Code commands (2 minutes)

# Telegram message settings
MAX_MESSAGE_LENGTH = 4000  # Telegram's message limit (with safety margin from 4096)

# Tool formatting
TOOL_EMOJIS = {
    'Write': '📝',
    'Edit': '✏️',
    'Read': '👀',
    'Bash': '🔧',
    'Glob': '🔍',
    'Grep': '🔍',
    'Task': '🤖',
}

# User-facing messages (English only)
MSG_READY = "Ready to work"
MSG_NEW_SESSION = "New session started. Previous context cleared."
MSG_COMPACT_STARTING = "🔄 Starting auto-compact... This will take 1-2 minutes."
MSG_COMPACT_SUCCESS = "✅ Auto-compact completed.\n\nNew session: {session_id}\nPrevious context saved in summary."
MSG_COMPACT_FAILED = "❌ Auto-compact failed: {error}\n\nUse /new to create a new session."
MSG_NO_SESSION = "❌ No active session found. Send a message first to create a session."
MSG_AUTO_COMPACT_NOTIFICATION = "🔄 Auto-compact started (context exceeded {threshold}k tokens).\n\nPlease wait 1-2 minutes, next commands will be processed after completion..."


def validate_config() -> None:
    """Validate that all required configuration is present."""
    if not TELEGRAM_BOT_TOKEN or not TELEGRAM_USER_ID:
        raise ValueError("Missing TELEGRAM_BOT_TOKEN or TELEGRAM_USER_ID in .env file")


def get_bot_started_message(bot_name: str) -> str:
    """Get bot started message with bot name."""
    return f"🦄 {bot_name} activated and ready to work"


def get_restart_message(bot_name: str) -> str:
    """Get restart message with bot name."""
    return f"🔄 Restarting {bot_name}..."


def get_claude_env() -> dict[str, str]:
    """
    Get environment variables for Claude Code execution.

    Returns:
        Dictionary with Claude-specific env vars configured
    """
    env = os.environ.copy()
    env['MAX_THINKING_TOKENS'] = str(MAX_THINKING_TOKENS)
    env['NODE_OPTIONS'] = f'--max-old-space-size={NODE_HEAP_SIZE_MB}'
    return env
