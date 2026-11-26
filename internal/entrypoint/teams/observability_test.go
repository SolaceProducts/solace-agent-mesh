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
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/observability"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestBotClient_SendActivity_RecordsMetric(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "t", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"id": "a1"}) //nolint:errcheck
	}))
	defer srv.Close()

	rec := installMetrics(t, observability.NewRecordingMetrics())
	bc := NewBotClient("app", "pw", "", slog.Default())
	bc.apiBaseURL = srv.URL

	if _, err := bc.SendActivity(context.Background(), srv.URL, "conv1", &Activity{Type: ActivityTypeMessage, Text: "hi"}); err != nil {
		t.Fatalf("SendActivity: %v", err)
	}

	if !rec.HasHistogram("outbound.request.duration",
		"service.peer.name", "TeamsGateway",
		"operation.name", "send_message",
		"error.type", "",
	) {
		t.Errorf("expected send_message histogram, got %+v", rec.Histograms("outbound.request.duration"))
	}
	if !rec.HasHistogram("outbound.request.duration",
		"service.peer.name", "TeamsGateway",
		"operation.name", "get_bot_token",
	) {
		t.Errorf("expected get_bot_token histogram, got %+v", rec.Histograms("outbound.request.duration"))
	}
}

func TestBotClient_UpdateActivity_RecordsMetric(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/botframework.com/oauth2/v2.0/token" {
			json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
				"access_token": "t", "token_type": "Bearer", "expires_in": 3600,
			})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rec := installMetrics(t, observability.NewRecordingMetrics())
	bc := NewBotClient("app", "pw", "", slog.Default())
	bc.apiBaseURL = srv.URL

	if err := bc.UpdateActivity(context.Background(), srv.URL, "conv1", "a1", &Activity{Type: ActivityTypeMessage, Text: "upd"}); err != nil {
		t.Fatalf("UpdateActivity: %v", err)
	}

	if !rec.HasHistogram("outbound.request.duration",
		"service.peer.name", "TeamsGateway",
		"operation.name", "update_message",
	) {
		t.Errorf("expected update_message histogram, got %+v", rec.Histograms("outbound.request.duration"))
	}
}

func TestBotClient_FetchToken_RecordsErrorType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_client"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	rec := installMetrics(t, observability.NewRecordingMetrics())
	bc := NewBotClient("app", "bad", "", slog.Default())
	bc.apiBaseURL = srv.URL

	if _, err := bc.SendActivity(context.Background(), srv.URL, "conv1", &Activity{Type: ActivityTypeMessage}); err == nil {
		t.Fatal("expected auth error")
	}

	tokenHists := func() int {
		n := 0
		for _, h := range rec.Histograms("outbound.request.duration") {
			if h.Labels["operation.name"] == "get_bot_token" {
				n++
			}
		}
		return n
	}()
	if tokenHists == 0 {
		t.Fatal("expected get_bot_token histogram")
	}

	for _, h := range rec.Histograms("outbound.request.duration") {
		if h.Labels["operation.name"] == "get_bot_token" {
			if h.Labels["error.type"] == "" {
				t.Errorf("expected non-empty error.type on failed token fetch, got %+v", h.Labels)
			}
		}
	}
}

func TestDownloadAttachment_RecordsMetric(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("x")) //nolint:errcheck
	}))
	defer srv.Close()

	rec := installMetrics(t, observability.NewRecordingMetrics())
	att := Attachment{Name: "x.txt", ContentURL: srv.URL + "/f"}

	_, err := downloadAttachment(context.Background(), att, 100, rec, slog.Default())
	if err != nil {
		t.Fatalf("downloadAttachment: %v", err)
	}

	if !rec.HasHistogram("outbound.request.duration",
		"service.peer.name", "TeamsGateway",
		"operation.name", "download_attachment",
		"error.type", "",
	) {
		t.Errorf("expected download_attachment histogram, got %+v", rec.Histograms("outbound.request.duration"))
	}
}

func TestDownloadAttachment_NilMetricsDoesNotPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("x")) //nolint:errcheck
	}))
	defer srv.Close()

	att := Attachment{Name: "x.txt", ContentURL: srv.URL}
	if _, err := downloadAttachment(context.Background(), att, 100, nil, slog.Default()); err != nil {
		t.Errorf("nil metrics: %v", err)
	}
	if _, err := downloadAttachment(context.Background(), att, 100, runtime.NoopMetrics{}, slog.Default()); err != nil {
		t.Errorf("noop metrics: %v", err)
	}
}
