// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type LogRecordHandler interface {
	OnLogRecord(ctx context.Context, taskID string, payload []byte)
}

func (g *GatewayCore) PublishLogRecord(ctx context.Context, taskID string, payload []byte) error {
	topic := a2a.GatewayLogTopic(g.cfg.Namespace, g.cfg.GatewayID, taskID)
	if err := g.svc.Broker().PublishGuaranteed(ctx, topic, &broker.Message{Payload: payload}); err != nil {
		return fmt.Errorf("publish log record: %w", err)
	}
	return nil
}

func (g *GatewayCore) handleLogRecord(ctx context.Context, msg *broker.Message) {
	taskID := extractTaskIDFromTopic(msg.Topic)
	h, ok := g.adapter.(LogRecordHandler)
	if !ok {
		g.warnTask(taskID, "log record for an adapter with no event log; dropping")
		return
	}
	h.OnLogRecord(ctx, taskID, msg.Payload)
}
