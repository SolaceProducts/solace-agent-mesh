// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	verifierKeyText  = "code_verifier"
	authPromptText   = "authentication_required"
	authURIKeyText   = "auth_uri"
	replayTimeout    = 15 * time.Second
	userIDHeaderName = "X-SAM-User-ID"
)

func (h *TestHarness) AssertNoCodeVerifierOnStream(t *testing.T, collected []CollectedEvent) {
	t.Helper()
	if !h.scenario.AssertNoCodeVerifierOnStream {
		return
	}
	httpGW, ok := h.gateway.(*HTTPGateway)
	if !ok {
		t.Error("assert_no_code_verifier_on_stream requires go-http gateway mode")
		return
	}
	taskID := httpGW.lastTaskID()
	if taskID == "" {
		t.Error("assert_no_code_verifier_on_stream: no task ID available")
		return
	}
	ctx, cancel := context.WithTimeout(t.Context(), replayTimeout+10*time.Second)
	defer cancel()

	live := make([]string, 0, len(collected))
	for _, ev := range collected {
		live = append(live, string(ev.Payload))
	}
	checkAuthPromptSurface(t, "live stream", strings.Join(live, "\n"))

	userID := h.effectiveUserID()
	replayed, err := httpGW.replayStream(ctx, taskID, userID)
	if err != nil {
		t.Errorf("assert_no_code_verifier_on_stream: replay: %v", err)
	} else {
		checkAuthPromptSurface(t, "replayed stream", replayed)
	}

	buffered, err := httpGW.bufferedEventsBody(ctx, taskID, userID)
	if err != nil {
		t.Errorf("assert_no_code_verifier_on_stream: events/buffered: %v", err)
	} else {
		checkAuthPromptSurface(t, "events/buffered", buffered)
	}

	rows, err := httpGW.eventBufferRows(ctx, taskID)
	if err != nil {
		t.Errorf("assert_no_code_verifier_on_stream: sse_event_buffer: %v", err)
	} else {
		checkAuthPromptSurface(t, "sse_event_buffer rows", rows)
	}
}

func checkAuthPromptSurface(t *testing.T, surface, body string) {
	t.Helper()
	if strings.Contains(body, verifierKeyText) {
		t.Errorf("assert_no_code_verifier_on_stream: %s carries %s", surface, verifierKeyText)
	}
	if !strings.Contains(body, authPromptText) || !strings.Contains(body, authURIKeyText) {
		t.Errorf("assert_no_code_verifier_on_stream: %s has no %s prompt with an %s, so it proves nothing",
			surface, authPromptText, authURIKeyText)
	}
}

func (g *HTTPGateway) replayStream(ctx context.Context, taskID, userID string) (string, error) {
	subCtx, cancel := context.WithTimeout(ctx, replayTimeout)
	defer cancel()

	sseClient := NewSSEClient(fmt.Sprintf("%s/api/v1/sse/subscribe/%s", g.baseURL, taskID), &http.Client{Timeout: 0}, g.logger)
	sseClient.Headers = map[string]string{userIDHeaderName: userID}
	events := make(chan SSEEvent, 100)
	go func() {
		_ = sseClient.Subscribe(subCtx, events)
		close(events)
	}()

	var body strings.Builder
	for evt := range events {
		body.Write(evt.Data)
		body.WriteByte('\n')
		if evt.Event == "final_response" || evt.Event == "error" {
			break
		}
	}
	if body.Len() == 0 {
		return "", fmt.Errorf("the replay delivered no events")
	}
	return body.String(), nil
}

func (g *HTTPGateway) bufferedEventsBody(ctx context.Context, taskID, userID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/v1/tasks/%s/events/buffered", g.baseURL, taskID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set(userIDHeaderName, userID)
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	return string(body), nil
}

func (g *HTTPGateway) eventBufferRows(ctx context.Context, taskID string) (string, error) {
	db, err := g.openStoreDB()
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.QueryContext(ctx, `SELECT event_data FROM sse_event_buffer WHERE task_id = ?`, taskID)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()

	var all strings.Builder
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return "", err
		}
		all.Write(data)
		all.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if all.Len() == 0 {
		return "", fmt.Errorf("no rows for task")
	}
	return all.String(), nil
}
