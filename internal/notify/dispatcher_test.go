// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestDispatcher_Webhook(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dir := newConfigDirectory(map[string][]Channel{
		"trent": {{Type: "webhook", Config: map[string]any{"url": srv.URL}}},
	})
	d := NewDispatcher(dir, nil, slog.Default())
	d.httpClient = srv.Client()
	d.skipURLValidation = true

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID:       "r1",
		RecipientUserID: "trent",
		Mode:            a2a.NotifyModeNotify,
		Title:           "Report ready",
		Message:         "Your report is ready.",
	})

	if len(recs) != 1 || recs[0].Status != "sent" {
		t.Fatalf("expected one sent record, got %+v", recs)
	}
	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("webhook body not JSON: %v (%s)", err, gotBody)
	}
	if payload["message"] != "Your report is ready." {
		t.Errorf("webhook payload missing message: %v", payload)
	}
}

func TestDispatcher_WebhookSlackFormat(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()

	dir := newConfigDirectory(map[string][]Channel{
		"trent": {{Type: "webhook", Config: map[string]any{"url": srv.URL, "webhook_type": "slack"}}},
	})
	d := NewDispatcher(dir, nil, slog.Default())
	d.httpClient = srv.Client()
	d.skipURLValidation = true

	d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "r2", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
		Title: "Hi", Message: "body",
	})

	var payload map[string]any
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if _, ok := payload["blocks"]; !ok {
		t.Errorf("expected Slack Block Kit payload with 'blocks', got %v", payload)
	}
}

func TestDispatcher_NoChannel(t *testing.T) {
	dir := newConfigDirectory(map[string][]Channel{})
	d := NewDispatcher(dir, nil, slog.Default())
	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "r5", RecipientUserID: "unknown", Mode: a2a.NotifyModeNotify, Message: "x",
	})
	if recs != nil {
		t.Errorf("expected nil records for unknown recipient, got %+v", recs)
	}
}

type metaInbox struct{ lastMeta map[string]any }

func (f *metaInbox) Add(_ context.Context, _, _, _ string, metadata map[string]any, _, _ string) (string, error) {
	f.lastMeta = metadata
	return "id-1", nil
}

func TestDispatcher_InputModeDeliversWithSurface(t *testing.T) {
	inbox := &metaInbox{}
	d := NewDispatcher(newConfigDirectory(map[string][]Channel{}), inbox, slog.Default())

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID:       "r6",
		RecipientUserID: "trent",
		Mode:            a2a.NotifyModeInput,
		Message:         "approve?",
		Surface:         json.RawMessage(`{"kind":"a2ui"}`),
	})

	if len(recs) != 1 || recs[0].ChannelType != "web" || recs[0].Status != "sent" {
		t.Fatalf("input-mode prompt should land in the inbox, got %+v", recs)
	}
	if inbox.lastMeta["request_id"] != "r6" {
		t.Errorf("inbox metadata must carry request_id, got %v", inbox.lastMeta["request_id"])
	}
	if inbox.lastMeta["mode"] != a2a.NotifyModeInput {
		t.Errorf("inbox metadata must mark mode=input, got %v", inbox.lastMeta["mode"])
	}
	if _, ok := inbox.lastMeta["surface"]; !ok {
		t.Error("inbox metadata must carry the A2UI surface so the UI can render the card")
	}
}

func TestDispatcher_InputModeMetadataEdges(t *testing.T) {
	inbox := &metaInbox{}
	d := NewDispatcher(newConfigDirectory(map[string][]Channel{}), inbox, slog.Default())

	d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID:       "r7",
		RecipientUserID: "trent",
		Mode:            a2a.NotifyModeInput,
		Message:         "no surface here",
		Metadata:        map[string]any{"task_id": "t-77", "trace_id": "tr-77"},
	})

	if _, ok := inbox.lastMeta["surface"]; ok {
		t.Error("a surface-less input prompt must not set a surface key")
	}
	if inbox.lastMeta["task_id"] != "t-77" || inbox.lastMeta["trace_id"] != "tr-77" {
		t.Errorf("caller metadata must be preserved alongside injected keys, got %v", inbox.lastMeta)
	}
	if inbox.lastMeta["request_id"] != "r7" || inbox.lastMeta["mode"] != a2a.NotifyModeInput {
		t.Errorf("injected request_id/mode missing, got %v", inbox.lastMeta)
	}
}

func TestDispatcher_ChannelFilter(t *testing.T) {
	dir := newConfigDirectory(map[string][]Channel{
		"trent": {
			{Type: "webhook", Config: map[string]any{"url": "https://example.com/hook"}},
			{Type: "email", Config: map[string]any{}},
		},
	})
	d := NewDispatcher(dir, nil, slog.Default())
	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "r7", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
		Channels: []string{"email"}, Message: "x",
	})
	if len(recs) != 1 || recs[0].ChannelType != "email" || recs[0].Status != "skipped" {
		t.Fatalf("expected one skipped email record, got %+v", recs)
	}
}
