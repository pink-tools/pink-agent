package projects

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

var (
	ErrProjectNotFound = errors.New("project not found")
	ErrSessionNotFound = errors.New("session not found")
	ErrNoActiveProject = errors.New("no active project")
	ErrNoActiveSession = errors.New("no active session, create one in the Mini App first")
)

// Session represents a ready Claude Code session.
// Only persisted sessions appear here — no transient states on disk.
type Session struct {
	ClaudeID string `json:"claudeId"`
	Name     string `json:"name"`
}

// Project represents a project containing sessions
type Project struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	ActiveSession string    `json:"activeSession"`
	Sessions      []Session `json:"sessions"`
}

func (p *Project) GetSession(claudeID string) *Session {
	for i := range p.Sessions {
		if p.Sessions[i].ClaudeID == claudeID {
			return &p.Sessions[i]
		}
	}
	return nil
}

func (p *Project) GetActiveSession() *Session {
	if p.ActiveSession == "" {
		return nil
	}
	return p.GetSession(p.ActiveSession)
}

// State represents the persisted application state.
// Only contains valid, complete data — never transient operations.
type State struct {
	ActiveProject string    `json:"activeProject"`
	Projects      []Project `json:"projects"`
	Cols          uint16    `json:"cols"`
	Rows          uint16    `json:"rows"`
}

func (s *State) GetProject(id string) *Project {
	for i := range s.Projects {
		if s.Projects[i].ID == id {
			return &s.Projects[i]
		}
	}
	return nil
}

func (s *State) GetActiveProject() *Project {
	if s.ActiveProject == "" {
		return nil
	}
	return s.GetProject(s.ActiveProject)
}

func (s *State) GetActiveSession() *Session {
	project := s.GetActiveProject()
	if project == nil {
		return nil
	}
	return project.GetActiveSession()
}

func (s *State) Clone() *State {
	clone := &State{
		ActiveProject: s.ActiveProject,
		Projects:      make([]Project, len(s.Projects)),
		Cols:          s.Cols,
		Rows:          s.Rows,
	}
	for i, p := range s.Projects {
		clone.Projects[i] = Project{
			ID:            p.ID,
			Name:          p.Name,
			ActiveSession: p.ActiveSession,
			Sessions:      make([]Session, len(p.Sessions)),
		}
		copy(clone.Projects[i].Sessions, p.Sessions)
	}
	return clone
}

// AllSessions returns all session IDs across all projects
func (s *State) AllSessions() []string {
	var ids []string
	for _, p := range s.Projects {
		for _, sess := range p.Sessions {
			ids = append(ids, sess.ClaudeID)
		}
	}
	return ids
}

// Storage interface for state persistence
type Storage interface {
	Load() (*State, error)
	Save(state *State) error
}

// FileStorage implements Storage using JSON file
type FileStorage struct {
	path string
}

func NewFileStorage(path string) *FileStorage {
	return &FileStorage{path: path}
}

func (s *FileStorage) Load() (*State, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *FileStorage) Save(state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmp, s.path)
}

// PendingSession represents an in-memory session being created.
// Never persisted to disk — crash = lost, not corrupted.
type PendingSession struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Error     string `json:"error,omitempty"`
}

// Manager handles state mutations.
// Persisted state uses clone-modify-save pattern.
// Pending sessions live in memory only.
type Manager struct {
	state    *State
	storage  Storage
	onChange func(*State, []PendingSession)

	mu       sync.Mutex
	pending  []PendingSession
}

func NewManager(storage Storage) (*Manager, error) {
	state, err := storage.Load()
	if err != nil {
		return nil, err
	}
	return &Manager{state: state, storage: storage}, nil
}

func (m *Manager) SetOnChange(fn func(*State, []PendingSession)) {
	m.onChange = fn
}

func (m *Manager) notifyChange() {
	if m.onChange != nil {
		m.onChange(m.state, m.pending)
	}
}

func (m *Manager) save(newState *State) error {
	m.state = newState
	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.notifyChange()
	return nil
}

func (m *Manager) State() *State {
	return m.state
}

func (m *Manager) PendingSessions() []PendingSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]PendingSession, len(m.pending))
	copy(result, m.pending)
	return result
}

func (m *Manager) GetActiveSession() *Session {
	return m.state.GetActiveSession()
}

// --- Projects ---

func (m *Manager) CreateProject(name string) error {
	newState := m.state.Clone()
	project := Project{
		ID:   uuid.New().String(),
		Name: name,
	}
	newState.Projects = append(newState.Projects, project)
	newState.ActiveProject = project.ID

	return m.save(newState)
}

func (m *Manager) DeleteProject(id string) error {
	newState := m.state.Clone()
	idx := -1
	for i, p := range newState.Projects {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrProjectNotFound
	}

	newState.Projects = append(newState.Projects[:idx], newState.Projects[idx+1:]...)

	if newState.ActiveProject == id {
		if len(newState.Projects) > 0 {
			newState.ActiveProject = newState.Projects[0].ID
		} else {
			newState.ActiveProject = ""
		}
	}

	return m.save(newState)
}

func (m *Manager) RenameProject(id, name string) error {
	newState := m.state.Clone()
	project := newState.GetProject(id)
	if project == nil {
		return ErrProjectNotFound
	}
	project.Name = name

	return m.save(newState)
}

func (m *Manager) SwitchProject(id string) error {
	newState := m.state.Clone()
	if newState.GetProject(id) == nil {
		return ErrProjectNotFound
	}
	newState.ActiveProject = id

	return m.save(newState)
}

// --- Sessions ---

// AddSession adds a ready session to a project and persists.
// If the project has no active session, this becomes active.
func (m *Manager) AddSession(projectID, claudeID, name string) error {
	newState := m.state.Clone()
	project := newState.GetProject(projectID)
	if project == nil {
		return ErrProjectNotFound
	}

	session := Session{
		ClaudeID: claudeID,
		Name:     name,
	}
	project.Sessions = append(project.Sessions, session)

	if project.ActiveSession == "" {
		project.ActiveSession = claudeID
	}

	return m.save(newState)
}

func (m *Manager) DeleteSession(claudeID string) error {
	newState := m.state.Clone()
	project := newState.GetActiveProject()
	if project == nil {
		return ErrNoActiveProject
	}

	idx := -1
	for i, s := range project.Sessions {
		if s.ClaudeID == claudeID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrSessionNotFound
	}

	project.Sessions = append(project.Sessions[:idx], project.Sessions[idx+1:]...)

	if project.ActiveSession == claudeID {
		if len(project.Sessions) > 0 {
			project.ActiveSession = project.Sessions[0].ClaudeID
		} else {
			project.ActiveSession = ""
		}
	}

	return m.save(newState)
}

func (m *Manager) RenameSession(claudeID, name string) error {
	newState := m.state.Clone()
	project := newState.GetActiveProject()
	if project == nil {
		return ErrNoActiveProject
	}

	session := project.GetSession(claudeID)
	if session == nil {
		return ErrSessionNotFound
	}
	session.Name = name

	return m.save(newState)
}

// SwitchSession updates the active session pointer.
// No PTY stop/start — all PTYs run concurrently.
func (m *Manager) SwitchSession(claudeID string) error {
	newState := m.state.Clone()

	var targetProject *Project
	for i := range newState.Projects {
		if newState.Projects[i].GetSession(claudeID) != nil {
			targetProject = &newState.Projects[i]
			break
		}
	}

	if targetProject == nil {
		return ErrSessionNotFound
	}

	newState.ActiveProject = targetProject.ID
	targetProject.ActiveSession = claudeID

	return m.save(newState)
}

func (m *Manager) SetTerminalSize(cols, rows uint16) error {
	newState := m.state.Clone()
	newState.Cols = cols
	newState.Rows = rows

	return m.save(newState)
}

func (m *Manager) GetTerminalSize() (uint16, uint16) {
	return m.state.Cols, m.state.Rows
}

// --- Pending Sessions (in-memory only) ---

// AddPendingSession tracks a session being created. Not persisted.
func (m *Manager) AddPendingSession(projectID, name string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := uuid.New().String()
	m.pending = append(m.pending, PendingSession{
		ID:        id,
		ProjectID: projectID,
		Name:      name,
	})

	m.notifyChange()
	return id
}

// FinishPendingSession removes pending entry and adds ready session to state.
func (m *Manager) FinishPendingSession(pendingID, claudeID string) error {
	m.mu.Lock()
	var found *PendingSession
	for i, p := range m.pending {
		if p.ID == pendingID {
			found = &m.pending[i]
			break
		}
	}
	if found == nil {
		m.mu.Unlock()
		return ErrSessionNotFound
	}

	projectID := found.ProjectID
	name := found.Name

	// Remove from pending
	for i, p := range m.pending {
		if p.ID == pendingID {
			m.pending = append(m.pending[:i], m.pending[i+1:]...)
			break
		}
	}
	m.mu.Unlock()

	return m.AddSession(projectID, claudeID, name)
}

// FailPendingSession marks a pending session as failed with error message.
func (m *Manager) FailPendingSession(pendingID, errorMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.pending {
		if m.pending[i].ID == pendingID {
			m.pending[i].Error = errorMsg
			break
		}
	}

	m.notifyChange()
}

// RemovePendingSession removes a pending session (user dismissed error).
func (m *Manager) RemovePendingSession(pendingID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, p := range m.pending {
		if p.ID == pendingID {
			m.pending = append(m.pending[:i], m.pending[i+1:]...)
			break
		}
	}

	m.notifyChange()
}
