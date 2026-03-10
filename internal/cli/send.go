package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pink-tools/pink-core"

	"pink-agent/internal/config"
)

func HandleSend(args []string) error {
	threadStr := os.Getenv("PINK_THREAD_ID")
	if threadStr == "" {
		return fmt.Errorf("PINK_THREAD_ID not set")
	}
	threadID, err := strconv.Atoi(threadStr)
	if err != nil {
		return fmt.Errorf("invalid PINK_THREAD_ID: %s", threadStr)
	}

	cfg, err := config.Load(filepath.Join(core.DataDir(serviceName), ".env"))
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Parse args
	if len(args) == 0 {
		return fmt.Errorf("usage: pink-agent send \"text\" | pink-agent send -f <file>")
	}

	// Send file
	if args[0] == "-f" {
		if len(args) < 2 {
			return fmt.Errorf("usage: pink-agent send -f <file>")
		}
		return sendFile(cfg.TelegramBotToken, cfg.TelegramGroupID, threadID, args[1])
	}

	// Send text
	text := args[0]
	return sendText(cfg.TelegramBotToken, cfg.TelegramGroupID, threadID, text)
}

func sendText(token string, chatID int64, threadID int, text string) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)

	body, _ := json.Marshal(map[string]any{
		"chat_id":           chatID,
		"message_thread_id": threadID,
		"text":              text,
	})

	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("telegram: %s", result.Description)
	}
	return nil
}

func sendFile(token string, chatID int64, threadID int, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	w.WriteField("chat_id", fmt.Sprintf("%d", chatID))
	w.WriteField("message_thread_id", fmt.Sprintf("%d", threadID))

	part, err := w.CreateFormFile("document", filepath.Base(path))
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return err
	}
	w.Close()

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendDocument", token)
	resp, err := http.Post(url, w.FormDataContentType(), &buf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("telegram: %s", result.Description)
	}
	return nil
}
