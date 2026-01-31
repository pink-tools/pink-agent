package websocket

import "encoding/json"

// Error codes
const (
	ErrCodeProjectNotFound     = "PROJECT_NOT_FOUND"
	ErrCodeSessionNotFound     = "SESSION_NOT_FOUND"
	ErrCodeNoActiveProject     = "NO_ACTIVE_PROJECT"
	ErrCodeNoActiveSession     = "NO_ACTIVE_SESSION"
	ErrCodeOperationInProgress = "OPERATION_IN_PROGRESS"
	ErrCodeInvalidParams       = "INVALID_PARAMS"
	ErrCodeInternalError       = "INTERNAL_ERROR"
	ErrCodeSessionCreateFailed = "SESSION_CREATE_FAILED"
	ErrCodeCompactFailed       = "COMPACT_FAILED"
	ErrCodeStoreError          = "STORE_ERROR"
	ErrCodePreviewNotAvailable = "PREVIEW_NOT_AVAILABLE"
)

// Request from client
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Response to client
type Response struct {
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Error *Error `json:"error,omitempty"`
}

// Error details
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Event pushed to client
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// Param types for each method

type ProjectCreateParams struct {
	Name string `json:"name"`
}

type ProjectDeleteParams struct {
	ID string `json:"id"`
}

type ProjectRenameParams struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ProjectSwitchParams struct {
	ID string `json:"id"`
}

type SessionCreateParams struct {
	Name string `json:"name,omitempty"`
}

type SessionDeleteParams struct {
	ClaudeID string `json:"claudeId"`
}

type SessionRenameParams struct {
	ClaudeID string `json:"claudeId"`
	Name     string `json:"name"`
}

type SessionSwitchParams struct {
	ClaudeID string `json:"claudeId"`
}

type TerminalResizeParams struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type StoreGetParams struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
}

type StoreAddParams struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

type StoreDeleteParams struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
}

type StoreSendParams struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
}

// Event data types

type StoreFileData struct {
	ProjectID string `json:"projectId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

type ProjectFilesData struct {
	ProjectID   string     `json:"projectId"`
	ProjectName string     `json:"projectName"`
	Files       []FileInfo `json:"files"`
}

type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}
