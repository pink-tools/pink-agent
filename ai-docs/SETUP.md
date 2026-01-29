# Pink Agent - Setup & Deployment

## Prerequisites

### Required Software

```bash
# Claude Code CLI
# Install from: https://docs.anthropic.com/claude-code

# Cloudflared (for tunnel)
brew install cloudflared

# Go (for building)
brew install go

# Bun (for frontend)
brew install oven-sh/bun/bun
```

### Telegram Bot

1. Message [@BotFather](https://t.me/BotFather)
2. `/newbot` → follow prompts
3. Copy the bot token
4. `/setmenubutton` → select your bot → enter Mini App URL (after deployment)

### Get Your User ID

Message [@userinfobot](https://t.me/userinfobot) → it will reply with your ID.

## Installation

### Backend

```bash
git clone git@github.com:pink-tools/pink-agent.git
cd pink-agent
go build -o pink-agent ./cmd/pink-agent
```

### Frontend

```bash
# Clone if not exists
git clone git@github.com:pink-tools/pink-agent-ui.git ~/Desktop/_claude/pink-agent-ui

cd ~/Desktop/_claude/pink-agent-ui
bun install
```

## Configuration

### Create Config Directory

```bash
mkdir -p ~/pink-tools/pink-agent
```

### Create .env File

```bash
cat > ~/pink-tools/pink-agent/.env << 'EOF'
TELEGRAM_BOT_TOKEN=your_bot_token_here
TELEGRAM_USER_ID=your_telegram_user_id
EOF
```

### Optional: MCP Config

For MCP integration, create `~/pink-tools/pink-agent/mcp-config.json`:

Pink-agent passes this config to Claude via `claude --mcp-config`.

```json
{
  "mcpServers": {
    "telegram": {
      "type": "stdio",
      "command": "uv",
      "args": ["--directory", "/path/to/pink-telegram-mcp", "run", "main.py"]
    }
  }
}
```

## Running

### Production

```bash
./pink-agent
```

This will:
1. Start Telegram bot
2. Create cloudflared tunnel
3. Set Mini App menu button
4. Wait for connections

### Development

#### Quick Start

```bash
# Terminal 1: Backend (dev mode)
cd ~/Desktop/_claude/pink-agent
go build -o ~/pink-tools/pink-agent/pink-agent ./cmd/pink-agent && ENVIRONMENT=development ~/pink-tools/pink-agent/pink-agent

# Terminal 2: Frontend
cd ~/Desktop/_claude/pink-agent-ui
bun dev
```

Open: `http://localhost:5173/?api=https://pink-mir.pinkhaired.com` (or your tunnel URL)

#### What ENVIRONMENT=development enables

1. `/dev/ws` endpoint available without Telegram auth (for local UI testing)
2. Logs `dev_url` with localhost URL

**Note:** `ENVIRONMENT=development` is passed at runtime, not stored in `.env`. Default is production.

#### Testing without UI

Connect to WebSocket directly:
```bash
# Quick test with websocat
websocat ws://localhost:7466/dev/ws
```

Send JSON commands:
```json
{"type":"sync"}
{"type":"create_project","name":"Test"}
```

#### Rebuild after code changes

```bash
go build -o ~/pink-tools/pink-agent/pink-agent ./cmd/pink-agent
# Then restart pink-agent
```

## Frontend Deployment (Hetzner)

Frontend is hosted on Hetzner server as Docker container.

### Deploy

```bash
ssh hetzner "/home/websites/pink-agent.pinkhaired.com/deploy.sh"
```

### Force Reset + Deploy

```bash
ssh hetzner "cd /home/websites/pink-agent.pinkhaired.com && git fetch origin && git reset --hard origin/main && ./deploy.sh"
```

### Server Structure

```
/home/websites/pink-agent.pinkhaired.com/
├── docker-compose.yml
├── Dockerfile
├── nginx.conf
├── deploy.sh
└── src/              # git repo (pink-agent-web)
```

### Nginx Config

Nginx reverse proxies to Docker container on port 3030.

Location: `/etc/nginx/sites-enabled/pink-agent.pinkhaired.com`

## Services Overview

| Service | Location | Description |
|---------|----------|-------------|
| Pink Agent (Go) | Local Mac | Main backend, runs Claude PTY |
| Cloudflared | Local Mac | Tunnel to expose WebSocket |
| Frontend | Hetzner Docker | Static files + Nginx |
| Telegram Bot | Telegram API | Menu button, message handling |
| Claude Code | Local Mac | AI assistant in PTY |

## Troubleshooting

### "Tunnel already in use"

Another browser tab has the Mini App open. Close it.

### Bot not responding

Check if pink-agent is running. Check `~/pink-tools/pink-agent/.env` values.

### Tunnel keeps restarting

Cloudflared connection issues. Check internet connection.

### Voice messages not working

Install [pink-transcriber](https://github.com/pinkhairedboy/pink-transcriber).

### Session stuck on "Creating"

Claude Code might be hanging. Check terminal output from pink-agent.

### Named tunnel not working

1. Check `TUNNEL_NAME` and `TUNNEL_ID` both set in `.env`
2. Check credentials file exists: `~/.cloudflared/{TUNNEL_ID}.json`
3. Verify DNS points to correct tunnel: `dig +short pink-{name}.pinkhaired.com`
4. If DNS wrong — see "Fix DNS" in "Issuing Named Tunnels" section
5. Clear local DNS cache after fix: `sudo dscacheutil -flushcache; sudo killall -HUP mDNSResponder`

## Data Locations

| Data | Location |
|------|----------|
| Config | `~/pink-tools/pink-agent/.env` |
| State | `~/pink-tools/pink-agent/state.json` |
| Project files | `~/pink-tools/pink-agent/store/{project-id}/` |
| Temp files | `/tmp/pink-agent/files/` |
| MCP config | `~/pink-tools/pink-agent/mcp-config.json` |

## Logs

Pink Agent logs to stdout. Run in terminal to see logs.

For persistent logging:
```bash
./pink-agent 2>&1 | tee pink-agent.log
```

## Issuing Named Tunnels (Admin)

Named tunnels provide permanent URLs like `https://pink-{name}.pinkhaired.com`.

### Create tunnel for user

```bash
# 1. Create tunnel
cloudflared tunnel create pink-{name}
# Output: Tunnel credentials written to ~/.cloudflared/{tunnel-id}.json
# SAVE THIS UUID - you'll need it

# 2. Try to route DNS
cloudflared tunnel route dns pink-{name} pink-{name}.pinkhaired.com
```

### 3. Fix DNS (REQUIRED - cloudflared picks wrong tunnel)

`cloudflared tunnel route dns` часто создаёт CNAME на старый/удалённый туннель. Это происходит ВСЕГДА если раньше существовал туннель с таким именем. Нужно исправить вручную:

```bash
# Проверить на какой туннель указывает DNS
dig +short pink-{name}.pinkhaired.com
# Вернёт: {some-tunnel-id}.cfargotunnel.com

# Сравнить с ID созданного туннеля
cloudflared tunnel list | grep pink-{name}
# ID должен совпадать. Если нет — исправляем:

# Fix via Cloudflare API
ZONE_ID=b9cdcfe1bad2d6002f2a485a427f6f45
CF_TOKEN="your-cloudflare-api-token"  # from PROJECTS.md or Cloudflare dashboard

# Get DNS record ID
curl -s "https://api.cloudflare.com/client/v4/zones/$ZONE_ID/dns_records?name=pink-{name}.pinkhaired.com" \
  -H "Authorization: Bearer $CF_TOKEN" | jq '.result[0].id'

# Update to correct tunnel ID
curl -X PUT "https://api.cloudflare.com/client/v4/zones/$ZONE_ID/dns_records/{record-id}" \
  -H "Authorization: Bearer $CF_TOKEN" \
  -H "Content-Type: application/json" \
  --data '{"type":"CNAME","name":"pink-{name}","content":"{correct-tunnel-id}.cfargotunnel.com","proxied":true}'

# Verify fix
dig +short pink-{name}.pinkhaired.com
# Should now show correct tunnel ID
```

### 4. Send to user

1. Copy `~/.cloudflared/{tunnel-id}.json` to user (keep original filename!)
2. User places it in their `~/.cloudflared/` (filename must be `{tunnel-id}.json`)
3. User adds to `~/pink-tools/pink-agent/.env`:
   ```
   TUNNEL_NAME=pink-{name}
   TUNNEL_ID={tunnel-id}
   ```

**Important:** Both `TUNNEL_NAME` and `TUNNEL_ID` are required. Credentials file MUST be named by tunnel UUID (e.g. `130ba346-b3a8-42ac-9ef1-35ff769ec782.json`).

### Existing tunnels

| Name | URL | User |
|------|-----|------|
| pink-mir | https://pink-mir.pinkhaired.com | Miro |
| pink-alex | https://pink-alex.pinkhaired.com | Alex |
