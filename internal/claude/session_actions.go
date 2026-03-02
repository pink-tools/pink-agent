package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	core "github.com/pink-tools/pink-core"
	"pink-agent/internal/platform"
)

const initSessionPrompt = `You are a new Pink Agent session.
Project: %s

Read configuration: /Users/.claude/CLAUDE.md

PROJECT CONTEXT (project-specific memory):
%s

MEMORIZE: When user asks to remember/memorize something for this project, update PROJECT.md via:
/Users/pink-tools/pink-agent/pink-agent store add PROJECT.md "full updated content here"`

type claudeEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
}

func CreateSession(projectName, projectContext string) (string, error) {
	prompt := fmt.Sprintf(initSessionPrompt, projectName, projectContext)

	out, err := runClaude("--print", prompt, "--output-format=json", "--dangerously-skip-permissions")
	if err != nil {
		return "", err
	}

	return parseSessionID(out)
}

func runClaude(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	command, prefixArgs := platform.ClaudeExecutable()
	cmd := exec.CommandContext(ctx, command, append(prefixArgs, args...)...)
	cmd.Env = cleanEnv()
	cmd.Dir = core.BaseDir()
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", errors.New("claude command timed out after 60s")
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", errors.New(string(exitErr.Stderr))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func parseSessionID(output string) (string, error) {
	events, err := parseEvents(output)
	if err != nil {
		return "", err
	}

	for _, e := range events {
		if e.Type == "result" {
			if e.IsError {
				return "", errors.New("claude returned error")
			}
			if e.SessionID == "" {
				return "", errors.New("no session_id in response")
			}
			return e.SessionID, nil
		}
	}
	return "", errors.New("no result event in response")
}

func parseEvents(output string) ([]claudeEvent, error) {
	var events []claudeEvent
	if err := json.Unmarshal([]byte(output), &events); err != nil {
		return nil, err
	}
	return events, nil
}
