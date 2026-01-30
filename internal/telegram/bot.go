package telegram

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"

	otel "github.com/pink-tools/pink-otel"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	api      *tgbotapi.BotAPI
	userID   int64
	handlers *Handlers
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
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	otel.Info(ctx, "telegram bot started", otel.Attr{"username", b.api.Self.UserName})

	for {
		select {
		case <-ctx.Done():
			return
		case update := <-updates:
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
	var reply string

	switch {
	case msg.Voice != nil:
		b.setReaction(msg.Chat.ID, msg.MessageID, "⚡")

		path, err := b.downloadFile(msg.Voice.FileID, "voice.ogg")
		if err != nil {
			b.setReaction(msg.Chat.ID, msg.MessageID, "")
			reply = "Failed to download voice: " + err.Error()
		} else {
			transcribed, errMsg := b.handlers.HandleVoice(path)
			b.setReaction(msg.Chat.ID, msg.MessageID, "")
			if errMsg != "" {
				reply = errMsg
			} else if transcribed != "" {
				reply = transcribed
			}
		}

	case msg.Document != nil:
		path, err := b.downloadFile(msg.Document.FileID, msg.Document.FileName)
		if err != nil {
			reply = "Failed to download file: " + err.Error()
		} else {
			reply = b.handlers.HandleFile(path)
		}

	case msg.Text != "":
		reply = b.handlers.HandleText(msg.Text)

	default:
		return
	}

	if reply != "" {
		b.SendMessage(msg.Chat.ID, reply)
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
