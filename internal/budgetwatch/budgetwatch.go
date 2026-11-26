// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetwatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
)

const pullFailureWarnAfter = time.Minute

const confirmCheckInterval = 30 * time.Second

const silentBlockRepullAfter = 2 * confirmCheckInterval

var errPullDidNotConfirm = errors.New("the pulled budget state did not confirm the gate")

type Fetch func(ctx context.Context) (budget.EnforcementState, error)

type Watcher struct {
	gate         *budget.Gate
	fetch        Fetch
	broker       broker.Broker
	namespace    string
	logger       *slog.Logger
	warnAfter    time.Duration
	confirmEvery time.Duration
	repullAfter  time.Duration
	now          func() time.Time

	lastHeard atomic.Int64
	stopping  atomic.Bool
	sub       broker.Queue
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

func New(gate *budget.Gate, fetch Fetch, brk broker.Broker, namespace string, logger *slog.Logger) *Watcher {
	return &Watcher{
		gate:         gate,
		fetch:        fetch,
		broker:       brk,
		namespace:    namespace,
		logger:       logger.With("logger_name", "budget-watch"),
		warnAfter:    pullFailureWarnAfter,
		confirmEvery: confirmCheckInterval,
		repullAfter:  silentBlockRepullAfter,
		now:          time.Now,
	}
}

func (w *Watcher) Init(context.Context, config.Config) error { return nil }

func (w *Watcher) Start(ctx context.Context) error {
	if w.broker == nil || w.gate == nil {
		return nil
	}
	topic := a2a.BudgetStateTopic(w.namespace)
	sub, err := w.broker.CreateDirectSubscription()
	if err != nil {
		return fmt.Errorf("create budget state subscription: %w", err)
	}
	if err := w.broker.Subscribe(sub, topic); err != nil {
		if delErr := w.broker.DeleteQueue(sub.Name()); delErr != nil {
			w.logger.Warn("budget state subscription cleanup after failed subscribe", "error", delErr)
		}
		return fmt.Errorf("subscribe to budget state: %w", err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	w.sub, w.cancel = sub, cancel
	w.wg.Go(func() {
		broker.RunReceiveLoop(loopCtx, w.broker, sub, w.logger,
			broker.ReceiveLoopOptions{Teardown: w.stopping.Load, LoopName: "budgetState"},
			func(msg *broker.Message) {
				msg.Ack()
				w.handle(loopCtx, msg)
			})
	})
	if w.fetch != nil {
		w.wg.Go(func() { w.keepConfirmed(loopCtx) })
	}
	return nil
}

func (w *Watcher) handle(ctx context.Context, msg *broker.Message) {
	state, err := budget.DecodeState(msg.Payload)
	if err != nil {
		w.logger.Warn("budget state broadcast could not be decoded; waiting for the next one", "error", err, "payloadBytes", len(msg.Payload))
		return
	}
	w.apply(ctx, state)
}

func (w *Watcher) apply(ctx context.Context, state budget.EnforcementState) {
	w.lastHeard.Store(w.now().UnixNano())
	w.gate.Apply(ctx, state)
}

func (w *Watcher) keepConfirmed(ctx context.Context) {
	for {
		w.pullUntilConfirmed(ctx)
		for !w.unconfirmed() && !w.silentAboutABlock() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.confirmEvery):
			}
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func (w *Watcher) unconfirmed() bool {
	return w.gate.Unconfirmed()
}

func (w *Watcher) silentAboutABlock() bool {
	return w.gate.HoldsBlock() &&
		w.now().Sub(time.Unix(0, w.lastHeard.Load())) >= w.repullAfter
}

func (w *Watcher) pullUntilConfirmed(ctx context.Context) {
	bo := backoff.Exponential{Initial: time.Second, Max: 30 * time.Second, Jitter: 0.2}
	started := time.Now()
	warned := false
	for {
		state, err := w.fetch(ctx)
		if err == nil {
			w.apply(ctx, state)
			if !w.unconfirmed() {
				return
			}
			err = errPullDidNotConfirm
		}
		if !warned && time.Since(started) >= w.warnAfter {
			warned = true
			w.logger.Warn("budget state still unconfirmed after pulling; budget enforcement here acts on what it last knew until the platform answers or broadcasts",
				"error", err, "unconfirmedForSeconds", int64(time.Since(started).Seconds()))
		}
		w.logger.Debug("budget state pull did not confirm the gate; retrying", "error", err, "backoff", bo.Current())
		if bo.Wait(ctx) != nil {
			return
		}
	}
}

func (w *Watcher) Stop(context.Context) error {
	w.stopping.Store(true)
	if w.cancel == nil {
		return nil
	}
	w.cancel()
	w.wg.Wait()
	if err := w.broker.Unsubscribe(w.sub, a2a.BudgetStateTopic(w.namespace)); err != nil {
		w.logger.Warn("budget state unsubscribe failed", "error", err)
	}
	if err := w.broker.DeleteQueue(w.sub.Name()); err != nil {
		w.logger.Warn("budget state subscription cleanup failed", "error", err)
	}
	return nil
}

func (w *Watcher) Health() error { return nil }
