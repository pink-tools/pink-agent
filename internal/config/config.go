package config

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Config struct {
	TelegramBotToken string
	TelegramUserID   int64
	TunnelName       string
	TunnelID         string
	Port             int
}

func Load(envPath string) (*Config, error) {
	env, err := parseEnvFile(envPath)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		TelegramBotToken: env["TELEGRAM_BOT_TOKEN"],
		TunnelName:       env["TUNNEL_NAME"],
		TunnelID:         env["TUNNEL_ID"],
		Port:             7466,
	}

	if uid := env["TELEGRAM_USER_ID"]; uid != "" {
		id, err := strconv.ParseInt(uid, 10, 64)
		if err != nil {
			return nil, errors.New("invalid TELEGRAM_USER_ID")
		}
		cfg.TelegramUserID = id
	}

	if port := env["PORT"]; port != "" {
		p, err := strconv.Atoi(port)
		if err != nil {
			return nil, errors.New("invalid PORT")
		}
		cfg.Port = p
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.TelegramBotToken == "" {
		return errors.New("TELEGRAM_BOT_TOKEN required")
	}
	if c.TelegramUserID == 0 {
		return errors.New("TELEGRAM_USER_ID required")
	}
	if c.TunnelName == "" {
		return errors.New("TUNNEL_NAME required")
	}
	if c.TunnelID == "" {
		return errors.New("TUNNEL_ID required")
	}
	return nil
}

func parseEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	return env, scanner.Err()
}

func CheckDeps() error {
	if _, err := exec.LookPath("claude"); err != nil {
		return errors.New("claude not found in PATH")
	}
	if _, err := exec.LookPath("cloudflared"); err != nil {
		return errors.New("cloudflared not found in PATH")
	}
	return nil
}
