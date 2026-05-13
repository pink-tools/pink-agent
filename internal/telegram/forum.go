package telegram

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Thin helpers over go-telegram/bot that hide chatID and produce values
// in the shapes pink-agent uses.

func CreateForumTopic(ctx context.Context, api *bot.Bot, chatID int64, name string) (int, error) {
	topic, err := api.CreateForumTopic(ctx, &bot.CreateForumTopicParams{
		ChatID: chatID,
		Name:   name,
	})
	if err != nil {
		return 0, err
	}
	return topic.MessageThreadID, nil
}

func DeleteForumTopic(ctx context.Context, api *bot.Bot, chatID int64, threadID int) error {
	_, err := api.DeleteForumTopic(ctx, &bot.DeleteForumTopicParams{
		ChatID:          chatID,
		MessageThreadID: threadID,
	})
	return err
}

func SetReaction(ctx context.Context, api *bot.Bot, chatID int64, messageID int, emoji string) {
	params := &bot.SetMessageReactionParams{
		ChatID:    chatID,
		MessageID: messageID,
	}
	if emoji != "" {
		params.Reaction = []models.ReactionType{
			{
				Type:              models.ReactionTypeTypeEmoji,
				ReactionTypeEmoji: &models.ReactionTypeEmoji{Emoji: emoji},
			},
		}
	} else {
		params.Reaction = []models.ReactionType{}
	}
	api.SetMessageReaction(ctx, params)
}

func SetBotCommands(ctx context.Context, api *bot.Bot, chatID int64) {
	api.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Commands: []models.BotCommand{
			{Command: "stop", Description: "Stop Claude"},
			{Command: "store", Description: "List project files"},
		},
		Scope: &models.BotCommandScopeChat{ChatID: chatID},
	})
}

func SendChatAction(ctx context.Context, api *bot.Bot, chatID int64, threadID int, action models.ChatAction) {
	api.SendChatAction(ctx, &bot.SendChatActionParams{
		ChatID:          chatID,
		MessageThreadID: threadID,
		Action:          action,
	})
}
