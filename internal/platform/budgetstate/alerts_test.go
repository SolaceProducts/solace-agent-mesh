// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type staticAdmins []Recipient

type countingMetrics struct {
	runtime.NoopMetrics
	mu       sync.Mutex
	counters map[string]int64
}

func (m *countingMetrics) Counter(name string, delta int64, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counters == nil {
		m.counters = make(map[string]int64)
	}
	m.counters[name+" "+strings.Join(labels, ",")] += delta
}

func (m *countingMetrics) count(key string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[key]
}

type failingSets struct {
	Store
	fail atomic.Int32
}

func (s *failingSets) SetAlertState(ctx context.Context, state store.BudgetAlertState) error {
	if s.fail.Add(-1) >= 0 {
		return errors.New("write refused")
	}
	return s.Store.SetAlertState(ctx, state)
}

type alertRig struct {
	*monitorRig
	notifyQueue broker.Queue
	notifyOn    atomic.Bool
	admins      staticAdmins
	metrics     *countingMetrics
	at          time.Time
}

var testAdmin = Recipient{PlatformUserID: "0192f0a6-3c1b-7d2e-8f4a-5b6c7d8e9f00", LoginID: "admin@example.com", CanReviewBudgets: true}

func newAlertRig(t *testing.T) *alertRig {
	t.Helper()
	r := &alertRig{monitorRig: newMonitorRig(t), admins: staticAdmins{testAdmin}, metrics: &countingMetrics{}, at: testNow}
	q, err := r.brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if err := r.brk.Subscribe(q, a2a.NotifyRequestSubscription(testNamespace)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	r.notifyQueue = q
	r.notifyOn.Store(true)
	r.mon.alerting = r.alerting()
	return r
}

func (r *alertRig) alerting() Alerting {
	return Alerting{Admins: &r.admins, NotifyEnabled: r.notifyOn.Load, Metrics: r.metrics}
}

func (a *staticAdmins) BudgetAdmins(ctx context.Context) ([]Recipient, error) {
	return (*a)[:len(*a):len(*a)], nil
}

func (r *alertRig) restart() {
	r.mon = &monitor{
		store:     r.mon.store,
		broker:    r.brk,
		namespace: testNamespace,
		logger:    r.mon.logger,
		clock:     r.clock,
		alerting:  r.alerting(),
	}
}

func (r *alertRig) recomputeOnSignal() {
	r.t.Helper()
	r.at = r.at.Add(coalesce)
	r.clock.advance(r.at)
	r.signal(a2a.BudgetsChangedTopic(testNamespace))
	r.recomputed()
}

func (r *alertRig) alerts() []a2a.NotifyRequest {
	r.t.Helper()
	var reqs []a2a.NotifyRequest
	for {
		ctx, cancel := context.WithTimeout(r.t.Context(), 50*time.Millisecond)
		msg, err := r.brk.Receive(ctx, r.notifyQueue)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) || msg == nil {
			return reqs
		}
		if err != nil {
			r.t.Fatalf("receive: %v", err)
		}
		msg.Ack()
		var req a2a.NotifyRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			r.t.Fatalf("decode notify request: %v", err)
		}
		if want := a2a.NotifyRequestTopic(testNamespace, firstNonEmpty(req.RecipientPlatformUserID, req.RecipientUserID)); msg.Topic != want {
			r.t.Errorf("published on %q, want %q", msg.Topic, want)
		}
		reqs = append(reqs, req)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (r *alertRig) onlyAlert(what string) a2a.NotifyRequest {
	r.t.Helper()
	reqs := r.alerts()
	if len(reqs) != 1 {
		r.t.Fatalf("%s: %d alerts published, want 1: %+v", what, len(reqs), reqs)
	}
	return reqs[0]
}

func (r *alertRig) noAlert(what string) {
	r.t.Helper()
	if reqs := r.alerts(); len(reqs) != 0 {
		r.t.Fatalf("%s: alerts published %+v, want none", what, reqs)
	}
}

func (f *fixture) human(email string) string {
	f.t.Helper()
	u := &store.User{Issuer: "https://idp.example.com", Sub: "sub-" + email, Email: new(email), ProvisionedVia: "jit"}
	if err := store.NewUsersRepo(f.db).UpsertUser(f.t.Context(), u, nil); err != nil {
		f.t.Fatalf("create user: %v", err)
	}
	return u.ID
}

func (f *fixture) deprovision(id string) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), `UPDATE users SET active = 0 WHERE id = ?`, id); err != nil {
		f.t.Fatalf("deprovision: %v", err)
	}
}

func (f *fixture) alertStates() []store.BudgetAlertState {
	f.t.Helper()
	rows, err := store.NewBudgetAlertStatesRepo(f.db).AlertStates(f.t.Context(), budget.PeriodStart(testNow).UnixMilli())
	if err != nil {
		f.t.Fatalf("read alert states: %v", err)
	}
	return rows
}

func assertLevel(t *testing.T, req a2a.NotifyRequest, level budget.Level) {
	t.Helper()
	if req.Metadata["kind"] != string(level) {
		t.Errorf("alert kind = %v, want %s", req.Metadata["kind"], level)
	}
}

func TestAlerts_AUserIsAlertedOnceAtTheirWarningAndOnceAtTheirLimit(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(100)
	r.warnUserDefaultAt(80)
	alice := r.human("alice@example.com")
	r.start(1)
	r.recomputed()
	r.noAlert("the term's start")

	r.spendAs(alice, "", testNow, 85)
	r.recomputeOnSignal()
	warn := r.onlyAlert("crossing the warning")
	assertLevel(t, warn, budget.LevelWarn)
	if warn.RecipientPlatformUserID != alice || warn.RecipientUserID != "alice@example.com" || warn.Category != a2a.CategoryBudgetSelfWarn {
		t.Errorf("warning sent to %q/%q in %q, want alice by both ids in %q", warn.RecipientPlatformUserID, warn.RecipientUserID, warn.Category, a2a.CategoryBudgetSelfWarn)
	}
	if !strings.Contains(warn.Message, "85%") || !strings.Contains(warn.Message, "1 November 2026") {
		t.Errorf("warning message %q, want the actual percentage and the reset date", warn.Message)
	}

	r.recomputeOnSignal()
	r.noAlert("a recompute with nothing new")

	r.spendAs(alice, "", testNow, 15)
	r.recomputeOnSignal()
	limit := r.onlyAlert("reaching the limit")
	assertLevel(t, limit, budget.LevelLimit)
	if limit.Category != a2a.CategoryBudgetSelfLimit {
		t.Errorf("limit alert in %q, want %q", limit.Category, a2a.CategoryBudgetSelfLimit)
	}
	if limit.Dedup == warn.Dedup || !strings.HasPrefix(limit.Dedup, "budget:user:"+budget.UserKey(alice, "")+":") {
		t.Errorf("dedup keys %q and %q, want distinct keys per level naming the subject", warn.Dedup, limit.Dedup)
	}
}

func TestAlerts_AUsersAlertCarriesNoAmounts(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(10_000_000_000)
	alice := r.human("alice@example.com")
	r.spendAs(alice, "", testNow, 10_000_000_000)
	r.start(1)
	r.recomputed()

	alert := r.onlyAlert("a user at the limit")
	for _, amount := range []string{"1.00", budget.FormatAmount(10_000_000_000)} {
		if strings.Contains(alert.Title+alert.Message, amount) {
			t.Errorf("a user's own alert %q / %q discloses the amount %s", alert.Title, alert.Message, amount)
		}
	}
	if _, linked := alert.Metadata["link_path"]; linked {
		t.Error("a user's own alert links to the budget administration screen")
	}
}

func TestAlerts_CrossingTheWarningAndTheLimitAtOnceAlertsOnlyTheLimit(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(100)
	r.warnUserDefaultAt(80)
	alice := r.human("alice@example.com")
	r.start(1)
	r.recomputed()

	r.spendAs(alice, "", testNow, 120)
	r.recomputeOnSignal()
	assertLevel(t, r.onlyAlert("jumping past both"), budget.LevelLimit)
}

func TestAlerts_ANewLeaderDoesNotAlertAgain(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(100)
	alice := r.human("alice@example.com")
	r.spendAs(alice, "", testNow, 100)
	stop := r.start(1)
	r.recomputed()
	r.onlyAlert("the first leader")
	stop()

	r.restart()
	r.start(2)
	r.recomputed()
	r.noAlert("a new leader over the same database")
}

func TestAlerts_AnAlertWhoseRecordFailedIsSentAgainUnderTheSameDedupKey(t *testing.T) {
	r := newAlertRig(t)
	failing := &failingSets{Store: r.mon.store}
	failing.fail.Store(1)
	r.mon.store = failing
	r.limitUserDefault(100)
	alice := r.human("alice@example.com")
	r.spendAs(alice, "", testNow, 100)
	r.start(1)
	r.recomputed()
	first := r.onlyAlert("the crossing whose record fails")

	r.recomputeOnSignal()
	second := r.onlyAlert("the next recompute")
	if first.Dedup != second.Dedup || first.RequestID == second.RequestID {
		t.Errorf("resent with dedup %q (was %q), want the same key on a new request", second.Dedup, first.Dedup)
	}
	r.recomputeOnSignal()
	r.noAlert("once the crossing is recorded")
}

func TestAlerts_WhatChangesTheCapReArmsTheAlert(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(r *alertRig, user string)
		want   budget.Level
	}{
		{"a top-up that leaves them past the new warning", func(r *alertRig, user string) { r.topUpUser(user, 10, testPeriodEnd) }, budget.LevelWarn},
		{"a lower amount", func(r *alertRig, _ string) { r.limitUserDefault(90) }, budget.LevelLimit},
		{"a reassignment to a budget as small", func(r *alertRig, user string) { r.assign(user, new(int64(100))) }, budget.LevelLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newAlertRig(t)
			r.limitUserDefault(100)
			r.warnUserDefaultAt(80)
			alice := r.human("alice@example.com")
			r.spendAs(alice, "", testNow, 100)
			r.start(1)
			r.recomputed()
			assertLevel(t, r.onlyAlert("the limit"), budget.LevelLimit)

			tc.change(r, alice)
			r.recomputeOnSignal()
			assertLevel(t, r.onlyAlert(tc.name), tc.want)
		})
	}
}

func TestAlerts_FallingBelowTheAlertedLevelRecordsItWithoutAlerting(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(100)
	r.warnUserDefaultAt(80)
	alice := r.human("alice@example.com")
	r.spendAs(alice, "", testNow, 90)
	r.start(1)
	r.recomputed()
	r.onlyAlert("the warning")

	if _, err := r.db.ExecContext(t.Context(), `DELETE FROM token_spend_budget_period WHERE subject_kind = 'id'`); err != nil {
		t.Fatalf("drop the spend: %v", err)
	}
	r.recomputeOnSignal()
	r.noAlert("falling below the warning")
	if rows := r.alertStates(); len(rows) != 1 || rows[0].AlertedLevel != string(budget.LevelNone) {
		t.Errorf("alert states = %+v, want the user lowered to none", rows)
	}
}

func TestAlerts_ThePeriodEndStartsAfreshAndPrunesTheClosedPeriod(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(100)
	alice := r.human("alice@example.com")
	r.spendAs(alice, "", testNow, 100)
	r.start(1)
	r.recomputed()
	r.onlyAlert("the limit")

	r.clock.waitArmed(t, testPeriodEnd)
	r.clock.advance(testPeriodEnd)
	r.recomputed()
	r.noAlert("a new period with nothing spent")
	if rows, err := store.NewBudgetAlertStatesRepo(r.db).AlertStates(t.Context(), budget.PeriodStart(testNow).UnixMilli()); err != nil || len(rows) != 0 {
		t.Errorf("last period's alert states = %+v (err %v), want them deleted", rows, err)
	}
}

func TestAlerts_WithNotifyOffTheCrossingIsRecordedAndNeverSent(t *testing.T) {
	r := newAlertRig(t)
	r.notifyOn.Store(false)
	r.limitUserDefault(100)
	alice := r.human("alice@example.com")
	r.spendAs(alice, "", testNow, 100)
	r.start(1)
	r.recomputed()
	r.noAlert("notify off")
	if rows := r.alertStates(); len(rows) != 1 || rows[0].AlertedLevel != string(budget.LevelLimit) {
		t.Fatalf("alert states = %+v, want the limit recorded", rows)
	}

	r.notifyOn.Store(true)
	r.recomputeOnSignal()
	r.noAlert("turning notify on later")
}

func TestAlerts_TheMeshIsAlertedToBudgetAdminsWithAmounts(t *testing.T) {
	r := newAlertRig(t)
	r.limitMesh(10_000_000_000)
	r.warnMeshAt(50)
	r.topUpMesh(10_000_000_000, testPeriodEnd)
	r.spend(testNow, 15_000_000_000)
	r.start(1)
	r.recomputed()

	alert := r.onlyAlert("the mesh warning")
	assertLevel(t, alert, budget.LevelWarn)
	if alert.RecipientPlatformUserID != testAdmin.PlatformUserID || alert.Category != a2a.CategoryBudgetMeshWarn {
		t.Errorf("mesh alert to %q in %q, want the admin in %q", alert.RecipientPlatformUserID, alert.Category, a2a.CategoryBudgetMeshWarn)
	}
	for _, want := range []string{"spent $1.50 of its $2.00 budget ($1.00 plus a $1.00 top-up)", "75%"} {
		if !strings.Contains(alert.Message, want) {
			t.Errorf("mesh alert %q lacks %q", alert.Message, want)
		}
	}
	if alert.Metadata["link_path"] != budgetsLinkPath || alert.Metadata["budget_id"] != store.SeededMeshBudgetID || alert.Metadata["subject"] != alertSubjectMesh {
		t.Errorf("mesh alert metadata = %v, want the budgets link, the mesh budget and the mesh subject", alert.Metadata)
	}
}

func TestAlerts_AMeshCrossingWithNoAdminIsStillRecorded(t *testing.T) {
	r := newAlertRig(t)
	r.admins = nil
	r.limitMesh(100)
	r.spend(testNow, 100)
	r.start(1)
	r.recomputed()
	r.noAlert("no admin")
	if rows := r.alertStates(); len(rows) != 1 || rows[0].SubjectType != store.BudgetSubjectMesh {
		t.Errorf("alert states = %+v, want the mesh crossing recorded", rows)
	}
	if n := r.logs.count(t, "WARN", "budget alert has no administrator to go to; the crossing is recorded"); n != 1 {
		t.Errorf("the missing administrator was logged %d times, want once", n)
	}
}

func TestAlerts_ASystemUsersCrossingGoesToBudgetAdmins(t *testing.T) {
	for _, tc := range []struct {
		name     string
		id, sub  string
		wantName string
	}{
		{"by row", rbac.EvidenceSystemUserID, "", ""},
		{"by name only", "", "system:nightly", "system:nightly"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newAlertRig(t)
			r.limitUserDefault(100)
			r.spendAs(tc.id, tc.sub, testNow, 100)
			r.start(1)
			r.recomputed()
			alert := r.onlyAlert("the system user's limit")
			if alert.RecipientPlatformUserID != testAdmin.PlatformUserID || alert.Category != a2a.CategoryBudgetSystemUserLimit ||
				alert.Metadata["subject"] != alertSubjectSystemUser {
				t.Errorf("system user alert = %+v, want it sent to the admin in %q", alert, a2a.CategoryBudgetSystemUserLimit)
			}
			if tc.wantName != "" && !strings.Contains(alert.Title, tc.wantName) {
				t.Errorf("title %q does not name the system user %q", alert.Title, tc.wantName)
			}
		})
	}
}

func TestAlerts_AUserKnownOnlyByNameIsAlertedByThatName(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(100)
	r.spendAs("", "bob@example.com", testNow, 100)
	r.start(1)
	r.recomputed()
	alert := r.onlyAlert("the limit")
	if alert.RecipientUserID != "bob@example.com" || alert.RecipientPlatformUserID != "" || alert.Category != a2a.CategoryBudgetSelfLimit {
		t.Errorf("alert to %q/%q in %q, want bob by name in %q", alert.RecipientPlatformUserID, alert.RecipientUserID, alert.Category, a2a.CategoryBudgetSelfLimit)
	}
}

func TestAlerts_ADeprovisionedOrDeletedUserIsNeverAlerted(t *testing.T) {
	r := newAlertRig(t)
	r.limitUserDefault(100)
	gone := r.human("gone@example.com")
	r.deprovision(gone)
	r.spendAs(gone, "", testNow, 100)
	r.spendAs(idgen.New(), "", testNow, 100)
	r.start(1)
	r.recomputed()
	r.noAlert("users who cannot be reached")
	if rows := r.alertStates(); len(rows) != 2 {
		t.Errorf("alert states = %+v, want both crossings recorded", rows)
	}
}

func TestAlerts_ACrossingIsLoggedOnceAndCounted(t *testing.T) {
	r := newAlertRig(t)
	r.limitMesh(100)
	r.spend(testNow, 100)
	r.start(1)
	r.recomputed()
	if n := r.logs.count(t, "INFO", msgBudgetLevelCrossed); n != 1 {
		t.Errorf("the mesh reaching its limit was logged %d times at INFO, want once", n)
	}
	if n := r.logs.count(t, "WARN", msgBudgetLevelCrossed); n != 0 {
		t.Errorf("the mesh reaching its limit was logged %d times at WARN, want none: the blocked transition is the WARN", n)
	}
	if !slices.ContainsFunc(r.alerts(), func(req a2a.NotifyRequest) bool { return req.Category == a2a.CategoryBudgetMeshLimit }) {
		t.Error("the mesh's limit was not alerted")
	}
	if n := r.metrics.count(metricCrossings + " subject,mesh,level,limit"); n != 1 {
		t.Errorf("the mesh's crossing was counted %d times, want once", n)
	}
	r.recomputeOnSignal()
	if n := r.metrics.count(metricCrossings + " subject,mesh,level,limit"); n != 1 {
		t.Errorf("a recompute with nothing new counted the crossing again (%d)", n)
	}
}

type failingPublish struct {
	broker.Broker
	failAt int32
	calls  atomic.Int32
}

func (b *failingPublish) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if b.calls.Add(1) == b.failAt {
		return errors.New("broker refused the message")
	}
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

type countingAdmins struct {
	staticAdmins
	calls atomic.Int32
}

func (a *countingAdmins) BudgetAdmins(ctx context.Context) ([]Recipient, error) {
	a.calls.Add(1)
	return a.staticAdmins.BudgetAdmins(ctx)
}

func TestAlerts_AFailedPublishRecordsNothingAndIsSentAgainUnderTheSameDedupKey(t *testing.T) {
	r := newAlertRig(t)
	r.mon.broker = &failingPublish{Broker: r.brk, failAt: 1}
	r.limitUserDefault(100)
	alice := r.human("alice@example.com")
	r.spendAs(alice, "", testNow, 100)
	r.start(1)
	r.recomputed()
	r.noAlert("the refused publish")
	if rows := r.alertStates(); len(rows) != 0 {
		t.Fatalf("alert states = %+v after a refused publish, want nothing recorded", rows)
	}

	r.recomputeOnSignal()
	retried := r.onlyAlert("the next recompute")
	assertLevel(t, retried, budget.LevelLimit)
	if want := "budget:user:" + budget.UserKey(alice, "") + ":"; !strings.HasPrefix(retried.Dedup, want) {
		t.Errorf("retried with dedup %q, want the subject's key %q…", retried.Dedup, want)
	}
	if rows := r.alertStates(); len(rows) != 1 || rows[0].AlertedLevel != string(budget.LevelLimit) {
		t.Errorf("alert states = %+v, want the limit recorded once sent", rows)
	}
	r.recomputeOnSignal()
	r.noAlert("once the crossing is recorded")
}

func TestAlerts_APartialPublishToSeveralAdminsIsRetriedToAllUnderOneDedupKey(t *testing.T) {
	r := newAlertRig(t)
	second := Recipient{PlatformUserID: idgen.New(), LoginID: "second-admin"}
	third := Recipient{PlatformUserID: idgen.New(), LoginID: "third-admin"}
	r.admins = staticAdmins{testAdmin, second, third}
	r.mon.broker = &failingPublish{Broker: r.brk, failAt: 2}
	r.limitMesh(100)
	r.spend(testNow, 100)
	r.start(1)
	r.recomputed()
	partial := r.alerts()
	if len(partial) != 1 || partial[0].RecipientPlatformUserID != testAdmin.PlatformUserID {
		t.Fatalf("the partial publish sent %+v, want only the first admin's alert", partial)
	}
	if rows := r.alertStates(); len(rows) != 0 {
		t.Fatalf("alert states = %+v after a partial publish, want nothing recorded", rows)
	}

	r.recomputeOnSignal()
	retried := r.alerts()
	var recipients []string
	for _, req := range retried {
		recipients = append(recipients, req.RecipientPlatformUserID)
		if req.Dedup != partial[0].Dedup {
			t.Errorf("retry to %s carries dedup %q, want the first attempt's %q", req.RecipientPlatformUserID, req.Dedup, partial[0].Dedup)
		}
	}
	if want := []string{testAdmin.PlatformUserID, second.PlatformUserID, third.PlatformUserID}; !slices.Equal(recipients, want) {
		t.Errorf("retry went to %v, want every admin %v", recipients, want)
	}
	r.recomputeOnSignal()
	r.noAlert("once the crossing is recorded")
}

func TestAlerts_TheAdminsAreResolvedOncePerRecomputeAndOnlyWhenNeeded(t *testing.T) {
	r := newAlertRig(t)
	admins := &countingAdmins{staticAdmins: staticAdmins{testAdmin}}
	r.mon.alerting.Admins = admins
	r.limitMesh(1000)
	r.limitUserDefault(100)
	r.start(1)
	r.recomputed()
	if n := admins.calls.Load(); n != 0 {
		t.Fatalf("admins resolved %d times with no crossing, want none", n)
	}

	r.spend(testNow, 1000)
	r.spendAs(rbac.EvidenceSystemUserID, "", testNow, 100)
	r.spendAs("", "system:nightly", testNow, 100)
	r.recomputeOnSignal()
	if got := len(r.alerts()); got != 3 {
		t.Fatalf("%d alerts published, want the mesh and both system users", got)
	}
	if n := admins.calls.Load(); n != 1 {
		t.Errorf("admins resolved %d times for three admin alerts in one recompute, want once", n)
	}
}

func TestAlerts_LogsNeverNameAUserByEmailOrIdentityName(t *testing.T) {
	r := newAlertRig(t)
	r.mon.logger = slog.New(slog.NewJSONHandler(r.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r.mon.broker = &failingPublish{Broker: r.brk, failAt: 1}
	r.limitMesh(1000)
	r.limitUserDefault(100)
	r.warnUserDefaultAt(80)
	alice := r.human("alice@example.com")
	r.spendAs(alice, "", testNow, 85)
	r.spendAs("", "bob@example.com", testNow, 100)
	r.spend(testNow, 1000)
	r.start(1)
	r.recomputed()
	r.spendAs(alice, "", testNow, 15)
	r.recomputeOnSignal()
	r.recomputeOnSignal()
	if len(r.alerts()) == 0 {
		t.Fatal("no alert was published, so the canary checked no alert path")
	}
	if n := r.logs.count(t, "DEBUG", msgBudgetLevelCrossed); n == 0 {
		t.Fatal("no user crossing was logged, so the canary checked no user log line")
	}

	r.logs.mu.Lock()
	logged := r.logs.buf.String()
	r.logs.mu.Unlock()
	for _, secret := range []string{"alice@", "bob@", "admin@example.com", "sub-alice"} {
		if strings.Contains(logged, secret) {
			t.Errorf("the logs name %q:\n%s", secret, logged)
		}
	}
}

func TestPercentOf_RoundsTheExactShareDown(t *testing.T) {
	for _, tc := range []struct {
		name          string
		spent, amount int64
		topUp         int64
		want          int
	}{
		{"a share floating point puts just under its integer", 57, 100, 0, 57},
		{"a share between integers", 199, 200, 0, 99},
		{"a top-up in the cap", 150, 100, 100, 75},
		{"nothing spent", 0, 100, 0, 0},
		{"past the cap", 300, 100, 0, 300},
		{"a spend too large to multiply by 100 in int64", math.MaxInt64, math.MaxInt64 / 2, 0, 200},
		{"a zero cap", 5, 0, 0, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := standing{spent: tc.spent, budget: store.LimitingBudget{Amount: new(tc.amount)}, topUp: tc.topUp}
			if got := percentOf(st); got != tc.want {
				t.Errorf("percentOf(%d of %d + %d) = %d, want %d", tc.spent, tc.amount, tc.topUp, got, tc.want)
			}
		})
	}
	if got := percentOf(standing{spent: 10}); got != 0 {
		t.Errorf("percentOf of an unlimited budget = %d, want 0", got)
	}
}

func TestAlerts_TheMeshLevelFellIsLoggedOnlyWhenItFell(t *testing.T) {
	r := newAlertRig(t)
	r.limitMesh(1_000_000)
	r.warnMeshAt(80)
	r.spend(testNow, 850_000)
	r.start(1)
	r.recomputed()
	assertLevel(t, r.onlyAlert("the mesh warning"), budget.LevelWarn)

	r.limitMesh(2_000_000)
	r.recomputeOnSignal()
	if n := r.logs.count(t, "INFO", msgMeshLevelFell); n != 1 {
		t.Fatalf("logged %q %d times after the limit was raised past the warning, want once", msgMeshLevelFell, n)
	}

	r.limitMesh(1_900_000)
	r.recomputeOnSignal()
	r.noAlert("a cap change that leaves the mesh below its warning")
	if n := r.logs.count(t, "INFO", msgMeshLevelFell); n != 1 {
		t.Errorf("logged %q %d times, want no line for a cap change that lowered no level", msgMeshLevelFell, n)
	}
}
