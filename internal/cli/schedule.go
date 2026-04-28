package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pink-tools/pink-core"
)

const scheduleHelp = `pink-agent schedule — schedule a self-trigger to send a prompt to this session at a future time.

Usage:
  pink-agent schedule <when> "text"      Add a scheduled trigger
  pink-agent schedule list               List schedules in current project
  pink-agent schedule list --all         List schedules across all projects
  pink-agent schedule cancel <id>        Cancel one schedule by ID
  pink-agent schedule cancel --all       Cancel all schedules in current project
  pink-agent schedule help               Show this help

Time formats:
  Relative:  1h, 30m, 2h30m, 1d, 1d2h, 45s
  Absolute:  RFC3339 UTC, e.g. 2026-04-29T15:00:00Z
             RFC3339 with offset, e.g. 2026-04-29T18:00:00+03:00

When the trigger fires, the bot sends the text to this Claude session as a
[SCHEDULE TRIGGER] message — Claude can then chain another schedule call.

Examples:
  pink-agent schedule 1h "Remind me to log lunch"
  pink-agent schedule 2026-04-29T15:00:00Z "Call about project X"
`

func HandleSchedule(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent schedule <when> \"text\" | list | cancel | help")
	}

	switch args[0] {
	case "help", "--help", "-h":
		fmt.Print(scheduleHelp)
		return nil
	case "list":
		return scheduleList(args[1:])
	case "cancel":
		return scheduleCancel(args[1:])
	default:
		return scheduleAdd(args)
	}
}

func scheduleAdd(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: pink-agent schedule <when> \"text\"")
	}

	projectID := os.Getenv("PINK_PROJECT_ID")
	threadStr := os.Getenv("PINK_THREAD_ID")
	if projectID == "" || threadStr == "" {
		return fmt.Errorf("PINK_PROJECT_ID and PINK_THREAD_ID must be set")
	}
	threadID, err := strconv.Atoi(threadStr)
	if err != nil {
		return fmt.Errorf("invalid PINK_THREAD_ID: %s", threadStr)
	}

	when := args[0]
	prompt := strings.Join(args[1:], " ")

	payload, _ := json.Marshal(map[string]any{
		"projectId": projectID,
		"threadId":  threadID,
		"when":      when,
		"prompt":    prompt,
	})
	response, err := core.SendCommand(serviceName, "addSchedule:"+string(payload))
	if err != nil {
		return fmt.Errorf("agent not running")
	}
	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
	}

	var s scheduleEntry
	if err := json.Unmarshal([]byte(response), &s); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	fmt.Printf("Scheduled %s — fires at %s (%s)\n",
		s.ID,
		time.Unix(s.TriggerAt, 0).UTC().Format(time.RFC3339),
		humanDelta(s.TriggerAt))
	return nil
}

func scheduleList(args []string) error {
	target := os.Getenv("PINK_PROJECT_ID")
	for _, a := range args {
		if a == "--all" {
			target = "*"
		}
	}
	if target == "" {
		return fmt.Errorf("PINK_PROJECT_ID not set (or pass --all)")
	}

	response, err := core.SendCommand(serviceName, "listSchedules:"+target)
	if err != nil {
		return fmt.Errorf("agent not running")
	}
	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
	}

	var list []scheduleEntry
	if err := json.Unmarshal([]byte(response), &list); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if len(list) == 0 {
		fmt.Println("No schedules")
		return nil
	}
	for _, s := range list {
		fmt.Printf("  %s  %s  (%s)  %q\n",
			s.ID,
			time.Unix(s.TriggerAt, 0).UTC().Format(time.RFC3339),
			humanDelta(s.TriggerAt),
			s.Prompt)
	}
	return nil
}

func scheduleCancel(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent schedule cancel <id> | --all")
	}

	if args[0] == "--all" {
		projectID := os.Getenv("PINK_PROJECT_ID")
		if projectID == "" {
			return fmt.Errorf("PINK_PROJECT_ID not set")
		}
		payload, _ := json.Marshal(map[string]any{"projectId": projectID, "all": true})
		response, err := core.SendCommand(serviceName, "cancelSchedule:"+string(payload))
		if err != nil {
			return fmt.Errorf("agent not running")
		}
		if strings.HasPrefix(response, "ERROR:") {
			return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
		}
		fmt.Printf("Cancelled %s schedule(s)\n", response)
		return nil
	}

	payload, _ := json.Marshal(map[string]any{"id": args[0]})
	response, err := core.SendCommand(serviceName, "cancelSchedule:"+string(payload))
	if err != nil {
		return fmt.Errorf("agent not running")
	}
	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
	}
	fmt.Println("Cancelled")
	return nil
}

type scheduleEntry struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	ThreadID  int    `json:"threadId"`
	TriggerAt int64  `json:"triggerAt"`
	Prompt    string `json:"prompt"`
	CreatedAt int64  `json:"createdAt"`
}

func humanDelta(triggerAt int64) string {
	d := time.Until(time.Unix(triggerAt, 0))
	if d < 0 {
		return "overdue " + (-d).Truncate(time.Second).String()
	}
	return "in " + d.Truncate(time.Second).String()
}
