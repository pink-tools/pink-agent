package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/pink-tools/pink-core"

	"pink-agent/internal/usage"
)

func HandleUsage(args []string) error {
	threadID := os.Getenv("PINK_THREAD_ID")

	// Running inside a Claude session with daemon — send formatted message to topic
	if threadID != "" && core.IsRunning(serviceName) {
		response, err := core.SendCommand(serviceName, "usage:"+threadID)
		if err != nil {
			return fmt.Errorf("agent not running")
		}
		if strings.HasPrefix(response, "ERROR:") {
			return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
		}
		return nil
	}

	// Standalone — print to stdout
	u, err := usage.Fetch()
	if err != nil {
		return err
	}

	fmt.Print(usage.FormatText(u))
	return nil
}
