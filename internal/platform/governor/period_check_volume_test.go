// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package governor

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendvolume"
)

const (
	periodDriftVolumeRowsEnv  = "SAM_TEST_PERIOD_DRIFT_VOLUME_ROWS"
	periodDriftVolumeUsersEnv = "SAM_TEST_PERIOD_DRIFT_VOLUME_USERS"
)

const (
	defaultVolumeUsers = 10_000
	volumeUserStride   = 7919
	periodVolumeDays   = 28
)

var volumeCost = spendvolume.Cost{UncachedInput: 30_000_000, Output: 12_000_000, CacheRead: 750_000, CacheWrite: 300_000}

func volumeUsers(t *testing.T, env string) int {
	t.Helper()
	if n, _ := strconv.Atoi(os.Getenv(env)); n > 0 {
		return n
	}
	return defaultVolumeUsers
}

func TestPeriodDriftVolume(t *testing.T) {
	var counts []int
	for _, field := range strings.Split(os.Getenv(periodDriftVolumeRowsEnv), ",") {
		if n, _ := strconv.Atoi(strings.TrimSpace(field)); n > 0 {
			counts = append(counts, n)
		}
	}
	if len(counts) == 0 {
		t.Skipf("set %s to row counts such as 1500000,4500000 to time the period check", periodDriftVolumeRowsEnv)
	}
	users := volumeUsers(t, periodDriftVolumeUsersEnv)
	for _, rows := range counts {
		timePeriodDrift(t, rows, users)
	}
}

func timePeriodDrift(t *testing.T, rows, users int) {
	t.Helper()
	db, err := store.Open(store.TestDBURL(t), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := t.Context()
	periodStart := budget.PeriodStart(midOctober)

	seeded := spendvolume.Timed(func() {
		err = spendvolume.InsertLedger(ctx, db, spendvolume.Ledger{
			Rows: rows, Users: users, UserStride: volumeUserStride, Days: periodVolumeDays, FirstDay: periodStart.UnixMilli(),
			Cost: volumeCost, BatchRows: 500_000,
		})
		if err == nil {
			err = spendvolume.InsertPeriodRowsFromLedger(ctx, db, spendvolume.DefaultTracePrefix)
		}
		if err == nil {
			err = spendvolume.Analyze(ctx, db)
		}
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	})
	t.Logf("seeded %d calls by %d users in %s", rows, users, seeded)

	repo := store.NewSpendLedgerRepo(db)
	attrs := func(drift int) []any {
		return []any{"dialect", db.Dialect(), "rows", rows, "users", users, "drift", drift}
	}
	check := func() ([]store.BudgetPeriodDrift, time.Duration) {
		var drift []store.BudgetPeriodDrift
		took := spendvolume.Timed(func() { drift, err = repo.PeriodDrift(ctx, periodStart.UnixMilli(), periodCheckTimeout) })
		if err != nil {
			t.Fatalf("period drift at %d rows: %v", rows, err)
		}
		return drift, took
	}

	drift, took := check()
	t.Log(spendvolume.Format("PeriodDrift.agreeing", took, append(attrs(len(drift)), "timeoutShare", share(took))...))
	if len(drift) != 0 {
		t.Errorf("drift at %d rows = %d subjects, want the seeded period rows to agree with the ledger", rows, len(drift))
	}

	if _, err := db.ExecContext(ctx, `UPDATE token_spend_budget_period SET total_cost = total_cost + 1
		WHERE subject_kind = 'id' AND subject_ref = ?`, spendvolume.UserID(0)); err != nil {
		t.Fatalf("drift one user: %v", err)
	}
	drift, took = check()
	t.Log(spendvolume.Format("PeriodDrift.oneUserOff", took, append(attrs(len(drift)), "timeoutShare", share(took))...))
	if len(drift) != 1 {
		t.Errorf("drift after one user's row moved = %d subjects, want 1", len(drift))
	}
}

func share(took time.Duration) string {
	return strconv.FormatFloat(float64(took)/float64(periodCheckTimeout), 'f', 4, 64)
}
