// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsNonRetryableBotStatus(t *testing.T) {
	nonRetry := []int{400, 401, 403, 404, 405, 409, 410, 422}
	retry := []int{408, 429, 500, 502, 503, 504}

	for _, c := range nonRetry {
		if !isNonRetryableBotStatus(c) {
			t.Errorf("isNonRetryableBotStatus(%d) = false, want true (Python no_retry)", c)
		}
	}
	for _, c := range retry {
		if isNonRetryableBotStatus(c) {
			t.Errorf("isNonRetryableBotStatus(%d) = true, want false (Python retries)", c)
		}
	}
}

func TestBotClient_RetryPolicy_FiveAttempts(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "mock-token", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL
	bc.client = &http.Client{Timeout: 5 * time.Second}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	_, err := bc.SendActivity(ctx, srv.URL, "conv-1", &Activity{Type: ActivityTypeMessage, Text: "hi"})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}

	got := atomic.LoadInt32(&attempts)
	if got != 5 {
		t.Errorf("attempts = %d, want 5 (Python stop_after_attempt(5))", got)
	}
}

func TestBotClient_RetryPolicy_NoRetryOn4xx(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 405, 409, 410, 422} {
		var attempts int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
					"access_token": "t", "token_type": "Bearer", "expires_in": 3600,
				})
				return
			}
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(code)
		}))

		bc := NewBotClient("app-id", "app-password", "", slog.Default())
		bc.apiBaseURL = srv.URL

		_, _ = bc.SendActivity(context.Background(), srv.URL, "conv-1", &Activity{Type: ActivityTypeMessage})
		srv.Close()

		if got := atomic.LoadInt32(&attempts); got != 1 {
			t.Errorf("status %d: attempts = %d, want 1 (non-retryable)", code, got)
		}
	}
}

func TestSendTeamsMessage_AttachesCardWhenProvided(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "t", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		gotBody, _ = readAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"id": "act-1"}) //nolint:errcheck
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	meta := &TeamsTaskMeta{ServiceURL: srv.URL, ConversationID: "conv-1"}
	card := StatusCard("Working...", "task-1", "", true)

	id, err := sendTeamsMessage(context.Background(), bc, meta, "Working...", &card, "reply-42")
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if id != "act-1" {
		t.Errorf("activity ID = %q", id)
	}

	var m map[string]any
	if err := json.Unmarshal(gotBody, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["text"] != "Working..." {
		t.Errorf("text = %v", m["text"])
	}
	if m["replyToId"] != "reply-42" {
		t.Errorf("replyToId = %v", m["replyToId"])
	}
	atts, ok := m["attachments"].([]any)
	if !ok || len(atts) != 1 {
		t.Fatalf("attachments = %v", m["attachments"])
	}
}

func TestSendTeamsMessage_NoAttachmentsFieldWhenCardNil(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "t", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		gotBody, _ = readAll(r.Body)
		json.NewEncoder(w).Encode(map[string]any{"id": "act-1"}) //nolint:errcheck
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	meta := &TeamsTaskMeta{ServiceURL: srv.URL, ConversationID: "conv-1"}
	_, err := sendTeamsMessage(context.Background(), bc, meta, "plain text", nil, "")
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	var m map[string]any
	_ = json.Unmarshal(gotBody, &m)
	if _, ok := m["attachments"]; ok {
		t.Errorf("attachments field present when card=nil; must be omitted to avoid preserving prior attachments on update")
	}
	if _, ok := m["replyToId"]; ok {
		t.Errorf("replyToId present when empty")
	}
}

func TestUpdateTeamsMessage_PutsToActivityID(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "t", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	meta := &TeamsTaskMeta{ServiceURL: srv.URL, ConversationID: "conv-1"}
	if err := updateTeamsMessage(context.Background(), bc, meta, "act-99", "updated text", nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotPath != "/v3/conversations/conv-1/activities/act-99" {
		t.Errorf("path = %q", gotPath)
	}

	if err := updateTeamsMessage(context.Background(), bc, meta, "", "x", nil); err == nil {
		t.Error("expected error on empty activityID")
	}
}

func TestSendTypingIndicator_NonFatalOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "t", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	bc := NewBotClient("app-id", "app-password", "", slog.Default())
	bc.apiBaseURL = srv.URL

	meta := &TeamsTaskMeta{ServiceURL: srv.URL, ConversationID: "conv-1"}
	sendTypingIndicator(context.Background(), bc, meta, "bot-app-id")
	sendTypingIndicator(context.Background(), nil, meta, "bot-app-id")
	sendTypingIndicator(context.Background(), bc, nil, "bot-app-id")
}

func readAll(r interface {
	Read([]byte) (int, error)
}) ([]byte, error) {
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 1024)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, err
		}
	}
}
