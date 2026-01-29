package telegram

type PTYWriter interface {
	Write(text string) error
}

type Transcriber interface {
	Transcribe(path string) (string, error)
}

type Handlers struct {
	pty         PTYWriter
	transcriber Transcriber
}

func NewHandlers(pty PTYWriter, transcriber Transcriber) *Handlers {
	return &Handlers{
		pty:         pty,
		transcriber: transcriber,
	}
}

func (h *Handlers) HandleText(text string) string {
	if err := h.pty.Write(text); err != nil {
		return err.Error()
	}
	return ""
}

func (h *Handlers) HandleVoice(audioPath string) (transcribed string, errMsg string) {
	text, err := h.transcriber.Transcribe(audioPath)
	if err != nil {
		return "", "Transcription failed: " + err.Error()
	}

	voicePrefix := "[VOICE INPUT: May contain speech recognition errors. Ask for clarification if unclear.] "
	if err := h.pty.Write(voicePrefix + text); err != nil {
		return "", err.Error()
	}
	return text, ""
}

func (h *Handlers) HandleFile(filePath string) string {
	if err := h.pty.Write("File: " + filePath); err != nil {
		return err.Error()
	}
	return ""
}
