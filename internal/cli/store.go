package cli

import (
	"fmt"
	"path/filepath"

	"github.com/pink-tools/pink-core"
	"pink-agent/internal/projects"
)

func HandleStore(args []string) error {
	dataDir := core.DataDir(serviceName)
	storePath := filepath.Join(dataDir, "store")
	statePath := filepath.Join(dataDir, "state.json")

	storage := projects.NewFileStorage(statePath)
	stateManager, err := projects.NewManager(storage)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	fileStore := projects.NewFileStore(storePath)

	// Parse flags
	projectID := ""
	force := false
	i := 0
	for i < len(args) {
		if args[i] == "-p" && i+1 < len(args) {
			projectName := args[i+1]
			for _, p := range stateManager.State().Projects {
				if p.Name == projectName {
					projectID = p.ID
					break
				}
			}
			if projectID == "" {
				return fmt.Errorf("project not found: %s", projectName)
			}
			args = append(args[:i], args[i+2:]...)
		} else if args[i] == "--force" || args[i] == "-f" {
			force = true
			args = append(args[:i], args[i+1:]...)
		} else {
			i++
		}
	}

	// Use active project if not specified
	if projectID == "" {
		project := stateManager.State().GetActiveProject()
		if project == nil {
			return fmt.Errorf("no active project")
		}
		projectID = project.ID
	}

	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent store [list|get|add] ...")
	}

	switch args[0] {
	case "list":
		files, err := fileStore.List(projectID)
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Println(f.Name)
		}

	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: pink-agent store get <path>")
		}
		content, err := fileStore.Get(projectID, args[1])
		if err != nil {
			return err
		}
		fmt.Print(string(content))

	case "add":
		if len(args) < 3 {
			return fmt.Errorf("usage: pink-agent store add <path> <content>")
		}
		path := args[1]
		// Check if file exists
		if !force {
			if _, err := fileStore.Get(projectID, path); err == nil {
				return fmt.Errorf("file already exists: %s (use --force to overwrite)", path)
			}
		}
		if err := fileStore.Add(projectID, path, []byte(args[2])); err != nil {
			return err
		}
		// Notify UI
		core.SendCommand(serviceName, "refreshStore")
		return nil

	default:
		return fmt.Errorf("unknown store command: %s", args[0])
	}

	return nil
}
