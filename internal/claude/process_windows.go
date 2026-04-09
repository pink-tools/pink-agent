//go:build windows

package claude

import (
	"os"
	"os/exec"
	"strconv"
)

func interruptProcess(process *os.Process) error {
	// Windows doesn't support SIGINT for child processes.
	// Kill the entire process tree — manager will respawn on next message.
	return exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(process.Pid)).Run()
}
