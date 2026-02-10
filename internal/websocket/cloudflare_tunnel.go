package websocket

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Tunnel struct {
	name       string
	id         string
	port       int
	cmd        *exec.Cmd
	ready      chan struct{}
	readyOnce  sync.Once
	configPath string
	crashErr   error
	crashed    chan struct{}
}

func New(name, id string, port int) *Tunnel {
	return &Tunnel{
		name:    name,
		id:      id,
		port:    port,
		ready:   make(chan struct{}),
		crashed: make(chan struct{}),
	}
}

func (t *Tunnel) Start() error {
	homeDir, _ := os.UserHomeDir()
	credFile := fmt.Sprintf("%s/.cloudflared/%s.json", homeDir, t.id)

	// Create local config to override remote config
	configContent := fmt.Sprintf(`tunnel: %s
credentials-file: %s
ingress:
  - service: http://localhost:%d
`, t.id, credFile, t.port)

	configFile, err := os.CreateTemp("", "cloudflared-*.yml")
	if err != nil {
		return err
	}
	t.configPath = configFile.Name()
	configFile.WriteString(configContent)
	configFile.Close()

	t.cmd = exec.Command("cloudflared", "tunnel",
		"--config", t.configPath,
		"run",
	)

	// Capture stderr to detect ready state
	stderr, err := t.cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := t.cmd.Start(); err != nil {
		return err
	}

	// Monitor stderr for "Registered tunnel connection"
	go t.monitorReady(stderr)

	// Detect tunnel crash — capture cmd locally to avoid nil deref
	cmd := t.cmd
	go func() {
		err := cmd.Wait()
		if err != nil {
			t.crashErr = err
		}
		close(t.crashed)
	}()

	return nil
}

func (t *Tunnel) monitorReady(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	connections := 0

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Registered tunnel connection") {
			connections++
			if connections >= 2 {
				t.readyOnce.Do(func() {
					close(t.ready)
				})
			}
		}
	}
}

// WaitReady blocks until tunnel is connected to Cloudflare edge or 30s timeout.
func (t *Tunnel) WaitReady() error {
	select {
	case <-t.ready:
		return nil
	case <-time.After(30 * time.Second):
		return errors.New("tunnel not ready after 30s")
	}
}

func (t *Tunnel) Stop() {
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill()
		<-t.crashed // wait for the monitor goroutine to finish
	}
	if t.configPath != "" {
		os.Remove(t.configPath)
	}
}

// Crashed returns a channel that is closed when the tunnel process exits unexpectedly.
func (t *Tunnel) Crashed() <-chan struct{} {
	return t.crashed
}

// CrashError returns the error from the tunnel process crash, if any.
func (t *Tunnel) CrashError() error {
	return t.crashErr
}

func (t *Tunnel) URL() string {
	return fmt.Sprintf("https://%s.pinkhaired.com", t.name)
}
