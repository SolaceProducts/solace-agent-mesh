// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type fakeConnector struct {
	calls []struct {
		toolName, strBinary, userID string
		args                        map[string]any
	}
	err error
}

func (f *fakeConnector) Invoke(_ context.Context, toolName, strBinary string, args map[string]any, userID, _ string) (map[string]any, error) {
	f.calls = append(f.calls, struct {
		toolName, strBinary, userID string
		args                        map[string]any
	}{toolName, strBinary, userID, args})
	if f.err != nil {
		return nil, f.err
	}
	return map[string]any{"success": true}, nil
}

func connectorDir() Directory {
	return newConfigDirectory(map[string][]Channel{
		"trent": {{Type: "connector", Config: map[string]any{
			"tool_name":  "slack_send_message_990e8400",
			"str_binary": "slack",
			"args":       map[string]any{"channel": "C0123"},
		}}},
	})
}

func TestDispatcher_ConnectorChannel(t *testing.T) {
	fc := &fakeConnector{}
	d := NewDispatcher(connectorDir(), nil, slog.Default())
	d.SetConnectorInvoker(fc)

	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "c1", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
		Channels: []string{"connector"}, SenderUserID: "boss@example.org",
		Title: "Report ready", Message: "done",
	})

	if len(recs) != 1 || recs[0].ChannelType != "connector" || recs[0].Status != "sent" {
		t.Fatalf("expected one sent connector record, got %+v", recs)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("expected one invoke, got %d", len(fc.calls))
	}
	c := fc.calls[0]
	if c.toolName != "slack_send_message_990e8400" || c.strBinary != "slack" {
		t.Errorf("unexpected tool addressing: %+v", c)
	}
	if c.args["channel"] != "C0123" {
		t.Errorf("static arg not preserved: %v", c.args["channel"])
	}
	if c.args["text"] != "Report ready\n\ndone" {
		t.Errorf("composed text = %q", c.args["text"])
	}
	if c.userID != "boss@example.org" {
		t.Errorf("userID = %q, want sender", c.userID)
	}
}

func TestDispatcher_ConnectorUnavailableSkipped(t *testing.T) {
	d := NewDispatcher(connectorDir(), nil, slog.Default())
	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "c2", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
		Channels: []string{"connector"}, Message: "x",
	})
	if len(recs) != 1 || recs[0].ChannelType != "connector" || recs[0].Status != "skipped" {
		t.Fatalf("expected one skipped connector record, got %+v", recs)
	}
}

func TestDispatcher_ConnectorError(t *testing.T) {
	fc := &fakeConnector{err: errors.New("str unavailable")}
	d := NewDispatcher(connectorDir(), nil, slog.Default())
	d.SetConnectorInvoker(fc)
	recs := d.Deliver(t.Context(), a2a.NotifyRequest{
		RequestID: "c3", RecipientUserID: "trent", Mode: a2a.NotifyModeNotify,
		Channels: []string{"connector"}, Message: "x",
	})
	if len(recs) != 1 || recs[0].Status != "failed" || recs[0].Error == "" {
		t.Fatalf("expected one failed connector record, got %+v", recs)
	}
}

func TestInterpretConnectorResult(t *testing.T) {
	if err := interpretConnectorResult(nil); err != nil {
		t.Errorf("nil result: %v", err)
	}
	if err := interpretConnectorResult(map[string]any{"success": true}); err != nil {
		t.Errorf("success:true: %v", err)
	}
	if err := interpretConnectorResult(map[string]any{}); err != nil {
		t.Errorf("no success key (assume ok): %v", err)
	}
	if err := interpretConnectorResult(map[string]any{"success": false, "error": "channel_not_found"}); err == nil {
		t.Error("success:false should be a failure")
	}
	if err := interpretConnectorResult(map[string]any{"error": "boom"}); err == nil {
		t.Error("non-empty error field should be a failure")
	}
}

func TestParseConnectorChannel_Validation(t *testing.T) {
	if _, err := parseConnectorChannel(map[string]any{"str_binary": "slack"}); err == nil {
		t.Error("expected error when tool_name is missing")
	}
	if _, err := parseConnectorChannel(map[string]any{"tool_name": "x"}); err == nil {
		t.Error("expected error when str_binary is missing")
	}
	cc, err := parseConnectorChannel(map[string]any{"tool_name": "x", "str_binary": "slack", "text_arg": "message"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cc.textArg != "message" {
		t.Errorf("text_arg = %q, want message", cc.textArg)
	}
}
