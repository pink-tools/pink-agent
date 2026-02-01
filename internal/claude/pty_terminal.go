package claude

import (
	"bytes"
	"context"
	"errors"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	gopty "github.com/aymanbagabas/go-pty"
	core "github.com/pink-tools/pink-core"
	otel "github.com/pink-tools/pink-otel"
	"pink-agent/internal/platform"
	"pink-agent/internal/projects"
)

var readyMarker = []byte("tokens")
var numberRe = regexp.MustCompile(`\d+`)

const inputDelay = 250 * time.Millisecond

type StateProvider interface {
	GetActiveSession() *projects.Session
	GetTerminalSize() (uint16, uint16)
}

type Manager struct {
	state     StateProvider
	mcpConfig string

	sessionID     string
	pty           gopty.Pty
	cmd           *gopty.Cmd
	buffer        *RingBuffer
	ready         bool
	queue         []string
	onOutput      func([]byte)
	utf8Remainder []byte
	mu            sync.Mutex
}

func NewManager(state StateProvider, mcpConfig string) *Manager {
	return &Manager{
		state:     state,
		mcpConfig: mcpConfig,
		buffer:    NewRingBuffer(10 * 1024 * 1024), // 10MB
	}
}

func (m *Manager) Write(text string) error {
	session := m.state.GetActiveSession()
	if session == nil {
		return projects.ErrNoActiveSession
	}

	// If PTY not running or wrong session, start with this message already in queue
	// This prevents race condition where flushQueue runs before we add to queue
	if !m.isRunning() || m.sessionID != session.ClaudeID {
		m.startWithMessage(session.ClaudeID, text)
		return nil
	}

	if !m.ready {
		m.queue = append(m.queue, text)
		return nil
	}

	return m.writeToPTY(text)
}

func (m *Manager) writeToPTY(text string) error {
	if m.pty == nil {
		return errors.New("pty not running")
	}
	m.writeAndSubmit(text)
	return nil
}

func (m *Manager) writeAndSubmit(text string) {
	if m.pty == nil {
		otel.Error(context.Background(), "writeAndSubmit: pty is nil")
		return
	}
	m.pty.Write([]byte(text))
	time.Sleep(inputDelay)
	m.pty.Write([]byte("\r"))
}

func (m *Manager) Resize(cols, rows uint16) {
	if m.pty != nil {
		m.pty.Resize(int(cols), int(rows))
	}
}

func (m *Manager) SendEscape() {
	if m.pty != nil {
		m.pty.Write([]byte{0x1b}) // ESC
	}
}

func (m *Manager) Buffer() []byte {
	return m.buffer.Bytes()
}

// Tokens returns current token count from PTY buffer
func (m *Manager) Tokens() string {
	buf := m.buffer.Bytes()
	idx := bytes.LastIndex(buf, readyMarker)
	if idx == -1 {
		return ""
	}
	// Find last number before "tokens"
	before := buf[:idx]
	matches := numberRe.FindAll(before, -1)
	if len(matches) == 0 {
		return ""
	}
	return string(matches[len(matches)-1])
}

func (m *Manager) SetOutputHandler(fn func([]byte)) {
	m.onOutput = fn
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil {
		return
	}

	// Log session stopping
	if session := m.state.GetActiveSession(); session != nil {
		otel.Info(context.Background(), "session stopping", otel.Attr{"name", session.Name})
	}

	if m.cmd.Process != nil {
		m.cmd.Process.Kill()
		go m.cmd.Wait()
	}
	if m.pty != nil {
		m.pty.Close()
	}
	m.cmd = nil
	m.pty = nil
	m.ready = false
}

func (m *Manager) Start() {
	session := m.state.GetActiveSession()
	if session == nil {
		return
	}
	if !m.isRunning() || m.sessionID != session.ClaudeID {
		m.start(session.ClaudeID)
	}
}

func (m *Manager) isRunning() bool {
	if m.cmd == nil || m.cmd.Process == nil {
		return false
	}
	return m.cmd.ProcessState == nil
}

func (m *Manager) start(sessionID string) {
	m.startWithMessage(sessionID, "")
}

func (m *Manager) startWithMessage(sessionID string, initialMessage string) {
	m.Stop()

	m.sessionID = sessionID
	m.ready = false
	if initialMessage != "" {
		m.queue = []string{initialMessage}
	} else {
		m.queue = nil
	}
	m.buffer.Clear()
	m.utf8Remainder = nil

	// Log session starting
	sessionName := sessionID[:8] // fallback to truncated ID
	if session := m.state.GetActiveSession(); session != nil {
		sessionName = session.Name
	}
	otel.Info(context.Background(), "session starting", otel.Attr{"name", sessionName})

	cols, rows := m.state.GetTerminalSize()

	// Create PTY
	p, err := gopty.New()
	if err != nil {
		otel.Error(context.Background(), "failed to create pty", otel.Attr{"error", err.Error()}, otel.Attr{"sessionID", sessionID})
		return
	}

	p.Resize(int(cols), int(rows))

	// Build command
	command, prefixArgs := platform.ClaudeExecutable()
	args := append(prefixArgs, "--resume", sessionID, "--dangerously-skip-permissions")
	if m.mcpConfig != "" {
		if _, err := os.Stat(m.mcpConfig); err == nil {
			args = append(args, "--mcp-config", m.mcpConfig)
		}
	}

	cmd := p.Command(command, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	cmd.Dir = core.BaseDir()

	if err := cmd.Start(); err != nil {
		otel.Error(context.Background(), "failed to start pty command", otel.Attr{"error", err.Error()}, otel.Attr{"sessionID", sessionID}, otel.Attr{"command", command}, otel.Attr{"args", args})
		p.Close()
		return
	}

	m.pty = p
	m.cmd = cmd

	go m.readLoop(p)
}

func (m *Manager) readLoop(pty gopty.Pty) {
	buf := make([]byte, 4096)
	for {
		n, err := pty.Read(buf)
		if err != nil {
			// Check if process exited with error (not from Stop())
			cmd := m.cmd
			if cmd != nil {
				cmd.Wait()
				if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
					otel.Error(context.Background(), "claude exited with error",
						otel.Attr{"exitCode", cmd.ProcessState.ExitCode()},
						otel.Attr{"sessionID", m.sessionID[:8]},
					)
				}
			}
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		// Handle UTF-8 boundaries
		if len(m.utf8Remainder) > 0 {
			data = append(m.utf8Remainder, data...)
			m.utf8Remainder = nil
		}

		validEnd := findValidUTF8End(data)
		if validEnd < len(data) {
			m.utf8Remainder = make([]byte, len(data)-validEnd)
			copy(m.utf8Remainder, data[validEnd:])
			data = data[:validEnd]
		}

		if len(data) == 0 {
			continue
		}

		m.buffer.Write(data)

		// Check buffer for ready marker after writing
		if !m.ready && bytes.Contains(m.buffer.Bytes(), readyMarker) {
			m.ready = true
			go m.flushQueue()
		}

		if m.onOutput != nil {
			m.onOutput(data)
		}
	}
}

func (m *Manager) flushQueue() {
	// Log session ready
	sessionName := m.sessionID[:8]
	if session := m.state.GetActiveSession(); session != nil {
		sessionName = session.Name
	}
	otel.Info(context.Background(), "session ready", otel.Attr{"name", sessionName}, otel.Attr{"queueLen", len(m.queue)})

	if len(m.queue) == 0 {
		return
	}

	// Small delay after tokens marker
	time.Sleep(500 * time.Millisecond)

	combined := m.combineQueue()
	m.queue = nil
	m.writeAndSubmit(combined)
}

func (m *Manager) combineQueue() string {
	return strings.Join(m.queue, "\n")
}

// RingBuffer is a simple circular buffer
type RingBuffer struct {
	data []byte
	size int
	pos  int
	full bool
}

func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		data: make([]byte, size),
		size: size,
	}
}

func (r *RingBuffer) Write(p []byte) {
	for _, b := range p {
		r.data[r.pos] = b
		r.pos = (r.pos + 1) % r.size
		if r.pos == 0 {
			r.full = true
		}
	}
}

func (r *RingBuffer) Bytes() []byte {
	if !r.full {
		return r.data[:r.pos]
	}
	result := make([]byte, r.size)
	copy(result, r.data[r.pos:])
	copy(result[r.size-r.pos:], r.data[:r.pos])
	return result
}

func (r *RingBuffer) Clear() {
	r.pos = 0
	r.full = false
}

// findValidUTF8End returns index of last complete UTF-8 sequence.
func findValidUTF8End(data []byte) int {
	if len(data) == 0 {
		return 0
	}

	for i := 1; i <= 4 && i <= len(data); i++ {
		idx := len(data) - i
		b := data[idx]

		if b&0xC0 == 0x80 {
			continue
		}

		var expectedLen int
		switch {
		case b&0x80 == 0x00:
			expectedLen = 1
		case b&0xE0 == 0xC0:
			expectedLen = 2
		case b&0xF0 == 0xE0:
			expectedLen = 3
		case b&0xF8 == 0xF0:
			expectedLen = 4
		default:
			return len(data)
		}

		if len(data)-idx >= expectedLen {
			return len(data)
		}
		return idx
	}

	return len(data)
}
