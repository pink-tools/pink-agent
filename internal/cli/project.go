package cli

import (
	"encoding/json"
	"fmt"
	"os"
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
		return fmt.Errorf("usage: pink-agent project list|create|delete")
	}

	switch args[0] {
	case "list":
		return projectList()
	case "create":
		return projectCreate(args[1:])
	case "delete":
		return projectDelete(args[1:])
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

func projectCreate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent project create \"Name\" [\"Prompt\"]")
	}

	name := args[0]
	var prompt string
	if len(args) > 1 {
		prompt = args[1]
	}

	payload, _ := json.Marshal(map[string]string{"name": name, "prompt": prompt})
	response, err := core.SendCommand(serviceName, "createProject:"+string(payload))
	if err != nil {
		return fmt.Errorf("agent not running")
	}
	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
	}

	var result struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		ThreadID int    `json:"threadId"`
	}
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	fmt.Printf("Created project \"%s\" (%s)\n", result.Name, result.ID[:8])
	return nil
}

func projectDelete(args []string) error {
	var projectID string
	if len(args) > 0 {
		id, err := resolveProjectByArg(args[0])
		if err != nil {
			return err
		}
		projectID = id
	} else {
		id := os.Getenv("PINK_PROJECT_ID")
		if id == "" {
			return fmt.Errorf("no project specified and PINK_PROJECT_ID not set")
		}
		projectID = id
	}

	payload, _ := json.Marshal(map[string]string{"id": projectID})
	response, err := core.SendCommand(serviceName, "deleteProject:"+string(payload))
	if err != nil {
		return fmt.Errorf("agent not running")
	}
	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
	}

	fmt.Println("Deleted project")
	return nil
}

func resolveProjectByArg(idOrName string) (string, error) {
	response, err := core.SendCommand(serviceName, "getState")
	if err != nil {
		return "", fmt.Errorf("agent not running")
	}
	if strings.HasPrefix(response, "ERROR:") {
		return "", fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
	}

	var s cliState
	if err := json.Unmarshal([]byte(response), &s); err != nil {
		return "", fmt.Errorf("parse state: %w", err)
	}

	// Try name match (case-insensitive)
	nameLower := strings.ToLower(idOrName)
	for _, p := range s.Projects {
		if strings.ToLower(p.Name) == nameLower {
			return p.ID, nil
		}
	}

	// Try ID prefix match
	for _, p := range s.Projects {
		if strings.HasPrefix(p.ID, idOrName) {
			return p.ID, nil
		}
	}

	return "", fmt.Errorf("project not found: %s", idOrName)
}
