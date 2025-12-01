#!/bin/bash
set -e

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
CLAUDE_DIR="$HOME/.claude"
COMMANDS_DIR="$CLAUDE_DIR/commands"

echo "Pink Agent - Setup"
echo ""

# Create ~/.claude/commands directory if needed
mkdir -p "$COMMANDS_DIR"

# Copy CLAUDE.md.example to ~/.claude/CLAUDE.md (only if doesn't exist)
if [ ! -f "$CLAUDE_DIR/CLAUDE.md" ]; then
    echo "Creating ~/.claude/CLAUDE.md from template..."
    cp "$SCRIPT_DIR/templates/CLAUDE.md.example" "$CLAUDE_DIR/CLAUDE.md"
    echo "✓ Created ~/.claude/CLAUDE.md"
else
    echo "✓ ~/.claude/CLAUDE.md already exists (not overwriting)"
fi

# Copy summarize.md to ~/.claude/commands/summarize.md (always update)
echo "Copying summarize command..."
cp "$SCRIPT_DIR/templates/claude-commands/summarize.md" "$COMMANDS_DIR/summarize.md"
echo "✓ Installed ~/.claude/commands/summarize.md"

echo ""
echo "Setup complete!"
echo ""
echo "Next steps:"
echo "1. Configure Telegram credentials: cp .env.example .env && nano .env"
echo "2. Run the agent: caffeinate -id uv run pink-agent (macOS) or uv run pink-agent (Linux/Windows)"
echo ""
