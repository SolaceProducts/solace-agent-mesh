// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendvolume"
)

const (
	purgeVolumeRowsEnv  = "SAM_TEST_PURGE_VOLUME_ROWS_PER_DAY"
	purgeVolumeUsersEnv = "SAM_TEST_PURGE_VOLUME_USERS"
)

const retainedVolumeDays = 2

var purgeNow = time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)

type stepTimes struct{ ledger, periods, rollups, alertStates time.Duration }

type timedLedger struct {
	Ledger
	took *time.Duration
}

func (l timedLedger) DeleteOlderThan(ctx context.Context, before time.Time, batch int) (int64, error) {
	start := time.Now()
	defer func() { *l.took += time.Since(start) }()
	return l.Ledger.DeleteOlderThan(ctx, before, batch)
}

type timedPeriods struct {
	EndedBudgetPeriods
	took *time.Duration
}

func (p timedPeriods) DeleteBudgetPeriodsEndedBy(ctx context.Context, before time.Time) (int64, error) {
	start := time.Now()
	defer func() { *p.took += time.Since(start) }()
	return p.EndedBudgetPeriods.DeleteBudgetPeriodsEndedBy(ctx, before)
}

type timedRollups struct {
	RollupDays
	took *time.Duration
}

func (r timedRollups) DeleteDaysBefore(ctx context.Context, day int64) (int64, error) {
	start := time.Now()
	defer func() { *r.took += time.Since(start) }()
	return r.RollupDays.DeleteDaysBefore(ctx, day)
}

type timedAlertStates struct {
	PastAlertStates
	took *time.Duration
}

func (a timedAlertStates) DeleteAlertStatesBefore(ctx context.Context, periodStart int64) (int64, error) {
	start := time.Now()
	defer func() { *a.took += time.Since(start) }()
	return a.PastAlertStates.DeleteAlertStatesBefore(ctx, periodStart)
}

func TestPurgeVolume(t *testing.T) {
	perDay, _ := strconv.Atoi(os.Getenv(purgeVolumeRowsEnv))
	if perDay <= 0 {
		t.Skipf("set %s to rows per day, such as 50000, to time the purge", purgeVolumeRowsEnv)
	}
	users := volumeUsers(t, purgeVolumeUsersEnv)
	for _, pastDays := range []int{1, 7} {
		timePurge(t, perDay, users, pastDays)
	}
}

func timePurge(t *testing.T, perDay, users, pastDays int) {
	t.Helper()
	db, err := store.Open(store.TestDBURL(t), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	cutoff := purgeNow.Add(-RetentionPeriod)
	floor := RetentionFloor(purgeNow)
	cutoffDay := floor - dayMillis
	pastRows, retainedRows := perDay*pastDays, perDay*retainedVolumeDays
	currentPeriod := budget.PeriodStart(purgeNow).UnixMilli()

	seed := func(rows, days int, first int64) {
		err := spendvolume.InsertLedger(ctx, db, spendvolume.Ledger{
			Rows: rows, Users: users, UserStride: volumeUserStride, Days: days, FirstDay: first,
			Cost: volumeCost, BatchRows: 500_000,
		})
		if err != nil {
			t.Fatalf("seed ledger: %v", err)
		}
	}
	seeded := spendvolume.Timed(func() {
		seed(pastRows, pastDays, cutoffDay-int64(pastDays)*dayMillis)
		seed(retainedRows, retainedVolumeDays, floor)
		err := spendvolume.InsertLedgerDetails(ctx, db, spendvolume.DefaultTracePrefix)
		if err == nil {
			err = spendvolume.InsertPeriodRowsFromLedger(ctx, db, spendvolume.DefaultTracePrefix)
		}
		if err == nil {
			err = spendvolume.InsertAlertStatesFromPeriods(ctx, db)
		}
		if err == nil {
			err = store.NewBudgetAlertStatesRepo(db).SetAlertState(ctx, store.BudgetAlertState{
				SubjectType: store.BudgetSubjectMesh, SubjectRef: store.BudgetMeshSubjectRef,
				PeriodStart: currentPeriod, AlertedLevel: "warn", CapKey: "perf-volume", UpdatedTime: currentPeriod,
			})
		}
		if err == nil {
			err = spendvolume.Analyze(ctx, db)
		}
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	})
	rollups := store.NewSpendRollupRepo(db)
	rolled := spendvolume.Timed(func() {
		for day := cutoffDay - int64(pastDays)*dayMillis; day < floor+retainedVolumeDays*dayMillis; day += dayMillis {
			if day == cutoffDay {
				continue
			}
			if err := rollups.RefreshDay(ctx, day, true); err != nil {
				t.Fatalf("roll up %d: %v", day, err)
			}
		}
	})
	t.Logf("seeded %d calls past retention and %d retained in %s, rolled up in %s", pastRows, retainedRows, seeded, rolled)

	count := func(query string, args ...any) int64 {
		n, err := dbutil.QueryVal[int64](ctx, db, query, args...)
		if err != nil {
			t.Fatalf("count (%s): %v", query, err)
		}
		return n
	}
	endedPeriods := count(`SELECT COUNT(*) FROM token_spend_budget_period WHERE period_end <= ?`, cutoff.UnixMilli())
	allPeriods := count(`SELECT COUNT(*) FROM token_spend_budget_period`)
	pastRollups := count(`SELECT COUNT(*) FROM token_spend_daily WHERE day < ?`, floor)
	pastAlertStates := count(`SELECT COUNT(*) FROM budget_alert_state WHERE period_start < ?`, currentPeriod)

	var times stepTimes
	ledger := store.NewSpendLedgerRepo(db)
	purge := purger{
		targets: PurgeTargets{
			Ledger:      timedLedger{Ledger: ledger, took: &times.ledger},
			Periods:     timedPeriods{EndedBudgetPeriods: ledger, took: &times.periods},
			Rollups:     timedRollups{RollupDays: rollups, took: &times.rollups},
			TopUps:      store.NewBudgetTopUpsRepo(db),
			AlertStates: timedAlertStates{PastAlertStates: store.NewBudgetAlertStatesRepo(db), took: &times.alertStates},
		},
		logger: discardLogger,
		now:    func() time.Time { return purgeNow },
	}
	total := spendvolume.Timed(func() {
		if err := purge.run(ctx, leader.Term{Number: 1}); err != nil {
			t.Fatalf("purge: %v", err)
		}
	})

	attrs := []any{"dialect", db.Dialect(), "pastDays", pastDays, "rowsPerDay", perDay, "rows", pastRows, "users", users}
	t.Log(spendvolume.Format("purge.total", total, attrs...))
	t.Log(spendvolume.Format("purge.ledger", times.ledger, attrs...))
	t.Log(spendvolume.Format("purge.periods", times.periods, append(attrs, "periodRows", endedPeriods)...))
	t.Log(spendvolume.Format("purge.rollups", times.rollups, append(attrs, "rollupRows", pastRollups)...))
	t.Log(spendvolume.Format("purge.alertStates", times.alertStates, append(attrs, "alertStateRows", pastAlertStates)...))

	if got := count(`SELECT COUNT(*) FROM token_spend_ledger`); got != int64(retainedRows) {
		t.Errorf("ledger rows after the purge of %d days = %d, want only the %d retained", pastDays, got, retainedRows)
	}
	if got := count(`SELECT COUNT(*) FROM token_spend_ledger_details`); got != int64(retainedRows) {
		t.Errorf("details rows after the purge of %d days = %d, want only the %d retained", pastDays, got, retainedRows)
	}
	if got := count(`SELECT COUNT(*) FROM token_spend_daily WHERE day < ?`, floor); got != 0 {
		t.Errorf("rollup rows before the retention floor = %d, want none", got)
	}
	if got := count(`SELECT COUNT(*) FROM token_spend_daily WHERE day >= ?`, floor); got == 0 {
		t.Error("no rollup rows from the retained days remain")
	}
	if got, want := count(`SELECT COUNT(*) FROM token_spend_budget_period`), allPeriods-endedPeriods; got != want {
		t.Errorf("budget period rows after the purge = %d, want the %d of periods still running", got, want)
	}
	if got := count(`SELECT COUNT(*) FROM budget_alert_state WHERE period_start < ?`, currentPeriod); got != 0 {
		t.Errorf("alert states of past periods after the purge = %d, want none", got)
	}
	if got := count(`SELECT COUNT(*) FROM budget_alert_state WHERE period_start = ?`, currentPeriod); got != 1 {
		t.Errorf("alert states of the current period after the purge = %d, want the 1 seeded", got)
	}
	if pastAlertStates == 0 {
		t.Error("no alert state of a past period was seeded; the test no longer exercises that step")
	}
	if pastDays > 1 && endedPeriods == 0 {
		t.Errorf("no budget period ended by the cutoff in a %d-day purge; the test no longer exercises that step", pastDays)
	}
}
