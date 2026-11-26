// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	goruntime "runtime"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/delivery"
)

const eventWorkerQueueCap = 512

const (
	minEventWorkers = 16
	maxEventWorkers = 32
)

const maxExplicitEventWorkers = 64

func resolveEventWorkers(configured int, logger *slog.Logger) int {
	if configured > 0 {
		if configured > maxExplicitEventWorkers {
			if logger != nil {
				logger.Warn("event_workers exceeds maximum; clamping",
					"configured", configured,
					"maxEventWorkers", maxExplicitEventWorkers,
				)
			}
			return maxExplicitEventWorkers
		}
		return configured
	}
	n := goruntime.GOMAXPROCS(0)
	return max(min(n, maxEventWorkers), minEventWorkers)
}

func hashTaskID(s string) uint64 {
	const (
		offset = uint64(1469598103934665603)
		prime  = uint64(1099511628211)
	)
	h := offset
	for i := range len(s) {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func eventRoutingKey(msg *broker.Message) string {
	if id := extractTaskIDFromTopic(msg.Topic); id != "" {
		return id
	}
	return msg.Topic
}

func (g *GatewayCore) eventDispatchLoop(ctx context.Context) {
	broker.RunReceiveLoop(ctx, g.svc.Broker(), g.eventQueue, g.logger, broker.ReceiveLoopOptions{
		Health:   &g.health,
		Teardown: g.teardown.Load,
		LoopName: "event",
	}, func(msg *broker.Message) {
		idx := hashTaskID(eventRoutingKey(msg)) % uint64(len(g.eventWorkers))
		select {
		case g.eventWorkers[idx] <- msg:
		case <-ctx.Done():
		}
	})
}

func (g *GatewayCore) eventWorkerLoop(ctx context.Context, ch <-chan *broker.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			if !g.loadTaskState(ctx, msg) {
				return
			}
			mctx, d := delivery.Attach(ctx, msg.ReplicationGroupMessageID, msg.Redelivered, msg.Ack)
			switch {
			case strings.Contains(msg.Topic, "/response/"):
				g.handleResponse(mctx, msg)
			case a2a.IsGatewayLogTopic(msg.Topic):
				g.handleLogRecord(mctx, msg)
			default:
				g.handleStatusEvent(mctx, msg)
			}
			d.Release()
		}
	}
}
