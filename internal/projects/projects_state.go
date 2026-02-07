package projects

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"

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

// stateCmd is a mutation sent to the loop goroutine.
// silent=true means no notification after mutation (e.g. terminal size).
type stateCmd struct {
	fn     func(state *State, pending []PendingSession) (*State, []PendingSession, error)
	result chan error
	silent bool
}

// stateSnapshot is sent from loop() to notifyLoop() for broadcast.
type stateSnapshot struct {
	state   *State
	pending []PendingSession
}

// Manager handles state mutations via a serialized command loop.
// Reads are lock-free via atomic.Pointer.
// Mutations go through a command channel processed by loop().
// Notifications are decoupled via notifyLoop().
type Manager struct {
	state   atomic.Pointer[State]
	storage Storage

	pending []PendingSession // owned by loop() goroutine

	cmds     chan stateCmd
	notify   chan stateSnapshot
	done     chan struct{}
	noteDone chan struct{}

	onNotify func(*State, []PendingSession)
}

func NewManager(storage Storage, onNotify func(*State, []PendingSession)) (*Manager, error) {
	state, err := storage.Load()
	if err != nil {
		return nil, err
	}

	m := &Manager{
		storage:  storage,
		cmds:     make(chan stateCmd, 16),
		notify:   make(chan stateSnapshot, 1),
		done:     make(chan struct{}),
		noteDone: make(chan struct{}),
		onNotify: onNotify,
	}
	m.state.Store(state)

	go m.loop()
	go m.notifyLoop()

	return m, nil
}

// Close shuts down the command loop. Must be called on shutdown.
func (m *Manager) Close() {
	close(m.cmds)
	<-m.done
	<-m.noteDone
}

// loop processes mutations sequentially from the cmds channel.
// Owns pending slice — no external synchronization needed.
func (m *Manager) loop() {
	defer func() {
		close(m.notify)
		close(m.done)
	}()
	for cmd := range m.cmds {
		currentState := m.state.Load()
		newState, newPending, err := cmd.fn(currentState, m.pending)
		if err != nil {
			cmd.result <- err
			continue
		}
		if newState != nil {
			m.state.Store(newState)
		}
		m.pending = newPending
		cmd.result <- nil

		if cmd.silent {
			continue
		}

		// Send snapshot for notification (non-blocking, latest wins)
		pendingCopy := make([]PendingSession, len(m.pending))
		copy(pendingCopy, m.pending)

		snap := stateSnapshot{
			state:   m.state.Load(),
			pending: pendingCopy,
		}

		select {
		case m.notify <- snap:
		default:
			// Channel full — drain and replace with latest
			select {
			case <-m.notify:
			default:
			}
			m.notify <- snap
		}
	}
}

// notifyLoop reads snapshots and calls the broadcast callback.
// Decoupled from mutations — broadcast can block (e.g. WebSocket write)
// without holding up the mutation loop.
func (m *Manager) notifyLoop() {
	defer close(m.noteDone)
	for snap := range m.notify {
		if m.onNotify != nil {
			m.onNotify(snap.state, snap.pending)
		}
	}
}

func (m *Manager) mutate(fn func(*State, []PendingSession) (*State, []PendingSession, error)) error {
	result := make(chan error, 1)
	m.cmds <- stateCmd{fn: fn, result: result}
	return <-result
}

func (m *Manager) mutateSilent(fn func(*State, []PendingSession) (*State, []PendingSession, error)) error {
	result := make(chan error, 1)
	m.cmds <- stateCmd{fn: fn, result: result, silent: true}
	return <-result
}

// --- Lock-free reads ---

func (m *Manager) State() *State {
	return m.state.Load()
}

func (m *Manager) GetActiveSession() *Session {
	return m.state.Load().GetActiveSession()
}

func (m *Manager) PendingSessions() []PendingSession {
	ch := make(chan []PendingSession, 1)
	m.cmds <- stateCmd{
		fn: func(s *State, p []PendingSession) (*State, []PendingSession, error) {
			cp := make([]PendingSession, len(p))
			copy(cp, p)
			ch <- cp
			return nil, p, nil
		},
		result: make(chan error, 1),
		silent: true,
	}
	return <-ch
}

// --- Projects ---

func (m *Manager) CreateProject(name string) error {
	return m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		newState := s.Clone()
		project := Project{
			ID:   uuid.New().String(),
			Name: name,
		}
		newState.Projects = append(newState.Projects, project)
		newState.ActiveProject = project.ID

		if err := m.storage.Save(newState); err != nil {
			return nil, p, err
		}
		return newState, p, nil
	})
}

func (m *Manager) DeleteProject(id string) error {
	return m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		newState := s.Clone()
		idx := -1
		for i, proj := range newState.Projects {
			if proj.ID == id {
				idx = i
				break
			}
		}
		if idx == -1 {
			return nil, p, ErrProjectNotFound
		}

		newState.Projects = append(newState.Projects[:idx], newState.Projects[idx+1:]...)

		if newState.ActiveProject == id {
			if len(newState.Projects) > 0 {
				newState.ActiveProject = newState.Projects[0].ID
			} else {
				newState.ActiveProject = ""
			}
		}

		if err := m.storage.Save(newState); err != nil {
			return nil, p, err
		}
		return newState, p, nil
	})
}

func (m *Manager) RenameProject(id, name string) error {
	return m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		newState := s.Clone()
		project := newState.GetProject(id)
		if project == nil {
			return nil, p, ErrProjectNotFound
		}
		project.Name = name

		if err := m.storage.Save(newState); err != nil {
			return nil, p, err
		}
		return newState, p, nil
	})
}

func (m *Manager) SwitchProject(id string) error {
	return m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		newState := s.Clone()
		if newState.GetProject(id) == nil {
			return nil, p, ErrProjectNotFound
		}
		newState.ActiveProject = id

		if err := m.storage.Save(newState); err != nil {
			return nil, p, err
		}
		return newState, p, nil
	})
}

// --- Sessions ---

func (m *Manager) AddSession(projectID, claudeID, name string) error {
	return m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		newState := s.Clone()
		project := newState.GetProject(projectID)
		if project == nil {
			return nil, p, ErrProjectNotFound
		}

		project.Sessions = append(project.Sessions, Session{ClaudeID: claudeID, Name: name})
		if project.ActiveSession == "" {
			project.ActiveSession = claudeID
		}

		if err := m.storage.Save(newState); err != nil {
			return nil, p, err
		}
		return newState, p, nil
	})
}

func (m *Manager) DeleteSession(claudeID string) error {
	return m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		newState := s.Clone()
		project := newState.GetActiveProject()
		if project == nil {
			return nil, p, ErrNoActiveProject
		}

		idx := -1
		for i, sess := range project.Sessions {
			if sess.ClaudeID == claudeID {
				idx = i
				break
			}
		}
		if idx == -1 {
			return nil, p, ErrSessionNotFound
		}

		project.Sessions = append(project.Sessions[:idx], project.Sessions[idx+1:]...)

		if project.ActiveSession == claudeID {
			if len(project.Sessions) > 0 {
				project.ActiveSession = project.Sessions[0].ClaudeID
			} else {
				project.ActiveSession = ""
			}
		}

		if err := m.storage.Save(newState); err != nil {
			return nil, p, err
		}
		return newState, p, nil
	})
}

func (m *Manager) RenameSession(claudeID, name string) error {
	return m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		newState := s.Clone()
		project := newState.GetActiveProject()
		if project == nil {
			return nil, p, ErrNoActiveProject
		}

		session := project.GetSession(claudeID)
		if session == nil {
			return nil, p, ErrSessionNotFound
		}
		session.Name = name

		if err := m.storage.Save(newState); err != nil {
			return nil, p, err
		}
		return newState, p, nil
	})
}

func (m *Manager) SwitchSession(claudeID string) error {
	return m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		newState := s.Clone()

		var targetProject *Project
		for i := range newState.Projects {
			if newState.Projects[i].GetSession(claudeID) != nil {
				targetProject = &newState.Projects[i]
				break
			}
		}
		if targetProject == nil {
			return nil, p, ErrSessionNotFound
		}

		newState.ActiveProject = targetProject.ID
		targetProject.ActiveSession = claudeID

		if err := m.storage.Save(newState); err != nil {
			return nil, p, err
		}
		return newState, p, nil
	})
}

func (m *Manager) SetTerminalSize(cols, rows uint16) error {
	return m.mutateSilent(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		newState := s.Clone()
		newState.Cols = cols
		newState.Rows = rows

		if err := m.storage.Save(newState); err != nil {
			return nil, p, err
		}
		return newState, p, nil
	})
}

func (m *Manager) GetTerminalSize() (uint16, uint16) {
	s := m.state.Load()
	return s.Cols, s.Rows
}

// --- Pending Sessions (in-memory only) ---

func (m *Manager) AddPendingSession(projectID, name string) string {
	id := uuid.New().String()
	m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		p = append(p, PendingSession{
			ID:        id,
			ProjectID: projectID,
			Name:      name,
		})
		return nil, p, nil
	})
	return id
}

func (m *Manager) FinishPendingSession(pendingID, claudeID string) error {
	return m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		var found *PendingSession
		for i := range p {
			if p[i].ID == pendingID {
				found = &p[i]
				break
			}
		}
		if found == nil {
			return nil, p, ErrSessionNotFound
		}

		projectID := found.ProjectID
		name := found.Name

		// Remove from pending
		for i := range p {
			if p[i].ID == pendingID {
				p = append(p[:i], p[i+1:]...)
				break
			}
		}

		// Add session to state
		newState := s.Clone()
		project := newState.GetProject(projectID)
		if project == nil {
			return nil, p, ErrProjectNotFound
		}

		project.Sessions = append(project.Sessions, Session{ClaudeID: claudeID, Name: name})
		if project.ActiveSession == "" {
			project.ActiveSession = claudeID
		}

		if err := m.storage.Save(newState); err != nil {
			return nil, p, err
		}
		return newState, p, nil
	})
}

func (m *Manager) FailPendingSession(pendingID, errorMsg string) {
	m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		for i := range p {
			if p[i].ID == pendingID {
				p[i].Error = errorMsg
				break
			}
		}
		return nil, p, nil
	})
}

func (m *Manager) RemovePendingSession(pendingID string) {
	m.mutate(func(s *State, p []PendingSession) (*State, []PendingSession, error) {
		for i := range p {
			if p[i].ID == pendingID {
				p = append(p[:i], p[i+1:]...)
				break
			}
		}
		return nil, p, nil
	})
}
