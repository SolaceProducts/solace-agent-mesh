// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendvolume"
)

const (
	serviceVolumeRowsEnv  = "SAM_TEST_USAGE_VOLUME_ROWS"
	serviceVolumeUsersEnv = "SAM_TEST_USAGE_VOLUME_USERS"
)

const controlTimeout = 10 * time.Second

func TestServiceVolume(t *testing.T) {
	rows, _ := strconv.Atoi(os.Getenv(serviceVolumeRowsEnv))
	if rows <= 0 {
		t.Skipf("set %s to a row count to time the usage service", serviceVolumeRowsEnv)
	}
	users := 10_000
	if n, _ := strconv.Atoi(os.Getenv(serviceVolumeUsersEnv)); n > 0 {
		users = n
	}
	db := store.TestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("the service volume test sizes a server tenant; set SAM_TEST_PLATFORM_DB_URL")
	}
	now := midOctober
	first, today := governor.RetentionFloor(now), startOfDay(now.UnixMilli())
	days := int((today-first)/dayMillis) + 1

	seeded := spendvolume.Timed(func() { seedServiceVolume(t, db, rows, users, days, first) })
	t.Logf("seeded %d calls by %d users over %d days in %s", rows, users, days, seeded)
	rollups := store.NewSpendRollupRepo(db)
	backfill := spendvolume.Timed(func() {
		for d := today; d >= first; d -= dayMillis {
			if err := rollups.RefreshDay(t.Context(), d, true); err != nil {
				t.Fatalf("roll up %d: %v", d, err)
			}
		}
	})
	if err := rollups.MarkRefreshed(t.Context(), now.UnixMilli()); err != nil {
		t.Fatalf("mark refreshed: %v", err)
	}
	if err := rollups.MarkReconciled(t.Context(), now.UnixMilli()); err != nil {
		t.Fatalf("mark reconciled: %v", err)
	}
	if err := spendvolume.Analyze(t.Context(), db); err != nil {
		t.Fatalf("analyze the rollups: %v", err)
	}
	t.Logf("backfilled %d days in %s", days, backfill)

	svc := &Service{
		Usage:     store.NewSpendUsageRepo(db, nil),
		Budgets:   Budgets{Assignments: store.NewBudgetAssignmentsRepo(db), Budgets: store.NewBudgetsRepo(db), TopUps: store.NewBudgetTopUpsRepo(db)},
		Workflows: store.NewWorkflowRepo(db),
		Logger:    slog.Default(),
		Now:       func() time.Time { return now },
	}
	ctx := t.Context()
	someone := store.UsageSubject{Kind: store.UsageUsers, ID: spendvolume.UserID(7)}
	page := func(kind store.UsageKind, period Period, sort ListSort, desc bool, size int) func() error {
		return func() error {
			_, err := svc.List(ctx, kind, ListRequest{Window: WindowRequest{Period: period}, AllSubjects: true,
				Sort: sort, Descending: desc, PageNumber: 1, PageSize: size})
			return err
		}
	}
	calls := []struct {
		name string
		call func() error
	}{
		{"summary, this month", func() error { _, err := svc.Summary(ctx, WindowRequest{}); return err }},
		{"summary, last 90 days", func() error {
			_, err := svc.Summary(ctx, WindowRequest{Period: PeriodLast90Days})
			return err
		}},
		{"savings, this month", func() error { _, err := svc.Savings(ctx, WindowRequest{}); return err }},
		{"savings, last 90 days", func() error {
			_, err := svc.Savings(ctx, WindowRequest{Period: PeriodLast90Days})
			return err
		}},
		{"agents top 25, this month", page(store.UsageAgents, PeriodThisMonth, "cost", true, 25)},
		{"agents bottom 5, this month", page(store.UsageAgents, PeriodThisMonth, "cost", false, 5)},
		{"agents top 25, last 90 days", page(store.UsageAgents, PeriodLast90Days, "cost", true, 25)},
		{"users top 25, this month", page(store.UsageUsers, PeriodThisMonth, "cost", true, 25)},
		{"users top 25, last month", page(store.UsageUsers, PeriodLastMonth, "cost", true, 25)},
		{"users top 25, last 90 days", page(store.UsageUsers, PeriodLast90Days, "cost", true, 25)},
		{"users by budget usage, this month", page(store.UsageUsers, PeriodThisMonth, ListSort(SortByBudgetUsage), true, 25)},
		{"users by budget usage, last 90 days", page(store.UsageUsers, PeriodLast90Days, ListSort(SortByBudgetUsage), true, 25)},
		{"users by budget, this month", page(store.UsageUsers, PeriodThisMonth, ListSort(SortByBudget), true, 25)},
		{"user panel, this month", func() error { _, err := svc.User(ctx, someone, WindowRequest{}, ScopeTenant); return err }},
		{"user panel, last 90 days", func() error {
			_, err := svc.User(ctx, someone, WindowRequest{Period: PeriodLast90Days}, ScopeTenant)
			return err
		}},
		{"self months", func() error { _, err := svc.Months(ctx, someone); return err }},
	}
	for _, c := range calls {
		var err error
		took := spendvolume.Timed(func() { err = c.call() })
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		t.Logf("%-38s %s", c.name, took)
		if took > controlTimeout {
			t.Errorf("%s took %s, past the %s control timeout", c.name, took, controlTimeout)
		}
	}
}

func seedServiceVolume(t *testing.T, db *store.DB, rows, users, days int, first int64) {
	t.Helper()
	ctx := t.Context()
	steps := []struct {
		name string
		run  func() error
	}{
		{"users", func() error { return spendvolume.InsertUsers(ctx, db, spendvolume.Users{Count: users}) }},
		{"tier default", func() error { return spendvolume.SetTierDefault(ctx, db, 1_000_000_000_000) }},
		{"budgets", func() error { return spendvolume.InsertBudgets(ctx, db, spendvolume.Budgets{}) }},
		{"assignments", func() error { return spendvolume.InsertAssignments(ctx, db, users) }},
		{"ledger", func() error {
			return spendvolume.InsertLedger(ctx, db, spendvolume.Ledger{
				Rows: rows, Users: users, UserStride: 7919, Days: days, FirstDay: first,
				Cost: spendvolume.Cost{UncachedInput: 30_000_000, Output: 12_000_000, CacheRead: 750_000, CacheWrite: 300_000},
			})
		}},
		{"details", func() error { return spendvolume.InsertLedgerDetails(ctx, db, spendvolume.DefaultTracePrefix) }},
		{"analyze", func() error { return spendvolume.Analyze(ctx, db) }},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			t.Fatalf("seed %s: %v", s.name, err)
		}
	}
}
