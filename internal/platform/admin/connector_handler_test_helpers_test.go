// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func (h *connectorRedeployer) writeConnectorRedeployEvent(ctx context.Context, eventType, connectorID, userID string) {
	agentIDs := h.findAgentsByConnector(ctx, connectorID)
	h.writeRedeployEvent(ctx, eventType, connectorID, userID, agentIDs)
}

func (h *connectorRedeployer) writeRedeployEvent(ctx context.Context, eventType, connectorID, userID string, agentIDs []string) {
	if h.outboxRepo == nil || len(agentIDs) == 0 {
		return
	}
	payload, _ := json.Marshal(redeployPayload{AgentIDs: agentIDs, UserID: userID})
	if err := h.outboxRepo.Create(ctx, &store.OutboxEvent{
		EventType:   eventType,
		AggregateID: connectorID,
		Payload:     string(payload),
	}); err != nil {
		h.logger.Error("failed to write outbox event", "event_type", eventType, "error", err)
	}
}
