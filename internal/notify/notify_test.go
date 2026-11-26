// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestNotifyCore_EndToEnd(t *testing.T) {
	ctx := t.Context()

	brk := broker.NewDevBroker()
	if err := brk.Connect(ctx); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	defer func() { _ = brk.Disconnect(ctx) }()

	const namespace = "test-ns"
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))

	appConfig := map[string]any{
		"namespace":    namespace,
		"database_url": "sqlite://" + t.TempDir() + "/notify.db",
	}
	core := NewNotifyCore(appConfig, NotifyExtensions{})
	core.SetServices(rt.Services())
	if err := core.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := core.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = core.Stop(ctx) }()

	req := a2a.NotifyRequest{
		RequestID:       "e2e-1",
		RecipientUserID: "trent@example.org",
		Mode:            a2a.NotifyModeNotify,
		Title:           "E2E",
		Message:         "end to end body",
	}
	data, _ := json.Marshal(req)
	if err := brk.PublishGuaranteed(ctx, a2a.NotifyRequestTopic(namespace, req.RecipientUserID), &broker.Message{Payload: data}); err != nil {
		t.Fatalf("publish NotifyRequest: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		rows, err := core.inbox.ListByUser(ctx, "trent@example.org", store.InboxFilter{}, 10, 0)
		if err != nil {
			t.Fatalf("ListByUser: %v", err)
		}
		if len(rows) == 1 && rows[0].Body == "end to end body" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("notification did not reach the inbox in time; rows=%+v", rows)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
