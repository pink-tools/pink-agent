package cli

import (
	"fmt"
	"path/filepath"

	"github.com/pink-tools/pink-core"
	"pink-agent/internal/config"
	"pink-agent/internal/telegram"
)

func HandleSend(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent send <text> | pink-agent send -f <file>")
	}

	dataDir := core.DataDir(serviceName)
	envPath := filepath.Join(dataDir, ".env")

	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	chatID := cfg.TelegramUserID

	if args[0] == "-f" {
		if len(args) < 2 {
			return fmt.Errorf("usage: pink-agent send -f <file>")
		}
		return telegram.SendFile(cfg.TelegramBotToken, chatID, args[1])
	}

	return telegram.SendMessage(cfg.TelegramBotToken, chatID, args[0])
}
