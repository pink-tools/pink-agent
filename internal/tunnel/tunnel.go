package tunnel

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type Tunnel struct {
	name       string
	id         string
	port       int
	cmd        *exec.Cmd
	ready      chan struct{}
	readyOnce  sync.Once
	configPath string
}

func New(name, id string, port int) *Tunnel {
	return &Tunnel{
		name:  name,
		id:    id,
		port:  port,
		ready: make(chan struct{}),
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

// WaitReady blocks until tunnel is connected to Cloudflare edge
func (t *Tunnel) WaitReady() {
	<-t.ready
}

func (t *Tunnel) Stop() {
	if t.cmd != nil && t.cmd.Process != nil {
		t.cmd.Process.Kill()
		t.cmd.Wait()
	}
	if t.configPath != "" {
		os.Remove(t.configPath)
	}
}

func (t *Tunnel) URL() string {
	return fmt.Sprintf("https://%s.pinkhaired.com", t.name)
}
