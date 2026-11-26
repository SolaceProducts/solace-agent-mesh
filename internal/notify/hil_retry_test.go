// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func TestInputRequestThatCannotBeStoredIsReturnedToTheQueue(t *testing.T) {
	core := newTestCore(t, true, nil)
	if _, err := core.db.ExecContext(t.Context(), `DROP TABLE notify_pending_requests`); err != nil {
		t.Fatalf("break the pending store: %v", err)
	}
	payload, err := json.Marshal(a2a.NotifyRequest{
		RequestID:       "req-unstored",
		RecipientUserID: "alice",
		Mode:            a2a.NotifyModeInput,
		RequesterAddr:   a2a.NotifyResponseTopic("test-ns", "wf", "req-unstored"),
		Title:           "Your input is requested",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var acks, nacks atomic.Int32
	msg := broker.NewDeliveredMessage("m", a2a.NotifyRequestTopic("test-ns", "alice"), payload,
		func() { acks.Add(1) }, func() { nacks.Add(1) })

	core.handleMessage(t.Context(), msg)

	if acks.Load() != 0 || nacks.Load() != 1 {
		t.Errorf("acks=%d nacks=%d, want the request returned to the queue once", acks.Load(), nacks.Load())
	}
}

func TestInputRequestStoredAfterAShortOutageIsAcknowledged(t *testing.T) {
	core := newTestCore(t, true, nil)
	if _, err := core.db.ExecContext(t.Context(), `ALTER TABLE notify_pending_requests RENAME TO notify_pending_requests_hidden`); err != nil {
		t.Fatalf("hide the pending store: %v", err)
	}
	time.AfterFunc(150*time.Millisecond, func() {
		_, _ = core.db.ExecContext(context.Background(), `ALTER TABLE notify_pending_requests_hidden RENAME TO notify_pending_requests`)
	})
	payload, err := json.Marshal(a2a.NotifyRequest{
		RequestID:       "req-outage",
		RecipientUserID: "alice",
		Mode:            a2a.NotifyModeInput,
		RequesterAddr:   a2a.NotifyResponseTopic("test-ns", "wf", "req-outage"),
		Title:           "Your input is requested",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var acks, nacks atomic.Int32
	msg := broker.NewDeliveredMessage("m", a2a.NotifyRequestTopic("test-ns", "alice"), payload,
		func() { acks.Add(1) }, func() { nacks.Add(1) })

	core.handleMessage(t.Context(), msg)

	if acks.Load() != 1 || nacks.Load() != 0 {
		t.Errorf("acks=%d nacks=%d, want the request stored and acknowledged once", acks.Load(), nacks.Load())
	}
	if pr, err := core.pending.Get(t.Context(), "req-outage"); err != nil || pr == nil {
		t.Errorf("pending request = %v (%v), want it stored", pr, err)
	}
}
