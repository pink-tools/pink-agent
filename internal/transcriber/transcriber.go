package transcriber

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func transcriberPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	name := "pink-transcriber"
	if runtime.GOOS == "windows" {
		name = "pink-transcriber.exe"
	}
	return filepath.Join(home, "pink-tools", "pink-transcriber", name), nil
}

func Transcribe(audioPath string) (string, error) {
	if _, err := os.Stat(audioPath); err != nil {
		return "", err
	}

	path, err := transcriberPath()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(path, "transcribe", audioPath)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", errors.New(string(exitErr.Stderr))
		}
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}
