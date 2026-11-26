// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

func inboundIDFor(taskID string) string { return "in-" + taskID }

func (a *EventMeshAdapter) seedPending(t *testing.T, taskID string, pt *pendingTask) {
	t.Helper()
	a.ensureIngress()
	h := pt.handlerCfg
	if h.Name == "" {
		h.Name = "handler-" + taskID
	}
	if a.findHandler(h.Name) == nil {
		a.cfg.EventHandlers = append(a.cfg.EventHandlers, h)
	}
	inbound := ""
	if pt.deferredACK {
		inbound = inboundIDFor(taskID)
		if _, err := a.ingress.store(t.Context(), inbound, taskID, "{}"); err != nil {
			t.Fatalf("store inbound: %v", err)
		}
		if err := a.ingress.markSubmitted(t.Context(), inbound, taskID, ""); err != nil {
			t.Fatalf("mark inbound: %v", err)
		}
	}
	if err := a.ingress.putRouting(t.Context(), taskID, taskRouting{
		HandlerName:           h.Name,
		ForwardContext:        pt.forwardContext,
		DeferredACK:           pt.deferredACK,
		UserID:                pt.userID,
		SentStructuredRequest: pt.sentStructuredRequest,
		InboundID:             inbound,
	}); err != nil {
		t.Fatalf("put routing: %v", err)
	}
}

func (a *EventMeshAdapter) pendingExists(t *testing.T, taskID string) bool {
	t.Helper()
	_, found, _ := a.ingress.routing(t.Context(), taskID)
	return found
}

func (a *EventMeshAdapter) holdFor(t *testing.T, taskID string, msg *broker.Message, policy AckFailurePolicy) {
	t.Helper()
	a.ensureIngress()
	a.held.hold(inboundIDFor(taskID), msg, time.Hour, policy)
}

func (a *EventMeshAdapter) heldCount(t *testing.T) int {
	t.Helper()
	a.held.poll(t.Context())
	return a.held.count()
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testMsg(acked, nacked *atomic.Bool) *broker.Message {
	return broker.NewDeliveredMessage("id", "topic", nil,
		func() { acked.Store(true) },
		func() { nacked.Store(true) },
	)
}
