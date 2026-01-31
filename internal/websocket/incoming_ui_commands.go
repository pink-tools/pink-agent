package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	otel "github.com/pink-tools/pink-otel"
	"pink-agent/internal/claude"
	"pink-agent/internal/projects"
)

// handleRequest processes incoming request and returns response
func (s *Server) handleRequest(ctx context.Context, req Request) {
	var err *Error

	switch req.Method {
	// Projects
	case "project.create":
		err = s.handleProjectCreate(ctx, req)
	case "project.delete":
		err = s.handleProjectDelete(ctx, req)
	case "project.rename":
		err = s.handleProjectRename(ctx, req)
	case "project.switch":
		err = s.handleProjectSwitch(ctx, req)

	// Sessions
	case "session.create":
		err = s.handleSessionCreate(ctx, req)
	case "session.delete":
		err = s.handleSessionDelete(ctx, req)
	case "session.rename":
		err = s.handleSessionRename(ctx, req)
	case "session.switch":
		err = s.handleSessionSwitch(ctx, req)
	case "session.compact":
		err = s.handleSessionCompact(ctx, req)

	// Terminal
	case "terminal.resize":
		err = s.handleTerminalResize(ctx, req)
	case "terminal.cancel":
		err = s.handleTerminalCancel(ctx, req)
	case "terminal.activate":
		err = s.handleTerminalActivate(ctx, req)

	// Sync
	case "sync.state":
		err = s.handleSyncState(ctx, req)
	case "sync.buffer":
		err = s.handleSyncBuffer(ctx, req)

	// Store
	case "store.list":
		err = s.handleStoreList(ctx, req)
	case "store.get":
		err = s.handleStoreGet(ctx, req)
	case "store.add":
		err = s.handleStoreAdd(ctx, req)
	case "store.delete":
		err = s.handleStoreDelete(ctx, req)
	case "store.send":
		err = s.handleStoreSend(ctx, req)

	default:
		err = &Error{Code: ErrCodeInvalidParams, Message: "unknown method: " + req.Method}
	}

	if err != nil {
		s.sendResponse(req.ID, false, err)
	} else {
		s.sendResponse(req.ID, true, nil)
	}
}

// mapError converts projects package errors to protocol errors
func mapError(err error) *Error {
	switch {
	case errors.Is(err, projects.ErrProjectNotFound):
		return &Error{Code: ErrCodeProjectNotFound, Message: err.Error()}
	case errors.Is(err, projects.ErrSessionNotFound):
		return &Error{Code: ErrCodeSessionNotFound, Message: err.Error()}
	case errors.Is(err, projects.ErrNoActiveProject):
		return &Error{Code: ErrCodeNoActiveProject, Message: err.Error()}
	case errors.Is(err, projects.ErrNoActiveSession):
		return &Error{Code: ErrCodeNoActiveSession, Message: err.Error()}
	case errors.Is(err, projects.ErrOperationInProgress):
		return &Error{Code: ErrCodeOperationInProgress, Message: err.Error()}
	default:
		return &Error{Code: ErrCodeInternalError, Message: err.Error()}
	}
}

// Project handlers

func (s *Server) handleProjectCreate(ctx context.Context, req Request) *Error {
	var params ProjectCreateParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	if err := s.state.CreateProject(params.Name); err != nil {
		return mapError(err)
	}

	otel.Info(ctx, "project created", otel.Attr{"name", params.Name})
	if project := s.state.State().GetActiveProject(); project != nil {
		s.store.InitProjectContext(project.ID)
	}
	return nil
}

func (s *Server) handleProjectDelete(ctx context.Context, req Request) *Error {
	var params ProjectDeleteParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	projectName := ""
	if p := s.state.State().GetProject(params.ID); p != nil {
		projectName = p.Name
	}

	s.pty.Stop()
	s.store.DeleteProject(params.ID)

	if err := s.state.DeleteProject(params.ID); err != nil {
		return mapError(err)
	}

	otel.Info(ctx, "project deleted", otel.Attr{"name", projectName})

	// Activate session in new active project if exists
	if s.state.State().GetActiveSession() != nil {
		s.activateSession()
	}
	return nil
}

func (s *Server) handleProjectRename(ctx context.Context, req Request) *Error {
	var params ProjectRenameParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	if err := s.state.RenameProject(params.ID, params.Name); err != nil {
		return mapError(err)
	}

	return nil
}

func (s *Server) handleProjectSwitch(ctx context.Context, req Request) *Error {
	var params ProjectSwitchParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	fromProject := ""
	if p := s.state.State().GetActiveProject(); p != nil {
		fromProject = p.Name
	}

	s.pty.Stop()

	if err := s.state.SwitchProject(params.ID); err != nil {
		return mapError(err)
	}

	toProject := ""
	if p := s.state.State().GetActiveProject(); p != nil {
		toProject = p.Name
	}
	otel.Info(ctx, "project switch", otel.Attr{"from", fromProject}, otel.Attr{"to", toProject})
	s.activateSession()
	return nil
}

// Session handlers

func (s *Server) handleSessionCreate(ctx context.Context, req Request) *Error {
	var params SessionCreateParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	project := s.state.State().GetActiveProject()
	if project == nil {
		return &Error{Code: ErrCodeNoActiveProject, Message: "no active project"}
	}

	sessionName := params.Name
	if sessionName == "" {
		sessionName = fmt.Sprintf("Session %d", len(project.Sessions)+1)
	}

	pendingID := "pending-" + fmt.Sprintf("%d", time.Now().UnixNano())
	if err := s.state.CreatePendingSession(sessionName, pendingID); err != nil {
		return mapError(err)
	}

	go func(name, pending string) {
		projectName, projectCtx := s.getProjectInfo()
		realClaudeID, createErr := claude.CreateSession(projectName, projectCtx)
		if createErr != nil {
			otel.Error(ctx, "create session failed", otel.Attr{"error", createErr.Error()})
			s.state.CancelPendingSession(pending)
			return
		}

		if err := s.state.FinishSession(pending, realClaudeID); err != nil {
			otel.Error(ctx, "finish session failed", otel.Attr{"error", err.Error()})
			return
		}

		otel.Info(ctx, "session created", otel.Attr{"name", name}, otel.Attr{"id", realClaudeID})
		s.activateSession()
	}(sessionName, pendingID)

	return nil
}

func (s *Server) handleSessionDelete(ctx context.Context, req Request) *Error {
	var params SessionDeleteParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	sessionName := ""
	isActiveSession := false
	if p := s.state.State().GetActiveProject(); p != nil {
		if sess := p.GetSession(params.ClaudeID); sess != nil {
			sessionName = sess.Name
		}
		isActiveSession = p.ActiveSession == params.ClaudeID
	}

	// Only stop PTY if deleting active session
	if isActiveSession {
		s.pty.Stop()
	}

	if err := s.state.DeleteSession(params.ClaudeID); err != nil {
		return mapError(err)
	}

	otel.Info(ctx, "session deleted", otel.Attr{"name", sessionName})

	// Only reactivate if we deleted the active session and there's another one
	if isActiveSession && s.state.State().GetActiveSession() != nil {
		s.activateSession()
	}
	return nil
}

func (s *Server) handleSessionRename(ctx context.Context, req Request) *Error {
	var params SessionRenameParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	if err := s.state.RenameSession(params.ClaudeID, params.Name); err != nil {
		return mapError(err)
	}

	return nil
}

func (s *Server) handleSessionSwitch(ctx context.Context, req Request) *Error {
	var params SessionSwitchParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	fromSession := ""
	if sess := s.state.State().GetActiveSession(); sess != nil {
		fromSession = sess.Name
	}

	s.pty.Stop()

	if err := s.state.SwitchSession(params.ClaudeID); err != nil {
		return mapError(err)
	}

	toSession := ""
	if sess := s.state.State().GetActiveSession(); sess != nil {
		toSession = sess.Name
	}
	otel.Info(ctx, "session switch", otel.Attr{"from", fromSession}, otel.Attr{"to", toSession})
	s.activateSession()
	return nil
}

func (s *Server) handleSessionCompact(ctx context.Context, req Request) *Error {
	state := s.state.State()
	project := state.GetActiveProject()
	if project == nil {
		return &Error{Code: ErrCodeNoActiveProject, Message: "no active project"}
	}
	session := project.GetActiveSession()
	if session == nil {
		return &Error{Code: ErrCodeNoActiveSession, Message: projects.ErrNoActiveSession.Error()}
	}

	if err := s.state.SetSessionStatus(session.ClaudeID, projects.SessionStatusCompacting); err != nil {
		otel.Error(ctx, "compact set status failed", otel.Attr{"error", err.Error()})
		return mapError(err)
	}

	s.pty.Stop()

	oldClaudeID := session.ClaudeID
	sessionName := session.Name + " (C)"

	go func() {
		summary, sumErr := claude.Summarize(oldClaudeID)
		if sumErr != nil {
			otel.Error(ctx, "compact summarize failed", otel.Attr{"error", sumErr.Error()})
			s.state.SetSessionStatus(oldClaudeID, projects.SessionStatusReady)
			return
		}

		_, projectCtx := s.getProjectInfo()
		newClaudeID, createErr := claude.CreateWithTakeover(summary, projectCtx)
		if createErr != nil {
			otel.Error(ctx, "compact create failed", otel.Attr{"error", createErr.Error()})
			s.state.SetSessionStatus(oldClaudeID, projects.SessionStatusReady)
			return
		}

		s.state.DeleteSession(oldClaudeID)

		pendingID := "pending-" + fmt.Sprintf("%d", time.Now().UnixNano())
		s.state.CreatePendingSession(sessionName, pendingID)
		s.state.FinishSession(pendingID, newClaudeID)
		s.activateSession()
	}()

	return nil
}

// Terminal handlers

func (s *Server) handleTerminalResize(ctx context.Context, req Request) *Error {
	var params TerminalResizeParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	s.state.SetTerminalSize(params.Cols, params.Rows)
	s.pty.Resize(params.Cols, params.Rows)
	s.sendEvent("terminal.ready", struct{}{})
	return nil
}

func (s *Server) handleTerminalCancel(ctx context.Context, req Request) *Error {
	s.pty.SendEscape()
	return nil
}

func (s *Server) handleTerminalActivate(ctx context.Context, req Request) *Error {
	s.activateSession()
	return nil
}

// Sync handlers

func (s *Server) handleSyncState(ctx context.Context, req Request) *Error {
	s.sendEvent("state", s.state.State())
	return nil
}

func (s *Server) handleSyncBuffer(ctx context.Context, req Request) *Error {
	s.sendEvent("terminal.buffer", string(s.pty.Buffer()))
	return nil
}

// Store handlers

func (s *Server) handleStoreList(ctx context.Context, req Request) *Error {
	s.broadcastStoreList()
	return nil
}

func (s *Server) handleStoreGet(ctx context.Context, req Request) *Error {
	var params StoreGetParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	project := s.state.State().GetProject(params.ProjectID)
	if project == nil {
		return &Error{Code: ErrCodeProjectNotFound, Message: "project not found"}
	}

	if !isTextFile(params.Path) {
		return &Error{Code: ErrCodePreviewNotAvailable, Message: "preview not available for this file type"}
	}

	content, err := s.store.Get(params.ProjectID, params.Path)
	if err != nil {
		return &Error{Code: ErrCodeStoreError, Message: err.Error()}
	}

	s.sendEvent("store.file", StoreFileData{ProjectID: params.ProjectID, Path: params.Path, Content: string(content)})
	return nil
}

func (s *Server) handleStoreAdd(ctx context.Context, req Request) *Error {
	var params StoreAddParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	project := s.state.State().GetProject(params.ProjectID)
	if project == nil {
		return &Error{Code: ErrCodeProjectNotFound, Message: "project not found"}
	}

	if err := s.store.Add(params.ProjectID, params.Path, []byte(params.Content)); err != nil {
		return &Error{Code: ErrCodeStoreError, Message: err.Error()}
	}
	s.broadcastStoreList()
	return nil
}

func (s *Server) handleStoreDelete(ctx context.Context, req Request) *Error {
	var params StoreDeleteParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	project := s.state.State().GetProject(params.ProjectID)
	if project == nil {
		return &Error{Code: ErrCodeProjectNotFound, Message: "project not found"}
	}

	if err := s.store.Delete(params.ProjectID, params.Path); err != nil {
		return &Error{Code: ErrCodeStoreError, Message: err.Error()}
	}

	// Send updated file list
	s.broadcastStoreList()
	return nil
}

func (s *Server) broadcastStoreList() {
	state := s.state.State()
	var result []ProjectFilesData

	for _, project := range state.Projects {
		files, err := s.store.List(project.ID)
		if err != nil {
			continue
		}

		projectFiles := ProjectFilesData{
			ProjectID:   project.ID,
			ProjectName: project.Name,
			Files:       make([]FileInfo, len(files)),
		}
		for i, f := range files {
			projectFiles.Files[i] = FileInfo{Name: f.Name, Size: f.Size}
		}
		result = append(result, projectFiles)
	}

	s.sendEvent("store.list", result)
}

func (s *Server) handleStoreSend(ctx context.Context, req Request) *Error {
	var params StoreSendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &Error{Code: ErrCodeInvalidParams, Message: err.Error()}
	}

	project := s.state.State().GetProject(params.ProjectID)
	if project == nil {
		return &Error{Code: ErrCodeProjectNotFound, Message: "project not found"}
	}

	fullPath := filepath.Join(s.store.Path(params.ProjectID), params.Path)

	exe, err := os.Executable()
	if err != nil {
		return &Error{Code: ErrCodeInternalError, Message: "failed to get executable path: " + err.Error()}
	}

	if err := exec.Command(exe, "send", "-f", fullPath).Run(); err != nil {
		return &Error{Code: ErrCodeInternalError, Message: "failed to send file: " + err.Error()}
	}
	return nil
}

// isTextFile checks if file extension indicates a text file
func isTextFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	textExts := map[string]bool{
		".txt": true, ".md": true, ".json": true, ".yaml": true, ".yml": true,
		".xml": true, ".html": true, ".css": true, ".js": true, ".ts": true,
		".go": true, ".py": true, ".rb": true, ".rs": true, ".c": true, ".h": true,
		".cpp": true, ".java": true, ".sh": true, ".bash": true, ".zsh": true,
		".env": true, ".toml": true, ".ini": true, ".cfg": true, ".conf": true,
		".log": true, ".csv": true, ".sql": true, ".graphql": true, ".vue": true,
		".jsx": true, ".tsx": true, ".svelte": true, ".astro": true, ".swift": true,
		".kt": true, ".scala": true, ".pl": true, ".php": true, ".lua": true,
	}
	return textExts[ext]
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
