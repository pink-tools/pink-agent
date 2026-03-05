package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"

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

type daemon struct {
	bot   *telegram.Bot
	state *state.Manager
}

func main() {
	dataDir := core.DataDir(serviceName)
	core.LoadEnv(serviceName)

	var d atomic.Pointer[daemon]

	cfg := core.Config{
		Name:    serviceName,
		Version: version,
		Usage: fmt.Sprintf(`pink-agent v%s - Telegram bot for Claude Code

Usage:
  pink-agent                          Start daemon
  pink-agent stop                     Stop daemon
  pink-agent status                   Check if running
  pink-agent project list             List projects
  pink-agent project create "Name" ["Prompt"] [--dir path]  Create project
  pink-agent project delete [id-or-name]       Delete project
  pink-agent store list|get|add|delete Manage project files
  pink-agent send "text"              Send text to topic
  pink-agent send -f <file>           Send file to topic
  pink-agent session list <dir>       List sessions from directory
  pink-agent session attach <id> <dir> Attach session as new topic
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
				Desc: "Project management (list, create, delete)",
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
			"session": {
				Desc: "Session management (list, attach)",
				Run:  cli.HandleSession,
			},
		},
		IPCHandler: func(cmd string) string {
			name, payload, _ := strings.Cut(cmd, ":")

			switch name {
			case "getState":
				if dm := d.Load(); dm != nil {
					data, err := json.Marshal(dm.state.State())
					if err != nil {
						return "ERROR:" + err.Error()
					}
					return string(data)
				}
				// Fallback: load from disk when daemon not ready
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

			case "createProject":
				dm := d.Load()
				if dm == nil {
					return "ERROR:daemon not ready"
				}
				var req struct {
					Name   string `json:"name"`
					Prompt string `json:"prompt"`
					Dir    string `json:"dir"`
				}
				if err := json.Unmarshal([]byte(payload), &req); err != nil {
					return "ERROR:" + err.Error()
				}
				result, err := dm.bot.CreateProject(req.Name, req.Prompt, req.Dir)
				if err != nil {
					return "ERROR:" + err.Error()
				}
				data, _ := json.Marshal(result)
				return string(data)

			case "attachSession":
				dm := d.Load()
				if dm == nil {
					return "ERROR:daemon not ready"
				}
				var req struct {
					SessionID string `json:"sessionId"`
					Dir       string `json:"dir"`
					Name      string `json:"name"`
				}
				if err := json.Unmarshal([]byte(payload), &req); err != nil {
					return "ERROR:" + err.Error()
				}
				result, err := dm.bot.AttachSession(req.SessionID, req.Dir, req.Name)
				if err != nil {
					return "ERROR:" + err.Error()
				}
				data, _ := json.Marshal(result)
				return string(data)

			case "deleteProject":
				dm := d.Load()
				if dm == nil {
					return "ERROR:daemon not ready"
				}
				var req struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal([]byte(payload), &req); err != nil {
					return "ERROR:" + err.Error()
				}
				if err := dm.bot.DeleteProject(req.ID); err != nil {
					return "ERROR:" + err.Error()
				}
				return "OK"

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
		return runDaemon(ctx, dataDir, &d)
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

func runDaemon(ctx context.Context, dataDir string, d *atomic.Pointer[daemon]) error {
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
	state.Migrate(dataDir, stateMgr)

	// Ensure all projects have store initialized
	for _, p := range stateMgr.State().Projects {
		fileStore.InitProject(p.ID)
	}

	// Initialize Claude process manager
	mcpConfig := filepath.Join(dataDir, "mcp-config.json")
	claudeMgr := claude.NewManager(mcpConfig)
	defer claudeMgr.StopAll()

	// Initialize Telegram bot
	bot, err := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramGroupID, stateMgr, claudeMgr, fileStore)
	if err != nil {
		return fmt.Errorf("create telegram bot: %w", err)
	}

	// Create forum topics for projects without one (migration or previous failure)
	bot.MigrateProjects(ctx)

	d.Store(&daemon{bot: bot, state: stateMgr})

	log.Info(ctx, "ready")

	// Blocking — runs until context cancelled
	bot.Start(ctx)

	return nil
}
