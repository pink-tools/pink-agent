//go:build !windows

package platform

// ClaudeExecutable returns the command and prefix args for running Claude CLI.
// On Unix/Mac, claude is a native binary.
func ClaudeExecutable() (command string, prefixArgs []string) {
	return "claude", nil
}
