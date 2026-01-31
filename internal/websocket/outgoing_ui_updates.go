package websocket

import (
	"encoding/json"

	"github.com/gorilla/websocket"
)

// activateSession starts PTY and sends state event to UI
func (s *Server) activateSession() {
	state := s.state.State()
	s.pty.Resize(state.Cols, state.Rows)
	s.pty.Start()
	s.sendEvent("state", s.state.State())
	s.sendEvent("terminal.buffer", string(s.pty.Buffer()))
}

// sendResponse sends response to a request
func (s *Server) sendResponse(id string, ok bool, err *Error) {
	s.send(Response{ID: id, OK: ok, Error: err})
}

// sendEvent sends event to UI
func (s *Server) sendEvent(eventType string, data any) {
	s.send(Event{Type: eventType, Data: data})
}

// BroadcastState sends state event to UI (called by StateManager onChange)
func (s *Server) BroadcastState(state any) {
	s.sendEvent("state", state)
}

// RefreshStore broadcasts updated store list (called via IPC from CLI)
func (s *Server) RefreshStore() {
	s.broadcastStoreList()
}

// send marshals and sends message to WebSocket
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
