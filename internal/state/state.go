package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
)

var ErrProjectNotFound = errors.New("project not found")

type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ThreadID  int    `json:"threadId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
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

func (m *Manager) CreateProject(name string) (string, error) {
	id := uuid.New().String()
	err := m.mutate(func(s *State) (*State, error) {
		ns := s.Clone()
		ns.Projects = append(ns.Projects, Project{
			ID:   id,
			Name: name,
		})
		return ns, m.storage.Save(ns)
	})
	return id, err
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

// Migrate handles legacy formats. Returns project IDs that need forum topics created.
func Migrate(dataDir string, m *Manager) []string {
	orphaned := migrateOldFormat(dataDir, m)
	migrateTopics(dataDir, m)
	migrateContext(dataDir, m)
	return orphaned
}

// migrateTopics reads the old state format with topics[] and merges them into projects.
func migrateTopics(dataDir string, m *Manager) {
	path := filepath.Join(dataDir, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var old struct {
		Topics []struct {
			ThreadID  int    `json:"threadId"`
			ProjectID string `json:"projectId"`
			SessionID string `json:"sessionId"`
		} `json:"topics"`
	}
	if err := json.Unmarshal(data, &old); err != nil || len(old.Topics) == 0 {
		return
	}

	for _, t := range old.Topics {
		if t.ProjectID == "" || t.ThreadID == 0 {
			continue
		}
		p := m.GetProject(t.ProjectID)
		if p == nil || p.ThreadID != 0 {
			continue
		}
		m.SetProjectThread(t.ProjectID, t.ThreadID)
		if t.SessionID != "" {
			m.SetProjectSession(t.ProjectID, t.SessionID)
		}
	}
}

// migrateOldFormat handles the origin/main state format with activeProject and sessions.
// Projects are already loaded by Storage.Load() (Go drops unknown fields).
// Returns project IDs that need forum topics created.
func migrateOldFormat(dataDir string, m *Manager) []string {
	path := filepath.Join(dataDir, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var old struct {
		ActiveProject string `json:"activeProject"`
		Projects      []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &old); err != nil || old.ActiveProject == "" {
		return nil // Not old format
	}

	// Collect projects that need forum topics
	var orphaned []string
	for _, op := range old.Projects {
		if op.ID == "" {
			continue
		}
		p := m.GetProject(op.ID)
		if p == nil || p.ThreadID != 0 {
			continue
		}
		orphaned = append(orphaned, op.ID)
	}

	// Re-save in new format (drops activeProject, sessions, cols, rows)
	m.storage.Save(m.State())

	return orphaned
}

// migrateContext moves Context field from state.json projects into store PROJECT.md files.
func migrateContext(dataDir string, m *Manager) {
	path := filepath.Join(dataDir, "state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	// Parse with Context field to detect legacy data
	var old struct {
		Projects []struct {
			ID      string `json:"id"`
			Context string `json:"context"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return
	}

	storeDir := filepath.Join(dataDir, "store")
	changed := false

	for _, op := range old.Projects {
		if op.ID == "" || op.Context == "" {
			continue
		}
		content := strings.TrimSpace(op.Context)
		if content == "" || content == "(empty)" {
			continue
		}

		// Only write if store PROJECT.md is empty/missing
		mdPath := filepath.Join(storeDir, op.ID, "PROJECT.md")
		existing, _ := os.ReadFile(mdPath)
		existingStr := strings.TrimSpace(string(existing))
		if existingStr != "" && existingStr != "(empty)" {
			continue
		}

		// Write context to store
		dir := filepath.Join(storeDir, op.ID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			continue
		}
		if err := os.WriteFile(mdPath, []byte(content+"\n"), 0644); err != nil {
			continue
		}
		changed = true
	}

	if !changed {
		return
	}

	// Re-save state without Context field (already removed from struct, just re-save)
	s := m.State()
	m.storage.Save(s)
}
