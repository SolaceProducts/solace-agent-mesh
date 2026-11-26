// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
)

const (
	heartbeat         = 30 * time.Second
	coalesce          = 200 * time.Millisecond
	largeUserListWarn = 50_000
)

type clock interface {
	Now() time.Time
	At(t time.Time) (<-chan time.Time, func())
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) At(t time.Time) (<-chan time.Time, func()) {
	timer := time.NewTimer(time.Until(t))
	return timer.C, func() { timer.Stop() }
}

type monitor struct {
	store     Store
	broker    broker.Broker
	namespace string
	logger    *slog.Logger
	clock     clock
	alerting  Alerting

	term             uint64
	published        *budget.EnforcementState
	publishedTime    time.Time
	meshInconsistent bool
	largeWarned      bool
	deadlineReached  bool
	prunedBefore     int64

	maxPayload int
}

func MonitorJob(s Store, brk broker.Broker, namespace string, logger *slog.Logger, alerting Alerting) leader.Job {
	m := &monitor{
		store:      s,
		broker:     brk,
		namespace:  namespace,
		logger:     logger.With("logger_name", "budget-state"),
		clock:      realClock{},
		maxPayload: maxStatePayload,
		alerting:   alerting,
	}
	return leader.Job{Name: leader.JobPlatformBudgetState, OverdueAfter: leader.NeverOverdue, Run: m.run}
}

func (m *monitor) run(ctx context.Context, term leader.Term) error {
	if term.Number != m.term {
		m.term, m.published = term.Number, nil
	}
	signals, stop, err := m.subscribe(ctx)
	if err != nil {
		return err
	}
	defer stop()

	var lastSignaled time.Time
	for {
		state, err := m.recompute(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		periodEnd, stopPeriodEnd := m.clock.At(state.PeriodEnd)
		beat, stopBeat := m.clock.At(m.publishedTime.Add(heartbeat))
		signaled := false
		select {
		case <-ctx.Done():
		case <-periodEnd:
		case <-beat:
		case <-signals:
			signaled = true
		}
		stopPeriodEnd()
		stopBeat()
		if ctx.Err() != nil {
			return nil
		}
		if signaled {
			if !m.waitOutBurst(ctx, lastSignaled) {
				return nil
			}
			select {
			case <-signals:
			default:
			}
			lastSignaled = m.clock.Now()
		}
	}
}

func (m *monitor) waitOutBurst(ctx context.Context, lastSignaled time.Time) bool {
	due := lastSignaled.Add(coalesce)
	if !m.clock.Now().Before(due) {
		return true
	}
	wait, release := m.clock.At(due)
	defer release()
	select {
	case <-ctx.Done():
		return false
	case <-wait:
		return true
	}
}

func (m *monitor) subscribe(ctx context.Context) (<-chan struct{}, func(), error) {
	topics := []string{a2a.SpendFiledTopic(m.namespace), a2a.BudgetsChangedTopic(m.namespace)}
	sub, err := m.broker.CreateDirectSubscription()
	if err != nil {
		return nil, nil, fmt.Errorf("create budget signal subscription: %w", err)
	}
	var subscribed []string
	cleanup := func() {
		var errs []error
		for _, topic := range subscribed {
			errs = append(errs, m.broker.Unsubscribe(sub, topic))
		}
		errs = append(errs, m.broker.DeleteQueue(sub.Name()))
		if err := errors.Join(errs...); err != nil {
			m.logger.Warn("budget signal subscription cleanup failed", "error", err)
		}
	}
	for _, topic := range topics {
		if err := m.broker.Subscribe(sub, topic); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("subscribe to budget signals: %w", err)
		}
		subscribed = append(subscribed, topic)
	}

	signals := make(chan struct{}, 1)
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	tearingDown := func() bool { return loopCtx.Err() != nil }
	go func() {
		defer close(done)
		broker.RunReceiveLoop(loopCtx, m.broker, sub, m.logger,
			broker.ReceiveLoopOptions{Teardown: tearingDown, LoopName: "budgetSignals"},
			func(msg *broker.Message) {
				msg.Ack()
				m.logger.Debug("budget signal received", "topic", msg.Topic)
				select {
				case signals <- struct{}{}:
				default:
				}
			})
	}()
	stop := func() {
		cancel()
		<-done
		cleanup()
	}
	return signals, stop, nil
}

func (m *monitor) recompute(ctx context.Context) (budget.EnforcementState, error) {
	now := m.clock.Now()
	a, err := assessWithin(ctx, m.store, now, m.maxPayload)
	state := a.state
	if err != nil {
		return state, err
	}
	if m.published == nil || !m.published.SameDecision(state) || state.UsersOmitted != m.published.UsersOmitted || state.WarningsOmitted != m.published.WarningsOmitted ||
		now.Sub(m.publishedTime) >= heartbeat {
		if err := m.publish(ctx, state); err != nil {
			return state, err
		}
		if m.published == nil {
			m.logger.Info("budget state published for this leader term", append([]any{
				"meshBlocked", state.MeshBlocked, "budgetsInUse", state.BudgetsInUse, "usersOmitted", state.UsersOmitted, "periodEnd", state.PeriodEnd,
			}, state.UserListAttrs()...)...)
		}
		m.logTransition(state)
		m.logOmission(state)
		m.logUsers(state)
		m.published, m.publishedTime = &state, now
	}
	m.alert(ctx, now, a)
	m.checkMeshPeriod(ctx, now)
	return state, nil
}

func (m *monitor) publish(ctx context.Context, state budget.EnforcementState) error {
	payload, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal budget state: %w", err)
	}
	topic := a2a.BudgetStateTopic(m.namespace)
	if err := m.broker.Publish(ctx, topic, &broker.Message{Topic: topic, Payload: payload}); err != nil {
		return fmt.Errorf("publish budget state: %w", err)
	}
	return nil
}

func (m *monitor) checkMeshPeriod(ctx context.Context, now time.Time) {
	periodStart := budget.PeriodStart(now).UnixMilli()
	check, err := m.store.CheckMeshPeriod(ctx, periodStart)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		m.logger.Warn("mesh budget period check could not read the period rows", "periodStart", periodStart, "error", err)
		return
	}
	switch consistent := check.Consistent(); {
	case !consistent && !m.meshInconsistent:
		m.logger.Error("mesh budget period row does not equal the sum of its user rows; enforcement acts on the mesh row",
			"periodStart", periodStart, "columns", check.Columns())
	case consistent && m.meshInconsistent:
		m.logger.Info("mesh budget period row equals the sum of its user rows again", "periodStart", periodStart)
	}
	m.meshInconsistent = !check.Consistent()
}

func (m *monitor) logTransition(state budget.EnforcementState) {
	if m.published == nil {
		return
	}
	wasBlocked := m.published.MeshBlocked
	switch {
	case state.MeshBlocked && !wasBlocked:
		m.logger.Warn("mesh budget is spent; enforcement points will refuse work until the period ends or the limit rises",
			"periodEnd", state.PeriodEnd)
	case !state.MeshBlocked && wasBlocked:
		m.logger.Info("mesh budget is no longer spent; enforcement points will accept work again")
	}
}

func (m *monitor) logOmission(state budget.EnforcementState) {
	wasOmitted := m.published != nil && m.published.UsersOmitted
	warningsWereOmitted := m.published != nil && m.published.WarningsOmitted
	switch {
	case state.WarningsOmitted && !warningsWereOmitted:
		m.logger.Warn("budget state is too large to send with its warning lists; until it fits, enforcement points keep the warnings they hold and otherwise warn no user",
			"maxPayloadBytes", m.maxPayload)
	case !state.WarningsOmitted && warningsWereOmitted && !state.UsersOmitted:
		m.logger.Info("budget state fits with its warning lists again")
	}
	switch {
	case state.UsersOmitted && !wasOmitted:
		m.logger.Error("budget state is too large to send with its user lists; until it fits, enforcement points keep the user blocks they hold and otherwise refuse no user",
			"maxPayloadBytes", m.maxPayload)
	case !state.UsersOmitted && wasOmitted:
		m.logger.Info("budget state fits with its user lists again")
	}
}

func (m *monitor) logUsers(state budget.EnforcementState) {
	if state.UsersOmitted {
		return
	}
	was := state
	if m.published != nil {
		was = *m.published
	}
	if state.DefaultUsersBlocked != was.DefaultUsersBlocked ||
		len(state.BlockedUsers) != len(was.BlockedUsers) || len(state.AdmittedUsers) != len(was.AdmittedUsers) {
		m.logger.Info("users refused by their own budget changed", append(state.UserListAttrs(), "periodEnd", state.PeriodEnd)...)
	}
	listed := len(state.BlockedUsers) + len(state.AdmittedUsers) + len(state.WarnedUsers) + len(state.UnwarnedUsers)
	if large := listed > largeUserListWarn; large != m.largeWarned {
		m.largeWarned = large
		if large {
			m.logger.Warn("budget state lists many users, so every broadcast of it is large", "listedUsers", listed)
		}
	}
}
