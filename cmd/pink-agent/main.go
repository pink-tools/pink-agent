package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/pink-tools/pink-core"
	otel "github.com/pink-tools/pink-otel"
	"pink-agent/internal/config"
	"pink-agent/internal/pty"
	"pink-agent/internal/state"
	"pink-agent/internal/store"
	"pink-agent/internal/telegram"
	"pink-agent/internal/transcriber"
	"pink-agent/internal/tunnel"
	"pink-agent/internal/websocket"
)

var version = "dev"

const (
	serviceName   = "pink-agent"
	webAppBaseURL = "https://pink-agent.pinkhaired.com"
)

func main() {
	dataDir := core.DataDir(serviceName)
	core.LoadEnv(serviceName)

	core.Run(core.Config{
		Name:    serviceName,
		Version: version,
		Usage: fmt.Sprintf(`pink-agent v%s - Telegram bot for Claude Code

Usage:
  pink-agent                          Start daemon
  pink-agent stop                     Stop daemon
  pink-agent status                   Check if running
  pink-agent send <text>              Send text message
  pink-agent send -f <file>           Send file
  pink-agent store list               List files in active project
  pink-agent store get <path>         Get file content
  pink-agent store add <path> <text>  Add file to store
  pink-agent --version                Show version
  pink-agent --help                   Show this help

Store flags:
  -p "Project Name"                   Operate on specific project
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
			"send": {
				Desc: "Send message or file",
				Run:  handleSend,
			},
			"store": {
				Desc: "File store operations",
				Run:  handleStore,
			},
		},
	}, func(ctx context.Context) error {
		return runDaemon(ctx, dataDir)
	})
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
	statePath := filepath.Join(dataDir, "state.json")
	storage := state.NewFileStorage(statePath)
	stateManager, err := state.NewManager(storage)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Initialize store
	storePath := filepath.Join(dataDir, "store")
	fileStore := store.New(storePath)

	// Initialize PTY manager
	mcpConfig := filepath.Join(dataDir, "mcp-config.json")
	ptyManager := pty.NewManager(stateManager, mcpConfig)

	// Initialize tunnel
	tun := tunnel.New(cfg.TunnelName, cfg.TunnelID, cfg.Port)
	if err := tun.Start(); err != nil {
		return fmt.Errorf("start tunnel: %w", err)
	}
	defer tun.Stop()

	// Wait for tunnel to be ready
	tun.WaitReady()
	otel.Info(ctx, "tunnel ready", map[string]any{"url": tun.URL()})

	// Initialize WebSocket server
	wsServer := websocket.NewServer(stateManager, ptyManager, fileStore, cfg.TelegramBotToken, cfg.TelegramUserID)

	// Start HTTP server
	http.HandleFunc("/ws", wsServer.Handler())

	// Dev mode: add /dev/ws without auth
	if os.Getenv("ENVIRONMENT") == "development" {
		http.HandleFunc("/dev/ws", wsServer.DevHandler())
	}

	server := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port)}
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			otel.Error(ctx, "http server error", map[string]any{"error": err.Error()})
		}
	}()

	// Initialize Telegram bot
	trans := &transcriberWrapper{}
	handlers := telegram.NewHandlers(ptyManager, trans)
	bot, err := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramUserID, handlers)
	if err != nil {
		return fmt.Errorf("create telegram bot: %w", err)
	}

	// Set menu button
	webAppURL := fmt.Sprintf("%s?api=%s", webAppBaseURL, url.QueryEscape(tun.URL()))
	if err := bot.SetMenuButton(webAppURL); err != nil {
		otel.Warn(ctx, "failed to set menu button", map[string]any{"error": err.Error()})
	}

	// Send startup message
	bot.SendMessage(cfg.TelegramUserID, "🦄 Pink Agent activated and ready to work")

	// Start Telegram bot in goroutine
	botCtx, botCancel := context.WithCancel(ctx)
	go bot.Start(botCtx)

	otel.Info(ctx, "started", map[string]any{
		"version": version,
		"url":     webAppURL,
	})

	// Wait for shutdown
	<-ctx.Done()

	// Cleanup
	botCancel()
	server.Shutdown(context.Background())
	ptyManager.Stop()

	return nil
}

type transcriberWrapper struct{}

func (t *transcriberWrapper) Transcribe(path string) (string, error) {
	return transcriber.Transcribe(path)
}

func handleSend(args []string) error {
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

func handleStore(args []string) error {
	dataDir := core.DataDir(serviceName)
	storePath := filepath.Join(dataDir, "store")
	statePath := filepath.Join(dataDir, "state.json")

	storage := state.NewFileStorage(statePath)
	stateManager, err := state.NewManager(storage)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	fileStore := store.New(storePath)

	// Parse -p flag for project
	projectID := ""
	i := 0
	for i < len(args) {
		if args[i] == "-p" && i+1 < len(args) {
			projectName := args[i+1]
			for _, p := range stateManager.State().Projects {
				if p.Name == projectName {
					projectID = p.ID
					break
				}
			}
			if projectID == "" {
				return fmt.Errorf("project not found: %s", projectName)
			}
			args = append(args[:i], args[i+2:]...)
		} else {
			i++
		}
	}

	// Use active project if not specified
	if projectID == "" {
		project := stateManager.State().GetActiveProject()
		if project == nil {
			return fmt.Errorf("no active project")
		}
		projectID = project.ID
	}

	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent store [list|get|add] ...")
	}

	switch args[0] {
	case "list":
		files, err := fileStore.List(projectID)
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Println(f.Name)
		}

	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: pink-agent store get <path>")
		}
		content, err := fileStore.Get(projectID, args[1])
		if err != nil {
			return err
		}
		fmt.Print(string(content))

	case "add":
		if len(args) < 3 {
			return fmt.Errorf("usage: pink-agent store add <path> <content>")
		}
		return fileStore.Add(projectID, args[1], []byte(args[2]))

	default:
		return fmt.Errorf("unknown store command: %s", args[0])
	}

	return nil
}
