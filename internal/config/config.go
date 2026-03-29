package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	BotToken         string
	OpenAIAPIKey     string
	DatabaseURL      string
	AuthorizedUserID int64
	OpenAIModel      string
}

func Load() (*Config, error) {
	botToken := os.Getenv("BOT_TOKEN")
	if botToken == "" {
		return nil, fmt.Errorf("BOT_TOKEN is required")
	}

	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}

	userIDStr := os.Getenv("AUTHORIZED_USER_ID")
	if userIDStr == "" {
		return nil, fmt.Errorf("AUTHORIZED_USER_ID is required")
	}
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid AUTHORIZED_USER_ID: %w", err)
	}

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-4o"
	}

	return &Config{
		BotToken:         botToken,
		OpenAIAPIKey:     openAIKey,
		DatabaseURL:      dbURL,
		AuthorizedUserID: userID,
		OpenAIModel:      model,
	}, nil
}
