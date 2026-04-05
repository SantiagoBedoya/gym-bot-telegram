package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const assemblyAIBase = "https://api.assemblyai.com/v2"

type TranscriptionService struct {
	apiKey string
	client *http.Client
}

func NewTranscriptionService(apiKey string) *TranscriptionService {
	return &TranscriptionService{
		apiKey: apiKey,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Transcribe uploads audio bytes to AssemblyAI and returns the transcript text.
func (s *TranscriptionService) Transcribe(ctx context.Context, audio []byte) (string, error) {
	uploadURL, err := s.upload(ctx, audio)
	if err != nil {
		return "", fmt.Errorf("upload audio: %w", err)
	}

	transcriptID, err := s.submit(ctx, uploadURL)
	if err != nil {
		return "", fmt.Errorf("submit transcript: %w", err)
	}

	text, err := s.poll(ctx, transcriptID)
	if err != nil {
		return "", fmt.Errorf("poll transcript: %w", err)
	}

	return text, nil
}

func (s *TranscriptionService) upload(ctx context.Context, audio []byte) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, assemblyAIBase+"/upload", bytes.NewReader(audio))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", s.apiKey)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}

	var result struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.UploadURL, nil
}

func (s *TranscriptionService) submit(ctx context.Context, audioURL string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"audio_url":          audioURL,
		"language_detection": true,
		"speech_model":       "universal-2",
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, assemblyAIBase+"/transcript", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, b)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.ID, nil
}

func (s *TranscriptionService) poll(ctx context.Context, id string) (string, error) {
	url := fmt.Sprintf("%s/transcript/%s", assemblyAIBase, id)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", s.apiKey)

		resp, err := s.client.Do(req)
		if err != nil {
			return "", err
		}

		var result struct {
			Status string `json:"status"`
			Text   string `json:"text"`
			Error  string `json:"error"`
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if err != nil {
			return "", err
		}

		switch result.Status {
		case "completed":
			return result.Text, nil
		case "error":
			return "", fmt.Errorf("assemblyai error: %s", result.Error)
		}
		// status == "queued" or "processing" → keep polling
	}
}
