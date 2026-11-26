// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const heldPollInterval = time.Second

type heldEvents struct {
	store  ingressStore
	logger *slog.Logger
	now    func() time.Time

	mu     sync.Mutex
	events map[string]*heldEvent
}

type heldEvent struct {
	msg      *broker.Message
	deadline time.Time
	policy   AckFailurePolicy
}

func newHeldEvents(s ingressStore, logger *slog.Logger) *heldEvents {
	return &heldEvents{store: s, logger: logger, now: time.Now, events: map[string]*heldEvent{}}
}

func (h *heldEvents) hold(messageID string, msg *broker.Message, timeout time.Duration, policy AckFailurePolicy) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events[messageID] = &heldEvent{msg: msg, deadline: h.now().Add(timeout), policy: policy}
}

func (h *heldEvents) run(ctx context.Context) {
	ticker := time.NewTicker(heldPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.poll(ctx)
		}
	}
}

func (h *heldEvents) poll(ctx context.Context) {
	h.mu.Lock()
	ids := slices.Collect(maps.Keys(h.events))
	h.mu.Unlock()
	if len(ids) == 0 {
		return
	}
	outcomes, err := h.store.outcomes(ctx, ids)
	if err != nil {
		h.logger.Warn("outcomes of held events not read; holding them", "count", len(ids), "error", err)
		return
	}
	now := h.now()
	for _, id := range ids {
		h.mu.Lock()
		e, ok := h.events[id]
		h.mu.Unlock()
		if !ok {
			continue
		}
		outcome, recorded := outcomes[id]
		if !recorded && now.Before(e.deadline) {
			continue
		}
		if !recorded {
			h.logger.Warn("held event timed out; applying its failure policy", "messageID", id, "action", e.policy.Action)
			outcome = store.InboundOutcomeReject
		}
		if h.settle(ctx, id, e, outcome) {
			h.mu.Lock()
			if h.events[id] == e {
				delete(h.events, id)
			}
			h.mu.Unlock()
		}
	}
}

func (h *heldEvents) apply(ctx context.Context, messageID, outcome string) {
	h.mu.Lock()
	e, ok := h.events[messageID]
	h.mu.Unlock()
	if !ok || !h.settle(ctx, messageID, e, outcome) {
		return
	}
	h.mu.Lock()
	if h.events[messageID] == e {
		delete(h.events, messageID)
	}
	h.mu.Unlock()
}

func (h *heldEvents) settle(ctx context.Context, messageID string, e *heldEvent, outcome string) bool {
	if outcome == store.InboundOutcomeAck || e.policy.Action == "ack" {
		e.msg.Ack()
		h.store.release(messageID)
		return true
	}
	if err := h.store.forget(ctx, messageID); err != nil {
		h.logger.Warn("rejected event not reset; holding it", "messageID", messageID, "error", err)
		return false
	}
	e.msg.Nack()
	return true
}

func (h *heldEvents) drainAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, e := range h.events {
		e.msg.Nack()
		h.logger.Debug("held event released on stop", "messageID", id)
	}
	h.events = map[string]*heldEvent{}
}

func (h *heldEvents) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events)
}

func settleFailure(msg *broker.Message, policy *AckFailurePolicy) {
	if policy.Action == "ack" {
		msg.Ack()
		return
	}
	msg.Nack()
}
