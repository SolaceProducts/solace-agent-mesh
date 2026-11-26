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
	"net/http"
	"time"
)

type AzureSTT struct {
	subscriptionKey string
	region          string
	language        string
	client          *http.Client
	logger          *slog.Logger
}

func NewAzureSTT(subscriptionKey, region, language string, logger *slog.Logger) *AzureSTT {
	return &AzureSTT{
		subscriptionKey: subscriptionKey,
		region:          region,
		language:        language,
		client:          &http.Client{Timeout: 30 * time.Second},
		logger:          logger,
	}
}

func (p *AzureSTT) Transcribe(ctx context.Context, audio []byte, mimeType string, _ STTOptions) (string, error) {
	url := fmt.Sprintf("https://%s.stt.speech.microsoft.com/speech/recognition/conversation/cognitiveservices/v1?language=%s",
		p.region, p.language)

	contentType := mimeType
	if contentType == "" {
		contentType = "audio/wav"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(audio))
	if err != nil {
		return "", fmt.Errorf("azure stt: create request: %w", err)
	}
	req.Header.Set("Ocp-Apim-Subscription-Key", p.subscriptionKey)
	req.Header.Set("Content-Type", contentType)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("azure stt: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("azure stt: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("azure stt: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		RecognitionStatus string `json:"RecognitionStatus"`
		DisplayText       string `json:"DisplayText"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("azure stt: parse response: %w", err)
	}

	if result.RecognitionStatus != "Success" {
		return "", fmt.Errorf("azure stt: recognition status: %s", result.RecognitionStatus)
	}

	return result.DisplayText, nil
}
