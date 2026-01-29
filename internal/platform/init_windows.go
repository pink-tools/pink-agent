//go:build windows

package platform

import "syscall"

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	allocConsole   = kernel32.NewProc("AllocConsole")
)

func init() {
	// Allocate a console for ConPTY to work properly
	// when started as a background process without a console
	allocConsole.Call()
}
