package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pink-tools/pink-core"

	"pink-agent/internal/store"
)

func HandleStore(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent store list|get|add|delete [args]")
	}

	// Parse -p flag for project name lookup
	var projectName string
	var filtered []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-p" && i+1 < len(args) {
			projectName = args[i+1]
			i++
			continue
		}
		filtered = append(filtered, args[i])
	}
	args = filtered

	projectID, err := resolveProjectID(projectName)
	if err != nil {
		return err
	}

	fs := store.New(filepath.Join(core.DataDir(serviceName), "store"))

	switch args[0] {
	case "list":
		return storeList(fs, projectID)
	case "get":
		if len(args) < 2 {
			return fmt.Errorf("usage: pink-agent store get <path>")
		}
		return storeGet(fs, projectID, args[1])
	case "add":
		return storeAdd(fs, projectID, args[1:])
	case "delete":
		if len(args) < 2 {
			return fmt.Errorf("usage: pink-agent store delete <path>")
		}
		return fs.Delete(projectID, args[1])
	default:
		return fmt.Errorf("unknown store command: %s", args[0])
	}
}

func resolveProjectID(nameOrID string) (string, error) {
	// -p flag takes priority over env var
	if nameOrID == "" {
		if id := os.Getenv("PINK_PROJECT_ID"); id != "" {
			return id, nil
		}
		return "", fmt.Errorf("PINK_PROJECT_ID not set and no -p flag provided")
	}

	return resolveProjectByArg(nameOrID)
}

func storeList(fs *store.FileStore, projectID string) error {
	files, err := fs.List(projectID)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Println("No files")
		return nil
	}
	for _, f := range files {
		fmt.Printf("  %s (%d bytes)\n", f.Name, f.Size)
	}
	return nil
}

func storeGet(fs *store.FileStore, projectID, path string) error {
	content, err := fs.Get(projectID, path)
	if err != nil {
		return err
	}
	fmt.Print(string(content))
	return nil
}

func storeAdd(fs *store.FileStore, projectID string, args []string) error {
	// Parse --force flag
	var force bool
	var filtered []string
	for _, a := range args {
		if a == "--force" {
			force = true
			continue
		}
		filtered = append(filtered, a)
	}

	if len(filtered) < 1 {
		return fmt.Errorf("usage: pink-agent store add [--force] <path> <content>")
	}

	path := filtered[0]

	var content string
	if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		content = strings.TrimSpace(string(data))
	} else if len(filtered) >= 2 {
		content = strings.Join(filtered[1:], " ")
	} else {
		return fmt.Errorf("usage: pink-agent store add [--force] <path> <content>")
	}

	// Check if file exists (unless --force)
	if !force {
		if existing, err := fs.Get(projectID, path); err == nil && len(existing) > 0 {
			existingStr := strings.TrimSpace(string(existing))
			if existingStr != "" && existingStr != "(empty)" {
				return fmt.Errorf("file already exists: %s (use --force to overwrite)", path)
			}
		}
	}

	return fs.Add(projectID, path, []byte(content))
}
