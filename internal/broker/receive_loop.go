// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package broker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/health"
)

type ReceiveLoopOptions struct {
	Health *health.State

	Teardown func() bool

	Resubscribes bool

	LoopName string

	LogFields []any

	PollInterval time.Duration
}

type QueueReceiver interface {
	Receive(ctx context.Context, queue Queue) (*Message, error)
}

const defaultReceivePollInterval = 5 * time.Second

const msgReceiveError = "receive error"

func RunReceiveLoop(ctx context.Context, brk QueueReceiver, q Queue, logger *slog.Logger, opts ReceiveLoopOptions, handle func(*Message)) {
	bo := backoff.Exponential{Initial: 100 * time.Millisecond, Max: 30 * time.Second, Jitter: 0.2}
	key := "queue:" + q.Name()
	states := opts.Health
	if states == nil {
		states = &health.State{}
	}
	pollInterval := opts.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultReceivePollInterval
	}

	fields := func(extra ...any) []any {
		base := []any{"queue", q.Name()}
		if opts.LoopName != "" {
			base = append(base, "loopName", opts.LoopName)
		}
		return append(append(base, extra...), opts.LogFields...)
	}

	for {
		recvCtx, cancel := context.WithTimeout(ctx, pollInterval)
		msg, err := brk.Receive(recvCtx, q)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				states.Clear(key)
				return
			}
			if errors.Is(err, context.DeadlineExceeded) {
				states.Clear(key)
				bo.Reset()
				continue
			}
			if errors.Is(err, ErrConsumeQueueUnavailable) {
				states.Set(key, health.Degraded, health.DetailConsumeQueueUnavailable)
				logger.Debug(msgReceiveError, fields("error", err, "backoff", bo.Current())...)
				if waitErr := bo.Wait(ctx); waitErr != nil {
					states.Clear(key)
					return
				}
				continue
			}
			if errors.Is(err, ErrQueueNotFound) {
				switch {
				case opts.Teardown != nil && opts.Teardown():
					states.Clear(key)
					logger.Info("receive loop exiting: queue removed for shutdown", fields()...)
				case opts.Resubscribes:
					states.Set(key, health.Degraded, health.DetailTemporaryReceive)
					logger.Warn("receive loop exiting: queue no longer exists, resubscribing", fields()...)
				default:
					states.Set(key, health.Fatal, health.DetailQueueGone)
					logger.Error("receive loop exiting: queue no longer exists", fields()...)
				}
				return
			}
			if errors.Is(err, ErrProvisionCooldown) {
				states.Set(key, health.Degraded, health.DetailProvisionCooldown)
				logger.Debug(msgReceiveError, fields("error", err, "backoff", bo.Current())...)
			} else {
				states.Set(key, health.Degraded, health.DetailTemporaryReceive)
				logger.Warn(msgReceiveError, fields("error", err, "backoff", bo.Current())...)
			}
			if waitErr := bo.Wait(ctx); waitErr != nil {
				states.Clear(key)
				return
			}
			continue
		}
		states.Clear(key)
		bo.Reset()
		if msg == nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		handle(msg)
		if ctx.Err() != nil {
			states.Clear(key)
			return
		}
	}
}
