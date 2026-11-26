// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package channels

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendWebhook_Success(t *testing.T) {
	var got []byte
	var gotMethod, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := map[string]any{
		"url":     srv.URL,
		"method":  "put",
		"headers": map[string]any{"X-Custom": "v"},
	}
	if err := SendWebhook(t.Context(), srv.Client(), cfg, Payload{Title: "T", Message: "M"}, true); err != nil {
		t.Fatalf("SendWebhook: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", gotMethod)
	}
	if gotHeader != "v" {
		t.Errorf("custom header not forwarded: %q", gotHeader)
	}
	var payload map[string]any
	if err := json.Unmarshal(got, &payload); err != nil || payload["message"] != "M" {
		t.Errorf("unexpected body: %s", got)
	}
}

func TestSendWebhook_MissingURL(t *testing.T) {
	if err := SendWebhook(t.Context(), http.DefaultClient, map[string]any{}, Payload{}, true); err == nil {
		t.Error("expected error for missing url")
	}
}

func TestSendWebhook_DeniedHeader(t *testing.T) {
	cfg := map[string]any{"url": "https://example.com/hook", "headers": map[string]any{"Authorization": "Bearer x"}}
	if err := SendWebhook(t.Context(), http.DefaultClient, cfg, Payload{}, true); err == nil {
		t.Error("expected error for denied Authorization header")
	}
}

func TestSendWebhook_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()
	err := SendWebhook(t.Context(), srv.Client(), map[string]any{"url": srv.URL}, Payload{Message: "x"}, true)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestFormatWebhookPayload_Variants(t *testing.T) {
	p := Payload{Title: "Hi", Message: "body", Fields: map[string]any{"k": "v"}}
	if got := FormatWebhookPayload(p, map[string]any{}); got == nil {
		t.Error("generic payload nil")
	}
	slack := FormatWebhookPayload(p, map[string]any{"webhook_type": "slack"}).(map[string]any)
	if _, ok := slack["blocks"]; !ok {
		t.Error("slack payload missing blocks")
	}
	teams := FormatWebhookPayload(p, map[string]any{"webhook_type": "teams"}).(map[string]any)
	if teams["@type"] != "MessageCard" {
		t.Error("teams payload not a MessageCard")
	}
}

func TestFormatPayloads_Defaults(t *testing.T) {
	if FormatSlackPayload(Payload{})["text"] != "Notification" {
		t.Error("slack default headline wrong")
	}
	if FormatTeamsPayload(Payload{})["title"] != "Notification" {
		t.Error("teams default headline wrong")
	}
}
