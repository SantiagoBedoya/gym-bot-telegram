package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/SantiagoBedoya/gym-bot/internal/db"
	"github.com/SantiagoBedoya/gym-bot/internal/services"
)

var greetings = []string{"hola", "hi", "hello", "hey", "buenas", "buenos", "good morning", "good afternoon", "good evening", "ey", "epa", "qué más"}

type TelegramHandler struct {
	openAI           *services.OpenAIService
	transcription    *services.TranscriptionService
	queries          *db.Queries
	botToken         string
	authorizedUserID int64
}

func NewTelegramHandler(openAI *services.OpenAIService, transcription *services.TranscriptionService, queries *db.Queries, botToken string, authorizedUserID int64) *TelegramHandler {
	return &TelegramHandler{
		openAI:           openAI,
		transcription:    transcription,
		queries:          queries,
		botToken:         botToken,
		authorizedUserID: authorizedUserID,
	}
}

func (h *TelegramHandler) Handle(ctx context.Context, b *tgbot.Bot, update *models.Update) {
	if update.CallbackQuery != nil {
		h.handleCallback(ctx, b, update.CallbackQuery)
		return
	}

	if update.Message == nil || update.Message.From == nil {
		return
	}
	if update.Message.From.ID != h.authorizedUserID {
		return
	}

	chatID := update.Message.Chat.ID

	// Voice message
	if update.Message.Voice != nil {
		h.handleVoice(ctx, b, chatID, update.Message.Voice.FileID)
		return
	}

	userMessage := update.Message.Text
	if userMessage == "" {
		return
	}

	if isGreeting(userMessage) {
		h.sendRoutineMenu(ctx, b, chatID)
		return
	}

	h.chat(ctx, b, chatID, userMessage)
}

func (h *TelegramHandler) handleVoice(ctx context.Context, b *tgbot.Bot, chatID int64, fileID string) {
	b.SendChatAction(ctx, &tgbot.SendChatActionParams{
		ChatID: chatID,
		Action: models.ChatActionTyping,
	})

	audio, err := h.downloadFile(ctx, b, fileID)
	if err != nil {
		log.Printf("[ERROR] downloading voice file: %v", err)
		b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "No pude descargar el audio. Intenta de nuevo.",
		})
		return
	}

	text, err := h.transcription.Transcribe(ctx, audio)
	if err != nil {
		log.Printf("[ERROR] transcribing voice: %v", err)
		b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "No pude transcribir el audio. Intenta de nuevo.",
		})
		return
	}

	if isGreeting(text) {
		h.sendRoutineMenu(ctx, b, chatID)
		return
	}

	h.chat(ctx, b, chatID, text)
}

func (h *TelegramHandler) downloadFile(ctx context.Context, b *tgbot.Bot, fileID string) ([]byte, error) {
	f, err := b.GetFile(ctx, &tgbot.GetFileParams{FileID: fileID})
	if err != nil {
		return nil, fmt.Errorf("get file info: %w", err)
	}

	url := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", h.botToken, f.FilePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
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

func (h *TelegramHandler) sendRoutineMenu(ctx context.Context, b *tgbot.Bot, chatID int64) {
	routines, err := h.queries.ListRoutines(ctx)
	if err != nil {
		log.Printf("[ERROR] listing routines for menu: %v", err)
	}

	var rows [][]models.InlineKeyboardButton
	for _, r := range routines {
		rows = append(rows, []models.InlineKeyboardButton{
			{Text: "Entrenar " + r, CallbackData: "Hoy entreno " + r},
		})
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: "Ver mis rutinas", CallbackData: "Lista mis rutinas"},
		{Text: "Ultima sesion", CallbackData: "Cual fue mi ultima sesion"},
	})

	_, err = b.SendMessage(ctx, &tgbot.SendMessageParams{
		ChatID: chatID,
		Text:   "Que hacemos hoy?",
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: rows,
		},
	})
	if err != nil {
		log.Printf("[ERROR] sending routine menu to chat %d: %v", chatID, err)
	}
}

func (h *TelegramHandler) chat(ctx context.Context, b *tgbot.Bot, chatID int64, userMessage string) {
	response, err := h.openAI.Chat(ctx, userMessage)
	if err != nil {
		log.Printf("[ERROR] openai chat: %v", err)
		b.SendMessage(ctx, &tgbot.SendMessageParams{
			ChatID: chatID,
			Text:   "Hubo un error al procesar tu mensaje. Intenta de nuevo.",
		})
		return
	}

	if response == "" {
		response = "No obtuve respuesta. Intenta de nuevo."
	}

	text, keyboard := parseQuickReplies(response)
	params := &tgbot.SendMessageParams{
		ChatID:    chatID,
		Text:      text,
		ParseMode: models.ParseModeHTML,
	}
	if keyboard != nil {
		params.ReplyMarkup = keyboard
	}
	if _, err := b.SendMessage(ctx, params); err != nil {
		log.Printf("[ERROR] sending message to chat %d: %v", chatID, err)
	}
}

func isGreeting(msg string) bool {
	normalized := strings.ToLower(strings.TrimSpace(msg))
	for _, g := range greetings {
		if normalized == g || strings.HasPrefix(normalized, g+" ") {
			return true
		}
	}
	return false
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
