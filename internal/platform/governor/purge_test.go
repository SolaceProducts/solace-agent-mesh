// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const dayMillis = store.SpendRollupDayMillis

func realPurger(db *store.DB, now time.Time) purger {
	ledger := store.NewSpendLedgerRepo(db)
	return purger{
		targets: PurgeTargets{
			Ledger:      ledger,
			Periods:     ledger,
			Rollups:     store.NewSpendRollupRepo(db),
			TopUps:      store.NewBudgetTopUpsRepo(db),
			AlertStates: store.NewBudgetAlertStatesRepo(db),
		},
		logger: discardLogger,
		now:    func() time.Time { return now },
	}
}

func TestPurge_DeletesOnlyLedgerRowsOlderThanRetentionFromTheGovernorsClock(t *testing.T) {
	db := store.TestDB(t)
	ledger := store.NewSpendLedgerRepo(db)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-RetentionPeriod)
	byAge := map[string]store.SpendLedgerEntry{
		"long expired":          ledgerEntry(cutoff.Add(-30 * 24 * time.Hour)),
		"just expired":          ledgerEntry(cutoff.Add(-time.Millisecond)),
		"exactly at the cutoff": ledgerEntry(cutoff),
		"just inside":           ledgerEntry(cutoff.Add(time.Millisecond)),
		"recent":                ledgerEntry(now),
	}
	var entries []store.SpendLedgerEntry
	for _, e := range byAge {
		entries = append(entries, e)
	}
	if _, err := ledger.Insert(t.Context(), entries); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := realPurger(db, now).run(t.Context(), leader.Term{}); err != nil {
		t.Fatalf("purge: %v", err)
	}

	for name, e := range byAge {
		wantKept := name != "long expired" && name != "just expired"
		if kept := readRow(t, db, e.ID) != nil; kept != wantKept {
			t.Errorf("%s: kept = %t, want %t", name, kept, wantKept)
		}
	}
}

func TestPurge_DeletesEndedBudgetPeriodsEvenWhenTheLedgerStepFails(t *testing.T) {
	db := store.TestDB(t)
	ledger := store.NewSpendLedgerRepo(db)
	if _, err := ledger.Insert(t.Context(), []store.SpendLedgerEntry{
		periodCheckEntry(midOctober, 5),
		periodCheckEntry(firstOfNovember, 7),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	p := realPurger(db, novemberStart.Add(RetentionPeriod))
	ledgerErr := errors.New("ledger unavailable")
	p.targets.Ledger = ledgerFunc{delete: func(context.Context, time.Time, int) (int64, error) { return 0, ledgerErr }}

	if err := p.run(t.Context(), leader.Term{}); !errors.Is(err, ledgerErr) {
		t.Fatalf("run error = %v, want the ledger failure", err)
	}

	starts, err := dbutil.QueryVals[int64](t.Context(), db, `SELECT DISTINCT period_start FROM token_spend_budget_period`)
	if err != nil {
		t.Fatalf("read budget periods: %v", err)
	}
	if len(starts) != 1 || starts[0] != novemberStart.UnixMilli() {
		t.Errorf("budget periods left start at %v, want only November's (%d)", starts, novemberStart.UnixMilli())
	}
}

func TestPurge_DropsRollupDaysBeforeTheRetentionFloor(t *testing.T) {
	db := store.TestDB(t)
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	floor := RetentionFloor(now)
	partlyPurged, firstRetained := floor-dayMillis, floor
	rollups := store.NewSpendRollupRepo(db)
	for _, day := range []int64{partlyPurged, firstRetained} {
		if _, err := store.NewSpendLedgerRepo(db).Insert(t.Context(), []store.SpendLedgerEntry{ledgerEntry(time.UnixMilli(day + 1))}); err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
		if err := rollups.RefreshDay(t.Context(), day, false); err != nil {
			t.Fatalf("seed rollup: %v", err)
		}
	}

	if err := realPurger(db, now).run(t.Context(), leader.Term{}); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if n := rolledCalls(t, db, partlyPurged); n != 0 {
		t.Errorf("rolled calls on the day before the floor = %d, want the day dropped", n)
	}
	if n := rolledCalls(t, db, firstRetained); n != 1 {
		t.Errorf("rolled calls on the first wholly retained day = %d, want it kept", n)
	}
}

func TestPurge_DeletesTopUpsOnceTheirBudgetPeriodHasRolledOver(t *testing.T) {
	db := store.TestDB(t)
	if _, err := db.ExecContext(t.Context(), `UPDATE budgets SET amount = 5000 WHERE id = ?`, store.SeededMeshBudgetID); err != nil {
		t.Fatalf("limit the mesh budget: %v", err)
	}
	october := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	november := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC)
	octoberTopUp := grantMeshTopUp(t, db, october)
	novemberTopUp := grantMeshTopUp(t, db, november)

	withinTheGrace := budget.PeriodEnd(october).Add(topUpExpiryGrace - time.Millisecond)
	if err := realPurger(db, withinTheGrace).run(t.Context(), leader.Term{}); err != nil {
		t.Fatalf("purge within the grace after the rollover: %v", err)
	}
	if !topUpExists(t, db, octoberTopUp) {
		t.Error("a purge within a minute of the rollover deleted October's top-up, which a reader whose clock runs behind still counts")
	}

	if err := realPurger(db, november).run(t.Context(), leader.Term{}); err != nil {
		t.Fatalf("purge after the rollover: %v", err)
	}
	if topUpExists(t, db, octoberTopUp) {
		t.Error("October's top-up survived the purge after the month rolled over")
	}
	if !topUpExists(t, db, novemberTopUp) {
		t.Error("the purge deleted November's active top-up")
	}
}

func TestPurge_DeletesAlertStatesOfPeriodsThatHaveEnded(t *testing.T) {
	db := store.TestDB(t)
	alerts := store.NewBudgetAlertStatesRepo(db)
	november := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	october := budget.PeriodStart(november.AddDate(0, -1, 0)).UnixMilli()
	current := budget.PeriodStart(november).UnixMilli()
	for _, periodStart := range []int64{october, current} {
		if err := alerts.SetAlertState(t.Context(), store.BudgetAlertState{
			SubjectType: store.BudgetSubjectMesh, SubjectRef: store.BudgetMeshSubjectRef, PeriodStart: periodStart, AlertedLevel: "warn", CapKey: "k", UpdatedTime: 1,
		}); err != nil {
			t.Fatalf("seed alert state: %v", err)
		}
	}

	if err := realPurger(db, november).run(t.Context(), leader.Term{}); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if got, err := alerts.AlertStates(t.Context(), october); err != nil || len(got) != 0 {
		t.Errorf("October's alert states after the purge = %+v (%v), want none", got, err)
	}
	if got, err := alerts.AlertStates(t.Context(), current); err != nil || len(got) != 1 {
		t.Errorf("November's alert states after the purge = %+v (%v), want the current period's state kept", got, err)
	}
}

type fakePurge struct {
	failing                 map[string]error
	ledgerCutoffs           []time.Time
	periodCutoffs           []time.Time
	rollupFloors, topUpNows []int64
	alertPeriodStarts       []int64
}

func (f *fakePurge) targets() PurgeTargets {
	return PurgeTargets{
		Ledger: ledgerFunc{delete: func(_ context.Context, before time.Time, _ int) (int64, error) {
			f.ledgerCutoffs = append(f.ledgerCutoffs, before)
			return 0, f.failing["spend ledger"]
		}},
		Periods: endedBudgetPeriodsFunc(func(before time.Time) (int64, error) {
			f.periodCutoffs = append(f.periodCutoffs, before)
			return 0, f.failing["budget periods"]
		}),
		Rollups: rollupDaysFunc(func(day int64) (int64, error) {
			f.rollupFloors = append(f.rollupFloors, day)
			return 0, f.failing["spend rollups"]
		}),
		TopUps: expiredTopUpsFunc(func(nowMs int64) (int64, error) {
			f.topUpNows = append(f.topUpNows, nowMs)
			return 0, f.failing["budget top-ups"]
		}),
		AlertStates: pastAlertStatesFunc(func(periodStart int64) (int64, error) {
			f.alertPeriodStarts = append(f.alertPeriodStarts, periodStart)
			return 0, f.failing["budget alert states"]
		}),
	}
}

func TestPurge_AFailedStepDoesNotStopTheOthersAndFailsTheRunNamingIt(t *testing.T) {
	steps := []string{"spend ledger", "budget periods", "spend rollups", "budget top-ups", "budget alert states"}
	cases := map[string][]string{"every step": steps}
	for _, step := range steps {
		cases[step] = []string{step}
	}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for name, failing := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakePurge{failing: map[string]error{}}
			for _, step := range failing {
				f.failing[step] = errors.New(step + " unavailable")
			}
			p := purger{targets: f.targets(), logger: discardLogger, now: func() time.Time { return now }}

			err := p.run(t.Context(), leader.Term{})

			for _, step := range failing {
				if !errors.Is(err, f.failing[step]) || !strings.Contains(err.Error(), "purge "+step) {
					t.Errorf("run error %v does not carry the %s failure, so the leader would not report it", err, step)
				}
			}
			if len(f.ledgerCutoffs) != 1 || len(f.periodCutoffs) != 1 || len(f.rollupFloors) != 1 || len(f.topUpNows) != 1 || len(f.alertPeriodStarts) != 1 {
				t.Errorf("steps ran %d, %d, %d, %d and %d times, want every step once",
					len(f.ledgerCutoffs), len(f.periodCutoffs), len(f.rollupFloors), len(f.topUpNows), len(f.alertPeriodStarts))
			}
		})
	}
}

func TestPurge_StepsGetTheirCutoffsFromTheGovernorsClock(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	f := &fakePurge{}
	p := purger{targets: f.targets(), logger: discardLogger, now: func() time.Time { return now }}

	if err := p.run(t.Context(), leader.Term{}); err != nil {
		t.Fatalf("purge: %v", err)
	}

	cutoff := now.Add(-RetentionPeriod)
	if !f.ledgerCutoffs[0].Equal(cutoff) || !f.periodCutoffs[0].Equal(cutoff) {
		t.Errorf("ledger and period cutoffs = %v and %v, want %v", f.ledgerCutoffs[0], f.periodCutoffs[0], cutoff)
	}
	if f.rollupFloors[0] != RetentionFloor(now) {
		t.Errorf("rollup floor = %d, want %d", f.rollupFloors[0], RetentionFloor(now))
	}
	if want := now.Add(-topUpExpiryGrace).UnixMilli(); f.topUpNows[0] != want {
		t.Errorf("top-up expiry cutoff = %d, want %d", f.topUpNows[0], want)
	}
	if want := budget.PeriodStart(now).UnixMilli(); f.alertPeriodStarts[0] != want {
		t.Errorf("alert state cutoff = %d, want the current period's start %d", f.alertPeriodStarts[0], want)
	}
}

func TestPurge_ATermEndingMidRunStopsTheStepsAndLogsTheEarlierFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	logs := &logCapture{}
	f := &fakePurge{failing: map[string]error{"spend ledger": errors.New("disk full")}}
	targets := f.targets()
	targets.Periods = endedBudgetPeriodsFunc(func(time.Time) (int64, error) {
		cancel()
		return 0, context.Canceled
	})
	p := purger{targets: targets, logger: logs.logger(), now: time.Now}

	if err := p.run(ctx, leader.Term{}); err == nil {
		t.Error("a run cut short reported success")
	}

	if len(f.rollupFloors) != 0 || len(f.topUpNows) != 0 {
		t.Errorf("rollup and top-up steps ran %d and %d times after the term ended, want none", len(f.rollupFloors), len(f.topUpNows))
	}
	out := logs.String()
	if !strings.Contains(out, "disk full") {
		t.Errorf("the ledger failure before the term ended was not logged; logs:\n%s", out)
	}
	if strings.Contains(out, "budget periods") {
		t.Errorf("the cancellation itself was logged as a failure; logs:\n%s", out)
	}
}

func TestRetentionFloor_IsTheFirstDayWhollyInsideRetention(t *testing.T) {
	midnight := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		now  time.Time
		want int64
	}{
		"mid-day":          {now: midnight.Add(10 * time.Hour), want: midnight.Add(-RetentionPeriod + 24*time.Hour).UnixMilli()},
		"exactly midnight": {now: midnight, want: midnight.Add(-RetentionPeriod).UnixMilli()},
	} {
		t.Run(name, func(t *testing.T) {
			if got := RetentionFloor(tc.now); got != tc.want {
				t.Errorf("floor = %s, want %s", time.UnixMilli(got).UTC(), time.UnixMilli(tc.want).UTC())
			}
		})
	}
}

type endedBudgetPeriodsFunc func(before time.Time) (int64, error)

func (f endedBudgetPeriodsFunc) DeleteBudgetPeriodsEndedBy(_ context.Context, before time.Time) (int64, error) {
	return f(before)
}

type rollupDaysFunc func(day int64) (int64, error)

func (f rollupDaysFunc) DeleteDaysBefore(_ context.Context, day int64) (int64, error) { return f(day) }

type pastAlertStatesFunc func(periodStart int64) (int64, error)

func (f pastAlertStatesFunc) DeleteAlertStatesBefore(_ context.Context, periodStart int64) (int64, error) {
	return f(periodStart)
}

type expiredTopUpsFunc func(nowMs int64) (int64, error)

func (f expiredTopUpsFunc) DeleteExpired(_ context.Context, nowMs int64) (int64, error) {
	return f(nowMs)
}

func grantMeshTopUp(t *testing.T, db *store.DB, at time.Time) string {
	t.Helper()
	granted, err := store.NewBudgetTopUpsRepo(db).Grant(t.Context(), store.BudgetTopUpGrant{
		SubjectType: store.BudgetSubjectMesh, SubjectRef: store.BudgetMeshSubjectRef, Amount: 100,
		ExpiresTime: budget.PeriodEnd(at).UnixMilli(), Actor: "admin", NowMs: at.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("grant a mesh top-up at %s: %v", at, err)
	}
	return granted.TopUp.ID
}

func topUpExists(t *testing.T, db *store.DB, id string) bool {
	t.Helper()
	n, err := dbutil.QueryVal[int](t.Context(), db, `SELECT COUNT(*) FROM budget_top_ups WHERE id = ?`, id)
	if err != nil {
		t.Fatalf("read top-up: %v", err)
	}
	return n == 1
}

func rolledCalls(t *testing.T, db *store.DB, day int64) int64 {
	t.Helper()
	n, err := dbutil.QueryVal[int64](t.Context(), db, `SELECT COALESCE(SUM(calls), 0) FROM token_spend_daily WHERE day = ?`, day)
	if err != nil {
		t.Fatalf("read rollup: %v", err)
	}
	return n
}
