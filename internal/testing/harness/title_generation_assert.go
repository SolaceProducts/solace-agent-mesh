// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func (h *TestHarness) AssertTitleGeneration(t *testing.T) {
	t.Helper()

	expected := h.scenario.ExpectedTitleGeneration
	if expected == nil {
		return
	}

	httpGW, ok := h.gateway.(*HTTPGateway)
	if !ok {
		t.Error("expected_title_generation requires go-http gateway mode")
		return
	}

	sessionID := httpGW.LastSessionID()
	if sessionID == "" {
		t.Error("expected_title_generation: no session ID available (did the task create a session?)")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	userMessage := expected.UserMessage
	agentResponse := expected.AgentResponse
	force := expected.Force

	err := httpGW.GenerateTitle(ctx, sessionID, userMessage, agentResponse, force)
	if err != nil {
		t.Errorf("expected_title_generation: generate-title call failed: %v", err)
		return
	}

	time.Sleep(500 * time.Millisecond)

	title, err := httpGW.GetSessionTitle(ctx, sessionID)
	if err != nil {
		t.Errorf("expected_title_generation: get session failed: %v", err)
		return
	}

	if expected.TitleNotEmpty && title == "" {
		t.Error("expected_title_generation: expected non-empty title, got empty")
	}
	if expected.TitleContains != "" && !strings.Contains(title, expected.TitleContains) {
		t.Errorf("expected_title_generation: title = %q, want it to contain %q", title, expected.TitleContains)
	}
	if expected.TitleEquals != "" && title != expected.TitleEquals {
		t.Errorf("expected_title_generation: title = %q, want %q", title, expected.TitleEquals)
	}
}

func (g *HTTPGateway) GenerateTitle(ctx context.Context, sessionID, userMessage, agentResponse string, force bool) error {
	body := map[string]any{
		"userMessage":   userMessage,
		"agentResponse": agentResponse,
		"force":         force,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/sessions/%s/generateTitle", g.baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("POST generate-title: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("generate-title: status %d, body: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (g *HTTPGateway) GetSessionTitle(ctx context.Context, sessionID string) (string, error) {
	url := fmt.Sprintf("%s/api/v1/sessions/%s", g.baseURL, sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET session: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GET session: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Name *string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode session response: %w", err)
	}

	if result.Name == nil {
		return "", nil
	}
	return *result.Name, nil
}

func (g *HTTPGateway) LastSessionID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastSessionID
}
