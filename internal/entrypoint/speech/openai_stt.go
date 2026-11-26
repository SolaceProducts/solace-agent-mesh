// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type OpenAISTT struct {
	apiKey string
	model  string
	url    string
	client *http.Client
	logger *slog.Logger
}

func NewOpenAISTT(apiKey, model, endpointURL string, logger *slog.Logger) *OpenAISTT {
	if normalized, err := normalizeURL(endpointURL); err == nil {
		if normalized != endpointURL {
			logger.Debug("normalized STT endpoint URL", "to", normalized)
		}
		endpointURL = normalized
	}
	return &OpenAISTT{
		apiKey: apiKey,
		model:  model,
		url:    endpointURL,
		client: &http.Client{Timeout: 30 * time.Second},
		logger: logger,
	}
}

func normalizeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Path != "" {
		u.Path = path.Clean("/" + u.Path)
	}
	return u.String(), nil
}

func (p *OpenAISTT) Transcribe(ctx context.Context, audio []byte, mimeType string, _ STTOptions) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	ext := ".wav"
	switch mimeType {
	case "audio/mp3", "audio/mpeg":
		ext = ".mp3"
	case "audio/mp4":
		ext = ".mp4"
	case "audio/webm":
		ext = ".webm"
	case "audio/ogg":
		ext = ".ogg"
	case "audio/flac":
		ext = ".flac"
	}

	part, err := writer.CreateFormFile("file", "audio"+ext)
	if err != nil {
		return "", fmt.Errorf("openai stt: create form file: %w", err)
	}
	if _, err = part.Write(audio); err != nil {
		return "", fmt.Errorf("openai stt: write audio: %w", err)
	}

	if err = writer.WriteField("model", p.model); err != nil {
		return "", fmt.Errorf("openai stt: write model field: %w", err)
	}
	if err = writer.WriteField("response_format", "text"); err != nil {
		return "", fmt.Errorf("openai stt: write format field: %w", err)
	}
	if err = writer.Close(); err != nil {
		return "", fmt.Errorf("openai stt: close writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, &body)
	if err != nil {
		return "", fmt.Errorf("openai stt: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai stt: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("openai stt: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai stt: status %d: %s", resp.StatusCode, string(respBody))
	}

	return extractTranscript(respBody), nil
}

func extractTranscript(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var obj struct {
			Text *string `json:"text"`
		}
		if err := json.Unmarshal(trimmed, &obj); err == nil && obj.Text != nil {
			return strings.TrimSpace(*obj.Text)
		}
	}
	return strings.TrimSpace(string(body))
}
