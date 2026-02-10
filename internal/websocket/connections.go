package websocket

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	otel "github.com/pink-tools/pink-otel"
	"pink-agent/internal/projects"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	pingInterval      = 30 * time.Second
	pongWait          = 60 * time.Second
	heartbeatInterval = 25 * time.Second
)

// StateManager interface for state operations
type StateManager interface {
	State() *projects.State
	PendingSessions() []projects.PendingSession
	CreateProject(name string) error
	DeleteProject(id string) error
	RenameProject(id, name string) error
	SwitchProject(id string) error
	AddSession(projectID, claudeID, name string) error
	AddPendingSession(projectID, name string) string
	FinishPendingSession(pendingID, claudeID string) error
	FailPendingSession(pendingID, errorMsg string)
	RemovePendingSession(pendingID string)
	DeleteSession(claudeID string) error
	RenameSession(claudeID, name string) error
	SwitchSession(claudeID string) error
	SetTerminalSize(cols, rows uint16) error
}

// PTYManager interface for PTY operations
type PTYManager interface {
	Write(sessionID, text string) error
	Buffer(sessionID string) []byte
	Tokens(sessionID string) string
	Resize(sessionID string, cols, rows uint16)
	ResizeAll(cols, rows uint16)
	SendEscape(sessionID string)
	StartSession(sessionID, name string) error
	StopSession(sessionID string)
	StopAll()
	SetTerminalSize(cols, rows uint16)
	IsRunning(sessionID string) bool
}

// Server handles WebSocket connections
type Server struct {
	state    StateManager
	pty      PTYManager
	store    *projects.FileStore
	botToken string
	userID   int64
	dev      bool

	conn   *websocket.Conn
	sendCh chan []byte
	connMu sync.Mutex
}

// NewServer creates a new WebSocket server
func NewServer(state StateManager, pty PTYManager, store *projects.FileStore, botToken string, userID int64) *Server {
	return &Server{
		state:    state,
		pty:      pty,
		store:    store,
		botToken: botToken,
		userID:   userID,
	}
}

// SetDev enables the development handler (no auth). Must be called before registering routes.
func (s *Server) SetDev(enabled bool) {
	s.dev = enabled
}

// Handler returns HTTP handler with Telegram auth
func (s *Server) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initData := r.URL.Query().Get("initData")
		if err := validateTelegramAuth(initData, s.botToken, s.userID); err != nil {
			otel.Warn(r.Context(), "websocket auth failed", otel.Attr{K: "error", V: err.Error()})
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		s.handleConnection(r.Context(), w, r)
	}
}

// DevHandler returns HTTP handler without auth. Only works when dev mode is explicitly enabled via SetDev(true).
func (s *Server) DevHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.dev {
			http.Error(w, "dev mode not enabled", http.StatusForbidden)
			return
		}
		s.handleConnection(r.Context(), w, r)
	}
}

func (s *Server) handleConnection(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	sendCh := make(chan []byte, 64)

	s.connMu.Lock()
	if s.conn != nil {
		s.conn.Close()
	}
	if s.sendCh != nil {
		close(s.sendCh)
	}
	s.conn = conn
	s.sendCh = sendCh
	s.connMu.Unlock()

	otel.Info(ctx, "mini app connected")

	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	go s.pingLoop(conn)
	go s.heartbeatLoop(conn, sendCh)
	go s.writeLoop(conn, sendCh)

	// Push current state + active session buffer on connect
	s.BroadcastState(s.state.State(), s.state.PendingSessions())
	if sess := s.state.State().GetActiveSession(); sess != nil {
		s.sendEvent("terminal.buffer", map[string]string{
			"sessionId": sess.ClaudeID,
			"data":      string(s.pty.Buffer(sess.ClaudeID)),
		})
	}

	s.readLoop(ctx, conn)

	otel.Info(ctx, "mini app disconnected")
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

// heartbeatLoop sends application-level heartbeat messages through sendCh.
// Send is under connMu to prevent sending on a closed channel after conn replacement.
func (s *Server) heartbeatLoop(conn *websocket.Conn, ch chan []byte) {
	msg, _ := json.Marshal(Event{Type: "heartbeat"})
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for range ticker.C {
		s.connMu.Lock()
		if s.conn != conn {
			s.connMu.Unlock()
			return
		}
		select {
		case ch <- msg:
		default:
		}
		s.connMu.Unlock()
	}
}

// writeLoop drains sendCh and writes to conn. Exits when conn errors or is replaced.
func (s *Server) writeLoop(conn *websocket.Conn, ch chan []byte) {
	for data := range ch {
		s.connMu.Lock()
		current := s.conn
		s.connMu.Unlock()
		if current != conn {
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
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

		// Check for application-level heartbeat from client
		var peek struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &peek) == nil && peek.Type == "heartbeat" {
			conn.SetReadDeadline(time.Now().Add(pongWait))
			continue
		}

		var req Request
		if err := json.Unmarshal(data, &req); err != nil {
			s.sendResponse("", false, &Error{Code: ErrCodeInvalidParams, Message: "invalid request: " + err.Error()})
			continue
		}

		s.handleRequest(ctx, req)
	}
}

// SendTerminalOutput sends terminal output data to the connected UI
func (s *Server) SendTerminalOutput(sessionID string, data []byte) {
	s.sendEvent("terminal.output", map[string]string{
		"sessionId": sessionID,
		"data":      string(data),
	})
}

// Telegram Mini App auth validation
type telegramUser struct {
	ID int64 `json:"id"`
}

func validateTelegramAuth(initData string, botToken string, allowedUserID int64) error {
	params, err := url.ParseQuery(initData)
	if err != nil {
		return err
	}

	hash := params.Get("hash")
	if hash == "" {
		return errors.New("missing hash")
	}

	userJSON := params.Get("user")
	if userJSON == "" {
		return errors.New("missing user")
	}

	var user telegramUser
	if err := json.Unmarshal([]byte(userJSON), &user); err != nil {
		return err
	}

	if user.ID != allowedUserID {
		return errors.New("unauthorized user")
	}

	params.Del("hash")

	var keys []string
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+params.Get(k))
	}
	dataCheckString := strings.Join(parts, "\n")

	secretKey := hmac.New(sha256.New, []byte("WebAppData"))
	secretKey.Write([]byte(botToken))

	h := hmac.New(sha256.New, secretKey.Sum(nil))
	h.Write([]byte(dataCheckString))
	expectedHash := hex.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(hash), []byte(expectedHash)) {
		return errors.New("invalid hash")
	}

	authDateStr := params.Get("auth_date")
	if authDateStr == "" {
		return errors.New("missing auth_date")
	}
	authDate, err := strconv.ParseInt(authDateStr, 10, 64)
	if err != nil {
		return errors.New("invalid auth_date")
	}
	if time.Now().Unix()-authDate > 3600 {
		return errors.New("auth_date too old")
	}

	return nil
}
