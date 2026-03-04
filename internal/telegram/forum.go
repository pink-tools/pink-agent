package telegram

import (
	"encoding/json"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Forum API helpers via MakeRequest (library lacks native support).

func CreateForumTopic(api *tgbotapi.BotAPI, chatID int64, name string) (int, error) {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params["name"] = name

	resp, err := api.MakeRequest("createForumTopic", params)
	if err != nil {
		return 0, err
	}

	var result struct {
		MessageThreadID int `json:"message_thread_id"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return 0, err
	}
	return result.MessageThreadID, nil
}

func CloseForumTopic(api *tgbotapi.BotAPI, chatID int64, threadID int) error {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_thread_id", threadID)
	_, err := api.MakeRequest("closeForumTopic", params)
	return err
}

func ReopenForumTopic(api *tgbotapi.BotAPI, chatID int64, threadID int) error {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_thread_id", threadID)
	_, err := api.MakeRequest("reopenForumTopic", params)
	return err
}

func EditForumTopic(api *tgbotapi.BotAPI, chatID int64, threadID int, name string) error {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_thread_id", threadID)
	params["name"] = name
	_, err := api.MakeRequest("editForumTopic", params)
	return err
}

func DeleteForumTopic(api *tgbotapi.BotAPI, chatID int64, threadID int) error {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_thread_id", threadID)
	_, err := api.MakeRequest("deleteForumTopic", params)
	return err
}

// SendToThread sends a text message to a forum topic.
func SendToThread(api *tgbotapi.BotAPI, chatID int64, threadID int, text, parseMode string, replyMarkup any) (int, error) {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_thread_id", threadID)
	params["text"] = text
	if parseMode != "" {
		params["parse_mode"] = parseMode
	}
	if replyMarkup != nil {
		data, err := json.Marshal(replyMarkup)
		if err != nil {
			return 0, err
		}
		params["reply_markup"] = string(data)
	}

	resp, err := api.MakeRequest("sendMessage", params)
	if err != nil {
		return 0, err
	}

	var result struct {
		MessageID int `json:"message_id"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return 0, err
	}
	return result.MessageID, nil
}

// EditMessage edits a message's text.
func EditMessage(api *tgbotapi.BotAPI, chatID int64, messageID int, text, parseMode string, replyMarkup any) error {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_id", messageID)
	params["text"] = text
	if parseMode != "" {
		params["parse_mode"] = parseMode
	}
	if replyMarkup != nil {
		data, err := json.Marshal(replyMarkup)
		if err != nil {
			return err
		}
		params["reply_markup"] = string(data)
	}

	_, err := api.MakeRequest("editMessageText", params)
	return err
}

// DeleteMessage deletes a message.
func DeleteMessage(api *tgbotapi.BotAPI, chatID int64, messageID int) error {
	params := tgbotapi.Params{}
	params.AddNonZero64("chat_id", chatID)
	params.AddNonZero("message_id", messageID)
	_, err := api.MakeRequest("deleteMessage", params)
	return err
}

// SetReaction sets or clears a reaction on a message.
func SetReaction(api *tgbotapi.BotAPI, chatID int64, messageID int, emoji string) {
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
	api.MakeRequest("setMessageReaction", params)
}

// SetBotCommands registers /stop command for the group chat.
func SetBotCommands(api *tgbotapi.BotAPI, chatID int64) {
	commands, _ := json.Marshal([]map[string]string{
		{"command": "stop", "description": "Stop Claude"},
		{"command": "store", "description": "List project files"},
	})
	scope, _ := json.Marshal(map[string]any{
		"type":    "chat",
		"chat_id": fmt.Sprintf("%d", chatID),
	})

	params := tgbotapi.Params{}
	params["commands"] = string(commands)
	params["scope"] = string(scope)
	api.MakeRequest("setMyCommands", params)
}
