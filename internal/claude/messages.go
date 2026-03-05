package claude

import "encoding/json"

// Input messages (written to stdin)

type UserMessage struct {
	Type    string      `json:"type"`
	Message MessageBody `json:"message"`
}

type MessageBody struct {
	Role    string        `json:"role"`
	Content []ContentPart `json:"content"`
}

type ContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func NewTextMessage(text string) UserMessage {
	return UserMessage{
		Type: "user",
		Message: MessageBody{
			Role: "user",
			Content: []ContentPart{
				{Type: "text", Text: text},
			},
		},
	}
}

// Output events (read from stdout)

type OutputEvent struct {
	Type      string          `json:"type"`
	Subtype   string          `json:"subtype,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Status    string          `json:"status,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Result    string          `json:"result,omitempty"`
	Raw       json.RawMessage `json:"-"`
}

// Stream events

type ContentBlockEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Content struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
		// Tool use fields
		Input json.RawMessage `json:"input,omitempty"`
	} `json:"content,omitempty"`
}

type ResultEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype,omitempty"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
	Duration  int    `json:"duration_ms,omitempty"`
}

// ParseEvent unmarshals raw JSON into an OutputEvent, preserving raw bytes.
func ParseEvent(data []byte) (OutputEvent, error) {
	var ev OutputEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return ev, err
	}
	ev.Raw = make(json.RawMessage, len(data))
	copy(ev.Raw, data)
	return ev, nil
}
