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
	pingInterval = 30 * time.Second
	pongWait     = 60 * time.Second
)

// StateManager interface for state operations
type StateManager interface {
	State() *projects.State
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
	SetSessionStatus(claudeID string, status projects.SessionStatus) error
	SetTerminalSize(cols, rows uint16) error
}

// PTYManager interface for PTY operations
type PTYManager interface {
	Write(text string) error
	Buffer() []byte
	Tokens() string
	Resize(cols, rows uint16)
	SendEscape()
	Stop()
	Start()
	SetOutputHandler(fn func([]byte))
}

// Server handles WebSocket connections
type Server struct {
	state    StateManager
	pty      PTYManager
	store    *projects.FileStore
	botToken string
	userID   int64

	conn   *websocket.Conn
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

// Handler returns HTTP handler with Telegram auth
func (s *Server) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initData := r.URL.Query().Get("initData")
		if err := validateTelegramAuth(initData, s.botToken, s.userID); err != nil {
			otel.Warn(r.Context(), "websocket auth failed", otel.Attr{"error", err.Error()})
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		s.handleConnection(r.Context(), w, r)
	}
}

// DevHandler returns HTTP handler without auth (for development)
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

	otel.Info(ctx, "mini app connected")

	// Keepalive
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})
	go s.pingLoop(conn)

	s.pty.SetOutputHandler(func(data []byte) {
		s.sendEvent("terminal.output", string(data))
	})

	// Don't activate session here - wait for resize → ready → activate flow
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

func (s *Server) readLoop(ctx context.Context, conn *websocket.Conn) {
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var req Request
		if err := json.Unmarshal(data, &req); err != nil {
			s.sendResponse("", false, &Error{Code: ErrCodeInvalidParams, Message: "invalid request: " + err.Error()})
			continue
		}

		s.handleRequest(ctx, req)
	}
}

// Telegram Mini App auth validation (inlined from auth package)
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

	return nil
}
