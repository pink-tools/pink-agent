package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pink-tools/pink-core"
)

func HandleProject(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent project <list>")
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

	var state sessionState
	if err := json.Unmarshal([]byte(response), &state); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}

	if len(state.Projects) == 0 {
		fmt.Println("No projects")
		return nil
	}

	for _, p := range state.Projects {
		marker := "  "
		if p.ID == state.ActiveProject {
			marker = "* "
		}
		sessionCount := len(p.Sessions)
		fmt.Printf("%s%s (%d sessions)\n", marker, p.Name, sessionCount)
	}

	return nil
}
