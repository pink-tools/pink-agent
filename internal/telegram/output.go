package telegram

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"pink-agent/internal/claude"
	"pink-agent/internal/render"
)

const maxMessageLen = 4096

// pendingTool holds a buffered tool call waiting for its result.
type pendingTool struct {
	name     string
	inputStr string
	html     string
	timer    *time.Timer
}

// TopicOutput tracks the output state for one forum topic.
type TopicOutput struct {
	threadID    int
	textBuffer  string // accumulated text for current content block
	blockType   string // current content block type: "text", "thinking", "tool_use"
	toolName    string // tool name from content_block_start
	toolInput   string // accumulated tool input JSON
	toolMsgID   int    // tool use message ID (for appending result)
	toolMsgHTML string // tool use message HTML (for editing)
	pending     *pendingTool

	thinkingBuffer   string
	lastInputTokens  int
	lastOutputTokens int
	contextWindow    int
	userMsgID        int    // user's message ID for clearing reaction
	lastTextMsgID    int    // last rendered text message (for context append)
	lastTextContent  string // its content
	lastTextMode     string // parse mode ("HTML" or "")
}

// OutputManager manages output for all topics.
type OutputManager struct {
	api    *tgbotapi.BotAPI
	chatID int64
	sender *Sender
	topics map[int]*TopicOutput
	mu     sync.Mutex
}

func NewOutputManager(api *tgbotapi.BotAPI, chatID int64, sender *Sender) *OutputManager {
	return &OutputManager{
		api:    api,
		chatID: chatID,
		sender: sender,
		topics: make(map[int]*TopicOutput),
	}
}

// HandleEvent processes a claude output event for a topic.
func (om *OutputManager) HandleEvent(threadID int, ev claude.OutputEvent) {
	// Unwrap stream_event → inner event
	if ev.Type == "stream_event" {
		var wrapper struct {
			Event json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal(ev.Raw, &wrapper); err != nil || wrapper.Event == nil {
			return
		}
		inner, err := claude.ParseEvent(wrapper.Event)
		if err != nil {
			return
		}
		ev = inner
	}

	om.mu.Lock()
	defer om.mu.Unlock()

	topic := om.getOrCreate(threadID)

	switch ev.Type {
	case "content_block_start":
		var block struct {
			ContentBlock struct {
				Type  string          `json:"type"`
				Name  string          `json:"name,omitempty"`
				Input json.RawMessage `json:"input,omitempty"`
			} `json:"content_block"`
		}
		json.Unmarshal(ev.Raw, &block)
		topic.blockType = block.ContentBlock.Type
		topic.toolName = block.ContentBlock.Name

		switch block.ContentBlock.Type {
		case "thinking":
			topic.thinkingBuffer = ""
		case "text":
			topic.textBuffer = ""
		case "tool_use":
			om.flushText(topic)
			topic.toolInput = ""
		}

	case "content_block_delta":
		var block struct {
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text,omitempty"`
				Thinking    string `json:"thinking,omitempty"`
				PartialJSON string `json:"partial_json,omitempty"`
			} `json:"delta"`
		}
		json.Unmarshal(ev.Raw, &block)

		switch block.Delta.Type {
		case "text_delta":
			topic.textBuffer += block.Delta.Text
		case "thinking_delta":
			topic.thinkingBuffer += block.Delta.Thinking
		case "input_json_delta":
			topic.toolInput += block.Delta.PartialJSON
		}

	case "content_block_stop":
		switch topic.blockType {
		case "tool_use":
			om.bufferTool(topic)
		case "text":
			om.flushText(topic)
		case "thinking":
			om.renderThinking(topic)
		}
		topic.blockType = ""

	case "message_start":
		// No action needed

	case "message_delta":
		var msg struct {
			Usage struct {
				InputTokens              int `json:"input_tokens"`
				CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int `json:"cache_read_input_tokens"`
				OutputTokens             int `json:"output_tokens"`
			} `json:"usage"`
		}
		json.Unmarshal(ev.Raw, &msg)
		u := msg.Usage
		topic.lastInputTokens = u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
		topic.lastOutputTokens = u.OutputTokens

	case "result":
		om.flushText(topic)

		var res struct {
			IsError    bool              `json:"is_error"`
			Result     string            `json:"result"`
			ModelUsage map[string]struct {
				ContextWindow int `json:"contextWindow"`
			} `json:"modelUsage"`
		}
		json.Unmarshal(ev.Raw, &res)

		for _, mu := range res.ModelUsage {
			topic.contextWindow = mu.ContextWindow
			break
		}

		if res.IsError {
			text := fmt.Sprintf("❌ %s", res.Result)
			om.sender.Send(topic.threadID, text, "", nil)
		}

		om.appendContext(topic)
		om.clearReaction(topic)

	case "user":
		om.handleToolResult(topic, ev)

	case "assistant":
		// Streaming events handle output — ignore complete assistant messages.
	}
}

// FlushAndInterrupt flushes partial output, clears reaction, and sends "Interrupted".
func (om *OutputManager) FlushAndInterrupt(threadID int) {
	om.mu.Lock()
	defer om.mu.Unlock()

	topic, ok := om.topics[threadID]
	if !ok {
		return
	}

	om.flushText(topic)
	om.clearReaction(topic)

	om.sender.Send(threadID, "Interrupted.", "", nil)
}

// Cleanup removes all state for a topic.
func (om *OutputManager) Cleanup(threadID int) {
	om.mu.Lock()
	defer om.mu.Unlock()

	if topic, ok := om.topics[threadID]; ok {
		if topic.pending != nil {
			topic.pending.timer.Stop()
		}
		delete(om.topics, threadID)
	}
}

// --- Internal ---

func (om *OutputManager) getOrCreate(threadID int) *TopicOutput {
	topic, ok := om.topics[threadID]
	if !ok {
		topic = &TopicOutput{threadID: threadID}
		om.topics[threadID] = topic
	}
	return topic
}

// SetUserMessage stores the user's message ID for reaction clearing on result.
func (om *OutputManager) SetUserMessage(threadID, msgID int) {
	om.mu.Lock()
	defer om.mu.Unlock()
	topic := om.getOrCreate(threadID)
	topic.userMsgID = msgID
}

func (om *OutputManager) renderThinking(topic *TopicOutput) {
	if topic.thinkingBuffer == "" {
		return
	}

	text := topic.thinkingBuffer
	if len(text) > 3800 {
		text = text[:3800] + "..."
	}

	html := fmt.Sprintf("<blockquote expandable>\U0001f4ad <i>%s</i></blockquote>", escapeHTMLStr(text))
	om.sender.Send(topic.threadID, html, "HTML", nil)

	topic.thinkingBuffer = ""
}

func (om *OutputManager) handleToolResult(topic *TopicOutput, ev claude.OutputEvent) {
	var msg struct {
		Message struct {
			Content []struct {
				IsError bool   `json:"is_error"`
				Content string `json:"content"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(ev.Raw, &msg); err != nil {
		return
	}

	for _, c := range msg.Message.Content {
		if c.Content == "" {
			continue
		}

		content := c.Content
		if len(content) > 3800 {
			content = content[:3800] + "..."
		}

		var prefix string
		if c.IsError {
			prefix = "❌"
		} else {
			prefix = "✓"
		}

		resultLine := fmt.Sprintf("\n%s <code>%s</code>", prefix, escapeHTMLStr(content))

		// Fast tool: result arrived before 3s timer — render tool+result together
		if topic.pending != nil {
			topic.pending.timer.Stop()
			base := strings.TrimSuffix(topic.pending.html, "</blockquote>")
			combined := base + resultLine + "</blockquote>"
			om.sender.Send(topic.threadID, combined, "HTML", nil)
			topic.pending = nil
			return
		}

		// Slow tool: tool was already rendered, edit to append result
		if topic.toolMsgID != 0 && topic.toolMsgHTML != "" {
			base := strings.TrimSuffix(topic.toolMsgHTML, "</blockquote>")
			newHTML := base + resultLine + "</blockquote>"
			om.sender.Edit(topic.threadID, topic.toolMsgID, newHTML, "HTML", nil)
		} else {
			text := fmt.Sprintf("<blockquote expandable>%s <code>%s</code></blockquote>",
				prefix, escapeHTMLStr(content))
			om.sender.Send(topic.threadID, text, "HTML", nil)
		}

		topic.toolMsgID = 0
		topic.toolMsgHTML = ""
		return
	}
}

func (om *OutputManager) appendContext(topic *TopicOutput) {
	total := topic.lastInputTokens + topic.lastOutputTokens
	window := topic.contextWindow
	if window == 0 {
		window = 200000
	}
	if total == 0 {
		return
	}

	ctx := fmt.Sprintf("<i>%d / %d</i>", total, window)

	if topic.lastTextMsgID != 0 && topic.lastTextContent != "" {
		content := topic.lastTextContent
		if topic.lastTextMode != "HTML" {
			content = escapeHTMLStr(content)
		}
		newContent := content + "\n" + ctx
		om.sender.Edit(topic.threadID, topic.lastTextMsgID, newContent, "HTML", nil)
	} else {
		om.sender.Send(topic.threadID, ctx, "HTML", nil)
	}

	topic.lastTextMsgID = 0
	topic.lastTextContent = ""
	topic.lastTextMode = ""
}

func (om *OutputManager) clearReaction(topic *TopicOutput) {
	if topic.userMsgID != 0 {
		SetReaction(om.api, om.chatID, topic.userMsgID, "")
		topic.userMsgID = 0
	}
}

// flushText renders the accumulated text buffer as a complete message.
func (om *OutputManager) flushText(topic *TopicOutput) {
	text := topic.textBuffer
	topic.textBuffer = ""

	if text == "" {
		return
	}

	html := render.Telegram(text)

	// Sender handles chunking, but we need the last msgID for context append
	msgID, _ := om.sender.SendSync(topic.threadID, html, "HTML", nil)
	topic.lastTextMsgID = msgID
	topic.lastTextContent = html
	topic.lastTextMode = "HTML"
}

// buildToolHTML builds the blockquote HTML for a tool call.
func buildToolHTML(name, inputJSON string) (html, inputStr string) {
	if inputJSON != "" {
		var input map[string]any
		if err := json.Unmarshal([]byte(inputJSON), &input); err == nil {
			if cmd, ok := input["command"].(string); ok {
				inputStr = cmd
			} else {
				data, _ := json.Marshal(input)
				inputStr = string(data)
			}
		} else {
			inputStr = inputJSON
		}
	}

	if len(inputStr) > 3800 {
		inputStr = inputStr[:3800] + "..."
	}

	if inputStr != "" {
		html = fmt.Sprintf("<blockquote expandable>\u25b6 <b>%s</b>\n<code>%s</code></blockquote>",
			escapeHTMLStr(name), escapeHTMLStr(inputStr))
	} else {
		html = fmt.Sprintf("<blockquote expandable>\u25b6 <b>%s</b></blockquote>", escapeHTMLStr(name))
	}
	return html, inputStr
}

const toolBufferTimeout = 3 * time.Second

// bufferTool stores the completed tool call and starts a timer.
// If the result arrives within 3s, tool+result render as one message.
// If the timer fires first, the tool renders immediately (slow tool path).
func (om *OutputManager) bufferTool(topic *TopicOutput) {
	html, inputStr := buildToolHTML(topic.toolName, topic.toolInput)
	name := topic.toolName

	topic.toolName = ""
	topic.toolInput = ""

	pt := &pendingTool{
		name:     name,
		inputStr: inputStr,
		html:     html,
	}
	topic.pending = pt

	pt.timer = time.AfterFunc(toolBufferTimeout, func() {
		om.mu.Lock()
		defer om.mu.Unlock()

		// Timer fired — tool is slow, render it now
		if topic.pending != pt {
			return // already consumed by handleToolResult
		}
		topic.pending = nil

		msgID, _ := om.sender.SendSync(topic.threadID, html, "HTML", nil)
		topic.toolMsgID = msgID
		topic.toolMsgHTML = html
	})
}

func splitHTML(html string, maxLen int) []string {
	if len(html) <= maxLen {
		return []string{html}
	}

	var chunks []string
	remaining := html

	for len(remaining) > 0 {
		if len(remaining) <= maxLen {
			chunks = append(chunks, remaining)
			break
		}

		splitIdx := findSplitPoint(remaining, maxLen)
		chunks = append(chunks, remaining[:splitIdx])
		remaining = remaining[splitIdx:]
	}

	return chunks
}

func findSplitPoint(text string, maxLen int) int {
	if len(text) <= maxLen {
		return len(text)
	}

	if idx := strings.LastIndex(text[:maxLen], "\n\n"); idx > maxLen/2 {
		return idx + 2
	}

	if idx := strings.LastIndex(text[:maxLen], "\n"); idx > maxLen/2 {
		return idx + 1
	}

	return maxLen
}
