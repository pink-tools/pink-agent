package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pink-tools/pink-core"
)

type sessionState struct {
	ActiveProject string    `json:"activeProject"`
	Projects      []project `json:"projects"`
}

type project struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	ActiveSession string    `json:"activeSession"`
	Sessions      []session `json:"sessions"`
}

type session struct {
	ClaudeID string `json:"claudeId"`
	Name     string `json:"name"`
	Status   string `json:"status"`
}

func HandleSession(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent session <list|new|switch> [args]")
	}

	switch args[0] {
	case "list":
		return sessionList()
	case "new":
		name := ""
		prompt := ""
		if len(args) > 1 {
			name = args[1]
		}
		if len(args) > 2 {
			prompt = strings.Join(args[2:], " ")
		}
		return sessionNew(name, prompt)
	case "switch":
		if len(args) < 2 {
			return fmt.Errorf("usage: pink-agent session switch <session-id>")
		}
		return sessionSwitch(args[1])
	default:
		return fmt.Errorf("unknown session command: %s", args[0])
	}
}

func sessionList() error {
	response, err := core.SendCommand(serviceName, "getState")
	if err != nil {
		return fmt.Errorf("agent not running")
	}

	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf(strings.TrimPrefix(response, "ERROR:"))
	}

	var state sessionState
	if err := json.Unmarshal([]byte(response), &state); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}

	// Find active project
	var activeProject *project
	for i := range state.Projects {
		if state.Projects[i].ID == state.ActiveProject {
			activeProject = &state.Projects[i]
			break
		}
	}

	if activeProject == nil {
		fmt.Println("No active project")
		return nil
	}

	fmt.Printf("Project: %s\n\n", activeProject.Name)

	if len(activeProject.Sessions) == 0 {
		fmt.Println("No sessions")
		return nil
	}

	for _, s := range activeProject.Sessions {
		marker := "  "
		if s.ClaudeID == activeProject.ActiveSession {
			marker = "* "
		}
		fmt.Printf("%s%s [%s] %s\n", marker, s.Name, s.Status, s.ClaudeID[:8])
	}

	return nil
}

func sessionSwitch(sessionID string) error {
	response, err := core.SendCommand(serviceName, "switchSession:"+sessionID)
	if err != nil {
		return fmt.Errorf("agent not running")
	}

	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf(strings.TrimPrefix(response, "ERROR:"))
	}

	fmt.Println("Switched")
	return nil
}

func sessionNew(name, prompt string) error {
	params := struct {
		Name   string `json:"name"`
		Prompt string `json:"prompt"`
	}{Name: name, Prompt: prompt}

	data, _ := json.Marshal(params)
	response, err := core.SendCommand(serviceName, "createSession:"+string(data))
	if err != nil {
		return fmt.Errorf("agent not running")
	}

	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf(strings.TrimPrefix(response, "ERROR:"))
	}

	fmt.Println("Creating session...")
	return nil
}
