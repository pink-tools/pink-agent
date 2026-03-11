package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pink-tools/pink-core"
)

func HandleSession(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent session list <dir> | attach <sessionID> <dir>")
	}

	switch args[0] {
	case "list":
		if len(args) < 2 {
			return fmt.Errorf("usage: pink-agent session list <dir>")
		}
		return sessionList(args[1])
	case "attach":
		if len(args) < 3 {
			return fmt.Errorf("usage: pink-agent session attach <sessionID> <dir>")
		}
		return sessionAttach(args[1], args[2])
	default:
		return fmt.Errorf("unknown session command: %s", args[0])
	}
}

type sessionEntry struct {
	ID      string
	Date    time.Time
	Preview string
}

func sessionList(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	encoded := encodeDir(abs)
	sessDir := filepath.Join(os.Getenv("HOME"), ".claude", "projects", encoded)

	entries, err := os.ReadDir(sessDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no sessions found for %s", abs)
		}
		return err
	}

	var sessions []sessionEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		sessionID := strings.TrimSuffix(e.Name(), ".jsonl")
		path := filepath.Join(sessDir, e.Name())

		info, err := e.Info()
		if err != nil {
			continue
		}

		preview := firstUserMessage(path)
		sessions = append(sessions, sessionEntry{
			ID:      sessionID,
			Date:    info.ModTime(),
			Preview: preview,
		})
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Date.After(sessions[j].Date)
	})

	if len(sessions) == 0 {
		fmt.Println("No sessions found")
		return nil
	}

	for _, s := range sessions {
		if s.Preview == "" {
			continue
		}
		preview := strings.ReplaceAll(s.Preview, "\n", " ")
		fmt.Printf("  %s  %s  %s\n", s.ID, s.Date.Format("2006-01-02"), preview)
	}

	return nil
}

func sessionAttach(sessionID, dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}

	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("directory not found: %s", abs)
	}

	// Validate session exists
	encoded := encodeDir(abs)
	sessFile := filepath.Join(os.Getenv("HOME"), ".claude", "projects", encoded, sessionID+".jsonl")
	if _, err := os.Stat(sessFile); err != nil {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	name := filepath.Base(abs)

	payload, _ := json.Marshal(map[string]string{
		"sessionId": sessionID,
		"dir":       abs,
		"name":      name,
	})
	response, err := core.SendCommand(serviceName, "attachSession:"+string(payload))
	if err != nil {
		return fmt.Errorf("agent not running")
	}
	if strings.HasPrefix(response, "ERROR:") {
		return fmt.Errorf("%s", strings.TrimPrefix(response, "ERROR:"))
	}

	var result struct {
		Name     string `json:"name"`
		ThreadID int    `json:"threadId"`
	}
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	fmt.Printf("Attached session %s to topic \"%s\"\n", sessionID, result.Name)
	return nil
}

// encodeDir converts absolute path to Claude's encoded dir name.
// /Users/miroslav/Desktop/polarizers.io → -Users-miroslav-Desktop-polarizers-io
func encodeDir(abs string) string {
	var b strings.Builder
	for _, c := range abs {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func firstUserMessage(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		var line struct {
			Type    string `json:"type"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue
		}
		if line.Type == "user" && line.Message.Content != "" {
			return line.Message.Content
		}
	}
	return ""
}
