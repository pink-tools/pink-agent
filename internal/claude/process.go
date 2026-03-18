package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	core "github.com/pink-tools/pink-core"
	"github.com/pink-tools/pink-core/log"
)

type Process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	cancel context.CancelFunc

	mu   sync.Mutex
	dead bool
}

type EventHandler func(OutputEvent)

// Start spawns a claude -p process with NDJSON protocol.
func Start(sessionID, mcpConfig, workDir string, extraEnv []string, onEvent EventHandler) (*Process, error) {
	ctx, cancel := context.WithCancel(context.Background())

	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--dangerously-skip-permissions",
	}

	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	if mcpConfig != "" {
		if _, err := os.Stat(mcpConfig); err == nil {
			args = append(args, "--mcp-config", mcpConfig)
		}
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = append(cleanEnv(), extraEnv...)
	if workDir != "" {
		cmd.Dir = workDir
	} else {
		cmd.Dir = core.HomeDir()
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}

	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	p := &Process{
		cmd:    cmd,
		stdin:  stdin,
		cancel: cancel,
	}

	go p.readLoop(stdout, onEvent)
	go p.waitLoop()

	return p, nil
}

func (p *Process) readLoop(r io.Reader, onEvent EventHandler) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		ev, err := ParseEvent(line)
		if err != nil {
			log.Warn(context.Background(), "parse event failed", log.Attr{K: "error", V: err.Error()})
			continue
		}

		onEvent(ev)
	}
}

func (p *Process) waitLoop() {
	p.cmd.Wait()
	p.mu.Lock()
	p.dead = true
	p.mu.Unlock()
}

// Send writes a user message to the process stdin.
func (p *Process) Send(text string) error {
	msg := NewTextMessage(text)
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead {
		return ErrProcessDead
	}

	_, err = p.stdin.Write(data)
	return err
}

// Interrupt sends SIGINT to the process.
func (p *Process) Interrupt() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.dead {
		return
	}
	p.cmd.Process.Signal(syscall.SIGINT)
}

// Stop closes stdin and kills the process.
func (p *Process) Stop() {
	p.mu.Lock()
	dead := p.dead
	p.mu.Unlock()

	if dead {
		return
	}

	p.stdin.Close()

	// Wait for graceful exit, then kill
	done := make(chan struct{})
	go func() {
		p.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	default:
		p.cancel()
	}
}

// Alive returns whether the process is still running.
func (p *Process) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.dead
}

// cleanEnv returns the current environment without CLAUDECODE variable.
func cleanEnv() []string {
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "CLAUDECODE=") {
			env = append(env, e)
		}
	}
	return env
}
