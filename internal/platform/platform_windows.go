//go:build windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	claudeCommand string
	claudeArgs    []string
	claudeOnce    sync.Once
)

// ClaudeExecutable returns the command and prefix args for running Claude CLI.
// Checks multiple install locations in order:
// 1. claude in PATH (winget install)
// 2. ~/.local/bin/claude (bash/Git Bash install)
// 3. npm global install (legacy)
func ClaudeExecutable() (command string, prefixArgs []string) {
	claudeOnce.Do(func() {
		// Try to find claude in PATH first (winget install)
		if path, err := exec.LookPath("claude"); err == nil {
			claudeCommand = path
			return
		}

		// Check ~/.local/bin with various extensions (Git Bash install)
		if home, err := os.UserHomeDir(); err == nil {
			localBinDir := filepath.Join(home, ".local", "bin")
			for _, name := range []string{"claude", "claude.exe", "claude.cmd"} {
				path := filepath.Join(localBinDir, name)
				if _, err := os.Stat(path); err == nil {
					claudeCommand = path
					return
				}
			}
		}

		// Try npm global install (node + cli.js)
		nodePath, err := exec.LookPath("node")
		if err == nil {
			cmd := exec.Command("npm", "root", "-g")
			if out, err := cmd.Output(); err == nil {
				npmRoot := strings.TrimSpace(string(out))
				cliJS := filepath.Join(npmRoot, "@anthropic-ai", "claude-code", "cli.js")
				if _, err := os.Stat(cliJS); err == nil {
					claudeCommand = nodePath
					claudeArgs = []string{cliJS}
					return
				}
			}
		}

		// Fallback
		claudeCommand = "claude"
	})

	return claudeCommand, claudeArgs
}
