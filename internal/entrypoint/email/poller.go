// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type Poller struct {
	Provider        EmailProvider
	Queue           *MessageQueue
	IntervalSeconds int
	FetchLimit      int
	Logger          *slog.Logger

	Metrics runtime.Metrics

	Claim func([]RawMessage) []RawMessage

	OnAcked func(uid ProviderUID)
}

func (p *Poller) Ack(ctx context.Context, uid ProviderUID) {
	if err := p.Provider.MarkProcessed(ctx, uid); err != nil {
		if p.Logger != nil {
			p.Logger.LogAttrs(ctx, slog.LevelWarn, "mark-processed failed",
				slog.String("eventType", "email.ack_failed"),
				slog.String("errorClass", "transient_infra"),
				slog.String("msgUID", string(uid)),
				slog.String("error", sanitizeForLog(err.Error())),
			)
		}
	}
	if p.OnAcked != nil {
		p.OnAcked(uid)
	}
}

func (p *Poller) Run(ctx context.Context) error {
	if p.Claim == nil {
		p.Claim = func(msgs []RawMessage) []RawMessage { return msgs }
	}
	logger := p.Logger.With("logger_name", "email-poller")
	metrics := runtime.MetricsOrNoop(p.Metrics)
	bo := newBackoff(time.Second, 30*time.Second)

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		if !p.Provider.IsConnected() {
			logger.LogAttrs(ctx, slog.LevelInfo, "connecting",
				slog.String("eventType", "email.connect_attempt"),
			)
			if err := p.Provider.Connect(ctx); err != nil {
				wait := bo.Next()
				logger.LogAttrs(ctx, slog.LevelWarn, "connect failed",
					slog.String("eventType", "email.connect_failed"),
					slog.String("errorClass", "transient_infra"),
					slog.Float64("retry_in_seconds", wait.Seconds()),
					slog.String("error", sanitizeForLog(err.Error())),
				)
				if !sleepWithCancel(ctx, wait) {
					return nil
				}
				continue
			}
			bo.Reset()
			logger.LogAttrs(ctx, slog.LevelInfo, "connected",
				slog.String("eventType", "email.connect_succeeded"),
			)
		}

		logger.LogAttrs(ctx, slog.LevelDebug, "poll cycle start",
			slog.String("eventType", "email.poll_cycle_started"),
		)
		if ts := p.Provider.LastSuccessfulAuthAt(); !ts.IsZero() {
			metrics.Gauge(MetricLastSuccessfulAuthSecondsAgo, time.Since(ts).Seconds())
		}
		msgs, err := p.Provider.FetchNew(ctx, p.FetchLimit)
		if err != nil {
			wait := bo.Next()
			logger.LogAttrs(ctx, slog.LevelWarn, "fetch failed",
				slog.String("eventType", "email.fetch_failed"),
				slog.String("errorClass", "transient_infra"),
				slog.Float64("retry_in_seconds", wait.Seconds()),
				slog.String("error", sanitizeForLog(err.Error())),
			)
			_ = p.Provider.Disconnect(ctx)
			if !sleepWithCancel(ctx, wait) {
				return nil
			}
			continue
		}
		toEnqueue := p.Claim(msgs)
		for _, m := range toEnqueue {
			p.Queue.Enqueue(ctx, m)
		}
		level := slog.LevelDebug
		if len(msgs) > 0 {
			level = slog.LevelInfo
		}
		logger.LogAttrs(ctx, level, "poll cycle complete",
			slog.String("eventType", "email.poll_cycle_completed"),
			slog.Int("fetched", len(msgs)),
			slog.Int("enqueued", len(toEnqueue)),
			slog.Int("skippedInFlight", len(msgs)-len(toEnqueue)),
		)

		if p.IntervalSeconds <= 0 {
			continue
		}
		if !sleepWithCancel(ctx, time.Duration(p.IntervalSeconds)*time.Second) {
			return nil
		}
	}
}

func sleepWithCancel(ctx context.Context, d time.Duration) bool {
	return backoff.Sleep(ctx, d) == nil
}

func newBackoff(base, max time.Duration) *backoff.Exponential {
	return &backoff.Exponential{Initial: base, Max: max}
}
