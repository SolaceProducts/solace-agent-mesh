// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type Idler struct {
	Provider   IdleCapableProvider
	Queue      *MessageQueue
	FetchLimit int
	Logger     *slog.Logger
	Metrics    runtime.Metrics

	Claim func([]RawMessage) []RawMessage

	OnAcked func(uid ProviderUID)

	MaxConsecutiveIdleFailures int

	BackoffBase time.Duration

	BackoffMax time.Duration

	KeepaliveInterval time.Duration

	ackMu       sync.Mutex
	pendingAcks []ProviderUID
	drained     bool

	ackWake chan struct{}

	refetchWake chan struct{}
}

var errIdleDowngrade = errors.New("email: idle downgraded to poll after consecutive failures")

const (
	eventFetchFailed    = "email.fetch_failed"
	eventIdleStopFailed = "email.idle_stop_failed"
)

func (i *Idler) Ack(ctx context.Context, uid ProviderUID) {
	i.ackMu.Lock()
	if i.drained {
		i.ackMu.Unlock()
		if ctx.Err() == nil {
			_ = i.Provider.MarkProcessed(ctx, uid)
		}
		if i.OnAcked != nil {
			i.OnAcked(uid)
		}
		return
	}
	i.pendingAcks = append(i.pendingAcks, uid)
	wake := i.ackWake
	i.ackMu.Unlock()

	if wake != nil {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

func (i *Idler) RequestRefetch() {
	i.ackMu.Lock()
	if i.refetchWake == nil {
		i.refetchWake = make(chan struct{}, 1)
	}
	wake := i.refetchWake
	i.ackMu.Unlock()

	select {
	case wake <- struct{}{}:
	default:
	}
}

func (i *Idler) drainPendingAcks(ctx context.Context) int {
	i.ackMu.Lock()
	pending := i.pendingAcks
	i.pendingAcks = nil
	i.ackMu.Unlock()

	var requeue []ProviderUID
	for _, uid := range pending {
		err := i.markProcessedCancellable(ctx, uid)
		if err != nil {
			requeue = append(requeue, uid)
			continue
		}
		if i.OnAcked != nil {
			i.OnAcked(uid)
		}
	}
	if len(requeue) > 0 {
		i.ackMu.Lock()
		i.pendingAcks = append(requeue, i.pendingAcks...)
		i.ackMu.Unlock()
		if i.Logger != nil {
			i.Logger.LogAttrs(ctx, slog.LevelWarn, "ack drain: STORE failed for some UIDs; will retry on next drain",
				slog.String("eventType", "email.idle_ack_retry_pending"),
				slog.Int("retryCount", len(requeue)),
			)
		}
	}
	return len(pending)
}

func (i *Idler) markProcessedCancellable(ctx context.Context, uid ProviderUID) error {
	done := make(chan error, 1)
	go func() {
		done <- i.Provider.MarkProcessed(ctx, uid)
	}()
	select {
	case err := <-done:
		if err != nil && i.Logger != nil {
			i.Logger.LogAttrs(ctx, slog.LevelWarn, "mark-processed failed (idler drain)",
				slog.String("eventType", "email.ack_failed"),
				slog.String("errorClass", "transient_infra"),
				slog.String("msgUID", string(uid)),
				slog.String("error", sanitizeForLog(err.Error())),
			)
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (i *Idler) hasPendingAcks() bool {
	i.ackMu.Lock()
	defer i.ackMu.Unlock()
	return len(i.pendingAcks) > 0
}

func (i *Idler) drainAndSeal(ctx context.Context) {
	i.ackMu.Lock()
	pending := i.pendingAcks
	i.pendingAcks = nil
	i.drained = true
	i.ackMu.Unlock()

	if ctx.Err() != nil {
		for _, uid := range pending {
			if i.OnAcked != nil {
				i.OnAcked(uid)
			}
		}
		return
	}

	for _, uid := range pending {
		_ = i.Provider.MarkProcessed(ctx, uid)
		if i.OnAcked != nil {
			i.OnAcked(uid)
		}
	}
}

func (i *Idler) ensureConnected(ctx context.Context, logger *slog.Logger, bo *backoff.Exponential, firstSinceConnect *bool) (cont, exit bool) {
	if i.Provider.IsConnected() {
		return false, false
	}
	logger.LogAttrs(ctx, slog.LevelInfo, "connecting",
		slog.String("eventType", "email.connect_attempt"),
	)
	if err := i.Provider.Connect(ctx); err != nil {
		wait := bo.Next()
		logger.LogAttrs(ctx, slog.LevelWarn, "connect failed",
			slog.String("eventType", "email.connect_failed"),
			slog.String("errorClass", "transient_infra"),
			slog.Float64("retry_in_seconds", wait.Seconds()),
			slog.String("error", sanitizeForLog(err.Error())),
		)
		if !sleepWithCancel(ctx, wait) {
			return false, true
		}
		return true, false
	}
	bo.Reset()
	*firstSinceConnect = true
	logger.LogAttrs(ctx, slog.LevelInfo, "connected",
		slog.String("eventType", "email.connect_succeeded"),
	)
	return false, false
}

func (i *Idler) reconcileAfterConnect(ctx context.Context, logger *slog.Logger, bo *backoff.Exponential) (cont, exit bool) {
	if err := i.fetchAndEnqueue(ctx); err != nil {
		wait := bo.Next()
		logger.LogAttrs(ctx, slog.LevelWarn, "fetch failed",
			slog.String("eventType", eventFetchFailed),
			slog.String("errorClass", "transient_infra"),
			slog.Float64("retry_in_seconds", wait.Seconds()),
			slog.String("error", sanitizeForLog(err.Error())),
		)
		_ = i.Provider.Disconnect(ctx)
		if !sleepWithCancel(ctx, wait) {
			return false, true
		}
		return true, false
	}
	if i.hasPendingAcks() {
		n := i.drainPendingAcks(ctx)
		logger.LogAttrs(ctx, slog.LevelDebug, "drained pending acks after reconnect",
			slog.String("eventType", "email.idle_acks_drained_post_reconnect"),
			slog.Int("count", n),
		)
	}
	return false, false
}

func (i *Idler) Run(ctx context.Context) error {
	if i.MaxConsecutiveIdleFailures <= 0 {
		i.MaxConsecutiveIdleFailures = 3
	}
	if i.Claim == nil {
		i.Claim = func(msgs []RawMessage) []RawMessage { return msgs }
	}
	i.ackMu.Lock()
	if i.ackWake == nil {
		i.ackWake = make(chan struct{}, 1)
	}
	if i.refetchWake == nil {
		i.refetchWake = make(chan struct{}, 1)
	}
	wake := i.ackWake
	refetch := i.refetchWake
	i.ackMu.Unlock()

	defer i.drainAndSeal(ctx)
	base := i.BackoffBase
	if base <= 0 {
		base = time.Second
	}
	maxBackoff := i.BackoffMax
	if maxBackoff <= 0 {
		maxBackoff = 30 * time.Second
	}
	keepalive := i.KeepaliveInterval
	if keepalive <= 0 {
		keepalive = 9 * time.Minute
	}
	logger := i.Logger.With("logger_name", "email-idler")
	bo := newBackoff(base, maxBackoff)
	consecutive := 0
	firstSinceConnect := true

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		if cont, exit := i.ensureConnected(ctx, logger, bo, &firstSinceConnect); exit {
			return nil
		} else if cont {
			continue
		}

		if firstSinceConnect {
			cont, exit := i.reconcileAfterConnect(ctx, logger, bo)
			if exit {
				return nil
			}
			if cont {
				continue
			}
			firstSinceConnect = false
		}

		events, sessionDone, err := i.Provider.Idle(ctx)
		if err != nil {
			consecutive++
			logger.LogAttrs(ctx, slog.LevelWarn, "idle start failed",
				slog.String("eventType", "email.idle_start_failed"),
				slog.String("errorClass", "transient_infra"),
				slog.Int("consecutive", consecutive),
				slog.String("error", sanitizeForLog(err.Error())),
			)
			if consecutive >= i.MaxConsecutiveIdleFailures {
				logger.LogAttrs(ctx, slog.LevelWarn, "idle downgraded to poll",
					slog.String("eventType", "email.idle_downgraded_to_poll"),
					slog.Int("consecutiveFailures", consecutive),
				)
				return errIdleDowngrade
			}
			if !sleepWithCancel(ctx, bo.Next()) {
				return nil
			}
			continue
		}
		consecutive = 0
		logger.LogAttrs(ctx, slog.LevelDebug, "idle started",
			slog.String("eventType", "email.idle_started"),
		)

		keepaliveTimer := time.NewTimer(keepalive)

		select {
		case <-ctx.Done():
			keepaliveTimer.Stop()
			return nil
		case <-sessionDone:
			keepaliveTimer.Stop()
			continue
		case <-wake:
			keepaliveTimer.Stop()
			if err := i.Provider.StopIdle(ctx); err != nil {
				logger.LogAttrs(ctx, slog.LevelWarn, "stop idle failed (ack drain)",
					slog.String("eventType", eventIdleStopFailed),
					slog.String("errorClass", "transient_infra"),
					slog.String("error", sanitizeForLog(err.Error())),
				)
				continue
			}
			n := i.drainPendingAcks(ctx)
			logger.LogAttrs(ctx, slog.LevelDebug, "drained pending acks",
				slog.String("eventType", "email.idle_acks_drained"),
				slog.Int("count", n),
			)
			continue
		case <-refetch:
			keepaliveTimer.Stop()
			if err := i.Provider.StopIdle(ctx); err != nil {
				logger.LogAttrs(ctx, slog.LevelWarn, "stop idle failed (refetch)",
					slog.String("eventType", eventIdleStopFailed),
					slog.String("errorClass", "transient_infra"),
					slog.String("error", sanitizeForLog(err.Error())),
				)
				continue
			}
			i.drainPendingAcks(ctx)
			if err := i.fetchAndEnqueue(ctx); err != nil {
				logger.LogAttrs(ctx, slog.LevelWarn, "refetch hint failed",
					slog.String("eventType", eventFetchFailed),
					slog.String("errorClass", "transient_infra"),
					slog.String("error", sanitizeForLog(err.Error())),
				)
				_ = i.Provider.Disconnect(ctx)
			}
			continue
		case ev := <-events:
			keepaliveTimer.Stop()
			if err := i.Provider.StopIdle(ctx); err != nil {
				logger.LogAttrs(ctx, slog.LevelWarn, "stop idle failed",
					slog.String("eventType", eventIdleStopFailed),
					slog.String("errorClass", "transient_infra"),
					slog.String("error", sanitizeForLog(err.Error())),
				)
				continue
			}
			i.drainPendingAcks(ctx)
			if ev.Kind == IdleEventExists {
				if err := i.fetchAndEnqueue(ctx); err != nil {
					logger.LogAttrs(ctx, slog.LevelWarn, "fetch after EXISTS failed",
						slog.String("eventType", eventFetchFailed),
						slog.String("errorClass", "transient_infra"),
						slog.String("error", sanitizeForLog(err.Error())),
					)
					_ = i.Provider.Disconnect(ctx)
				}
			}
		case <-keepaliveTimer.C:
			if i.cycleIdleForKeepalive(ctx, logger) {
				i.drainPendingAcks(ctx)
			}
		}
	}
}

func (i *Idler) cycleIdleForKeepalive(ctx context.Context, logger *slog.Logger) bool {
	if err := i.Provider.StopIdle(ctx); err != nil {
		logger.LogAttrs(ctx, slog.LevelInfo, "keepalive stop idle failed",
			slog.String("eventType", "email.idle_keepalive_stop_failed"),
			slog.String("errorClass", "transient_infra"),
			slog.String("error", sanitizeForLog(err.Error())),
		)
		return false
	}
	logger.LogAttrs(ctx, slog.LevelDebug, "keepalive cycled idle",
		slog.String("eventType", "email.idle_keepalive_cycled"),
	)
	return true
}

func (i *Idler) fetchAndEnqueue(ctx context.Context) error {
	msgs, err := i.Provider.FetchNew(ctx, i.FetchLimit)
	if err != nil {
		return err
	}
	for _, m := range i.Claim(msgs) {
		i.Queue.Enqueue(ctx, m)
	}
	return nil
}
