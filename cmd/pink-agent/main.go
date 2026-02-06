package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pink-tools/pink-core"
	otel "github.com/pink-tools/pink-otel"
	"pink-agent/internal/cli"
	"pink-agent/internal/claude"
	"pink-agent/internal/config"
	"pink-agent/internal/projects"
	"pink-agent/internal/telegram"
	"pink-agent/internal/websocket"
)

var version = "dev"
var ptyManager *claude.Manager
var stateManager *projects.Manager
var fileStore *projects.FileStore
var wsServer *websocket.Server

const (
	serviceName   = "pink-agent"
	webAppBaseURL = "https://pink-agent.pinkhaired.com"
)

// telegramPTYWriter adapts multi-PTY Manager to single-Write interface.
// Resolves active session from state and delegates to PTY manager.
type telegramPTYWriter struct{}

func (w *telegramPTYWriter) Write(text string) error {
	if stateManager == nil || ptyManager == nil {
		return projects.ErrNoActiveSession
	}
	session := stateManager.GetActiveSession()
	if session == nil {
		return projects.ErrNoActiveSession
	}
	return ptyManager.Write(session.ClaudeID, text)
}

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
  pink-agent tokens                   Show current token count
  pink-agent session list             List sessions in active project
  pink-agent session new [name] [prompt]  Create new session
  pink-agent session switch <id>      Switch to session
  pink-agent project list             List all projects
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
				Run:  cli.HandleSend,
			},
			"store": {
				Desc: "File store operations",
				Run:  cli.HandleStore,
			},
			"tokens": {
				Desc: "Show current token count",
				Run:  cli.HandleTokens,
			},
			"session": {
				Desc: "Session management (list, switch)",
				Run:  cli.HandleSession,
			},
			"project": {
				Desc: "Project management (list)",
				Run:  cli.HandleProject,
			},
		},
		IPCHandler: func(cmd string) string {
			switch {
			case cmd == "getContextTokens":
				if ptyManager == nil || stateManager == nil {
					return "0"
				}
				session := stateManager.GetActiveSession()
				if session == nil {
					return "0"
				}
				// Temporarily resize to wide terminal to get full status line
				oldCols, oldRows := stateManager.GetTerminalSize()
				ptyManager.Resize(session.ClaudeID, 200, 50)
				time.Sleep(100 * time.Millisecond)
				tokens := ptyManager.Tokens(session.ClaudeID)
				ptyManager.Resize(session.ClaudeID, oldCols, oldRows)
				if tokens == "" {
					return "0"
				}
				return tokens

			case cmd == "getState":
				if stateManager == nil {
					return "ERROR:not initialized"
				}
				data, err := json.Marshal(stateManager.State())
				if err != nil {
					return "ERROR:" + err.Error()
				}
				return string(data)

			case strings.HasPrefix(cmd, "switchSession:"):
				if stateManager == nil {
					return "ERROR:not initialized"
				}
				sessionID := strings.TrimPrefix(cmd, "switchSession:")
				if err := stateManager.SwitchSession(sessionID); err != nil {
					return "ERROR:" + err.Error()
				}
				return "OK"

			case cmd == "refreshStore":
				if wsServer != nil {
					wsServer.RefreshStore()
				}
				return "OK"

			case strings.HasPrefix(cmd, "createSession:"):
				if stateManager == nil {
					return "ERROR:not initialized"
				}
				jsonData := strings.TrimPrefix(cmd, "createSession:")
				var params struct {
					Name   string `json:"name"`
					Prompt string `json:"prompt"`
				}
				if err := json.Unmarshal([]byte(jsonData), &params); err != nil {
					return "ERROR:invalid params: " + err.Error()
				}
				project := stateManager.State().GetActiveProject()
				if project == nil {
					return "ERROR:no active project"
				}
				name := params.Name
				if name == "" {
					name = fmt.Sprintf("Session %d", len(project.Sessions)+1)
				}

				pendingID := stateManager.AddPendingSession(project.ID, name)

				// Build project context
				projectCtx := ""
				if fileStore != nil {
					if content, err := fileStore.Get(project.ID, "PROJECT.md"); err == nil {
						projectCtx = string(content)
					}
				}
				if params.Prompt != "" {
					if projectCtx != "" {
						projectCtx = projectCtx + "\n\n" + params.Prompt
					} else {
						projectCtx = params.Prompt
					}
				}

				go func(projectID, projectName, sessionName, pending, ctx string) {
					realID, err := claude.CreateSession(projectName, ctx)
					if err != nil {
						stateManager.FailPendingSession(pending, err.Error())
						return
					}
					if err := stateManager.FinishPendingSession(pending, realID); err != nil {
						return
					}
					ptyManager.StartSession(realID, sessionName)
				}(project.ID, project.Name, name, pendingID, projectCtx)
				return "OK"

			default:
				return "UNKNOWN"
			}
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
	storage := projects.NewFileStorage(statePath)
	stateManager, err = projects.NewManager(storage)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	// Initialize store
	storePath := filepath.Join(dataDir, "store")
	fileStore = projects.NewFileStore(storePath)

	// Initialize PTY manager
	mcpConfig := filepath.Join(dataDir, "mcp-config.json")
	ptyManager = claude.NewManager(mcpConfig)

	// Set terminal size from persisted state
	cols, rows := stateManager.GetTerminalSize()
	if cols > 0 && rows > 0 {
		ptyManager.SetTerminalSize(cols, rows)
	}

	// Eager start: launch all session PTYs
	for _, sessionID := range stateManager.State().AllSessions() {
		name := sessionID[:8]
		for _, p := range stateManager.State().Projects {
			if s := p.GetSession(sessionID); s != nil {
				name = s.Name
				break
			}
		}
		ptyManager.StartSession(sessionID, name)
	}

	// Initialize tunnel
	tun := websocket.New(cfg.TunnelName, cfg.TunnelID, cfg.Port)
	if err := tun.Start(); err != nil {
		return fmt.Errorf("start tunnel: %w", err)
	}
	defer tun.Stop()

	// Wait for tunnel to be ready
	tun.WaitReady()
	otel.Info(ctx, "tunnel ready", otel.Attr{K: "url", V: tun.URL()})

	// Initialize WebSocket server
	wsServer = websocket.NewServer(stateManager, ptyManager, fileStore, cfg.TelegramBotToken, cfg.TelegramUserID)

	// Register state change callback
	stateManager.SetOnChange(func(state *projects.State, pending []projects.PendingSession) {
		wsServer.BroadcastState(state, pending)
	})

	// Start HTTP server
	http.HandleFunc("/ws", wsServer.Handler())

	if os.Getenv("ENVIRONMENT") == "development" {
		http.HandleFunc("/dev/ws", wsServer.DevHandler())
	}

	server := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port)}
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			otel.Error(ctx, "http server error", otel.Attr{K: "error", V: err.Error()})
		}
	}()

	// Initialize Telegram bot with adapter
	handlers := telegram.NewHandlers(&telegramPTYWriter{})
	bot, err := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramUserID, handlers)
	if err != nil {
		return fmt.Errorf("create telegram bot: %w", err)
	}

	// Set menu button
	webAppURL := fmt.Sprintf("%s?api=%s", webAppBaseURL, url.QueryEscape(tun.URL()))
	if err := bot.SetMenuButton(webAppURL); err != nil {
		otel.Warn(ctx, "failed to set menu button", otel.Attr{K: "error", V: err.Error()})
	}

	bot.SendMessage(cfg.TelegramUserID, "🦄 Pink Agent activated and ready to work")

	botCtx, botCancel := context.WithCancel(ctx)
	go bot.Start(botCtx)

	otel.Info(ctx, "ready", otel.Attr{K: "url", V: webAppURL})

	<-ctx.Done()

	botCancel()
	server.Shutdown(context.Background())
	ptyManager.StopAll()

	return nil
}
