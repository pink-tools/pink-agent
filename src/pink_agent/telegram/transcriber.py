"""
Voice transcription using external pink-transcriber service.
"""

import subprocess
from pathlib import Path


def check_service() -> bool:
    """
    Check if pink-transcriber service is available.

    Returns:
        True if service is running and accessible, False otherwise
    """
    try:
        result = subprocess.run(
            ['pink-transcriber', '--health'],
            capture_output=True,
            text=True,
            timeout=5
        )
        return result.returncode == 0
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return False


def transcribe(audio_path: str) -> str:
    """
    Transcribe audio file to text using pink-transcriber service.

    Args:
        audio_path: Path to audio file (OGG format from Telegram)

    Returns:
        Transcribed text

    Raises:
        FileNotFoundError: If audio file doesn't exist
        RuntimeError: If transcription fails or service is unavailable
    """
    audio_file = Path(audio_path)

    if not audio_file.exists():
        raise FileNotFoundError(f"Audio file not found: {audio_path}")

    try:
        result = subprocess.run(
            ['pink-transcriber', str(audio_path)],
            capture_output=True,
            text=True,
            timeout=120  # 2 minutes timeout for transcription (handles long voice messages)
        )

        if result.returncode != 0:
            error_msg = result.stderr.strip() if result.stderr else "Unknown error"
            raise RuntimeError(f"Transcription failed: {error_msg}")

        transcribed_text = result.stdout.strip()

        if not transcribed_text:
            raise RuntimeError("Transcription returned empty result")

        return transcribed_text

    except FileNotFoundError:
        raise RuntimeError(
            "pink-transcriber service not found. "
            "Make sure pink-transcriber is installed and running."
        )
    except subprocess.TimeoutExpired:
        raise RuntimeError("Transcription timeout (>2 minutes) - voice message too long or service stuck")
    except Exception as e:
        raise RuntimeError(f"Transcription failed: {e}")
