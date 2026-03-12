package telegram

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type opKind int

const (
	opSend opKind = iota
	opEdit
)

type sendOp struct {
	kind      opKind
	threadID  int
	msgID     int
	text      string
	parseMode string
	markup    any
	result    chan sendResult
}

type sendResult struct {
	msgID int
	err   error
}

// Sender provides reliable message delivery with per-thread FIFO queues,
// proactive chunking, HTML fallback, and 429 handling.
type Sender struct {
	api    *tgbotapi.BotAPI
	chatID int64
	mu     sync.Mutex
	queues map[int]chan *sendOp
}

func NewSender(api *tgbotapi.BotAPI, chatID int64) *Sender {
	return &Sender{
		api:    api,
		chatID: chatID,
		queues: make(map[int]chan *sendOp),
	}
}

// Send enqueues a message for async delivery. Fire-and-forget.
func (s *Sender) Send(threadID int, text, parseMode string, markup any) {
	for _, chunk := range s.chunk(text, parseMode) {
		s.enqueue(&sendOp{
			kind:      opSend,
			threadID:  threadID,
			text:      chunk,
			parseMode: parseMode,
			markup:    markup,
		})
	}
}

// Edit enqueues a message edit for async delivery. Fire-and-forget.
func (s *Sender) Edit(threadID int, msgID int, text, parseMode string, markup any) {
	// Edits don't chunk — the caller is responsible for keeping edits within limits
	// (streaming edits are already bounded in doEdit)
	s.enqueue(&sendOp{
		kind:      opEdit,
		threadID:  threadID,
		msgID:     msgID,
		text:      text,
		parseMode: parseMode,
		markup:    markup,
	})
}

// SendSync sends a message and blocks until delivered. Returns (msgID, error).
func (s *Sender) SendSync(threadID int, text, parseMode string, markup any) (int, error) {
	chunks := s.chunk(text, parseMode)
	var lastMsgID int
	var lastErr error

	for _, chunk := range chunks {
		ch := make(chan sendResult, 1)
		s.enqueue(&sendOp{
			kind:      opSend,
			threadID:  threadID,
			text:      chunk,
			parseMode: parseMode,
			markup:    markup,
			result:    ch,
		})
		res := <-ch
		lastMsgID = res.msgID
		lastErr = res.err
	}

	return lastMsgID, lastErr
}

// EditSync edits a message and blocks until delivered. Returns error.
func (s *Sender) EditSync(threadID int, msgID int, text, parseMode string, markup any) error {
	ch := make(chan sendResult, 1)
	s.enqueue(&sendOp{
		kind:      opEdit,
		threadID:  threadID,
		msgID:     msgID,
		text:      text,
		parseMode: parseMode,
		markup:    markup,
		result:    ch,
	})
	return (<-ch).err
}

func (s *Sender) enqueue(op *sendOp) {
	s.mu.Lock()
	q, ok := s.queues[op.threadID]
	if !ok {
		q = make(chan *sendOp, 256)
		s.queues[op.threadID] = q
		go s.worker(op.threadID, q)
	}
	s.mu.Unlock()
	q <- op
}

func (s *Sender) worker(threadID int, q chan *sendOp) {
	for op := range q {
		var res sendResult
		switch op.kind {
		case opSend:
			res.msgID, res.err = s.doSend(op)
		case opEdit:
			res.err = s.doEditOp(op)
		}
		if op.result != nil {
			op.result <- res
		}
	}
}

func (s *Sender) doSend(op *sendOp) (int, error) {
	for {
		msgID, err := SendToThread(s.api, s.chatID, op.threadID, op.text, op.parseMode, op.markup)
		if err == nil {
			return msgID, nil
		}

		if op.parseMode != "" && isParseError(err) {
			return SendToThread(s.api, s.chatID, op.threadID, stripHTML(op.text), "", op.markup)
		}

		if wait := retryAfter(err); wait > 0 {
			time.Sleep(wait)
			continue
		}

		return 0, err
	}
}

func (s *Sender) doEditOp(op *sendOp) error {
	for {
		err := EditMessage(s.api, s.chatID, op.msgID, op.text, op.parseMode, op.markup)
		if err == nil {
			return nil
		}

		if op.parseMode != "" && isParseError(err) {
			return EditMessage(s.api, s.chatID, op.msgID, stripHTML(op.text), "", op.markup)
		}

		if wait := retryAfter(err); wait > 0 {
			time.Sleep(wait)
			continue
		}

		return err
	}
}

// chunk proactively splits text into ≤4096-rune pieces.
func (s *Sender) chunk(text string, parseMode string) []string {
	if utf8.RuneCountInString(text) <= maxMessageLen {
		return []string{text}
	}

	if parseMode == "HTML" {
		return chunkHTML(text, maxMessageLen)
	}

	return chunkPlain(text, maxMessageLen)
}

func chunkPlain(text string, maxRunes int) []string {
	var chunks []string
	for len(text) > 0 {
		if utf8.RuneCountInString(text) <= maxRunes {
			chunks = append(chunks, text)
			break
		}
		idx := findSplitPoint(text, maxRunes)
		chunks = append(chunks, text[:idx])
		text = text[idx:]
	}
	return chunks
}

// chunkHTML splits HTML text respecting open tags.
// At each split point, closes open tags and reopens them in the next chunk.
func chunkHTML(html string, maxRunes int) []string {
	var chunks []string

	for len(html) > 0 {
		if utf8.RuneCountInString(html) <= maxRunes {
			chunks = append(chunks, html)
			break
		}

		splitIdx := findSplitPoint(html, maxRunes-100) // reserve room for closing/opening tags
		openTags := findOpenTags(html[:splitIdx])

		// Close open tags at split point
		suffix := closeTags(openTags)
		chunk := html[:splitIdx] + suffix

		// Reopen tags in next chunk
		prefix := reopenTags(openTags)
		html = prefix + html[splitIdx:]

		chunks = append(chunks, chunk)
	}

	return chunks
}

// findOpenTags scans HTML and returns a stack of currently open tag names.
var tagRegex = regexp.MustCompile(`<(/?)([a-zA-Z]+)[^>]*>`)

func findOpenTags(html string) []string {
	matches := tagRegex.FindAllStringSubmatch(html, -1)
	var stack []string
	for _, m := range matches {
		isClose := m[1] == "/"
		tagName := strings.ToLower(m[2])
		if isClose {
			// Pop matching tag from stack
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i] == tagName {
					stack = append(stack[:i], stack[i+1:]...)
					break
				}
			}
		} else {
			// Skip self-closing / void tags
			if !isVoidTag(tagName) {
				stack = append(stack, tagName)
			}
		}
	}
	return stack
}

func isVoidTag(tag string) bool {
	switch tag {
	case "br", "hr", "img", "input", "meta", "link":
		return true
	}
	return false
}

func closeTags(tags []string) string {
	var sb strings.Builder
	for i := len(tags) - 1; i >= 0; i-- {
		sb.WriteString("</")
		sb.WriteString(tags[i])
		sb.WriteString(">")
	}
	return sb.String()
}

func reopenTags(tags []string) string {
	var sb strings.Builder
	for _, tag := range tags {
		sb.WriteString("<")
		sb.WriteString(tag)
		sb.WriteString(">")
	}
	return sb.String()
}

func isParseError(err error) bool {
	s := err.Error()
	return strings.Contains(s, "can't parse entities") || strings.Contains(s, "Can't parse entities")
}

func retryAfter(err error) time.Duration {
	s := err.Error()
	if !strings.Contains(s, "Too Many Requests") && !strings.Contains(s, "retry after") {
		return 0
	}
	// Try to extract retry_after from Telegram API error
	// Format: "Too Many Requests: retry after N"
	var apiErr struct {
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if json.Unmarshal([]byte(s), &apiErr) == nil && apiErr.Parameters.RetryAfter > 0 {
		return time.Duration(apiErr.Parameters.RetryAfter) * time.Second
	}
	// Fallback: parse from error string
	idx := strings.Index(s, "retry after ")
	if idx >= 0 {
		rest := s[idx+len("retry after "):]
		var n int
		fmt.Sscanf(rest, "%d", &n)
		if n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 5 * time.Second
}

func stripHTML(html string) string {
	return tagRegex.ReplaceAllString(html, "")
}
