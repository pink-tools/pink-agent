package usage

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type credentials struct {
	ClaudeAiOauth struct {
		AccessToken string `json:"accessToken"`
	} `json:"claudeAiOauth"`
}

type Window struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
}

type Usage struct {
	FiveHour *Window `json:"five_hour"`
	SevenDay *Window `json:"seven_day"`
}

func Fetch() (*Usage, error) {
	token, err := readOAuthToken()
	if err != nil {
		return nil, fmt.Errorf("read oauth token: %w", err)
	}

	req, err := http.NewRequest("GET", "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API returned %d", resp.StatusCode)
	}

	var u Usage
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &u, nil
}

func FormatText(u *Usage) string {
	now := time.Now()
	var s string

	if u.FiveHour != nil {
		s += fmt.Sprintf("5h:      %.0f%% (resets in %s)\n", u.FiveHour.Utilization, formatRemaining(u.FiveHour.ResetsAt, now))
	}
	if u.SevenDay != nil {
		s += fmt.Sprintf("weekly:  %.0f%% (resets in %s)\n", u.SevenDay.Utilization, formatRemaining(u.SevenDay.ResetsAt, now))
	}

	return s
}

func FormatHTML(u *Usage) string {
	now := time.Now()
	var s string

	if u.FiveHour != nil {
		s += fmt.Sprintf("5h:     %3.0f%%  resets in %s\n", u.FiveHour.Utilization, formatRemaining(u.FiveHour.ResetsAt, now))
	}
	if u.SevenDay != nil {
		s += fmt.Sprintf("weekly: %3.0f%%  resets in %s\n", u.SevenDay.Utilization, formatRemaining(u.SevenDay.ResetsAt, now))
	}

	return "<pre>" + s + "</pre>"
}

func readOAuthToken() (string, error) {
	if runtime.GOOS == "darwin" {
		return readFromKeychain()
	}
	return readFromFile()
}

func readFromKeychain() (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return "", fmt.Errorf("keychain read failed: %w", err)
	}

	var creds credentials
	if err := json.Unmarshal(out, &creds); err != nil {
		return "", fmt.Errorf("parse credentials: %w", err)
	}

	if creds.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf("no access token in credentials")
	}

	return creds.ClaudeAiOauth.AccessToken, nil
}

func readFromFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json"))
	if err != nil {
		return "", fmt.Errorf("read credentials file: %w", err)
	}

	var creds credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", fmt.Errorf("parse credentials: %w", err)
	}

	if creds.ClaudeAiOauth.AccessToken == "" {
		return "", fmt.Errorf("no access token in credentials")
	}

	return creds.ClaudeAiOauth.AccessToken, nil
}

func formatRemaining(resetsAt string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, resetsAt)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.999999-07:00", resetsAt)
		if err != nil {
			return resetsAt
		}
	}

	d := t.Sub(now)
	if d <= 0 {
		return "now"
	}

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
