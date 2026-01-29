package state

import (
	"github.com/google/uuid"
	"pink-agent/internal/domain"
)

type Manager struct {
	state   *domain.State
	storage Storage
}

func NewManager(storage Storage) (*Manager, error) {
	state, err := storage.Load()
	if err != nil {
		return nil, err
	}
	return &Manager{state: state, storage: storage}, nil
}

func (m *Manager) State() *domain.State {
	return m.state
}

func (m *Manager) GetActiveSession() *domain.Session {
	return m.state.GetActiveSession()
}

func (m *Manager) CreateProject(name string) error {
	if !m.state.IsIdle() {
		return domain.ErrOperationInProgress
	}

	newState := m.state.Clone()
	project := domain.Project{
		ID:   uuid.New().String(),
		Name: name,
	}
	newState.Projects = append(newState.Projects, project)
	newState.ActiveProject = project.ID

	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.state = newState
	return nil
}

func (m *Manager) DeleteProject(id string) error {
	if !m.state.IsIdle() {
		return domain.ErrOperationInProgress
	}

	newState := m.state.Clone()
	idx := -1
	for i, p := range newState.Projects {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return domain.ErrProjectNotFound
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
		return err
	}
	m.state = newState
	return nil
}

func (m *Manager) RenameProject(id, name string) error {
	if !m.state.IsIdle() {
		return domain.ErrOperationInProgress
	}

	newState := m.state.Clone()
	project := newState.GetProject(id)
	if project == nil {
		return domain.ErrProjectNotFound
	}
	project.Name = name

	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.state = newState
	return nil
}

func (m *Manager) SwitchProject(id string) error {
	if !m.state.IsIdle() {
		return domain.ErrOperationInProgress
	}

	newState := m.state.Clone()
	if newState.GetProject(id) == nil {
		return domain.ErrProjectNotFound
	}
	newState.ActiveProject = id

	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.state = newState
	return nil
}


func (m *Manager) DeleteSession(claudeID string) error {
	newState := m.state.Clone()
	project := newState.GetActiveProject()
	if project == nil {
		return domain.ErrNoActiveProject
	}

	idx := -1
	for i, s := range project.Sessions {
		if s.ClaudeID == claudeID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return domain.ErrSessionNotFound
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
		return err
	}
	m.state = newState
	return nil
}

func (m *Manager) RenameSession(claudeID, name string) error {
	if !m.state.IsIdle() {
		return domain.ErrOperationInProgress
	}

	newState := m.state.Clone()
	project := newState.GetActiveProject()
	if project == nil {
		return domain.ErrNoActiveProject
	}

	session := project.GetSession(claudeID)
	if session == nil {
		return domain.ErrSessionNotFound
	}
	session.Name = name

	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.state = newState
	return nil
}

func (m *Manager) SwitchSession(claudeID string) error {
	if !m.state.IsIdle() {
		return domain.ErrOperationInProgress
	}

	newState := m.state.Clone()
	project := newState.GetActiveProject()
	if project == nil {
		return domain.ErrNoActiveProject
	}

	if project.GetSession(claudeID) == nil {
		return domain.ErrSessionNotFound
	}
	project.ActiveSession = claudeID

	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.state = newState
	return nil
}

// CreatePendingSession creates a session with "creating" status and temporary ID
func (m *Manager) CreatePendingSession(name, pendingID string) error {
	if !m.state.IsIdle() {
		return domain.ErrOperationInProgress
	}

	newState := m.state.Clone()
	project := newState.GetActiveProject()
	if project == nil {
		return domain.ErrNoActiveProject
	}

	session := domain.Session{
		ClaudeID: pendingID,
		Name:     name,
		Status:   domain.SessionStatusCreating,
	}
	project.Sessions = append(project.Sessions, session)
	project.ActiveSession = pendingID

	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.state = newState
	return nil
}

// FinishSession updates pending session with real ClaudeID and sets status to ready
func (m *Manager) FinishSession(pendingID, realClaudeID string) error {
	newState := m.state.Clone()
	project := newState.GetActiveProject()
	if project == nil {
		return domain.ErrNoActiveProject
	}

	session := project.GetSession(pendingID)
	if session == nil {
		return domain.ErrSessionNotFound
	}

	session.ClaudeID = realClaudeID
	session.Status = domain.SessionStatusReady
	project.ActiveSession = realClaudeID

	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.state = newState
	return nil
}

// CancelPendingSession removes a session that failed to create
func (m *Manager) CancelPendingSession(pendingID string) error {
	newState := m.state.Clone()
	project := newState.GetActiveProject()
	if project == nil {
		return domain.ErrNoActiveProject
	}

	idx := -1
	for i, s := range project.Sessions {
		if s.ClaudeID == pendingID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return domain.ErrSessionNotFound
	}

	project.Sessions = append(project.Sessions[:idx], project.Sessions[idx+1:]...)

	if project.ActiveSession == pendingID {
		if len(project.Sessions) > 0 {
			project.ActiveSession = project.Sessions[0].ClaudeID
		} else {
			project.ActiveSession = ""
		}
	}

	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.state = newState
	return nil
}

func (m *Manager) SetSessionStatus(claudeID string, status domain.SessionStatus) error {
	newState := m.state.Clone()
	project := newState.GetActiveProject()
	if project == nil {
		return domain.ErrNoActiveProject
	}

	session := project.GetSession(claudeID)
	if session == nil {
		return domain.ErrSessionNotFound
	}
	session.Status = status

	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.state = newState
	return nil
}

func (m *Manager) SetTerminalSize(cols, rows uint16) error {
	newState := m.state.Clone()
	newState.Cols = cols
	newState.Rows = rows

	if err := m.storage.Save(newState); err != nil {
		return err
	}
	m.state = newState
	return nil
}

func (m *Manager) GetTerminalSize() (uint16, uint16) {
	return m.state.Cols, m.state.Rows
}
