// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendvolume"
)

const volumeRowsEnv = "SAM_TEST_SPEND_VOLUME_ROWS"

const volumeDays = 30

func TestSpendRollupVolume(t *testing.T) {
	rows, _ := strconv.Atoi(os.Getenv(volumeRowsEnv))
	if rows <= 0 {
		t.Skipf("set %s to a row count to time the rollups", volumeRowsEnv)
	}
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	first := rollupTestDay
	last := first + (volumeDays-1)*SpendRollupDayMillis

	elapsed := spendvolume.Timed(func() { seedVolumeLedger(t, db, rows, first) })
	t.Logf("seeded %d ledger rows over %d days on %s in %s", rows, volumeDays, db.Dialect(), elapsed)

	backfill := spendvolume.Timed(func() {
		for d := last; d >= first; d -= SpendRollupDayMillis {
			refreshDay(t, repo, d, true)
		}
	})
	dailyRows, err := dbutil.QueryVal[int64](t.Context(), db, `SELECT COUNT(*) FROM token_spend_daily`)
	if err != nil {
		t.Fatalf("count rollup rows: %v", err)
	}
	t.Logf("backfilled %d days into %d daily rows in %s", volumeDays, dailyRows, backfill)

	skipCheck := spendvolume.Timed(func() {
		if _, err := repo.LedgerCallsByDay(t.Context(), last-SpendRollupDayMillis, last+SpendRollupDayMillis); err != nil {
			t.Fatalf("ledger calls: %v", err)
		}
		if _, err := repo.RollupCallsByDay(t.Context(), last-SpendRollupDayMillis, last+SpendRollupDayMillis); err != nil {
			t.Fatalf("rollup calls: %v", err)
		}
	})
	t.Logf("skip check over two days: %s", skipCheck)

	unchanged := spendvolume.Timed(func() { refreshDay(t, repo, last, false) })
	t.Logf("recent pass over an unchanged day: %s", unchanged)

	fileCalls(t, db, rollupCall(func(e *SpendLedgerEntry) { e.CompletedTime = last + 1 }))
	grown := spendvolume.Timed(func() { refreshDay(t, repo, last, false) })
	t.Logf("recent pass over a day that gained a call: %s", grown)

	var total, calls int64
	month := spendvolume.Timed(func() {
		if err := db.QueryRowContext(t.Context(), `SELECT CAST(COALESCE(SUM(total_cost), 0) AS BIGINT), CAST(COALESCE(SUM(calls), 0) AS BIGINT)
			FROM token_spend_daily WHERE day >= ? AND day <= ?`, first, last).Scan(&total, &calls); err != nil {
			t.Fatalf("month totals: %v", err)
		}
	})
	byAgent := spendvolume.Timed(func() {
		if _, err := dbutil.QueryVals[int64](t.Context(), db, `SELECT CAST(SUM(total_cost) AS BIGINT) FROM token_spend_daily
			WHERE day >= ? AND day <= ? GROUP BY agent_id, agent_name ORDER BY 1 DESC LIMIT 25`, first, last); err != nil {
			t.Fatalf("month by agent: %v", err)
		}
	})
	t.Logf("month totals in %s, month top agents in %s", month, byAgent)

	usage := NewSpendUsageRepo(db, nil)
	monthEnd := last + SpendRollupDayMillis
	steps := map[string]time.Duration{}
	pageLoad := spendvolume.Timed(func() {
		steps["totals"] = spendvolume.Timed(func() {
			if _, err := usage.Totals(t.Context(), UsageSubject{}, first, monthEnd); err != nil {
				t.Fatalf("summary totals: %v", err)
			}
		})
		steps["daily"] = spendvolume.Timed(func() {
			if _, err := usage.Daily(t.Context(), UsageSubject{}, first, monthEnd); err != nil {
				t.Fatalf("summary daily: %v", err)
			}
		})
		for name, q := range map[string]UsageListQuery{
			"top agents":    {FromDay: first, ToDay: monthEnd, Sort: UsageSortCost, Descending: true, Limit: 25, AllSubjects: true},
			"bottom agents": {FromDay: first, ToDay: monthEnd, Sort: UsageSortCost, Limit: 5, AllSubjects: true},
		} {
			steps[name] = spendvolume.Timed(func() {
				if _, err := usage.List(t.Context(), UsageAgents, q); err != nil {
					t.Fatalf("agents list: %v", err)
				}
			})
		}
	})
	t.Logf("page load steps: %v", steps)
	everyUser := spendvolume.Timed(func() {
		if _, err := usage.List(t.Context(), UsageUsers, UsageListQuery{FromDay: first, ToDay: last + SpendRollupDayMillis,
			Sort: UsageSortCost, Descending: true, AllSubjects: true, Unpaged: true}); err != nil {
			t.Fatalf("every user: %v", err)
		}
	})
	t.Logf("page load (summary, daily, both agent lists) over the month: %s; every user, unpaged: %s", pageLoad, everyUser)

	if calls != int64(rows)+1 {
		t.Errorf("rolled calls = %d, want every seeded call and the one added (%d)", calls, rows+1)
	}
	for name, d := range map[string]time.Duration{"page load": pageLoad, "every user": everyUser, "skip check": skipCheck, "recent pass": grown, "month totals": month, "month top agents": byAgent} {
		if d > 30*time.Second {
			t.Errorf("%s took %s, far past what a dashboard can wait", name, d)
		}
	}
}

func seedVolumeLedger(t *testing.T, db *DB, rows int, first int64) {
	t.Helper()
	err := spendvolume.InsertLedger(t.Context(), db, spendvolume.Ledger{
		Rows: rows, Users: 500, Days: volumeDays, FirstDay: first,
		Cost: spendvolume.Cost{UncachedInput: 1000, Output: 400, CacheRead: 25, CacheWrite: 10},
	})
	if err != nil {
		t.Fatalf("seed ledger: %v", err)
	}
	if err := spendvolume.InsertLedgerDetails(t.Context(), db, spendvolume.DefaultTracePrefix); err != nil {
		t.Fatalf("seed details: %v", err)
	}
}
