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
	"sync"
	"sync/atomic"
	"time"

	"github.com/pink-tools/pink-core"
	"github.com/pink-tools/pink-core/log"
	"pink-agent/internal/cli"
	"pink-agent/internal/claude"
	"pink-agent/internal/config"
	"pink-agent/internal/projects"
	"pink-agent/internal/telegram"
	"pink-agent/internal/websocket"
)

var version = "dev"

var (
	ptyManagerPtr   atomic.Pointer[claude.Manager]
	stateManagerPtr atomic.Pointer[projects.Manager]
	fileStorePtr    atomic.Pointer[projects.FileStore]
	wsServerPtr     atomic.Pointer[websocket.Server]
	tokensMu        sync.Mutex
)

const (
	serviceName   = "pink-agent"
	webAppBaseURL = "https://pink-agent.pinkhaired.com"
)

// telegramPTYWriter routes messages to the active session's PTY.
type telegramPTYWriter struct{}

func (w *telegramPTYWriter) Write(text string) error {
	sm := stateManagerPtr.Load()
	pm := ptyManagerPtr.Load()
	if sm == nil || pm == nil {
		return projects.ErrNoActiveSession
	}
	session := sm.GetActiveSession()
	if session == nil {
		return projects.ErrNoActiveSession
	}
	return pm.Write(session.ClaudeID, text)
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
  pink-agent --dev                    Start daemon in dev mode (no auth)
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
				sm := stateManagerPtr.Load()
				pm := ptyManagerPtr.Load()
				if sm == nil || pm == nil {
					return "0"
				}
				session := sm.GetActiveSession()
				if session == nil {
					return "0"
				}
				tokensMu.Lock()
				defer tokensMu.Unlock()
				oldCols, oldRows := sm.GetTerminalSize()
				pm.Resize(session.ClaudeID, 200, 50)
				time.Sleep(500 * time.Millisecond)
				tokens := pm.Tokens(session.ClaudeID)
				pm.Resize(session.ClaudeID, oldCols, oldRows)
				if tokens == "" {
					return "0"
				}
				return tokens

			case cmd == "getState":
				sm := stateManagerPtr.Load()
				if sm == nil {
					return "ERROR:not initialized"
				}
				data, err := json.Marshal(sm.State())
				if err != nil {
					return "ERROR:" + err.Error()
				}
				return string(data)

			case strings.HasPrefix(cmd, "switchSession:"):
				sm := stateManagerPtr.Load()
				pm := ptyManagerPtr.Load()
				if sm == nil || pm == nil {
					return "ERROR:not initialized"
				}
				sessionID := strings.TrimPrefix(cmd, "switchSession:")
				oldProject := sm.State().GetActiveProject()
				if err := sm.SwitchSession(sessionID); err != nil {
					return "ERROR:" + err.Error()
				}
				newProject := sm.State().GetActiveProject()
				if oldProject != nil && newProject != nil && oldProject.ID != newProject.ID {
					for _, sess := range oldProject.Sessions {
						pm.StopSession(sess.ClaudeID)
					}
					for _, sess := range newProject.Sessions {
						if err := pm.StartSession(sess.ClaudeID, sess.Name); err != nil {
							return "ERROR:" + err.Error()
						}
					}
				}
				return "OK"

			case cmd == "refreshStore":
				ws := wsServerPtr.Load()
				if ws != nil {
					ws.RefreshStore()
				}
				return "OK"

			case strings.HasPrefix(cmd, "createSession:"):
				sm := stateManagerPtr.Load()
				fs := fileStorePtr.Load()
				if sm == nil {
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
				project := sm.State().GetActiveProject()
				if project == nil {
					return "ERROR:no active project"
				}
				name := params.Name
				if name == "" {
					name = fmt.Sprintf("Session %d", len(project.Sessions)+1)
				}

				pendingID := sm.AddPendingSession(project.ID, name)

				// Build project context
				projectCtx := ""
				if fs != nil {
					if content, err := fs.Get(project.ID, "PROJECT.md"); err == nil {
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

				go func(projectID, projectName, sessionName, pending, pCtx string) {
					realID, err := claude.CreateSession(projectName, pCtx)
					if err != nil {
						sm := stateManagerPtr.Load()
						if sm != nil {
							sm.FailPendingSession(pending, err.Error())
						}
						return
					}
					sm := stateManagerPtr.Load()
					pm := ptyManagerPtr.Load()
					if sm == nil || pm == nil {
						return
					}
					if err := sm.FinishPendingSession(pending, realID); err != nil {
						return
					}
					_ = pm.StartSession(realID, sessionName)
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

func hasFlag(name string) bool {
	for _, arg := range os.Args[1:] {
		if arg == name {
			return true
		}
	}
	return false
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

	// Initialize state — onNotify closure uses atomic load for wsServer
	statePath := filepath.Join(dataDir, "state.json")
	storage := projects.NewFileStorage(statePath)
	stateManager, err := projects.NewManager(storage, func(state *projects.State, pending []projects.PendingSession) {
		ws := wsServerPtr.Load()
		if ws != nil {
			ws.BroadcastState(state, pending)
		}
	})
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}
	stateManagerPtr.Store(stateManager)
	defer func() {
		stateManager.Close()
		stateManagerPtr.Store(nil)
	}()

	// Initialize store
	storePath := filepath.Join(dataDir, "store")
	fileStore := projects.NewFileStore(storePath)
	fileStorePtr.Store(fileStore)
	defer fileStorePtr.Store(nil)

	// Initialize PTY manager — output callback filters by active session
	mcpConfig := filepath.Join(dataDir, "mcp-config.json")
	ptyManager := claude.NewManager(mcpConfig, func(sessionID string, data []byte) {
		ws := wsServerPtr.Load()
		if ws == nil {
			return
		}
		session := stateManager.GetActiveSession()
		if session != nil && session.ClaudeID == sessionID {
			ws.SendTerminalOutput(sessionID, data)
		}
	})
	ptyManagerPtr.Store(ptyManager)
	defer func() {
		ptyManager.StopAll()
		ptyManagerPtr.Store(nil)
	}()

	// Set terminal size from persisted state
	cols, rows := stateManager.GetTerminalSize()
	if cols > 0 && rows > 0 {
		ptyManager.SetTerminalSize(cols, rows)
	}

	// Initialize tunnel
	tun := websocket.New(cfg.TunnelName, cfg.TunnelID, cfg.Port)
	if err := tun.Start(); err != nil {
		return fmt.Errorf("start tunnel: %w", err)
	}
	defer tun.Stop()

	// Wait for tunnel to be ready
	if err := tun.WaitReady(); err != nil {
		return fmt.Errorf("tunnel ready: %w", err)
	}
	log.Info(ctx, "tunnel ready", log.Attr{K: "url", V: tun.URL()})

	// Initialize WebSocket server
	wsServer := websocket.NewServer(stateManager, ptyManager, fileStore, cfg.TelegramBotToken, cfg.TelegramUserID)
	wsServerPtr.Store(wsServer)
	defer wsServerPtr.Store(nil)

	// Start PTYs for active project only
	if project := stateManager.State().GetActiveProject(); project != nil {
		for _, sess := range project.Sessions {
			if err := ptyManager.StartSession(sess.ClaudeID, sess.Name); err != nil {
				log.Error(ctx, "failed to start session", log.Attr{K: "name", V: sess.Name}, log.Attr{K: "error", V: err.Error()})
			}
		}
	}

	// Start HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", wsServer.Handler())

	devMode := hasFlag("--dev")
	if devMode {
		wsServer.SetDev(true)
		mux.HandleFunc("/dev/ws", wsServer.DevHandler())
	}

	server := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: mux}
	httpErrCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			httpErrCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	// Check for immediate bind failure
	select {
	case err := <-httpErrCh:
		return err
	case <-time.After(100 * time.Millisecond):
	}

	// Initialize Telegram bot with adapter
	handlers := telegram.NewHandlers(&telegramPTYWriter{})
	bot, err := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramUserID, handlers)
	if err != nil {
		return fmt.Errorf("create telegram bot: %w", err)
	}

	// Set menu button
	webAppURL := fmt.Sprintf("%s?api=%s", webAppBaseURL, url.QueryEscape(tun.URL()))
	if err := bot.SetMenuButton(webAppURL); err != nil {
		log.Warn(ctx, "failed to set menu button", log.Attr{K: "error", V: err.Error()})
	}

	bot.SendMessage(cfg.TelegramUserID, "🦄 Pink Agent activated and ready to work")

	botCtx, botCancel := context.WithCancel(ctx)
	go bot.Start(botCtx)

	log.Info(ctx, "ready", log.Attr{K: "url", V: webAppURL})

	select {
	case <-ctx.Done():
	case err := <-httpErrCh:
		botCancel()
		return err
	}

	botCancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)

	return nil
}
