package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	otel "github.com/pink-tools/pink-otel"
	"pink-agent/internal/auth"
	"pink-agent/internal/claude"
	"pink-agent/internal/domain"
	"pink-agent/internal/store"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	pingInterval = 30 * time.Second
	pongWait     = 60 * time.Second
)

type Command struct {
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	ID       string `json:"id,omitempty"`
	ClaudeID string `json:"claudeId,omitempty"`
	Text     string `json:"text,omitempty"`
	Path     string `json:"path,omitempty"`
	Content  string `json:"content,omitempty"`
	Cols     uint16 `json:"cols,omitempty"`
	Rows     uint16 `json:"rows,omitempty"`
}

type OutputMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type FullMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type StateMessage struct {
	Type  string        `json:"type"`
	State *domain.State `json:"state"`
}

type ErrorMessage struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

type StoreListMessage struct {
	Type  string           `json:"type"`
	Files []store.FileInfo `json:"files"`
}

type StoreGetMessage struct {
	Type    string `json:"type"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

type StateManager interface {
	State() *domain.State
	CreateProject(name string) error
	DeleteProject(id string) error
	RenameProject(id, name string) error
	SwitchProject(id string) error
	CreatePendingSession(name, pendingID string) error
	FinishSession(pendingID, realClaudeID string) error
	CancelPendingSession(pendingID string) error
	DeleteSession(claudeID string) error
	RenameSession(claudeID, name string) error
	SwitchSession(claudeID string) error
	SetSessionStatus(claudeID string, status domain.SessionStatus) error
	SetTerminalSize(cols, rows uint16) error
}

type PTYManager interface {
	Write(text string) error
	Buffer() []byte
	Resize(cols, rows uint16)
	SendEscape()
	Stop()
	Start()
	SetOutputHandler(fn func([]byte))
}

type Server struct {
	state      StateManager
	pty        PTYManager
	store      *store.Store
	botToken   string
	userID     int64

	conn   *websocket.Conn
	connMu sync.Mutex
}

func NewServer(state StateManager, pty PTYManager, store *store.Store, botToken string, userID int64) *Server {
	return &Server{
		state:    state,
		pty:      pty,
		store:    store,
		botToken: botToken,
		userID:   userID,
	}
}

func (s *Server) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initData := r.URL.Query().Get("initData")
		if err := auth.Validate(initData, s.botToken, s.userID); err != nil {
			otel.Warn(r.Context(), "websocket auth failed", otel.Attr{"error", err.Error()})
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		s.handleConnection(r.Context(), w, r)
	}
}

func (s *Server) DevHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.handleConnection(r.Context(), w, r)
	}
}

func (s *Server) handleConnection(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	s.connMu.Lock()
	if s.conn != nil {
		s.conn.Close()
	}
	s.conn = conn
	s.connMu.Unlock()

	// Keepalive
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	go s.pingLoop(conn)

	s.pty.SetOutputHandler(func(data []byte) {
		s.sendOutput(data)
	})

	// Don't activate session here - wait for resize → ready → activate flow
	s.readLoop(ctx, conn)
}

func (s *Server) pingLoop(conn *websocket.Conn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for range ticker.C {
		s.connMu.Lock()
		if s.conn != conn {
			s.connMu.Unlock()
			return
		}
		err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
		s.connMu.Unlock()
		if err != nil {
			return
		}
	}
}

func (s *Server) readLoop(ctx context.Context, conn *websocket.Conn) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var cmd Command
		if err := json.Unmarshal(data, &cmd); err != nil {
			s.sendError("invalid command")
			continue
		}

		s.handleCommand(ctx, cmd)
	}
}

func (s *Server) handleCommand(ctx context.Context, cmd Command) {
	var err error

	switch cmd.Type {
	// State mutations
	case "create_project":
		err = s.state.CreateProject(cmd.Name)
		if err == nil {
			otel.Info(ctx, "project created", otel.Attr{"name", cmd.Name})
			// Initialize PROJECT.md for new project
			if project := s.state.State().GetActiveProject(); project != nil {
				s.store.InitProjectContext(project.ID)
			}
			s.sendState()
		}

	case "delete_project":
		// Get name before delete
		projectName := ""
		if p := s.state.State().GetProject(cmd.ID); p != nil {
			projectName = p.Name
		}
		s.pty.Stop()
		s.store.DeleteProject(cmd.ID)
		err = s.state.DeleteProject(cmd.ID)
		if err == nil {
			otel.Info(ctx, "project deleted", otel.Attr{"name", projectName})
			s.sendState()
		}

	case "rename_project":
		err = s.state.RenameProject(cmd.ID, cmd.Name)
		if err == nil {
			s.sendState()
		}

	case "switch_project":
		// Get from before switch
		fromProject := ""
		if p := s.state.State().GetActiveProject(); p != nil {
			fromProject = p.Name
		}
		s.pty.Stop()
		err = s.state.SwitchProject(cmd.ID)
		if err == nil {
			toProject := ""
			if p := s.state.State().GetActiveProject(); p != nil {
				toProject = p.Name
			}
			otel.Info(ctx, "switched project", otel.Attr{"from", fromProject}, otel.Attr{"to", toProject})
			s.activateSession()
		}

	case "create_session":
		project := s.state.State().GetActiveProject()
		if project == nil {
			s.sendError("no active project")
			return
		}
		sessionName := cmd.Name
		if sessionName == "" {
			sessionName = fmt.Sprintf("Session %d", len(project.Sessions)+1)
		}

		// Create pending session with temporary ID
		pendingID := "pending-" + fmt.Sprintf("%d", time.Now().UnixNano())
		err = s.state.CreatePendingSession(sessionName, pendingID)
		if err != nil {
			break
		}
		s.sendState()

		go func(name, pending string) {

			projectName, projectCtx := s.getProjectInfo()
			realClaudeID, createErr := claude.CreateSession(projectName, projectCtx)
			if createErr != nil {
				otel.Error(ctx, "create session failed", otel.Attr{"error", createErr.Error()})
				s.state.CancelPendingSession(pending)
				s.sendError(createErr.Error())
				s.sendState()
				return
			}

			if err := s.state.FinishSession(pending, realClaudeID); err != nil {
				otel.Error(ctx, "finish session failed", otel.Attr{"error", err.Error()})
				s.sendError(err.Error())
				s.sendState()
				return
			}

			otel.Info(ctx, "session created", otel.Attr{"name", name}, otel.Attr{"id", realClaudeID})
			s.activateSession()
		}(sessionName, pendingID)
		return

	case "delete_session":
		// Get name before delete
		sessionName := ""
		if p := s.state.State().GetActiveProject(); p != nil {
			if sess := p.GetSession(cmd.ClaudeID); sess != nil {
				sessionName = sess.Name
			}
		}
		s.pty.Stop()
		err = s.state.DeleteSession(cmd.ClaudeID)
		if err == nil {
			otel.Info(ctx, "session deleted", otel.Attr{"name", sessionName})
			if s.state.State().GetActiveSession() != nil {
				s.activateSession()
			} else {
				s.sendState()
			}
		}

	case "rename_session":
		err = s.state.RenameSession(cmd.ClaudeID, cmd.Name)
		if err == nil {
			s.sendState()
		}

	case "switch_session":
		// Get from before switch
		fromSession := ""
		if sess := s.state.State().GetActiveSession(); sess != nil {
			fromSession = sess.Name
		}
		s.pty.Stop()
		err = s.state.SwitchSession(cmd.ClaudeID)
		if err == nil {
			toSession := ""
			if sess := s.state.State().GetActiveSession(); sess != nil {
				toSession = sess.Name
			}
			otel.Info(ctx, "switched session", otel.Attr{"from", fromSession}, otel.Attr{"to", toSession})
			s.activateSession()
		}

	case "compact":
		state := s.state.State()
		project := state.GetActiveProject()
		if project == nil {
			s.sendError("no active project")
			return
		}
		session := project.GetActiveSession()
		if session == nil {
			s.sendError(domain.ErrNoActiveSession.Error())
			return
		}

		// Set session status to compacting
		err = s.state.SetSessionStatus(session.ClaudeID, domain.SessionStatusCompacting)
		if err != nil {
			otel.Error(ctx, "compact set status failed", otel.Attr{"error", err.Error()})
			break
		}
		s.pty.Stop()
		s.sendState()

		oldClaudeID := session.ClaudeID
		sessionName := session.Name + " (C)"

		go func() {
			// Summarize old session
			summary, sumErr := claude.Summarize(oldClaudeID)
			if sumErr != nil {
				otel.Error(ctx, "compact summarize failed", otel.Attr{"error", sumErr.Error()})
				s.state.SetSessionStatus(oldClaudeID, domain.SessionStatusReady) // restore
				s.sendError("compact failed: " + sumErr.Error())
				s.sendState()
				return
			}

			// Create new session with context
			_, projectCtx := s.getProjectInfo()
			newClaudeID, createErr := claude.CreateWithTakeover(summary, projectCtx)
			if createErr != nil {
				otel.Error(ctx, "compact create failed", otel.Attr{"error", createErr.Error()})
				s.state.SetSessionStatus(oldClaudeID, domain.SessionStatusReady) // restore
				s.sendError("compact failed: " + createErr.Error())
				s.sendState()
				return
			}

			// Delete old session (still compacting, so IsIdle returns false)
			// Then add new session with ready status
			s.state.DeleteSession(oldClaudeID)

			pendingID := "pending-" + fmt.Sprintf("%d", time.Now().UnixNano())
			s.state.CreatePendingSession(sessionName, pendingID)
			s.state.FinishSession(pendingID, newClaudeID)
			s.activateSession()
		}()
		return

	// Terminal
	case "input":
		otel.Info(ctx, "input", otel.Attr{"text", cmd.Text})
		err = s.pty.Write(cmd.Text)

	case "resize":
		s.state.SetTerminalSize(cmd.Cols, cmd.Rows)
		s.pty.Resize(cmd.Cols, cmd.Rows)
		s.send(map[string]string{"type": "ready"})

	case "activate":
		s.activateSession()

	case "cancel":
		s.pty.SendEscape()

	case "sync":
		s.sendState()
		s.sendFull()

	// Store
	case "store_list":
		s.handleStoreList()

	case "store_get":
		s.handleStoreGet(cmd.Path)

	case "store_add":
		s.handleStoreAdd(cmd.Path, cmd.Content)

	case "store_delete":
		s.handleStoreDelete(cmd.Path)

	case "store_send":
		s.handleStoreSend(cmd.Path)

	default:
		s.sendError("unknown command: " + cmd.Type)
	}

	if err != nil {
		s.sendError(err.Error())
	}
}

func (s *Server) getProjectInfo() (string, string) {
	state := s.state.State()
	project := state.GetActiveProject()
	if project == nil {
		return "", ""
	}

	content, err := s.store.Get(project.ID, "PROJECT.md")
	if err != nil {
		return project.Name, ""
	}
	return project.Name, string(content)
}

func (s *Server) handleStoreList() {
	state := s.state.State()
	project := state.GetActiveProject()
	if project == nil {
		s.sendError("no active project")
		return
	}

	files, err := s.store.List(project.ID)
	if err != nil {
		s.sendError(err.Error())
		return
	}

	s.send(StoreListMessage{Type: "store_list", Files: files})
}

func (s *Server) handleStoreGet(path string) {
	state := s.state.State()
	project := state.GetActiveProject()
	if project == nil {
		s.sendError("no active project")
		return
	}

	content, err := s.store.Get(project.ID, path)
	if err != nil {
		s.sendError(err.Error())
		return
	}

	s.send(StoreGetMessage{Type: "store_get", Path: path, Content: string(content)})
}

func (s *Server) handleStoreAdd(path, content string) {
	state := s.state.State()
	project := state.GetActiveProject()
	if project == nil {
		s.sendError("no active project")
		return
	}

	if err := s.store.Add(project.ID, path, []byte(content)); err != nil {
		s.sendError(err.Error())
	}
}

func (s *Server) handleStoreDelete(path string) {
	state := s.state.State()
	project := state.GetActiveProject()
	if project == nil {
		s.sendError("no active project")
		return
	}

	if err := s.store.Delete(project.ID, path); err != nil {
		s.sendError(err.Error())
		return
	}

	s.send(map[string]string{"type": "store_deleted", "path": path})
}

func (s *Server) handleStoreSend(path string) {
	state := s.state.State()
	project := state.GetActiveProject()
	if project == nil {
		s.sendError("no active project")
		return
	}

	fullPath := filepath.Join(s.store.Path(project.ID), path)

	// Get path to current executable
	exe, err := os.Executable()
	if err != nil {
		s.sendError("failed to get executable path: " + err.Error())
		return
	}

	if err := exec.Command(exe, "send", "-f", fullPath).Run(); err != nil {
		s.sendError("failed to send file: " + err.Error())
	}
}

func (s *Server) activateSession() {
	state := s.state.State()
	s.pty.Resize(state.Cols, state.Rows)
	s.pty.Start()
	s.sendState()
	s.sendFull()
}

func (s *Server) sendState() {
	s.send(StateMessage{Type: "state", State: s.state.State()})
}

func (s *Server) sendFull() {
	s.send(FullMessage{Type: "full", Data: string(s.pty.Buffer())})
}

func (s *Server) sendOutput(data []byte) {
	s.send(OutputMessage{Type: "output", Data: string(data)})
}

func (s *Server) sendError(msg string) {
	s.send(ErrorMessage{Type: "error", Error: msg})
}

func (s *Server) send(msg any) {
	s.connMu.Lock()
	defer s.connMu.Unlock()

	if s.conn == nil {
		return
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	s.conn.WriteMessage(websocket.TextMessage, data)
}
