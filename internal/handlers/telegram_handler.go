package handlers

import (
	"context"
	"log"
	"strings"

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
	if update.CallbackQuery != nil {
		h.handleCallback(ctx, b, update.CallbackQuery)
		return
	}

	if update.Message == nil {
		return
	}
	if update.Message.From == nil {
		return
	}
	log.Printf("[DEBUG] mensaje de user ID: %d (autorizado: %d)", update.Message.From.ID, h.authorizedUserID)
	if update.Message.From.ID != h.authorizedUserID {
		log.Printf("[DEBUG] mensaje rechazado: ID no autorizado")
		return
	}

	userMessage := update.Message.Text
	if userMessage == "" {
		return
	}

	chatID := update.Message.Chat.ID

	h.chat(ctx, b, chatID, userMessage)
}

func (h *TelegramHandler) handleCallback(ctx context.Context, b *tgbot.Bot, cq *models.CallbackQuery) {
	if cq.From.ID != h.authorizedUserID {
		return
	}

	b.AnswerCallbackQuery(ctx, &tgbot.AnswerCallbackQueryParams{
		CallbackQueryID: cq.ID,
	})

	if cq.Message.Type != models.MaybeInaccessibleMessageTypeMessage || cq.Message.Message == nil {
		return
	}
	chatID := cq.Message.Message.Chat.ID

	b.SendChatAction(ctx, &tgbot.SendChatActionParams{
		ChatID: chatID,
		Action: models.ChatActionTyping,
	})

	h.chat(ctx, b, chatID, cq.Data)
}


func (h *TelegramHandler) chat(ctx context.Context, b *tgbot.Bot, chatID int64, userMessage string) {
	log.Printf("[DEBUG] llamando OpenAI con mensaje: %q", userMessage)
	response, err := h.openAI.Chat(ctx, userMessage)
	if err != nil {
		log.Printf("[DEBUG] openai error: %v", err)
		b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "Hubo un error al procesar tu mensaje. Intenta de nuevo.",
		})
		return
	}
	log.Printf("[DEBUG] respuesta OpenAI (len=%d): %q", len(response), response)

	if response == "" {
		response = "No obtuve respuesta. Intenta de nuevo."
	}

	text, keyboard := parseQuickReplies(response)
	log.Printf("[DEBUG] enviando mensaje a chatID %d", chatID)
	params := &tgbot.SendMessageParams{
		ChatID:    chatID,
		Text:      text,
		ParseMode: models.ParseModeHTML,
	}
	if keyboard != nil {
		params.ReplyMarkup = keyboard
	}
	_, sendErr := b.SendMessage(ctx, params)
	if sendErr != nil {
		log.Printf("[DEBUG] error enviando mensaje: %v", sendErr)
	}
}

// parseQuickReplies extracts a [QR:opt1|opt2|...] tag from the end of the AI response.
// Returns the clean text and an inline keyboard (nil if no tag found).
func parseQuickReplies(text string) (string, *models.InlineKeyboardMarkup) {
	const prefix = "[QR:"
	idx := strings.LastIndex(text, prefix)
	if idx == -1 {
		return text, nil
	}
	end := strings.Index(text[idx:], "]")
	if end == -1 {
		return text, nil
	}

	tag := text[idx : idx+end+1]
	inner := tag[len(prefix) : len(tag)-1]
	options := strings.Split(inner, "|")

	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton
	for _, opt := range options {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}
		row = append(row, models.InlineKeyboardButton{
			Text:         opt,
			CallbackData: opt,
		})
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	if len(rows) == 0 {
		return text, nil
	}

	cleanText := strings.TrimSpace(text[:idx])
	return cleanText, &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}
