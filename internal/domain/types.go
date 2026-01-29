package domain

import "errors"

var (
	ErrProjectNotFound     = errors.New("project not found")
	ErrSessionNotFound     = errors.New("session not found")
	ErrNoActiveProject     = errors.New("no active project")
	ErrNoActiveSession     = errors.New("No active session. Create a session in the Mini App first.")
	ErrOperationInProgress = errors.New("operation in progress")
)

type SessionStatus string

const (
	SessionStatusCreating   SessionStatus = "creating"
	SessionStatusReady      SessionStatus = "ready"
	SessionStatusCompacting SessionStatus = "compacting"
)


type Session struct {
	ClaudeID string        `json:"claudeId"`
	Name     string        `json:"name"`
	Status   SessionStatus `json:"status"`
}

func (s *Session) IsReady() bool {
	return s.Status == SessionStatusReady
}

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

type State struct {
	ActiveProject string    `json:"activeProject"`
	Projects      []Project `json:"projects"`
	Cols          uint16    `json:"cols"`
	Rows          uint16    `json:"rows"`
}

func (s *State) IsIdle() bool {
	// Check if any session is in creating/compacting state
	for _, p := range s.Projects {
		for _, sess := range p.Sessions {
			if sess.Status == SessionStatusCreating || sess.Status == SessionStatusCompacting {
				return false
			}
		}
	}
	return true
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
