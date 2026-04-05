package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	tgbot "github.com/go-telegram/bot"
	"github.com/joho/godotenv"

	"github.com/SantiagoBedoya/gym-bot/internal/config"
	"github.com/SantiagoBedoya/gym-bot/internal/db"
	"github.com/SantiagoBedoya/gym-bot/internal/handlers"
	"github.com/SantiagoBedoya/gym-bot/internal/services"
	"github.com/SantiagoBedoya/gym-bot/internal/tools"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Database
	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	queries := db.New(pool)

	// Tools dispatcher
	dispatcher := tools.NewDispatcher(pool)

	// OpenAI service
	openAISvc := services.NewOpenAIService(cfg.OpenAIAPIKey, cfg.OpenAIModel, queries, dispatcher)
	openAISvc.LoadState(ctx)

	// Transcription service
	transcriptionSvc := services.NewTranscriptionService(cfg.AssemblyAIAPIKey)

	// Telegram handler
	handler := handlers.NewTelegramHandler(openAISvc, transcriptionSvc, queries, cfg.BotToken, cfg.AuthorizedUserID)

	// Bot
	bot, err := tgbot.New(cfg.BotToken, tgbot.WithDefaultHandler(handler.Handle))
	if err != nil {
		log.Fatalf("bot: %v", err)
	}

	log.Println("gym-bot started")
	bot.Start(ctx)
}
