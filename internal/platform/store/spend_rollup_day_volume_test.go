// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"os"
	"strconv"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendvolume"
)

const (
	dayVolumeRowsEnv  = "SAM_TEST_ROLLUP_DAY_VOLUME_ROWS"
	dayVolumeUsersEnv = "SAM_TEST_ROLLUP_DAY_VOLUME_USERS"
)

func TestSpendRollupDayVolume(t *testing.T) {
	rows, _ := strconv.Atoi(os.Getenv(dayVolumeRowsEnv))
	if rows <= 0 {
		t.Skipf("set %s to a row count to time one day's refresh", dayVolumeRowsEnv)
	}
	users := 10_000
	if n, _ := strconv.Atoi(os.Getenv(dayVolumeUsersEnv)); n > 0 {
		users = n
	}
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	day := rollupTestDay

	seeded := spendvolume.Timed(func() {
		err := spendvolume.InsertLedger(t.Context(), db, spendvolume.Ledger{
			Rows: rows, Users: users, UserStride: 7919, Days: 1, FirstDay: day,
			Cost: spendvolume.Cost{UncachedInput: 1000, Output: 400, CacheRead: 25, CacheWrite: 10},
		})
		if err == nil {
			err = spendvolume.InsertLedgerDetails(t.Context(), db, spendvolume.DefaultTracePrefix)
		}
		if err == nil {
			err = spendvolume.Analyze(t.Context(), db)
		}
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	})
	t.Logf("seeded %d calls by %d users on one day in %s", rows, users, seeded)

	attrs := []any{"dialect", db.Dialect(), "rows", rows, "users", users}
	t.Log(spendvolume.Format("rollup.RefreshDay.first", spendvolume.Timed(func() { refreshDay(t, repo, day, true) }), attrs...))
	groups, err := dbutil.QueryVal[int64](t.Context(), db, `SELECT COUNT(*) FROM token_spend_daily WHERE day = ?`, day)
	if err != nil {
		t.Fatalf("count groups: %v", err)
	}
	attrs = append(attrs, "dailyRows", groups)
	t.Log(spendvolume.Format("rollup.RefreshDay.unchanged", spendvolume.Timed(func() { refreshDay(t, repo, day, false) }), attrs...))
	t.Log(spendvolume.Format("rollup.RefreshDay.rebuild", spendvolume.Timed(func() { refreshDay(t, repo, day, true) }), attrs...))
	t.Log(spendvolume.Format("rollup.callsByDay", spendvolume.Timed(func() {
		if _, err = repo.LedgerCallsByDay(t.Context(), day, day+SpendRollupDayMillis); err != nil {
			t.Fatalf("ledger calls: %v", err)
		}
		if _, err = repo.RollupCallsByDay(t.Context(), day, day+SpendRollupDayMillis); err != nil {
			t.Fatalf("rollup calls: %v", err)
		}
	}), attrs...))

	fileCalls(t, db, rollupCall(func(e *SpendLedgerEntry) { e.CompletedTime = day + 1 }))
	t.Log(spendvolume.Format("rollup.RefreshDay.gainedCall", spendvolume.Timed(func() { refreshDay(t, repo, day, false) }), attrs...))

	rolled, err := dbutil.QueryVal[int64](t.Context(), db, `SELECT COALESCE(SUM(calls), 0) FROM token_spend_daily WHERE day = ?`, day)
	if err != nil {
		t.Fatalf("sum rolled calls: %v", err)
	}
	if rolled != int64(rows)+1 {
		t.Errorf("the day's rollups hold %d calls, want every seeded call and the one added (%d)", rolled, rows+1)
	}
}
