package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/pink-tools/pink-core"
)

func HandleRefresh(args []string) error {
	threadID := os.Getenv("PINK_THREAD_ID")
	if threadID == "" {
		return fmt.Errorf("PINK_THREAD_ID not set")
	}

	response, err := core.SendCommand(serviceName, "refresh:"+threadID)
	if err != nil {
		return fmt.Errorf("agent not running")
	}
	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
	}

	fmt.Println("Session refreshed")
	return nil
}
