//go:build !windows

package claude

import (
	"os"
	"syscall"
)

func interruptProcess(process *os.Process) error {
	return process.Signal(syscall.SIGINT)
}
