// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type OutboxPublisher interface {
	PublishOutboxEvent(ctx context.Context, event store.OutboxEvent) error
}

type OutboxNotifier interface {
	Notify()
}

type OutboxRoute struct {
	Prefix    string
	Publisher OutboxPublisher
}

type CompositeOutboxPublisher struct {
	routes []OutboxRoute
	logger *slog.Logger
}

func NewCompositeOutboxPublisher(routes []OutboxRoute, logger *slog.Logger) *CompositeOutboxPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	cp := make([]OutboxRoute, len(routes))
	copy(cp, routes)
	return &CompositeOutboxPublisher{routes: cp, logger: logger}
}

func (c *CompositeOutboxPublisher) PublishOutboxEvent(ctx context.Context, event store.OutboxEvent) error {
	matched := 0
	for _, route := range c.routes {
		if !strings.HasPrefix(event.EventType, route.Prefix) {
			continue
		}
		matched++
		if err := route.Publisher.PublishOutboxEvent(ctx, event); err != nil {
			return fmt.Errorf("composite outbox: route %q failed for event %s (%s): %w",
				route.Prefix, event.ID, event.EventType, err)
		}
	}
	if matched == 0 {
		c.logger.Warn("composite outbox: no route matched event, marking as published to avoid poisoning",
			"eventID", event.ID, "eventType", event.EventType)
	}
	return nil
}

var _ OutboxPublisher = (*CompositeOutboxPublisher)(nil)

var _ OutboxNotifier = (*OutboxPoller)(nil)

type OutboxPoller struct {
	repo            *store.OutboxRepo
	publisher       OutboxPublisher
	pollInterval    time.Duration
	cleanupInterval time.Duration
	cleanupAge      time.Duration
	batchSize       int
	maxRetries      int
	backoffBase     time.Duration
	backoffMax      time.Duration
	logger          *slog.Logger

	notify chan struct{}

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type OutboxPollerConfig struct {
	PollInterval    time.Duration
	CleanupInterval time.Duration
	CleanupAge      time.Duration
	BatchSize       int
	MaxRetries      int
	BackoffBase     time.Duration
	BackoffMax      time.Duration
}

func DefaultOutboxPollerConfig() OutboxPollerConfig {
	return OutboxPollerConfig{
		PollInterval:    time.Second,
		CleanupInterval: 10 * time.Minute,
		CleanupAge:      24 * time.Hour,
		BatchSize:       100,
		MaxRetries:      10,
		BackoffBase:     5 * time.Second,
		BackoffMax:      10 * time.Minute,
	}
}

func NewOutboxPoller(repo *store.OutboxRepo, publisher OutboxPublisher, cfg OutboxPollerConfig, logger *slog.Logger) *OutboxPoller {
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("logger_name", "outbox-poller")
	defaults := DefaultOutboxPollerConfig()
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = defaults.MaxRetries
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = defaults.BackoffBase
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = defaults.BackoffMax
	}
	return &OutboxPoller{
		repo:            repo,
		publisher:       publisher,
		pollInterval:    cfg.PollInterval,
		cleanupInterval: cfg.CleanupInterval,
		cleanupAge:      cfg.CleanupAge,
		batchSize:       cfg.BatchSize,
		maxRetries:      cfg.MaxRetries,
		backoffBase:     cfg.BackoffBase,
		backoffMax:      cfg.BackoffMax,
		logger:          logger,
		notify:          make(chan struct{}, 1),
	}
}

func (p *OutboxPoller) Start(ctx context.Context) error {
	ctx, p.cancel = context.WithCancel(ctx)
	p.wg.Go(func() {
		p.run(ctx)
	})
	p.logger.Info("outbox poller started", "pollInterval", p.pollInterval, "batchSize", p.batchSize)
	return nil
}

func (p *OutboxPoller) Stop(_ context.Context) error {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
	p.logger.Info("outbox poller stopped")
	return nil
}

func (p *OutboxPoller) run(ctx context.Context) {
	pollTicker := time.NewTicker(p.pollInterval)
	defer pollTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-pollTicker.C:
			p.pollOnce(ctx)
		case <-p.notify:
			p.pollOnce(ctx)
		}
	}
}

func (p *OutboxPoller) Notify() {
	if p == nil || p.notify == nil {
		return
	}
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (p *OutboxPoller) pollOnce(ctx context.Context) {
	if has, err := p.repo.HasDispatchable(ctx); err != nil {
		p.logger.Error("outbox poll: check dispatchable", "error", err)
		return
	} else if !has {
		return
	}
	if p.repo.Dialect() == "postgres" {
		p.pollOnceClaimed(ctx)
		return
	}
	p.pollOnceUnlocked(ctx)
}

func (p *OutboxPoller) pollOnceUnlocked(ctx context.Context) {
	events, err := p.repo.GetDispatchable(ctx, p.batchSize)
	if err != nil {
		p.logger.Error("outbox poll: get dispatchable", "error", err)
		return
	}
	if len(events) == 0 {
		return
	}

	published := p.dispatch(ctx, events, func(e store.OutboxEvent, derr error) {
		p.handleDispatchFailure(ctx, e, derr)
	})

	if len(published) > 0 {
		if err := p.repo.MarkPublished(ctx, published); err != nil {
			p.logger.Error("outbox poll: mark published", "error", err)
		} else {
			p.logger.Debug("outbox poll: published events", "count", len(published))
		}
	}
}

func (p *OutboxPoller) pollOnceClaimed(ctx context.Context) {
	err := p.repo.WithTx(ctx, func(tx *dbutil.Tx) error {
		events, err := p.repo.ClaimDispatchable(ctx, tx, p.batchSize)
		if err != nil {
			return fmt.Errorf("claim dispatchable: %w", err)
		}
		if len(events) == 0 {
			return nil
		}

		published := p.dispatch(ctx, events, func(e store.OutboxEvent, derr error) {
			p.recordFailure(e, derr, func(id, lastError string, retryCount int, nextAttempt int64, dead bool) error {
				return p.repo.MarkFailedTx(ctx, tx, id, lastError, retryCount, nextAttempt, dead)
			})
		})

		if len(published) > 0 {
			if err := p.repo.MarkPublishedTx(ctx, tx, published); err != nil {
				return fmt.Errorf("mark published: %w", err)
			}
			p.logger.Debug("outbox poll: published events", "count", len(published))
		}
		return nil
	})
	if err != nil {
		p.logger.Error("outbox poll: dispatch transaction", "error", err)
	}
}

func (p *OutboxPoller) dispatch(ctx context.Context, events []store.OutboxEvent, onFailure func(store.OutboxEvent, error)) []string {
	var published []string
	for _, e := range events {
		if err := p.publishRecovered(ctx, e); err != nil {
			onFailure(e, err)
			continue
		}
		published = append(published, e.ID)
	}
	return published
}

func (p *OutboxPoller) publishRecovered(ctx context.Context, e store.OutboxEvent) (err error) {
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("outbox publisher panicked; recording the event as failed",
				"eventID", e.ID, "eventType", e.EventType, "aggregateID", e.AggregateID, "panicType", fmt.Sprintf("%T", r), "stack", string(debug.Stack()))
			err = fmt.Errorf("publisher panicked (%T)", r)
		}
	}()
	return p.publisher.PublishOutboxEvent(ctx, e)
}

func (p *OutboxPoller) handleDispatchFailure(ctx context.Context, e store.OutboxEvent, dispatchErr error) {
	p.recordFailure(e, dispatchErr, func(id, lastError string, retryCount int, nextAttempt int64, dead bool) error {
		return p.repo.MarkFailed(ctx, id, lastError, retryCount, nextAttempt, dead)
	})
}

func (p *OutboxPoller) recordFailure(e store.OutboxEvent, dispatchErr error, markFailed func(id, lastError string, retryCount int, nextAttempt int64, dead bool) error) {
	retryCount := e.RetryCount + 1
	dead := retryCount > p.maxRetries
	delay := p.backoffFor(retryCount)
	nextAttempt := time.Now().Add(delay).UnixMilli()

	markFailedErr := markFailed(e.ID, dispatchErr.Error(), retryCount, nextAttempt, dead)

	logArgs := []any{"eventID", e.ID, "eventType", e.EventType, "retry_count", retryCount, "error", dispatchErr}
	if markFailedErr != nil {
		logArgs = append(logArgs, "markFailedError", markFailedErr)
	}
	if dead {
		p.logger.Error("outbox poll: event marked dead after exceeding retry budget", logArgs...)
	} else {
		p.logger.Warn("outbox poll: publish event failed, will retry", append(logArgs, "next_attempt_at_ms", nextAttempt)...)
	}
}

func (p *OutboxPoller) backoffFor(retryCount int) time.Duration {
	if retryCount <= 1 {
		return p.backoffBase
	}
	d := p.backoffBase
	for range retryCount - 1 {
		d *= 2
		if d >= p.backoffMax {
			return p.backoffMax
		}
	}
	return d
}

func (p *OutboxPoller) CleanupJob() leader.Job {
	return leader.Job{
		Name:  leader.JobPlatformOutboxCleanup,
		Every: p.cleanupInterval,
		Run: func(ctx context.Context, _ leader.Term) error {
			deleted, err := p.repo.Cleanup(ctx, time.Now().Add(-p.cleanupAge))
			if err != nil {
				return fmt.Errorf("delete old outbox events: %w", err)
			}
			if deleted > 0 {
				p.logger.Debug("outbox cleanup: deleted old events", "count", deleted)
			}
			return nil
		},
	}
}

var _ BackgroundService = (*OutboxPoller)(nil)
