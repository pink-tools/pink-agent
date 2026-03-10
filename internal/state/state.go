package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/google/uuid"
)

var ErrProjectNotFound = errors.New("project not found")

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ThreadID  int    `json:"threadId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Dir       string `json:"dir"`
}

type State struct {
	Projects []Project `json:"projects"`
}

func (s *State) GetProject(id string) *Project {
	for i := range s.Projects {
		if s.Projects[i].ID == id {
			return &s.Projects[i]
		}
	}
	return nil
}

func (s *State) GetProjectByThread(threadID int) *Project {
	if threadID == 0 {
		return nil
	}
	for i := range s.Projects {
		if s.Projects[i].ThreadID == threadID {
			return &s.Projects[i]
		}
	}
	return nil
}

func (s *State) Clone() *State {
	clone := &State{
		Projects: make([]Project, len(s.Projects)),
	}
	copy(clone.Projects, s.Projects)
	return clone
}

// Storage persists state to disk.
type Storage struct {
	path string
}

func NewStorage(path string) *Storage {
	return &Storage{path: path}
}

func (s *Storage) Load() (*State, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return &State{}, nil
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *Storage) Save(state *State) error {
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

// stateCmd is a mutation sent to the loop goroutine.
type stateCmd struct {
	fn     func(s *State) (*State, error)
	result chan error
}

// Manager handles state mutations via a serialized command loop.
// Reads are lock-free via atomic.Pointer.
type Manager struct {
	state   atomic.Pointer[State]
	storage *Storage
	cmds    chan stateCmd
	done    chan struct{}
}

func NewManager(storage *Storage) (*Manager, error) {
	s, err := storage.Load()
	if err != nil {
		return nil, err
	}

	m := &Manager{
		storage: storage,
		cmds:    make(chan stateCmd, 16),
		done:    make(chan struct{}),
	}
	m.state.Store(s)

	go m.loop()
	return m, nil
}

func (m *Manager) Close() {
	close(m.cmds)
	<-m.done
}

func (m *Manager) loop() {
	defer close(m.done)
	for cmd := range m.cmds {
		current := m.state.Load()
		newState, err := cmd.fn(current)
		if err != nil {
			cmd.result <- err
			continue
		}
		if newState != nil {
			m.state.Store(newState)
		}
		cmd.result <- nil
	}
}

func (m *Manager) mutate(fn func(*State) (*State, error)) error {
	result := make(chan error, 1)
	m.cmds <- stateCmd{fn: fn, result: result}
	return <-result
}

// --- Lock-free reads ---

func (m *Manager) State() *State {
	return m.state.Load()
}

func (m *Manager) GetProject(id string) *Project {
	return m.state.Load().GetProject(id)
}

func (m *Manager) GetProjectByThread(threadID int) *Project {
	return m.state.Load().GetProjectByThread(threadID)
}

// --- Projects ---

func (m *Manager) CreateProject(name, dir string) (string, error) {
	id := uuid.New().String()
	err := m.mutate(func(s *State) (*State, error) {
		ns := s.Clone()
		ns.Projects = append(ns.Projects, Project{
			ID:   id,
			Name: name,
			Dir:  dir,
		})
		return ns, m.storage.Save(ns)
	})
	return id, err
}

func (m *Manager) SetProjectDir(id, dir string) error {
	return m.mutate(func(s *State) (*State, error) {
		ns := s.Clone()
		p := ns.GetProject(id)
		if p == nil {
			return nil, ErrProjectNotFound
		}
		p.Dir = dir
		return ns, m.storage.Save(ns)
	})
}

func (m *Manager) DeleteProject(id string) error {
	return m.mutate(func(s *State) (*State, error) {
		ns := s.Clone()
		idx := -1
		for i, p := range ns.Projects {
			if p.ID == id {
				idx = i
				break
			}
		}
		if idx == -1 {
			return nil, ErrProjectNotFound
		}
		ns.Projects = append(ns.Projects[:idx], ns.Projects[idx+1:]...)
		return ns, m.storage.Save(ns)
	})
}

func (m *Manager) RenameProject(id, name string) error {
	return m.mutate(func(s *State) (*State, error) {
		ns := s.Clone()
		p := ns.GetProject(id)
		if p == nil {
			return nil, ErrProjectNotFound
		}
		p.Name = name
		return ns, m.storage.Save(ns)
	})
}

func (m *Manager) SetProjectThread(id string, threadID int) error {
	return m.mutate(func(s *State) (*State, error) {
		ns := s.Clone()
		p := ns.GetProject(id)
		if p == nil {
			return nil, ErrProjectNotFound
		}
		p.ThreadID = threadID
		return ns, m.storage.Save(ns)
	})
}

func (m *Manager) SetProjectSession(id, sessionID string) error {
	return m.mutate(func(s *State) (*State, error) {
		ns := s.Clone()
		p := ns.GetProject(id)
		if p == nil {
			return nil, ErrProjectNotFound
		}
		p.SessionID = sessionID
		return ns, m.storage.Save(ns)
	})
}

func (m *Manager) ClearProjectThread(id string) error {
	return m.mutate(func(s *State) (*State, error) {
		ns := s.Clone()
		p := ns.GetProject(id)
		if p == nil {
			return nil, ErrProjectNotFound
		}
		p.ThreadID = 0
		p.SessionID = ""
		return ns, m.storage.Save(ns)
	})
}


