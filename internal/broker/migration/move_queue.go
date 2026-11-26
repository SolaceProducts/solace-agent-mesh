// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const defaultIdle = 2 * time.Second

type MovedQueue struct {
	Queue  string
	Topics []string
	Idle   time.Duration
}

type Handler func(ctx context.Context, msg *broker.Message)

func MoveQueue(ctx context.Context, b broker.Broker, m MovedQueue, handle Handler, logger *slog.Logger) (again bool, err error) {
	logger = logger.With("queue", m.Queue, "topics", m.Topics)

	q, done, err := bindExisting(b, m.Queue, "move", logger)
	if done || err != nil {
		return false, err
	}
	unsubscribed, err := removeSubscriptions(q, m.Queue, m.Topics)
	if err != nil {
		return true, err
	}
	drained, err := Drain(ctx, q, m.Idle, handle)
	q.Close()
	if err != nil {
		return true, fmt.Errorf("drain queue %q after %d messages: %w", m.Queue, drained, err)
	}
	deleted, err := deleteQueue(b, m.Queue, "move", unsubscribed, logger)
	if deleted {
		logger.Info("queue moved", "drained", drained)
	}
	return deleted || err != nil, err
}

func Drain(ctx context.Context, q broker.BoundQueue, idle time.Duration, handle Handler) (int, error) {
	if idle <= 0 {
		idle = defaultIdle
	}
	drained := 0
	for {
		recvCtx, cancel := context.WithTimeout(ctx, idle)
		msg, err := q.Receive(recvCtx)
		cancel()
		switch {
		case err == nil:
			handle(ctx, msg)
			drained++
		case ctx.Err() != nil:
			return drained, ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			return drained, nil
		default:
			return drained, err
		}
	}
}
