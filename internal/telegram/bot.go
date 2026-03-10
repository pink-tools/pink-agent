package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/pink-tools/pink-core"
	"github.com/pink-tools/pink-core/log"

	"pink-agent/internal/claude"
	agentcontext "pink-agent/internal/context"
	"pink-agent/internal/state"
	"pink-agent/internal/store"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

type messageBatch struct {
	messages []*ForumMessage
	files    []string
	timer    *time.Timer
}

// ForumUpdate is our own update struct that includes message_thread_id.
type ForumUpdate struct {
	UpdateID int           `json:"update_id"`
	Message  *ForumMessage `json:"message,omitempty"`
}

type ForumMessage struct {
	MessageID       int         `json:"message_id"`
	From            *TGUser     `json:"from,omitempty"`
	Chat            TGChat      `json:"chat"`
	Text            string      `json:"text,omitempty"`
	Caption         string      `json:"caption,omitempty"`
	MessageThreadID int         `json:"message_thread_id,omitempty"`
	Voice           *TGVoice    `json:"voice,omitempty"`
	Photo           []TGPhoto   `json:"photo,omitempty"`
	Document        *TGDocument `json:"document,omitempty"`
	Video           *TGVideo    `json:"video,omitempty"`
	Audio           *TGAudio    `json:"audio,omitempty"`
	IsTopicMessage  bool        `json:"is_topic_message,omitempty"`
	ForumTopicClosed   *struct{}        `json:"forum_topic_closed,omitempty"`
	ForumTopicCreated  *ForumTopicInfo  `json:"forum_topic_created,omitempty"`
	ForumTopicEdited   *ForumTopicInfo  `json:"forum_topic_edited,omitempty"`
	ForumTopicReopened *struct{}        `json:"forum_topic_reopened,omitempty"`
	ForumTopicDeleted  *struct{}        `json:"forum_topic_deleted,omitempty"`
}

type ForumTopicInfo struct {
	Name string `json:"name,omitempty"`
}

type TGUser struct {
	ID int64 `json:"id"`
}

type TGChat struct {
	ID int64 `json:"id"`
}

type TGVoice struct {
	FileID string `json:"file_id"`
}

type TGPhoto struct {
	FileID string `json:"file_id"`
}

type TGDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
}

type TGVideo struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
}

type TGAudio struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
}

type Bot struct {
	api          *tgbotapi.BotAPI
	chatID       int64
	ctx          context.Context // lifecycle context from Start loop
	state        *state.Manager
	claude       *claude.Manager
	store        *store.FileStore
	sender       *Sender
	output       *OutputManager
	agentContext string // assembled context for init prompts
	batches      map[int]*messageBatch
	batchesMu    sync.Mutex
	dmSent       map[int64]bool // chatID → already sent setup instructions
}

func NewBot(token string, chatID int64, stateMgr *state.Manager, claudeMgr *claude.Manager, fileStore *store.FileStore, embeddedContext string) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}

	sender := NewSender(api, chatID)

	b := &Bot{
		api:          api,
		chatID:       chatID,
		state:        stateMgr,
		claude:       claudeMgr,
		store:        fileStore,
		sender:       sender,
		agentContext: agentcontext.Build(embeddedContext),
		batches:      make(map[int]*messageBatch),
		dmSent:       make(map[int64]bool),
	}

	b.output = NewOutputManager(api, chatID, sender)

	return b, nil
}

func (b *Bot) Start(ctx context.Context) {
	b.ctx = ctx
	SetBotCommands(b.api, b.chatID)
	log.Info(ctx, "telegram bot started", log.Attr{K: "username", V: b.api.Self.UserName})

	offset := 0
	connected := true
	backoff := 3 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, newOffset, err := b.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if connected {
				log.Warn(ctx, "telegram disconnected, reconnecting...")
				connected = false
			}
			time.Sleep(backoff)
			if backoff < 60*time.Second {
				backoff *= 2
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
			}
			continue
		}

		offset = newOffset
		backoff = 3 * time.Second
		if !connected {
			log.Info(ctx, "telegram reconnected")
			connected = true
		}

		for _, update := range updates {
			b.handleUpdate(ctx, update)
		}
	}
}

// getUpdates fetches updates with context-aware HTTP request for clean shutdown.
func (b *Bot) getUpdates(ctx context.Context, offset int) ([]ForumUpdate, int, error) {
	body, _ := json.Marshal(map[string]any{
		"offset":          offset,
		"timeout":         60,
		"allowed_updates": []string{"message"},
	})

	url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates", b.api.Token)
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
	if err != nil {
		return nil, offset, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Timeout slightly longer than long-poll timeout so Telegram returns first
	client := &http.Client{Timeout: 65 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, offset, err
	}
	defer resp.Body.Close()

	var result struct {
		OK     bool          `json:"ok"`
		Result []ForumUpdate `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, offset, err
	}

	newOffset := offset
	for _, u := range result.Result {
		if u.UpdateID >= newOffset {
			newOffset = u.UpdateID + 1
		}
	}

	return result.Result, newOffset, nil
}

func (b *Bot) handleUpdate(ctx context.Context, update ForumUpdate) {
	msg := update.Message
	if msg == nil || msg.From == nil {
		return
	}

	// Only process messages from the configured group
	if msg.Chat.ID != b.chatID {
		b.replyDM(msg.Chat.ID)
		return
	}

	threadID := msg.MessageThreadID

	// Service messages for forum topics
	if threadID != 0 {
		if msg.ForumTopicCreated != nil {
			b.handleTopicCreated(ctx, threadID, msg.ForumTopicCreated.Name, msg.From.ID)
			return
		}
		if msg.ForumTopicEdited != nil && msg.ForumTopicEdited.Name != "" {
			b.handleTopicEdited(ctx, threadID, msg.ForumTopicEdited.Name)
			return
		}
		if msg.ForumTopicClosed != nil {
			b.handleTopicClosed(ctx, threadID)
			return
		}
	}

	// General topic — ignore
	if !msg.IsTopicMessage {
		return
	}

	// /stop command — flush batch, then interrupt
	if msg.Text == "/stop" || strings.HasPrefix(msg.Text, "/stop@") {
		b.flushExistingBatch(ctx, threadID)
		b.claude.Interrupt(threadID)
		b.output.FlushAndInterrupt(threadID)
		return
	}

	// /store command — flush batch, then handle
	if msg.Text == "/store" || strings.HasPrefix(msg.Text, "/store@") {
		b.flushExistingBatch(ctx, threadID)
		b.handleStoreCommand(threadID)
		return
	}

	// Voice — flush batch, then handle immediately
	if msg.Voice != nil {
		b.flushExistingBatch(ctx, threadID)
		b.handleVoice(ctx, msg)
		return
	}

	// Everything else — add to batch (text, photos, docs, video, audio)
	b.addToBatch(ctx, msg)
}

func (b *Bot) sendToClaude(ctx context.Context, threadID int, text string) {
	project := b.state.GetProjectByThread(threadID)
	if project == nil {
		return
	}

	// If process is not alive, respawn with resume
	if !b.claude.Alive(threadID) {
		sessionID := project.SessionID
		if err := b.spawnClaude(threadID, sessionID, nil); err != nil {
			b.sender.Send(threadID, "Failed to start Claude: "+err.Error(), "", nil)
			return
		}
		// Fresh session — inject project context
		if sessionID == "" {
			initPrompt := b.buildInitPrompt(project.ID)
			b.claude.Send(threadID, initPrompt)
		}
	}

	if err := b.claude.Send(threadID, text); err != nil {
		log.Error(ctx, "claude send failed", log.Attr{K: "error", V: err.Error()})
		b.sender.Send(threadID, "Failed to send: "+err.Error(), "", nil)
	}
}

// spawnClaude starts a claude process for a thread, wiring up event handling.
func (b *Bot) spawnClaude(threadID int, sessionID string, extraEnv []string) error {
	var workDir string
	extraEnv = append(extraEnv, "DISABLE_AUTO_COMPACT=true")
	// Inject project/thread env vars for CLI commands
	if p := b.state.GetProjectByThread(threadID); p != nil {
		extraEnv = append(extraEnv,
			fmt.Sprintf("PINK_PROJECT_ID=%s", p.ID),
			fmt.Sprintf("PINK_THREAD_ID=%d", threadID),
		)
		workDir = p.Dir
	}

	return b.claude.Start(threadID, sessionID, workDir, extraEnv, func(ev claude.OutputEvent) {
		b.output.HandleEvent(threadID, ev)

		// Save session ID from init event
		if ev.Type == "system" && ev.Subtype == "init" && ev.SessionID != "" {
			if p := b.state.GetProjectByThread(threadID); p != nil {
				b.state.SetProjectSession(p.ID, ev.SessionID)
			}
		}

		// Detect context limit
		if ev.Type == "result" && ev.IsError && ev.Result == "Prompt is too long" {
			go b.restartSession(b.ctx, threadID)
		}
	})
}

func (b *Bot) restartSession(ctx context.Context, threadID int) {
	p := b.state.GetProjectByThread(threadID)
	if p == nil {
		return
	}

	b.claude.Stop(threadID)
	b.output.Cleanup(threadID)

	b.sender.Send(threadID,
		"⚠️ Context limit reached. This session is done — starting fresh.\n\n"+
			"Your conversation history is gone. If you need something from the old session, forward the messages here.",
		"", nil)

	if err := b.spawnClaude(threadID, "", nil); err != nil {
		b.sender.Send(threadID, "Failed to restart: "+err.Error(), "", nil)
		return
	}

	prompt := b.buildRestartPrompt(p.ID)
	b.claude.Send(threadID, prompt)

	log.Info(ctx, "session restarted (context limit)", log.Attr{K: "project", V: p.Name})
}

func (b *Bot) handleTopicCreated(ctx context.Context, threadID int, name string, fromID int64) {
	// Skip topics created by the bot itself (via CLI or IPC)
	if fromID == b.api.Self.ID {
		return
	}

	// Guard: skip if project already exists for this thread
	if b.state.GetProjectByThread(threadID) != nil {
		return
	}

	// Create project and link to this thread
	projectID, err := b.state.CreateProject(name, core.HomeDir())
	if err != nil {
		log.Error(ctx, "create project failed", log.Attr{K: "error", V: err.Error()})
		return
	}
	b.state.SetProjectThread(projectID, threadID)
	b.store.InitProject(projectID)

	// Spawn Claude
	if err := b.spawnClaude(threadID, "", nil); err != nil {
		log.Error(ctx, "spawn claude failed", log.Attr{K: "error", V: err.Error()})
		b.sender.Send(threadID, "Failed to start Claude: "+err.Error(), "", nil)
		return
	}

	// Confirm activation
	b.sender.Send(threadID, "🦄 Pink Agent activated and ready to work.", "", nil)

	// Send init prompt
	initPrompt := b.buildInitPrompt(projectID)
	if err := b.claude.Send(threadID, initPrompt); err != nil {
		log.Error(ctx, "init send failed", log.Attr{K: "error", V: err.Error()})
	}
}

func (b *Bot) handleTopicEdited(ctx context.Context, threadID int, name string) {
	if p := b.state.GetProjectByThread(threadID); p != nil {
		b.state.RenameProject(p.ID, name)
	}
}

func (b *Bot) handleTopicClosed(ctx context.Context, threadID int) {
	p := b.state.GetProjectByThread(threadID)
	if p == nil {
		return
	}

	b.batchesMu.Lock()
	if batch, ok := b.batches[threadID]; ok {
		if batch.timer != nil {
			batch.timer.Stop()
		}
		delete(b.batches, threadID)
	}
	b.batchesMu.Unlock()
	b.claude.Stop(threadID)
	b.output.Cleanup(threadID)
	b.store.DeleteProject(p.ID)
	b.state.DeleteProject(p.ID)
	DeleteForumTopic(b.api, b.chatID, threadID)

	log.Info(ctx, "project deleted", log.Attr{K: "project", V: p.Name}, log.Attr{K: "threadId", V: threadID})
}

// CreateProjectResult is the response for CLI project creation.
type CreateProjectResult struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ThreadID int    `json:"threadId"`
}

// CreateProject creates a forum topic, project, and Claude session.
func (b *Bot) CreateProject(name, prompt, dir string) (*CreateProjectResult, error) {
	threadID, err := CreateForumTopic(b.api, b.chatID, name)
	if err != nil {
		return nil, fmt.Errorf("create topic: %w", err)
	}

	projectID, err := b.state.CreateProject(name, dir)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	b.state.SetProjectThread(projectID, threadID)
	b.store.InitProject(projectID)

	if err := b.spawnClaude(threadID, "", nil); err != nil {
		return nil, fmt.Errorf("spawn claude: %w", err)
	}

	initPrompt := b.buildInitPrompt(projectID)
	if err := b.claude.Send(threadID, initPrompt); err != nil {
		return nil, fmt.Errorf("send init prompt: %w", err)
	}

	if prompt != "" {
		if err := b.claude.Send(threadID, prompt); err != nil {
			return nil, fmt.Errorf("send prompt: %w", err)
		}
	}

	return &CreateProjectResult{
		ID:       projectID,
		Name:     name,
		ThreadID: threadID,
	}, nil
}

// DeleteProject stops the session, removes the topic, store, and state.
func (b *Bot) DeleteProject(projectID string) error {
	p := b.state.GetProject(projectID)
	if p == nil {
		return state.ErrProjectNotFound
	}

	if p.ThreadID != 0 {
		b.batchesMu.Lock()
		if batch, ok := b.batches[p.ThreadID]; ok {
			if batch.timer != nil {
				batch.timer.Stop()
			}
			delete(b.batches, p.ThreadID)
		}
		b.batchesMu.Unlock()
		b.claude.Stop(p.ThreadID)
		b.output.Cleanup(p.ThreadID)
		DeleteForumTopic(b.api, b.chatID, p.ThreadID)
	}

	b.store.DeleteProject(projectID)
	return b.state.DeleteProject(projectID)
}

const dmSetupText = `I only work in a Telegram group with Topics enabled.

Setup:
1. Create a private group
2. Enable Topics (Settings → Topics)
3. Add me to the group as admin
4. Set TELEGRAM_GROUP_ID in .env to your group ID`

func (b *Bot) replyDM(chatID int64) {
	if b.dmSent[chatID] {
		return
	}
	b.dmSent[chatID] = true
	msg := tgbotapi.NewMessage(chatID, dmSetupText)
	b.api.Send(msg)
}

func (b *Bot) handleVoice(ctx context.Context, msg *ForumMessage) {
	threadID := msg.MessageThreadID
	SetReaction(b.api, b.chatID, msg.MessageID, "\u270d\ufe0f")

	path, err := b.downloadFile(msg.Voice.FileID, "voice.ogg")
	if err != nil {
		SetReaction(b.api, b.chatID, msg.MessageID, "")
		b.sender.Send(threadID, "Failed to download voice: "+err.Error(), "", nil)
		return
	}
	defer os.Remove(path)

	text, err := transcribe(path)
	if err != nil {
		SetReaction(b.api, b.chatID, msg.MessageID, "")
		b.sender.Send(threadID, "Transcription failed: "+err.Error(), "", nil)
		return
	}

	// Show transcription in topic
	b.sender.Send(threadID, "\U0001f3a4 "+text, "", nil)

	// Switch to processing reaction
	SendChatAction(b.api, b.chatID, threadID, "typing")
	b.output.SetUserMessage(threadID, msg.MessageID)

	voicePrefix := "[VOICE INPUT: May contain speech recognition errors. Ask for clarification if unclear.] "
	b.sendToClaude(ctx, threadID, voicePrefix+text)
}

func (b *Bot) addToBatch(ctx context.Context, msg *ForumMessage) {
	threadID := msg.MessageThreadID

	// Download files outside the lock — HTTP requests can be slow
	var files []string
	if len(msg.Photo) > 0 {
		photo := msg.Photo[len(msg.Photo)-1]
		if path, err := b.downloadFile(photo.FileID, "photo.jpg"); err == nil {
			files = append(files, path)
		}
	}
	if msg.Document != nil {
		if path, err := b.downloadFile(msg.Document.FileID, msg.Document.FileName); err == nil {
			files = append(files, path)
		}
	}
	if msg.Video != nil {
		filename := "video.mp4"
		if msg.Video.FileName != "" {
			filename = msg.Video.FileName
		}
		if path, err := b.downloadFile(msg.Video.FileID, filename); err == nil {
			files = append(files, path)
		}
	}
	if msg.Audio != nil {
		filename := "audio.mp3"
		if msg.Audio.FileName != "" {
			filename = msg.Audio.FileName
		}
		if path, err := b.downloadFile(msg.Audio.FileID, filename); err == nil {
			files = append(files, path)
		}
	}

	b.batchesMu.Lock()
	batch, ok := b.batches[threadID]
	if !ok {
		batch = &messageBatch{}
		b.batches[threadID] = batch
	}
	batch.files = append(batch.files, files...)
	batch.messages = append(batch.messages, msg)

	if batch.timer != nil {
		batch.timer.Stop()
	}
	batch.timer = time.AfterFunc(2*time.Second, func() {
		b.flushBatch(ctx, threadID)
	})
	b.batchesMu.Unlock()
}

func (b *Bot) flushBatch(ctx context.Context, threadID int) {
	b.batchesMu.Lock()
	batch, ok := b.batches[threadID]
	if !ok {
		b.batchesMu.Unlock()
		return
	}
	delete(b.batches, threadID)
	b.batchesMu.Unlock()

	// Collect texts
	var texts []string
	for _, msg := range batch.messages {
		t := msg.Caption
		if t == "" {
			t = msg.Text
		}
		if t != "" {
			texts = append(texts, t)
		}
	}

	if len(texts) == 0 && len(batch.files) == 0 {
		return
	}

	// Build message
	var message string
	if len(batch.files) > 0 {
		message = "Files:\n"
		for _, f := range batch.files {
			message += f + "\n"
		}
		message += "\n"
	}
	message += strings.Join(texts, "\n")

	// Use last message ID for reply tracking
	lastMsg := batch.messages[len(batch.messages)-1]
	SendChatAction(b.api, b.chatID, threadID, "typing")
	b.output.SetUserMessage(threadID, lastMsg.MessageID)
	b.sendToClaude(ctx, threadID, message)
}

func (b *Bot) flushExistingBatch(ctx context.Context, threadID int) {
	b.batchesMu.Lock()
	batch, ok := b.batches[threadID]
	if ok && batch.timer != nil {
		batch.timer.Stop()
	}
	b.batchesMu.Unlock()
	if !ok {
		return
	}
	b.flushBatch(ctx, threadID)
}

func (b *Bot) downloadFile(fileID, filename string) (string, error) {
	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return "", err
	}

	resp, err := httpClient.Get(file.Link(b.api.Token))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	path := filepath.Join(os.TempDir(), fmt.Sprintf("%s-%s%s", base, fileID[:8], ext))
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return path, err
}

func (b *Bot) buildInitPrompt(projectID string) string {
	var parts []string
	parts = append(parts, "New Pink Agent session.")

	if projectID != "" {
		if p := b.state.GetProject(projectID); p != nil {
			parts = append(parts, fmt.Sprintf("Project: %s.", p.Name))
		}
	}

	parts = append(parts, fmt.Sprintf("\n\n%s\n\nWait for instructions.", b.agentContext))

	return strings.Join(parts, " ")
}

func (b *Bot) buildRestartPrompt(projectID string) string {
	var parts []string
	parts = append(parts, "Session restarted (context limit).")

	if projectID != "" {
		if p := b.state.GetProject(projectID); p != nil {
			parts = append(parts, fmt.Sprintf("Project: %s.", p.Name))
		}

		if ctx := readProjectContext(b.store, projectID); ctx != "" {
			parts = append(parts, fmt.Sprintf("\nProject context:\n%s\n", ctx))
		}
	}

	parts = append(parts, fmt.Sprintf("\n\n%s\n\nWait for instructions.", b.agentContext))

	return strings.Join(parts, " ")
}

func (b *Bot) buildAttachPrompt() string {
	return fmt.Sprintf("Continuing a Desktop session via pink-agent. Output now streams to Telegram.\n\n%s\n\nConfirm you're ready to continue.", b.agentContext)
}

// readProjectContext reads PROJECT.md from store, returns empty string if missing or placeholder.
func readProjectContext(fs *store.FileStore, projectID string) string {
	data, err := fs.Get(projectID, "PROJECT.md")
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" || content == "(empty)" {
		return ""
	}
	return content
}

// handleStoreCommand lists project files in the topic.
func (b *Bot) handleStoreCommand(threadID int) {
	p := b.state.GetProjectByThread(threadID)
	if p == nil {
		return
	}

	files, err := b.store.List(p.ID)
	if err != nil {
		b.sender.Send(threadID, "Failed to list files: "+err.Error(), "", nil)
		return
	}

	if len(files) == 0 {
		b.sender.Send(threadID, "No files in store.", "", nil)
		return
	}

	var sb strings.Builder
	sb.WriteString("<b>Store files:</b>\n")
	for _, f := range files {
		sb.WriteString(fmt.Sprintf("<code>%s</code> (%d bytes)\n", escapeHTMLStr(f.Name), f.Size))
	}
	b.sender.Send(threadID, sb.String(), "HTML", nil)
}

func escapeHTMLStr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// transcribe calls pink-transcriber to convert audio to text.
func transcribe(audioPath string) (string, error) {
	if _, err := os.Stat(audioPath); err != nil {
		return "", err
	}

	cmd := exec.Command(core.BinaryPath("pink-transcriber"), "transcribe", audioPath)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s", exitErr.Stderr)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// AttachSessionResult is the response for CLI session attach.
type AttachSessionResult struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ThreadID int    `json:"threadId"`
}

// AttachSession creates a project from an existing Claude session.
func (b *Bot) AttachSession(sessionID, dir, name string) (*AttachSessionResult, error) {
	threadID, err := CreateForumTopic(b.api, b.chatID, name)
	if err != nil {
		return nil, fmt.Errorf("create topic: %w", err)
	}

	projectID, err := b.state.CreateProject(name, dir)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	b.state.SetProjectThread(projectID, threadID)
	b.state.SetProjectSession(projectID, sessionID)
	b.store.InitProject(projectID)

	if err := b.spawnClaude(threadID, sessionID, nil); err != nil {
		return nil, fmt.Errorf("spawn claude: %w", err)
	}

	if err := b.claude.Send(threadID, b.buildAttachPrompt()); err != nil {
		return nil, fmt.Errorf("send attach prompt: %w", err)
	}

	return &AttachSessionResult{
		ID:       projectID,
		Name:     name,
		ThreadID: threadID,
	}, nil
}
