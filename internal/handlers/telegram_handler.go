package handlers

import (
	"context"
	"log"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/SantiagoBedoya/gym-bot/internal/services"
)

type TelegramHandler struct {
	openAI           *services.OpenAIService
	authorizedUserID int64
}

func NewTelegramHandler(openAI *services.OpenAIService, authorizedUserID int64) *TelegramHandler {
	return &TelegramHandler{
		openAI:           openAI,
		authorizedUserID: authorizedUserID,
	}
}

func (h *TelegramHandler) Handle(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}
	if update.Message.From == nil || update.Message.From.ID != h.authorizedUserID {
		return
	}

	userMessage := update.Message.Text
	if userMessage == "" {
		return
	}

	chatID := update.Message.Chat.ID

	// Send typing indicator
	b.SendChatAction(ctx, &tgbot.SendChatActionParams{
		ChatID: chatID,
		Action: models.ChatActionTyping,
	})

	response, err := h.openAI.Chat(ctx, userMessage)
	if err != nil {
		log.Printf("openai error: %v", err)
		b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "Hubo un error al procesar tu mensaje. Intenta de nuevo.",
		})
		return
	}

	if response == "" {
		response = "No obtuve respuesta. Intenta de nuevo."
	}

	b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID: chatID,
		Text:   response,
	})
}
