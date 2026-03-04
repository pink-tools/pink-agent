package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/pink-tools/pink-core"
	"github.com/pink-tools/pink-core/log"

	"pink-agent/internal/cli"
	"pink-agent/internal/claude"
	"pink-agent/internal/config"
	"pink-agent/internal/state"
	"pink-agent/internal/store"
	"pink-agent/internal/telegram"
)

var version = "dev"

const serviceName = "pink-agent"

func main() {
	dataDir := core.DataDir(serviceName)
	core.LoadEnv(serviceName)

	cfg := core.Config{
		Name:    serviceName,
		Version: version,
		Usage: fmt.Sprintf(`pink-agent v%s - Telegram bot for Claude Code

Usage:
  pink-agent                          Start daemon
  pink-agent stop                     Stop daemon
  pink-agent status                   Check if running
  pink-agent project list             List projects
  pink-agent store list|get|add|delete Manage project files
  pink-agent send "text"              Send text to topic
  pink-agent send -f <file>           Send file to topic
  pink-agent --version                Show version
  pink-agent --help                   Show this help
`, version),
		Commands: map[string]core.Command{
			"stop": {
				Desc: "Stop daemon",
				Run: func(args []string) error {
					if !core.IsRunning(serviceName) {
						fmt.Println("not running")
						return nil
					}
					return core.SendStop(serviceName)
				},
			},
			"status": {
				Desc: "Check if running",
				Run: func(args []string) error {
					if core.IsRunning(serviceName) {
						fmt.Println("running")
					} else {
						fmt.Println("not running")
					}
					return nil
				},
			},
			"project": {
				Desc: "Project management (list)",
				Run:  cli.HandleProject,
			},
			"store": {
				Desc: "Manage project files (list, get, add, delete)",
				Run:  cli.HandleStore,
			},
			"send": {
				Desc: "Send text or file to topic",
				Run:  cli.HandleSend,
			},
		},
		IPCHandler: func(cmd string) string {
			switch cmd {
			case "getState":
				storage := state.NewStorage(filepath.Join(dataDir, "state.json"))
				s, err := storage.Load()
				if err != nil {
					return "ERROR:" + err.Error()
				}
				data, err := json.Marshal(s)
				if err != nil {
					return "ERROR:" + err.Error()
				}
				return string(data)
			default:
				return "UNKNOWN"
			}
		},
	}

	actions := []core.Action{
		{Name: "install", Label: "Install", Desc: "Initial setup"},
	}
	handlers := map[string]core.ActionHandler{
		"install": {Describe: describeInstall, Execute: executeInstall},
	}
	core.HandleActions(&cfg, actions, handlers)
	core.Run(cfg, func(ctx context.Context) error {
		return runDaemon(ctx, dataDir)
	})
}

func describeInstall() core.FormSpec {
	return core.FormSpec{
		Title: "Pink Agent Setup",
		Fields: []core.Field{
			{Name: "TELEGRAM_BOT_TOKEN", Type: "text", Label: "Telegram Bot Token", Hint: "Get from @BotFather", Required: true},
			{Name: "TELEGRAM_GROUP_ID", Type: "text", Label: "Telegram Group ID", Hint: "Forum-enabled supergroup ID", Required: true},
		},
	}
}

func executeInstall(values map[string]any) error {
	env := make(map[string]string)
	for k, v := range values {
		env[k] = fmt.Sprintf("%v", v)
	}
	return core.SaveEnv(serviceName, env)
}

func runDaemon(ctx context.Context, dataDir string) error {
	envPath := filepath.Join(dataDir, ".env")

	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := config.CheckDeps(); err != nil {
		return fmt.Errorf("dependency check: %w", err)
	}

	// Initialize state
	storage := state.NewStorage(filepath.Join(dataDir, "state.json"))
	stateMgr, err := state.NewManager(storage)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	defer stateMgr.Close()

	// Initialize file store
	fileStore := store.New(filepath.Join(dataDir, "store"))

	// Migrate legacy formats
	orphaned := state.Migrate(dataDir, stateMgr)

	// Initialize Claude process manager
	mcpConfig := filepath.Join(dataDir, "mcp-config.json")
	claudeMgr := claude.NewManager(mcpConfig)
	defer claudeMgr.StopAll()

	// Initialize Telegram bot
	bot, err := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramGroupID, stateMgr, claudeMgr, fileStore)
	if err != nil {
		return fmt.Errorf("create telegram bot: %w", err)
	}

	// Create forum topics for migrated projects
	if len(orphaned) > 0 {
		bot.MigrateProjects(ctx, orphaned)
	}

	log.Info(ctx, "ready")

	// Blocking — runs until context cancelled
	bot.Start(ctx)

	return nil
}
