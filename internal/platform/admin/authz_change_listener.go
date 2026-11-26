// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	authzRebuildRetryInitial = time.Second
	authzRebuildRetryMax     = 30 * time.Second
	authzRebuildRetryJitter  = 0.2
)

type AuthzChangeListener struct {
	composer  *rbac.Composer
	lookup    *AuthzLookupHandler
	broker    broker.Broker
	namespace string
	metrics   runtime.Metrics
	logger    *slog.Logger

	retryInitial time.Duration
	retryMax     time.Duration

	stopping atomic.Bool
	sub      broker.Queue
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewAuthzChangeListener(composer *rbac.Composer, lookup *AuthzLookupHandler, brk broker.Broker, namespace string, metrics runtime.Metrics, logger *slog.Logger) *AuthzChangeListener {
	return &AuthzChangeListener{
		composer:     composer,
		lookup:       lookup,
		broker:       brk,
		namespace:    namespace,
		metrics:      metrics,
		logger:       logger.With("logger_name", "authz-change-listener"),
		retryInitial: authzRebuildRetryInitial,
		retryMax:     authzRebuildRetryMax,
	}
}

func (l *AuthzChangeListener) topics() []string {
	return []string{a2a.AuthzRolesChangedTopic(l.namespace), a2a.AuthzUsersChangedTopic(l.namespace)}
}

func (l *AuthzChangeListener) Start(ctx context.Context) error {
	sub, err := l.broker.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create authz change subscription: %w", err)
	}
	for _, topic := range l.topics() {
		if err := l.broker.Subscribe(sub, topic); err != nil {
			if delErr := l.broker.DeleteQueue(sub.Name()); delErr != nil {
				l.logger.Warn("authz change subscription cleanup after failed subscribe", "error", delErr)
			}
			return fmt.Errorf("subscribe to %s: %w", topic, err)
		}
	}

	loopCtx, cancel := context.WithCancel(ctx)
	l.sub, l.cancel = sub, cancel

	rolesChanged := make(chan struct{}, 1)
	usersChanged := make(chan struct{}, 1)
	rolesChanged <- struct{}{}
	rolesTopic := a2a.AuthzRolesChangedTopic(l.namespace)
	l.wg.Go(func() {
		broker.RunReceiveLoop(loopCtx, l.broker, sub, l.logger,
			broker.ReceiveLoopOptions{Teardown: l.stopping.Load, LoopName: "authzChange"},
			func(msg *broker.Message) {
				msg.Ack()
				signal := usersChanged
				if msg.Topic == rolesTopic {
					signal = rolesChanged
				}
				select {
				case signal <- struct{}{}:
				default:
				}
			})
	})
	l.wg.Go(func() {
		l.applyLoop(loopCtx, rolesChanged, usersChanged)
	})
	l.logger.Info("authz change listener started")
	return nil
}

func (l *AuthzChangeListener) applyLoop(ctx context.Context, rolesChanged, usersChanged <-chan struct{}) {
	bo := backoff.Exponential{Initial: l.retryInitial, Max: l.retryMax, Jitter: authzRebuildRetryJitter}
	var retry *time.Timer
	defer func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	for {
		var retryC <-chan time.Time
		if retry != nil {
			retryC = retry.C
		}
		select {
		case <-ctx.Done():
			return
		case <-usersChanged:
			l.invalidateLookups()
			continue
		case <-rolesChanged:
		case <-retryC:
		}
		if retry != nil {
			retry.Stop()
			retry = nil
		}
		if err := l.composer.Rebuild(ctx); err != nil {
			if l.metrics != nil {
				l.metrics.Counter("rbac_composer_rebuild_failures_total", 1)
			}
			l.logger.Warn("role graph rebuild after an authz change failed; retrying", "error", err)
			retry = time.NewTimer(bo.Next())
			continue
		}
		bo.Reset()
		l.invalidateLookups()
	}
}

func (l *AuthzChangeListener) invalidateLookups() {
	if l.lookup != nil {
		l.lookup.InvalidateAll()
	}
}

func (l *AuthzChangeListener) Stop(context.Context) error {
	l.stopping.Store(true)
	if l.cancel == nil {
		return nil
	}
	l.cancel()
	l.wg.Wait()
	for _, topic := range l.topics() {
		if err := l.broker.Unsubscribe(l.sub, topic); err != nil {
			l.logger.Warn("authz change unsubscribe failed", "topic", topic, "error", err)
		}
	}
	if err := l.broker.DeleteQueue(l.sub.Name()); err != nil {
		l.logger.Warn("authz change subscription cleanup failed", "error", err)
	}
	return nil
}
