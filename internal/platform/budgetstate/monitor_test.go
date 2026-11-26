// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const testWait = 5 * time.Second

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*fakeTimer]struct{}
	armed  chan struct{}
}

type fakeTimer struct {
	at time.Time
	c  chan time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now, timers: make(map[*fakeTimer]struct{}), armed: make(chan struct{})}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) At(t time.Time) (<-chan time.Time, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{at: t, c: make(chan time.Time, 1)}
	if t.After(c.now) {
		c.timers[timer] = struct{}{}
		close(c.armed)
		c.armed = make(chan struct{})
	} else {
		timer.c <- c.now
	}
	return timer.c, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.timers, timer)
	}
}

func (c *fakeClock) advance(to time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = to
	for timer := range c.timers {
		if !timer.at.After(to) {
			timer.c <- to
			delete(c.timers, timer)
		}
	}
}

func (c *fakeClock) waitArmed(t *testing.T, at time.Time) {
	t.Helper()
	deadline := time.After(testWait)
	for {
		c.mu.Lock()
		armed := c.armed
		found := false
		for timer := range c.timers {
			found = found || timer.at.Equal(at)
		}
		c.mu.Unlock()
		if found {
			return
		}
		select {
		case <-armed:
		case <-deadline:
			t.Fatalf("no timer was armed for %s", at)
		}
	}
}

type observedStore struct {
	Store
	checked chan struct{}
}

func (s observedStore) CheckMeshPeriod(ctx context.Context, periodStart int64) (store.BudgetPeriodMeshCheck, error) {
	check, err := s.Store.CheckMeshPeriod(ctx, periodStart)
	s.checked <- struct{}{}
	return check, err
}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) count(t *testing.T, level, msg string) int {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for line := range strings.Lines(b.buf.String()) {
		var rec struct{ Level, Msg string }
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if rec.Level == level && rec.Msg == msg {
			n++
		}
	}
	return n
}

type monitorRig struct {
	*fixture
	brk     *broker.DevBroker
	queue   broker.Queue
	clock   *fakeClock
	checked chan struct{}
	logs    *logBuffer
	mon     *monitor
}

func newMonitorRig(t *testing.T) *monitorRig {
	t.Helper()
	f := newFixture(t)
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) }) //nolint:contextcheck
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := brk.Subscribe(q, a2a.BudgetStateTopic(testNamespace)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	r := &monitorRig{fixture: f, brk: brk, queue: q, clock: newFakeClock(testNow), checked: make(chan struct{}, 16), logs: &logBuffer{}}
	r.mon = &monitor{
		store:      observedStore{Store: f.store, checked: r.checked},
		broker:     brk,
		namespace:  testNamespace,
		logger:     slog.New(slog.NewJSONHandler(r.logs, nil)),
		clock:      r.clock,
		maxPayload: maxStatePayload,
	}
	return r
}

func (r *monitorRig) start(term uint64) (stop func()) {
	r.t.Helper()
	ctx, cancel := context.WithCancel(r.t.Context())
	done := make(chan error, 1)
	go func() { done <- r.mon.run(ctx, leader.Term{Number: term}) }()
	stopped := false
	stop = func() {
		r.t.Helper()
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				r.t.Errorf("run returned %v when its term ended, want nil", err)
			}
		case <-time.After(testWait):
			r.t.Fatal("run did not return when its term ended")
		}
	}
	r.t.Cleanup(stop)
	return stop
}

func (r *monitorRig) signal(topic string) {
	r.t.Helper()
	if err := r.brk.Publish(r.t.Context(), topic, &broker.Message{Topic: topic}); err != nil {
		r.t.Fatalf("publish signal: %v", err)
	}
}

func (r *monitorRig) recomputed() {
	r.t.Helper()
	select {
	case <-r.checked:
	case <-time.After(testWait):
		r.t.Fatal("the monitor did not recompute")
	}
}

func (r *monitorRig) assertIdle(why string) {
	r.t.Helper()
	select {
	case <-r.checked:
		r.t.Fatalf("the monitor recomputed %s", why)
	default:
	}
}

func (r *monitorRig) published() (budget.EnforcementState, bool) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(r.t.Context(), 50*time.Millisecond)
	defer cancel()
	msg, err := r.brk.Receive(ctx, r.queue)
	if errors.Is(err, context.DeadlineExceeded) || msg == nil {
		return budget.EnforcementState{}, false
	}
	if err != nil {
		r.t.Fatalf("receive: %v", err)
	}
	msg.Ack()
	var state budget.EnforcementState
	if err := json.Unmarshal(msg.Payload, &state); err != nil {
		r.t.Fatalf("decode broadcast: %v", err)
	}
	return state, true
}

func (r *monitorRig) recomputedAndPublished(what string) budget.EnforcementState {
	r.t.Helper()
	r.recomputed()
	state, ok := r.published()
	if !ok {
		r.t.Fatalf("%s: nothing was published", what)
	}
	return state
}

func TestMonitorJob_RunsOncePerTermAndIsNeverOverdue(t *testing.T) {
	job := MonitorJob(newFixture(t).store, broker.NewDevBroker(), testNamespace, discardLogger, Alerting{})
	if job.Name != leader.JobPlatformBudgetState || job.Every != 0 || job.OverdueAfter != leader.NeverOverdue || job.Run == nil {
		t.Errorf("job = %+v, want %s once per term and never overdue", job, leader.JobPlatformBudgetState)
	}
}

func TestMonitor_ATermRecomputesAndPublishesAtItsStart(t *testing.T) {
	r := newMonitorRig(t)
	r.limitMesh(100)

	stop := r.start(1)
	first := r.recomputedAndPublished("the first term's start")
	if first.MeshBlocked || !first.BudgetsInUse || !first.PeriodEnd.Equal(testPeriodEnd) {
		t.Fatalf("the first term published %+v, want an unblocked state in use until %s", first, testPeriodEnd)
	}
	stop()

	r.clock.advance(testNow.Add(time.Second))
	r.start(2)
	if state := r.recomputedAndPublished("the next term's start"); state.MeshBlocked || !state.ComputedAt.Equal(r.clock.Now()) {
		t.Errorf("the next term published %+v, want the unchanged state computed now", state)
	}
}

func TestMonitor_OneSpendFiledSignalBlocksTheMeshOnceTheCrossingBatchIsFiled(t *testing.T) {
	r := newMonitorRig(t)
	r.limitMesh(100)
	r.start(1)
	r.recomputedAndPublished("the term's start")

	r.spend(testNow, 100)
	r.signal(a2a.SpendFiledTopic(testNamespace))
	if state := r.recomputedAndPublished("the spend filed signal"); !state.MeshBlocked {
		t.Errorf("the crossing batch's signal published %+v, want blocked", state)
	}
}

func TestMonitor_ALateEventEarlierInThePeriodCountsOnItsSignal(t *testing.T) {
	r := newMonitorRig(t)
	r.limitMesh(100)
	r.start(1)
	r.recomputedAndPublished("the term's start")

	r.spend(testNow.Add(-24*time.Hour), 100)
	r.signal(a2a.SpendFiledTopic(testNamespace))
	if state := r.recomputedAndPublished("the spend filed signal"); !state.MeshBlocked {
		t.Errorf("a late event earlier in the period published %+v, want blocked", state)
	}
}

func TestMonitor_ABudgetsChangedSignalUnblocksARaisedLimit(t *testing.T) {
	r := newMonitorRig(t)
	r.limitMesh(100)
	r.spend(testNow, 100)
	r.start(1)
	if state := r.recomputedAndPublished("the term's start"); !state.MeshBlocked {
		t.Fatalf("state = %+v, want blocked", state)
	}

	r.limitMesh(1000)
	r.signal(a2a.BudgetsChangedTopic(testNamespace))
	if state := r.recomputedAndPublished("the budgets changed signal"); state.MeshBlocked {
		t.Errorf("raising the limit published %+v, want unblocked", state)
	}
}

func TestMonitor_ASignalWithNothingChangedPublishesNothing(t *testing.T) {
	r := newMonitorRig(t)
	r.limitMesh(100)
	r.start(1)
	r.recomputedAndPublished("the term's start")

	r.signal(a2a.BudgetsChangedTopic(testNamespace))
	r.recomputed()
	if state, ok := r.published(); ok {
		t.Errorf("a signal with no change behind it published %+v", state)
	}
}

func TestMonitor_SignalsInABurstWaitForOneRecompute(t *testing.T) {
	r := newMonitorRig(t)
	r.limitMesh(100)
	r.start(1)
	r.recomputedAndPublished("the term's start")

	r.signal(a2a.SpendFiledTopic(testNamespace))
	r.recomputed()

	r.spend(testNow, 100)
	r.signal(a2a.SpendFiledTopic(testNamespace))
	r.signal(a2a.SpendFiledTopic(testNamespace))
	r.clock.waitArmed(t, testNow.Add(coalesce))
	r.assertIdle("again before the burst's wait was out")

	r.clock.advance(testNow.Add(coalesce))
	if state := r.recomputedAndPublished("the burst"); !state.MeshBlocked {
		t.Errorf("the burst published %+v, want blocked", state)
	}
}

func TestMonitor_ThePeriodEndPublishesTheNewPeriodsState(t *testing.T) {
	r := newMonitorRig(t)
	r.limitMesh(100)
	r.spend(testNow, 100)
	r.start(1)
	if state := r.recomputedAndPublished("the term's start"); !state.MeshBlocked {
		t.Fatalf("state = %+v, want blocked", state)
	}

	r.clock.waitArmed(t, testPeriodEnd)
	r.clock.advance(testPeriodEnd)
	state := r.recomputedAndPublished("the period's end")
	if state.MeshBlocked || !state.PeriodEnd.Equal(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("the new period published %+v, want unblocked until December", state)
	}
}

func TestMonitor_TheHeartbeatRecomputesAndRepublishes(t *testing.T) {
	r := newMonitorRig(t)
	r.limitMesh(100)
	r.start(1)
	r.recomputedAndPublished("the term's start")

	r.spend(testNow, 100)
	r.clock.waitArmed(t, testNow.Add(heartbeat))
	r.clock.advance(testNow.Add(heartbeat))
	if state := r.recomputedAndPublished("the heartbeat"); !state.MeshBlocked {
		t.Fatalf("the heartbeat published %+v, want the spend it was not signalled about to block", state)
	}

	next := testNow.Add(2 * heartbeat)
	r.clock.waitArmed(t, next)
	r.clock.advance(next)
	if state := r.recomputedAndPublished("the next heartbeat"); !state.MeshBlocked || !state.ComputedAt.Equal(next) {
		t.Errorf("the next heartbeat published %+v, want the unchanged blocked state computed now", state)
	}
}

func TestMonitor_EndingTheTermStopsRunAndItsSubscription(t *testing.T) {
	r := newMonitorRig(t)
	stop := r.start(1)
	r.recomputed()
	for _, topic := range []string{a2a.SpendFiledTopic(testNamespace), a2a.BudgetsChangedTopic(testNamespace)} {
		if got := len(r.brk.MatchingQueues(topic)); got != 1 {
			t.Fatalf("%d subscriptions match %s during the term, want 1", got, topic)
		}
	}

	stop()
	for _, topic := range []string{a2a.SpendFiledTopic(testNamespace), a2a.BudgetsChangedTopic(testNamespace)} {
		if got := r.brk.MatchingQueues(topic); len(got) != 0 {
			t.Errorf("subscriptions %v still match %s after the term", got, topic)
		}
	}
}

func TestMonitor_AnInconsistentMeshRowIsLoggedOnceUntilItRecovers(t *testing.T) {
	const (
		inconsistent = "mesh budget period row does not equal the sum of its user rows; enforcement acts on the mesh row"
		recovered    = "mesh budget period row equals the sum of its user rows again"
	)
	r := newMonitorRig(t)
	r.spend(testNow, 100)
	stop := r.start(1)
	r.recomputed()

	editMeshRow := func(delta int64) {
		t.Helper()
		if _, err := r.db.ExecContext(t.Context(),
			`UPDATE token_spend_budget_period SET output_cost = output_cost + ? WHERE subject_kind = 'mesh'`, delta); err != nil {
			t.Fatalf("edit the mesh row: %v", err)
		}
	}
	at := testNow
	recomputeOnSignal := func() {
		t.Helper()
		at = at.Add(coalesce)
		r.clock.advance(at)
		r.signal(a2a.BudgetsChangedTopic(testNamespace))
		r.recomputed()
	}
	editMeshRow(1)
	for range 3 {
		recomputeOnSignal()
	}
	editMeshRow(-1)
	recomputeOnSignal()
	stop()

	if n := r.logs.count(t, "ERROR", inconsistent); n != 1 {
		t.Errorf("a mesh row inconsistent over three recomputes was logged %d times, want once", n)
	}
	if n := r.logs.count(t, "INFO", recovered); n != 1 {
		t.Errorf("the mesh row's recovery was logged %d times, want once", n)
	}
}

func TestMonitor_AFailedReadFailsTheRunAndPublishesNothing(t *testing.T) {
	r := newMonitorRig(t)
	r.limitMesh(100)
	if err := r.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if err := r.mon.run(t.Context(), leader.Term{Number: 1}); err == nil {
		t.Error("a run that could not read the database succeeded")
	}
	if state, ok := r.published(); ok {
		t.Errorf("a failed run published %+v", state)
	}
	if got := r.brk.MatchingQueues(a2a.SpendFiledTopic(testNamespace)); len(got) != 0 {
		t.Errorf("a failed run left subscriptions %v", got)
	}
}

func TestMonitor_AUserIsBlockedOnTheSignalOfTheirCrossingBatchAndCountedOnce(t *testing.T) {
	r := newMonitorRig(t)
	r.limitUserDefault(100)
	alice := idgen.New()
	r.start(1)
	r.recomputedAndPublished("the term's start")

	r.spendAs(alice, "", testNow, 100)
	r.signal(a2a.SpendFiledTopic(testNamespace))
	state := r.recomputedAndPublished("the spend filed signal")
	if !state.UserBlocked(budget.UserKey(alice, "")) || state.MeshBlocked {
		t.Fatalf("alice's crossing batch published %+v, want her blocked and the mesh not", state)
	}

	r.clock.waitArmed(t, testNow.Add(heartbeat))
	r.clock.advance(testNow.Add(heartbeat))
	r.recomputedAndPublished("the heartbeat")
	if n := r.logs.count(t, "INFO", "users refused by their own budget changed"); n != 1 {
		t.Errorf("the change in refused users was logged %d times across a heartbeat, want once", n)
	}
}

func TestMonitor_ABudgetsChangedSignalUnblocksAUserGivenMore(t *testing.T) {
	for _, tc := range []struct {
		name string
		give func(r *monitorRig, user string)
	}{
		{"an assigned budget", func(r *monitorRig, user string) { r.assign(user, new(int64(1000))) }},
		{"a top-up", func(r *monitorRig, user string) { r.topUpUser(user, 100, testPeriodEnd) }},
		{"a higher default", func(r *monitorRig, _ string) { r.limitUserDefault(1000) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newMonitorRig(t)
			r.limitUserDefault(100)
			alice := idgen.New()
			r.spendAs(alice, "", testNow, 100)
			r.start(1)
			if state := r.recomputedAndPublished("the term's start"); !state.UserBlocked(budget.UserKey(alice, "")) {
				t.Fatalf("state = %+v, want alice blocked", state)
			}

			tc.give(r, alice)
			r.signal(a2a.BudgetsChangedTopic(testNamespace))
			if state := r.recomputedAndPublished("the budgets changed signal"); state.UserBlocked(budget.UserKey(alice, "")) {
				t.Errorf("%s published %+v, want alice admitted", tc.name, state)
			}
		})
	}
}

func TestMonitor_ThePeriodEndClearsUserBlocks(t *testing.T) {
	r := newMonitorRig(t)
	r.limitUserDefault(100)
	alice := idgen.New()
	r.spendAs(alice, "", testNow, 100)
	r.start(1)
	if state := r.recomputedAndPublished("the term's start"); !state.UserBlocked(budget.UserKey(alice, "")) {
		t.Fatalf("state = %+v, want alice blocked", state)
	}

	r.clock.waitArmed(t, testPeriodEnd)
	r.clock.advance(testPeriodEnd)
	if state := r.recomputedAndPublished("the period's end"); len(state.BlockedUsers) != 0 {
		t.Errorf("the new period published %+v, want nobody blocked", state)
	}
}

func TestMonitor_AStateTooLargeToSendStillCarriesTheMeshDecision(t *testing.T) {
	r := newMonitorRig(t)
	r.limitMesh(100)
	r.limitUserDefault(10)
	for range 20 {
		r.spendAs(idgen.New(), "", testNow, 10)
	}
	full, err := Compute(t.Context(), r.store, testNow)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	payload, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r.mon.maxPayload = len(payload) - 1
	r.start(1)

	state := r.recomputedAndPublished("the term's start")
	if !state.MeshBlocked || !state.UsersOmitted || len(state.BlockedUsers) != 0 {
		t.Fatalf("published %+v, want the mesh blocked and the user lists omitted", state)
	}
	if n := r.logs.count(t, "ERROR", "budget state is too large to send with its user lists; until it fits, enforcement points keep the user blocks they hold and otherwise refuse no user"); n != 1 {
		t.Errorf("the omission was logged %d times at ERROR, want once", n)
	}

	r.mon.maxPayload = maxStatePayload
	r.signal(a2a.BudgetsChangedTopic(testNamespace))
	state = r.recomputedAndPublished("a recompute once the lists fit")
	if state.UsersOmitted || len(state.BlockedUsers) != 20 {
		t.Errorf("published %+v, want the 20 blocked users listed again", state)
	}
	if n := r.logs.count(t, "INFO", "budget state fits with its user lists again"); n != 1 {
		t.Errorf("the recovery was logged %d times, want once", n)
	}
}

func TestMonitor_AStateTooLargeForItsWarningsStillCarriesItsUserBlocks(t *testing.T) {
	r := newMonitorRig(t)
	r.limitUserDefault(100)
	r.warnUserDefaultAt(50)
	for range 5 {
		r.spendAs(idgen.New(), "", testNow, 100)
	}
	for range 20 {
		r.spendAs(idgen.New(), "", testNow, 60)
	}
	full, err := Compute(t.Context(), r.store, testNow)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	payload, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r.mon.maxPayload = len(payload) - 1
	r.start(1)

	state := r.recomputedAndPublished("the term's start")
	if state.UsersOmitted || !state.WarningsOmitted || len(state.BlockedUsers) != 5 || len(state.WarnedUsers) != 0 {
		t.Fatalf("published %+v, want the 5 blocks kept and the warnings omitted", state)
	}
	if n := r.logs.count(t, "WARN", "budget state is too large to send with its warning lists; until it fits, enforcement points keep the warnings they hold and otherwise warn no user"); n != 1 {
		t.Errorf("the omission was logged %d times at WARN, want once", n)
	}
}

func (b *logBuffer) records(t *testing.T, msg string) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var found []map[string]any
	for line := range strings.Lines(b.buf.String()) {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if rec["msg"] == msg {
			found = append(found, rec)
		}
	}
	return found
}

func TestMonitor_LogsEachTermsFirstStateOnceCountingUsersApartFromPendingIdentities(t *testing.T) {
	const (
		termStart    = "budget state published for this leader term"
		usersChanged = "users refused by their own budget changed"
	)
	r := newMonitorRig(t)
	r.assign(idgen.New(), new(int64(0)))
	r.assignPending(store.BudgetIdentitySub, "kim-sub", new(int64(0)))

	stop := r.start(1)
	r.recomputed()
	r.clock.advance(testNow.Add(coalesce))
	r.signal(a2a.BudgetsChangedTopic(testNamespace))
	r.recomputed()
	stop()
	r.start(2)
	r.recomputed()

	starts := r.logs.records(t, termStart)
	if len(starts) != 2 {
		t.Fatalf("logged the term's first state %d times over two terms and an unchanged recompute, want once per term", len(starts))
	}
	for key, want := range map[string]any{"level": "INFO", "blockedUsers": float64(1), "blockedIdentities": float64(1), "meshBlocked": false, "budgetsInUse": true} {
		if starts[0][key] != want {
			t.Errorf("term-start line %s = %v, want %v", key, starts[0][key], want)
		}
	}
	if n := r.logs.count(t, "INFO", usersChanged); n != 0 {
		t.Errorf("a term's first state was also logged %d times as a change in refused users, want 0", n)
	}
}

func TestMonitor_LogsEachMeshBlockTransitionOnceWithoutAlerting(t *testing.T) {
	const (
		spent       = "mesh budget is spent; enforcement points will refuse work until the period ends or the limit rises"
		notSpentNow = "mesh budget is no longer spent; enforcement points will accept work again"
	)
	r := newMonitorRig(t)
	r.limitMesh(1000)
	r.spend(testNow, 100)
	r.start(1)
	if state := r.recomputedAndPublished("the term's start"); state.MeshBlocked {
		t.Fatalf("state = %+v, want unblocked", state)
	}

	r.limitMesh(100)
	r.signal(a2a.BudgetsChangedTopic(testNamespace))
	if state := r.recomputedAndPublished("the limit lowered"); !state.MeshBlocked {
		t.Fatalf("state = %+v, want blocked", state)
	}

	r.clock.waitArmed(t, testNow.Add(heartbeat))
	r.clock.advance(testNow.Add(heartbeat))
	r.recomputedAndPublished("the heartbeat")

	r.limitMesh(1000)
	r.clock.advance(testNow.Add(heartbeat + coalesce))
	r.signal(a2a.BudgetsChangedTopic(testNamespace))
	if state := r.recomputedAndPublished("the limit raised"); state.MeshBlocked {
		t.Fatalf("state = %+v, want unblocked", state)
	}

	if n := r.logs.count(t, "WARN", spent); n != 1 {
		t.Errorf("logged %q %d times, want once", spent, n)
	}
	if n := r.logs.count(t, "INFO", notSpentNow); n != 1 {
		t.Errorf("logged %q %d times, want once", notSpentNow, n)
	}
}

func TestMonitor_AMeshAlreadyBlockedAtATermsStartIsReportedOnlyInTheTermStartLine(t *testing.T) {
	const (
		termStart = "budget state published for this leader term"
		spent     = "mesh budget is spent; enforcement points will refuse work until the period ends or the limit rises"
	)
	r := newMonitorRig(t)
	r.limitMesh(100)
	r.spend(testNow, 100)
	stop := r.start(1)
	r.recomputed()
	stop()
	r.start(2)
	r.recomputed()

	if n := r.logs.count(t, "WARN", spent); n != 0 {
		t.Errorf("logged %q %d times over two term starts, want none", spent, n)
	}
	starts := r.logs.records(t, termStart)
	if len(starts) != 2 {
		t.Fatalf("logged the term's first state %d times, want once per term", len(starts))
	}
	for _, start := range starts {
		if start["meshBlocked"] != true {
			t.Errorf("term-start line meshBlocked = %v, want true", start["meshBlocked"])
		}
	}
}
