package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pink-tools/pink-core"
)

type cliState struct {
	Projects []cliProject `json:"projects"`
}

type cliProject struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ThreadID  int    `json:"threadId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

func HandleProject(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent project list")
	}

	switch args[0] {
	case "list":
		return projectList()
	default:
		return fmt.Errorf("unknown project command: %s", args[0])
	}
}

func projectList() error {
	response, err := core.SendCommand(serviceName, "getState")
	if err != nil {
		return fmt.Errorf("agent not running")
	}

	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
	}

	var state cliState
	if err := json.Unmarshal([]byte(response), &state); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}

	if len(state.Projects) == 0 {
		fmt.Println("No projects")
		return nil
	}

	for _, p := range state.Projects {
		line := fmt.Sprintf("  %s (%s)", p.Name, p.ID[:8])
		if p.ThreadID != 0 {
			line += fmt.Sprintf(" thread:%d", p.ThreadID)
		}
		if p.SessionID != "" {
			line += fmt.Sprintf(" session:%s", p.SessionID[:8])
		}
		fmt.Println(line)
	}

	return nil
}
