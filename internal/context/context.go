package agentcontext

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pink-tools/pink-core"
)

// Build assembles the full agent context from embedded context + installed services.
func Build(ownContext string) string {
	var parts []string

	// Own embedded context (agent instructions + code rules)
	if ownContext != "" {
		ownBin := core.BinaryPath("pink-agent")
		text := strings.TrimSpace(ownContext)
		text = strings.ReplaceAll(text, "    pink-agent ", "    "+ownBin+" ")
		text = strings.ReplaceAll(text, "    pink-agent\n", "    "+ownBin+"\n")
		parts = append(parts, text)
	}

	// Collect context from installed services via --claude
	svcs := discoverServices()
	for _, svc := range svcs {
		bin := core.BinaryPath(svc)
		if svc == "pink-agent" {
			continue // skip self
		}
		out, err := exec.Command(bin, "--claude").Output()
		if err != nil {
			continue
		}
		text := strings.TrimSpace(string(out))
		if text != "" {
			// Replace bare command names with absolute paths
			text = strings.ReplaceAll(text, "    "+svc+" ", "    "+bin+" ")
			text = strings.ReplaceAll(text, "    "+svc+"\n", "    "+bin+"\n")
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, "\n\n---\n\n")
}

// discoverServices returns names of installed pink-tools services.
func discoverServices() []string {
	var names []string

	// Try orchestrator --services first
	orchBin := core.BinaryPath("pink-orchestrator")
	if out, err := exec.Command(orchBin, "--services").Output(); err == nil {
		json.Unmarshal(out, &names)
	}

	// If orchestrator didn't return results, scan ~/pink-tools/
	if len(names) == 0 {
		entries, err := os.ReadDir(core.PinkToolsDir())
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			bin := filepath.Join(core.PinkToolsDir(), name, name)
			if _, err := os.Stat(bin); err == nil {
				names = append(names, name)
			}
		}
	}

	// Always include orchestrator itself (not in --services list)
	hasOrch := false
	for _, n := range names {
		if n == "pink-orchestrator" {
			hasOrch = true
			break
		}
	}
	if !hasOrch {
		if _, err := os.Stat(orchBin); err == nil {
			names = append(names, "pink-orchestrator")
		}
	}

	return names
}
