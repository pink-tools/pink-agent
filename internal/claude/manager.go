package claude

import (
	"errors"
	"sync"
)

var ErrProcessDead = errors.New("process not running")

type Manager struct {
	mcpConfig string
	processes map[int]*Process // threadID → process
	mu        sync.Mutex
}

func NewManager(mcpConfig string) *Manager {
	return &Manager{
		mcpConfig: mcpConfig,
		processes: make(map[int]*Process),
	}
}

// Start spawns a claude process for the given thread.
// If sessionID is empty, starts a new session. Otherwise resumes.
func (m *Manager) Start(threadID int, sessionID, workDir string, extraEnv []string, onEvent EventHandler) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing process for this thread
	if old, ok := m.processes[threadID]; ok {
		old.Stop()
		delete(m.processes, threadID)
	}

	p, err := Start(sessionID, m.mcpConfig, workDir, extraEnv, func(ev OutputEvent) {
		onEvent(ev)

		// Auto-remove on process death after result event
		if ev.Type == "result" {
			// Process might still be alive (waiting for next input)
			// Don't remove — it stays alive for follow-up messages
		}
	})
	if err != nil {
		return err
	}

	m.processes[threadID] = p
	return nil
}

// Send sends a message to the process for the given thread.
func (m *Manager) Send(threadID int, text string) error {
	m.mu.Lock()
	p, ok := m.processes[threadID]
	m.mu.Unlock()

	if !ok {
		return ErrProcessDead
	}
	return p.Send(text)
}

// Interrupt sends SIGINT to the process for the given thread.
func (m *Manager) Interrupt(threadID int) {
	m.mu.Lock()
	p, ok := m.processes[threadID]
	m.mu.Unlock()

	if ok {
		p.Interrupt()
	}
}

// Stop kills the process for the given thread.
func (m *Manager) Stop(threadID int) {
	m.mu.Lock()
	p, ok := m.processes[threadID]
	if ok {
		delete(m.processes, threadID)
	}
	m.mu.Unlock()

	if ok {
		p.Stop()
	}
}

// Alive returns whether a process is running for the given thread.
func (m *Manager) Alive(threadID int) bool {
	m.mu.Lock()
	p, ok := m.processes[threadID]
	m.mu.Unlock()

	return ok && p.Alive()
}

// StopAll kills all processes.
func (m *Manager) StopAll() {
	m.mu.Lock()
	procs := make(map[int]*Process, len(m.processes))
	for k, v := range m.processes {
		procs[k] = v
	}
	m.processes = make(map[int]*Process)
	m.mu.Unlock()

	for _, p := range procs {
		p.Stop()
	}
}
