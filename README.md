# pink-agent

Remote terminal control via Telegram with real-time WebSocket streaming.

## Installation

Download from [releases](https://github.com/pink-tools/pink-agent/releases):

```bash
curl -L https://github.com/pink-tools/pink-agent/releases/latest/download/pink-agent-darwin-arm64 -o pink-agent
chmod +x pink-agent
sudo mv pink-agent /usr/local/bin/
```

## Requirements

- [cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/)
- [pink-transcriber](https://github.com/pink-tools/pink-transcriber) (optional, for voice)

## Configuration

Create `~/pink-tools/pink-agent/.env`:

```bash
TELEGRAM_BOT_TOKEN=your_bot_token
TELEGRAM_USER_ID=your_user_id

# Named tunnel (request from admin)
TUNNEL_NAME=pink-yourname
TUNNEL_ID=your-tunnel-uuid
# + place credentials file: ~/.cloudflared/{TUNNEL_ID}.json
```

Get credentials:
1. Bot token: [@BotFather](https://t.me/botfather)
2. User ID: [@userinfobot](https://t.me/userinfobot)
3. Named tunnel — setup:
   ```bash
   cloudflared tunnel create pink-yourname
   cloudflared tunnel route dns pink-yourname pink-yourname.yourdomain.com
   ```
   Then set `TUNNEL_NAME=pink-yourname`, `TUNNEL_ID={uuid from create output}`

## Usage

```bash
pink-agent                              # Start daemon
pink-agent send "message"               # Send to Telegram
pink-agent send -f file.png             # Send file
pink-agent store list                   # List project files
pink-agent store add file.md "content"  # Add file
pink-agent store get file.md            # Read file
pink-agent --version                    # Show version
pink-agent --health                     # Check configuration
```

## Build from Source

```bash
git clone https://github.com/pink-tools/pink-agent.git
cd pink-agent
make build
```
