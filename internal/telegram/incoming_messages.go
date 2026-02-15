package telegram

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/pink-tools/pink-core"
	"github.com/pink-tools/pink-core/log"
)

// PTYWriter interface for writing to PTY
type PTYWriter interface {
	Write(text string) error
}

// Handlers processes incoming Telegram messages
type Handlers struct {
	pty PTYWriter
}

// NewHandlers creates message handlers
func NewHandlers(pty PTYWriter) *Handlers {
	return &Handlers{pty: pty}
}

// HandleMessage writes text with optional file paths to PTY
func (h *Handlers) HandleMessage(text string, files []string) string {
	var message string

	if len(files) > 0 {
		message = "Files:\n"
		for _, f := range files {
			message += f + "\n"
		}
		message += "\n"
	}

	message += text

	// Log with preview
	preview := text
	if len(preview) > 50 {
		preview = preview[:50] + "..."
	}
	attrs := []log.Attr{{K: "text", V: preview}}
	if len(files) > 0 {
		attrs = append(attrs, log.Attr{K: "files", V: len(files)})
	}
	log.Info(context.Background(), "message received", attrs...)

	if err := h.pty.Write(message); err != nil {
		log.Error(context.Background(), "pty write failed", log.Attr{K: "error", V: err.Error()})
		return err.Error()
	}
	return ""
}

// HandleText writes text directly to PTY
func (h *Handlers) HandleText(text string) string {
	return h.HandleMessage(text, nil)
}

// HandleVoice transcribes audio and writes to PTY
func (h *Handlers) HandleVoice(audioPath string) (transcribed string, errMsg string) {
	text, err := transcribe(audioPath)
	if err != nil {
		return "", "Transcription failed: " + err.Error()
	}

	// Log with preview
	preview := text
	if len(preview) > 50 {
		preview = preview[:50] + "..."
	}
	log.Info(context.Background(), "message received", log.Attr{K: "text", V: "🎤 " + preview})

	voicePrefix := "[VOICE INPUT: May contain speech recognition errors. Ask for clarification if unclear.] "
	if err := h.pty.Write(voicePrefix + text); err != nil {
		log.Error(context.Background(), "pty write failed", log.Attr{K: "error", V: err.Error()})
		return "", err.Error()
	}
	return text, ""
}


// transcribe calls pink-transcriber to convert audio to text
func transcribe(audioPath string) (string, error) {
	if _, err := os.Stat(audioPath); err != nil {
		return "", err
	}

	cmd := exec.Command(core.BinaryPath("pink-transcriber"), "transcribe", audioPath)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", errors.New(string(exitErr.Stderr))
		}
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}
