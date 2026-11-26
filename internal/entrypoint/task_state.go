// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type TaskStateLoader interface {
	LoadTask(ctx context.Context, taskID string) error
}

const (
	taskStateRetryMin = 500 * time.Millisecond
	taskStateRetryMax = 30 * time.Second
)

func isGatewaySubmittableTaskID(taskID string) bool {
	_, err := uuid.Parse(taskID)
	return err == nil
}

func (g *GatewayCore) loadTaskState(ctx context.Context, msg *broker.Message) bool {
	loader, ok := g.adapter.(TaskStateLoader)
	if !ok {
		return true
	}
	taskID := extractTaskIDFromTopic(msg.Topic)
	if !isGatewaySubmittableTaskID(taskID) {
		return true
	}
	wait := taskStateRetryMin
	for attempt := 1; ; attempt++ {
		err := loader.LoadTask(ctx, taskID)
		if err == nil {
			return true
		}
		g.logger.Warn("task state could not be loaded; holding the task's events until it can",
			"taskID", taskID, "attempt", attempt, "retryIn", wait, "error", err)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
		wait = min(2*wait, taskStateRetryMax)
	}
}
