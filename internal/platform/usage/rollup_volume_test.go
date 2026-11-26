// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"io"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendvolume"
)

const reconcileVolumeRowsEnv = "SAM_TEST_ROLLUP_RECONCILE_VOLUME_ROWS"

func TestRollupReconcileVolume(t *testing.T) {
	rows, _ := strconv.Atoi(os.Getenv(reconcileVolumeRowsEnv))
	if rows <= 0 {
		t.Skipf("set %s to a row count to time the rollup reconcile", reconcileVolumeRowsEnv)
	}
	users := 10_000
	if n, _ := strconv.Atoi(os.Getenv(serviceVolumeUsersEnv)); n > 0 {
		users = n
	}
	db := store.TestDB(t)
	ctx := t.Context()
	now := midOctober
	first := governor.RetentionFloor(now)
	reconciledThrough := startOfDay(now.UnixMilli()) - 2*dayMillis
	days := int((reconciledThrough-first)/dayMillis) + 1

	seeded := spendvolume.Timed(func() {
		err := spendvolume.InsertLedger(ctx, db, spendvolume.Ledger{
			Rows: rows, Users: users, UserStride: 7919, Days: days, FirstDay: first,
			Cost: spendvolume.Cost{UncachedInput: 30_000_000, Output: 12_000_000, CacheRead: 750_000, CacheWrite: 300_000},
		})
		if err == nil {
			err = spendvolume.InsertLedgerDetails(ctx, db, spendvolume.DefaultTracePrefix)
		}
		if err == nil {
			err = spendvolume.Analyze(ctx, db)
		}
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	})
	t.Logf("seeded %d calls by %d users over %d days in %s", rows, users, days, seeded)

	rollups := store.NewSpendRollupRepo(db)
	jobs := &rollupJobs{rollups: rollups, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), now: func() time.Time { return now }}
	reconcile := func() {
		if err := jobs.reconcile(ctx, leader.Term{Number: 1}); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}
	attrs := []any{"dialect", db.Dialect(), "rows", rows, "users", users, "days", days}

	t.Log(spendvolume.Format("rollup.reconcile.backfill", spendvolume.Timed(reconcile), attrs...))
	t.Log(spendvolume.Format("rollup.reconcile.insync", spendvolume.Timed(reconcile), attrs...))
	t.Log(spendvolume.Format("rollup.staleDays", spendvolume.Timed(func() {
		stale, err := jobs.staleDays(ctx, first, reconciledThrough+dayMillis)
		if err != nil {
			t.Fatalf("stale days: %v", err)
		}
		if len(stale) != 0 {
			t.Errorf("%d days stale after the backfill, want none", len(stale))
		}
	}), attrs...))

	if _, err := db.ExecContext(ctx, `UPDATE token_spend_rollup_state SET grain_version = grain_version - 1 WHERE id = 1`); err != nil {
		t.Fatalf("age the grain: %v", err)
	}
	t.Log(spendvolume.Format("rollup.reconcile.grainRebuild", spendvolume.Timed(reconcile), attrs...))

	rolled, err := dbutil.QueryVal[int64](ctx, db, `SELECT COALESCE(SUM(calls), 0) FROM token_spend_daily`)
	if err != nil {
		t.Fatalf("sum rolled calls: %v", err)
	}
	if rolled != int64(rows) {
		t.Errorf("rollups hold %d calls, want every seeded call (%d)", rolled, rows)
	}
}
