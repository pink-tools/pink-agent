package telegram

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	otel "github.com/pink-tools/pink-otel"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	api          *tgbotapi.BotAPI
	userID       int64
	handlers     *Handlers
	pendingFiles []string // files waiting for text
}

func NewBot(token string, userID int64, handlers *Handlers) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}

	return &Bot{
		api:      api,
		userID:   userID,
		handlers: handlers,
	}, nil
}

func (b *Bot) Start(ctx context.Context) {
	config := tgbotapi.NewUpdate(0)
	config.Timeout = 60

	otel.Info(ctx, "telegram bot started", otel.Attr{"username", b.api.Self.UserName})

	connected := true
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := b.api.GetUpdates(config)
		if err != nil {
			if connected {
				otel.Warn(ctx, "telegram disconnected, reconnecting...")
				connected = false
			}
			time.Sleep(3 * time.Second)
			continue
		}

		if !connected {
			otel.Info(ctx, "telegram reconnected")
			connected = true
		}

		for _, update := range updates {
			if update.UpdateID >= config.Offset {
				config.Offset = update.UpdateID + 1
			}

			if update.Message == nil {
				continue
			}
			if update.Message.From.ID != b.userID {
				continue
			}

			b.handleUpdate(ctx, update)
		}
	}
}

func (b *Bot) handleUpdate(ctx context.Context, update tgbotapi.Update) {
	msg := update.Message

	// Voice is special — transcribe and send
	if msg.Voice != nil {
		b.handleVoice(ctx, msg)
		return
	}

	// Download any files in this message
	if msg.Photo != nil && len(msg.Photo) > 0 {
		photo := msg.Photo[len(msg.Photo)-1]
		if path, err := b.downloadFile(photo.FileID, "photo.jpg"); err == nil {
			b.pendingFiles = append(b.pendingFiles, path)
		}
	}

	if msg.Document != nil {
		if path, err := b.downloadFile(msg.Document.FileID, msg.Document.FileName); err == nil {
			b.pendingFiles = append(b.pendingFiles, path)
		}
	}

	if msg.Video != nil {
		filename := "video.mp4"
		if msg.Video.FileName != "" {
			filename = msg.Video.FileName
		}
		if path, err := b.downloadFile(msg.Video.FileID, filename); err == nil {
			b.pendingFiles = append(b.pendingFiles, path)
		}
	}

	if msg.Audio != nil {
		filename := "audio.mp3"
		if msg.Audio.FileName != "" {
			filename = msg.Audio.FileName
		}
		if path, err := b.downloadFile(msg.Audio.FileID, filename); err == nil {
			b.pendingFiles = append(b.pendingFiles, path)
		}
	}

	// Get text from Caption or Text
	text := msg.Caption
	if text == "" {
		text = msg.Text
	}

	// No text yet — files accumulate, wait for text
	if text == "" {
		return
	}

	// Text received — send with all accumulated files
	files := b.pendingFiles
	b.pendingFiles = nil

	reply := b.handlers.HandleMessage(text, files)
	if reply != "" {
		b.SendMessage(msg.Chat.ID, reply)
	}
}

func (b *Bot) handleVoice(ctx context.Context, msg *tgbotapi.Message) {
	b.setReaction(msg.Chat.ID, msg.MessageID, "⚡")

	path, err := b.downloadFile(msg.Voice.FileID, "voice.ogg")
	if err != nil {
		b.setReaction(msg.Chat.ID, msg.MessageID, "")
		b.SendMessage(msg.Chat.ID, "Failed to download voice: "+err.Error())
		return
	}

	transcribed, errMsg := b.handlers.HandleVoice(path)
	b.setReaction(msg.Chat.ID, msg.MessageID, "")

	if errMsg != "" {
		b.SendMessage(msg.Chat.ID, errMsg)
	} else if transcribed != "" {
		b.SendMessage(msg.Chat.ID, "🎤 "+transcribed)
	}
}

func (b *Bot) downloadFile(fileID, filename string) (string, error) {
	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return "", err
	}

	resp, err := http.Get(file.Link(b.api.Token))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	path := filepath.Join(os.TempDir(), filename)
	out, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return path, err
}

func (b *Bot) SendMessage(chatID int64, text string) {
	SendMessage(b.api.Token, chatID, text)
}

func (b *Bot) SendFile(chatID int64, path string) {
	SendFile(b.api.Token, chatID, path)
}

func (b *Bot) SetMenuButton(webAppURL string) error {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", b.userID)
	params.AddInterface("menu_button", map[string]any{
		"type": "web_app",
		"text": "Open",
		"web_app": map[string]string{
			"url": webAppURL,
		},
	})
	_, err := b.api.MakeRequest("setChatMenuButton", params)
	return err
}

func (b *Bot) setReaction(chatID int64, messageID int, emoji string) {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_id", messageID)

	if emoji != "" {
		params.AddInterface("reaction", []map[string]any{
			{"type": "emoji", "emoji": emoji},
		})
	} else {
		params.AddInterface("reaction", []map[string]any{})
	}

	b.api.MakeRequest("setMessageReaction", params)
}

