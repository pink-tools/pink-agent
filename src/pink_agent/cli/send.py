"""
Send command - send messages and files to user via Telegram Bot API.

Usage:
    pink-agent send "text message"
    pink-agent send -f file.png
    pink-agent send "caption" -f file1.png -f file2.jpg
"""

import argparse
import sys
import os
import mimetypes
import requests
from pathlib import Path


def load_config():
    """Load bot configuration from .env file."""
    from dotenv import load_dotenv

    # Find .env in pink-agent root
    config_path = Path(__file__).parent.parent.parent.parent / '.env'
    if not config_path.exists():
        print(f"Error: .env file not found at {config_path}", file=sys.stderr)
        sys.exit(1)

    load_dotenv(config_path)

    token = os.getenv('TELEGRAM_BOT_TOKEN')
    user_id = os.getenv('TELEGRAM_USER_ID')

    if not token or not user_id:
        print("Error: TELEGRAM_BOT_TOKEN or TELEGRAM_USER_ID not found in .env", file=sys.stderr)
        sys.exit(1)

    return token, int(user_id)


def send_text(token: str, chat_id: int, text: str):
    """Send text message via Bot API."""
    url = f"https://api.telegram.org/bot{token}/sendMessage"
    data = {
        'chat_id': chat_id,
        'text': text,
    }

    response = requests.post(url, json=data)

    if not response.ok:
        print(f"Error sending text: {response.text}", file=sys.stderr)
        sys.exit(1)


def send_file(token: str, chat_id: int, file_path: str, caption: str = None):
    """Send file via Bot API (auto-detect photo/document)."""
    path = Path(file_path)

    if not path.exists():
        print(f"Error: file not found: {file_path}", file=sys.stderr)
        sys.exit(1)

    # Determine MIME type
    mime_type, _ = mimetypes.guess_type(str(path))

    # Use sendPhoto for images, sendDocument for everything else
    if mime_type and mime_type.startswith('image/'):
        endpoint = 'sendPhoto'
        file_field = 'photo'
    else:
        endpoint = 'sendDocument'
        file_field = 'document'

    url = f"https://api.telegram.org/bot{token}/{endpoint}"

    data = {'chat_id': chat_id}
    if caption:
        data['caption'] = caption

    with open(path, 'rb') as f:
        files = {file_field: f}
        response = requests.post(url, data=data, files=files)

    if not response.ok:
        print(f"Error sending file {file_path}: {response.text}", file=sys.stderr)
        sys.exit(1)


def send_main():
    """Entry point for pink-agent send subcommand."""
    parser = argparse.ArgumentParser(
        prog='pink-agent send',
        description='Send messages and files to user via Telegram bot'
    )
    parser.add_argument(
        'text',
        nargs='?',
        help='Text message or caption for files'
    )
    parser.add_argument(
        '-f', '--file',
        action='append',
        dest='files',
        help='File to send (can be repeated for multiple files)'
    )

    # Parse args starting from sys.argv[2] (skip 'pink-agent' and 'send')
    args = parser.parse_args(sys.argv[2:])

    # Load config
    token, user_id = load_config()

    # Validate input
    if not args.text and not args.files:
        print("Error: provide either text or files", file=sys.stderr)
        parser.print_help()
        sys.exit(1)

    # Send files
    if args.files:
        for i, file_path in enumerate(args.files):
            # Only first file gets caption
            caption = args.text if i == 0 and args.text else None
            send_file(token, user_id, file_path, caption)

    # Send text-only message if no files
    elif args.text:
        send_text(token, user_id, args.text)


