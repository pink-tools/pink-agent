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

	// Tell Claude where pink-tools binaries live
	parts = append(parts, "pink-tools binaries: "+core.PinkToolsDir()+"/<name>/<name>")

	// Own embedded context (agent instructions + code rules)
	if ownContext != "" {
		parts = append(parts, strings.TrimSpace(ownContext))
	}

	// Collect context from installed services via --claude
	for _, svc := range discoverServices() {
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
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, "\n\n---\n\n")
}

// discoverServices returns names of installed pink-tools services.
func discoverServices() []string {
	// Try orchestrator --services first
	orchBin := core.BinaryPath("pink-orchestrator")
	if out, err := exec.Command(orchBin, "--services").Output(); err == nil {
		var names []string
		if json.Unmarshal(out, &names) == nil {
			return names
		}
	}

	// Fallback: scan ~/pink-tools/ for binaries
	entries, err := os.ReadDir(core.PinkToolsDir())
	if err != nil {
		return nil
	}

	var names []string
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
	return names
}
