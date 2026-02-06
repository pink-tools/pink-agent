package websocket

import (
	"encoding/json"

	"github.com/gorilla/websocket"
	"pink-agent/internal/projects"
)

// BroadcastState sends merged state (persisted + pending) to UI
func (s *Server) BroadcastState(state *projects.State, pending []projects.PendingSession) {
	s.sendEvent("state", map[string]any{
		"state":           state,
		"pendingSessions": pending,
	})
}

// RefreshStore broadcasts updated store list (called via IPC from CLI)
func (s *Server) RefreshStore() {
	s.broadcastStoreList()
}

// sendResponse sends response to a request
func (s *Server) sendResponse(id string, ok bool, err *Error) {
	s.send(Response{ID: id, OK: ok, Error: err})
}

// sendEvent sends event to UI
func (s *Server) sendEvent(eventType string, data any) {
	s.send(Event{Type: eventType, Data: data})
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
