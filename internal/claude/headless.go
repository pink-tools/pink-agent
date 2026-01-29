package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const initSessionPrompt = `You are a new Pink Agent session.
Project: %s

Read configuration: ~/.claude/CLAUDE.md

PROJECT CONTEXT (project-specific memory):
%s

MEMORIZE: When user asks to remember/memorize something for this project, update PROJECT.md via:
pink-agent store add PROJECT.md "full updated content here"`

const summarizePrompt = `Create a structured session summary for initializing a new Claude Code session.

The summary will be given to a new instance of Claude Code to continue work where we left off. Include ALL necessary context for seamless continuation.

FORMAT (use this structure, omitting sections that don't apply):

WORKING DIRECTORIES (if applicable):
- List 1-5 main directories where work was done (absolute paths starting with ~)

FILES TO READ FOR CONTEXT (if applicable):
- List 5-15 most important files that new agent should read immediately
- Use absolute paths (~/...)
- Prioritize: documentation, core implementation, recently modified files

SESSION OVERVIEW:
- Describe what we accomplished/discussed
- Focus on: main topics, key decisions, technical implementations
- Be specific about technologies/patterns used

RECENT STEPS (if applicable):
- List last 5-10 interactions:
  1. User: [what user asked]
     I: [what I did - use past tense]`

const takeoverPrompt = `Previous session summary:

%s

PROJECT CONTEXT (project-specific memory):
%s

INSTRUCTIONS:
1. Read ALL files listed in "Files to read" section above using the Read tool. Do this NOW, before responding.
2. After reading each file, confirm you've loaded its content.
3. Only after reading ALL listed files, provide a brief summary of the context you've loaded.
4. DO NOT make any changes to files — this is read-only context loading.

MEMORIZE: When user asks to remember/memorize something for this project, update PROJECT.md via:
pink-agent store add PROJECT.md "full updated content here"

Start reading the files now.`

type claudeEvent struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	Result    string `json:"result"`
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

func Summarize(sessionID string) (string, error) {
	out, err := runClaude("--print", summarizePrompt, "--resume", sessionID, "--output-format=json")
	if err != nil {
		return "", err
	}

	return parseResult(out)
}

func CreateWithTakeover(summary, projectContext string) (string, error) {
	prompt := fmt.Sprintf(takeoverPrompt, summary, projectContext)

	out, err := runClaude("--print", prompt, "--output-format=json", "--dangerously-skip-permissions")
	if err != nil {
		return "", err
	}

	return parseSessionID(out)
}

func runClaude(args ...string) (string, error) {
	cmd := exec.Command("claude", args...)
	cmd.Dir, _ = os.UserHomeDir() // sessions are stored relative to cwd
	out, err := cmd.Output()
	if err != nil {
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

func parseResult(output string) (string, error) {
	events, err := parseEvents(output)
	if err != nil {
		return "", err
	}

	for _, e := range events {
		if e.Type == "result" {
			if e.IsError {
				return "", errors.New("claude returned error")
			}
			return e.Result, nil
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
