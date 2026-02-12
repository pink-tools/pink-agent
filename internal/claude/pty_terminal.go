package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	gopty "github.com/aymanbagabas/go-pty"
	core "github.com/pink-tools/pink-core"
	"github.com/pink-tools/pink-core/log"
	"pink-agent/internal/platform"
)

var readyMarker = []byte("bypass")
var contextTokensRe = regexp.MustCompile(`(\d+)\x1b\[1Ctokens`)

const (
	inputDelay     = 250 * time.Millisecond
	fileInputDelay = 1500 * time.Millisecond
)

// Terminal is a single PTY process for one Claude Code session
type Terminal struct {
	sessionID     string
	name          string
	manager       *Manager
	pty           gopty.Pty
	cmd           *gopty.Cmd
	buffer        *RingBuffer
	ready         atomic.Bool
	queue         []string
	utf8Remainder []byte
	onExit        func(string)
	mu            sync.Mutex
}

func (t *Terminal) Write(text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.isRunning() {
		return fmt.Errorf("terminal %s not running", t.name)
	}

	if !t.ready.Load() {
		t.queue = append(t.queue, text)
		return nil
	}

	return t.writeToPTY(text)
}

func (t *Terminal) writeToPTY(text string) error {
	if t.pty == nil {
		return errors.New("pty not running")
	}
	return t.writeAndSubmit(text)
}

func (t *Terminal) writeAndSubmit(text string) error {
	if t.pty == nil {
		log.Error(context.Background(), "writeAndSubmit: pty is nil", log.Attr{K: "sessionID", V: t.sessionID[:min(8, len(t.sessionID))]})
		return errors.New("pty not running")
	}

	if runtime.GOOS == "windows" {
		// Write rune by rune — bulk writes to ConPTY trigger Claude Code's
		// paste mode which truncates input (keeps only last buffer chunk).
		data := []byte(text)
		for i := 0; i < len(data); {
			_, size := utf8.DecodeRune(data[i:])
			if _, err := t.pty.Write(data[i : i+size]); err != nil {
				return fmt.Errorf("pty write: %w", err)
			}
			i += size
		}
	} else {
		if _, err := t.pty.Write([]byte(text)); err != nil {
			return fmt.Errorf("pty write: %w", err)
		}
	}

	delay := inputDelay
	if containsFilePath(text) {
		delay = fileInputDelay
	}
	time.Sleep(delay)
	if _, err := t.pty.Write([]byte("\r")); err != nil {
		return fmt.Errorf("pty write newline: %w", err)
	}
	return nil
}

var filePathRe = regexp.MustCompile(`(?m)^/[^\s]+\.\w+$`)

func containsFilePath(text string) bool {
	return filePathRe.MatchString(text)
}

func (t *Terminal) Resize(cols, rows uint16) {
	t.mu.Lock()
	p := t.pty
	t.mu.Unlock()
	if p != nil {
		p.Resize(int(cols), int(rows))
	}
}

func (t *Terminal) SendEscape() {
	t.mu.Lock()
	p := t.pty
	t.mu.Unlock()
	if p != nil {
		p.Write([]byte{0x1b})
	}
}

func (t *Terminal) Buffer() []byte {
	return t.buffer.Bytes()
}

func (t *Terminal) Tokens() string {
	buf := t.buffer.Bytes()
	matches := contextTokensRe.FindAllSubmatch(buf, -1)
	if len(matches) == 0 {
		return ""
	}
	return string(matches[len(matches)-1][1])
}

func (t *Terminal) isRunning() bool {
	if t.cmd == nil || t.cmd.Process == nil {
		return false
	}
	return t.cmd.ProcessState == nil
}

func (t *Terminal) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.cmd == nil {
		return
	}

	log.Info(context.Background(), "session stopping", log.Attr{K: "name", V: t.name})

	cmd := t.cmd
	if cmd.Process != nil {
		cmd.Process.Kill()
		go cmd.Wait()
	}
	if t.pty != nil {
		t.pty.Close()
	}
	t.cmd = nil
	t.pty = nil
	t.ready.Store(false)
}

func (t *Terminal) readLoop(pty gopty.Pty) {
	defer func() {
		if r := recover(); r != nil {
			log.Error(context.Background(), "readLoop panic", log.Attr{K: "recover", V: fmt.Sprintf("%v", r)}, log.Attr{K: "sessionID", V: t.sessionID[:min(8, len(t.sessionID))]})
		}
		if t.onExit != nil {
			t.onExit(t.sessionID)
		}
	}()

	buf := make([]byte, 4096)
	for {
		n, err := pty.Read(buf)
		if err != nil {
			t.mu.Lock()
			cmd := t.cmd
			t.mu.Unlock()
			if cmd != nil {
				cmd.Wait()
				if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
					log.Error(context.Background(), "claude exited with error",
						log.Attr{K: "exitCode", V: cmd.ProcessState.ExitCode()},
						log.Attr{K: "sessionID", V: t.sessionID[:min(8, len(t.sessionID))]},
					)
				}
			}
			return
		}

		data := make([]byte, n)
		copy(data, buf[:n])

		if len(t.utf8Remainder) > 0 {
			data = append(t.utf8Remainder, data...)
			t.utf8Remainder = nil
		}

		validEnd := findValidUTF8End(data)
		if validEnd < len(data) {
			t.utf8Remainder = make([]byte, len(data)-validEnd)
			copy(t.utf8Remainder, data[validEnd:])
			data = data[:validEnd]
		}

		if len(data) == 0 {
			continue
		}

		t.buffer.Write(data)

		if !t.ready.Load() && bytes.Contains(t.buffer.Bytes(), readyMarker) {
			t.ready.Store(true)
			go t.flushQueue()
		}

		if t.manager.onOutput != nil {
			t.manager.onOutput(t.sessionID, data)
		}
	}
}

func (t *Terminal) flushQueue() {
	defer func() {
		if r := recover(); r != nil {
			log.Error(context.Background(), "flushQueue panic", log.Attr{K: "recover", V: fmt.Sprintf("%v", r)}, log.Attr{K: "sessionID", V: t.sessionID[:min(8, len(t.sessionID))]})
		}
	}()

	t.mu.Lock()
	defer t.mu.Unlock()

	log.Info(context.Background(), "session ready", log.Attr{K: "name", V: t.name}, log.Attr{K: "queueLen", V: len(t.queue)})

	if len(t.queue) == 0 {
		return
	}

	combined := strings.Join(t.queue, "\n")
	t.queue = nil

	if t.pty == nil {
		return
	}
	if err := t.writeAndSubmit(combined); err != nil {
		log.Error(context.Background(), "flushQueue write failed", log.Attr{K: "error", V: err.Error()}, log.Attr{K: "sessionID", V: t.sessionID[:min(8, len(t.sessionID))]})
	}
}

// Manager manages all Terminal instances
type Manager struct {
	terminals map[string]*Terminal
	mcpConfig string
	cols      uint16
	rows      uint16
	onOutput  func(string, []byte)
	mu        sync.Mutex
}

func NewManager(mcpConfig string, onOutput func(sessionID string, data []byte)) *Manager {
	return &Manager{
		terminals: make(map[string]*Terminal),
		mcpConfig: mcpConfig,
		onOutput:  onOutput,
	}
}

func (m *Manager) SetTerminalSize(cols, rows uint16) {
	m.mu.Lock()
	m.cols = cols
	m.rows = rows
	m.mu.Unlock()
	m.ResizeAll(cols, rows)
}

func (m *Manager) StartSession(sessionID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Stop existing terminal for this session if any
	if t, ok := m.terminals[sessionID]; ok {
		t.stop()
	}

	t := &Terminal{
		sessionID: sessionID,
		name:      name,
		manager:   m,
		buffer:    NewRingBuffer(10 * 1024 * 1024),
	}

	// Self-cleanup: when process exits, remove from map (only if still the same terminal)
	t.onExit = func(id string) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.terminals[id] == t {
			delete(m.terminals, id)
			log.Info(context.Background(), "terminal self-cleanup", log.Attr{K: "name", V: name})
		}
	}

	log.Info(context.Background(), "session starting", log.Attr{K: "name", V: name})

	p, err := gopty.New()
	if err != nil {
		return fmt.Errorf("create pty: %w", err)
	}

	if m.cols > 0 && m.rows > 0 {
		p.Resize(int(m.cols), int(m.rows))
	}

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
		p.Close()
		return fmt.Errorf("start pty command %s: %w", command, err)
	}

	t.pty = p
	t.cmd = cmd
	m.terminals[sessionID] = t

	go t.readLoop(p)
	return nil
}

func (m *Manager) StopSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if t, ok := m.terminals[sessionID]; ok {
		t.stop()
		delete(m.terminals, sessionID)
	}
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, t := range m.terminals {
		t.stop()
		delete(m.terminals, id)
	}
}

func (m *Manager) Write(sessionID, text string) error {
	m.mu.Lock()
	t, ok := m.terminals[sessionID]
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("no terminal for session %s", sessionID[:min(8, len(sessionID))])
	}
	return t.Write(text)
}

func (m *Manager) Resize(sessionID string, cols, rows uint16) {
	m.mu.Lock()
	t, ok := m.terminals[sessionID]
	m.mu.Unlock()

	if ok {
		t.Resize(cols, rows)
	}
}

func (m *Manager) ResizeAll(cols, rows uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, t := range m.terminals {
		t.Resize(cols, rows)
	}
}

func (m *Manager) SendEscape(sessionID string) {
	m.mu.Lock()
	t, ok := m.terminals[sessionID]
	m.mu.Unlock()

	if ok {
		t.SendEscape()
	}
}

func (m *Manager) Buffer(sessionID string) []byte {
	m.mu.Lock()
	t, ok := m.terminals[sessionID]
	m.mu.Unlock()

	if ok {
		return t.Buffer()
	}
	return nil
}

func (m *Manager) Tokens(sessionID string) string {
	m.mu.Lock()
	t, ok := m.terminals[sessionID]
	m.mu.Unlock()

	if ok {
		return t.Tokens()
	}
	return ""
}

func (m *Manager) IsRunning(sessionID string) bool {
	m.mu.Lock()
	t, ok := m.terminals[sessionID]
	m.mu.Unlock()

	if ok {
		return t.isRunning()
	}
	return false
}

// RingBuffer is a simple circular buffer with internal synchronization
type RingBuffer struct {
	mu   sync.Mutex
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
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range p {
		r.data[r.pos] = b
		r.pos = (r.pos + 1) % r.size
		if r.pos == 0 {
			r.full = true
		}
	}
}

func (r *RingBuffer) Bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		result := make([]byte, r.pos)
		copy(result, r.data[:r.pos])
		return result
	}
	result := make([]byte, r.size)
	copy(result, r.data[r.pos:])
	copy(result[r.size-r.pos:], r.data[:r.pos])
	return result
}

func (r *RingBuffer) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pos = 0
	r.full = false
}

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
