// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spendvolume_test

import (
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/spendvolume"
)

const (
	seedPrefix = "perfseed-"
	keepPrefix = "keep-"
	rows       = 2000
	users      = 50
	systems    = 4
	firstDay   = 1_784_160_000_000 - 1_784_160_000_000%86_400_000
)

var periodStart = time.UnixMilli(firstDay).UTC()

func count(t *testing.T, db *store.DB, table string) int64 {
	t.Helper()
	n, err := dbutil.QueryVal[int64](t.Context(), db, `SELECT COUNT(*) FROM `+table)
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func seed(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := t.Context()
	start := time.Date(periodStart.Year(), periodStart.Month(), 1, 0, 0, 0, 0, time.UTC)
	steps := []func() error{
		func() error {
			return spendvolume.InsertUsers(ctx, db, spendvolume.Users{Count: users, Issuer: "https://perf-seed"})
		},
		func() error {
			return spendvolume.InsertBudgets(ctx, db, spendvolume.Budgets{NamePrefix: "Perf seed budget "})
		},
		func() error { return spendvolume.InsertAssignments(ctx, db, users) },
		func() error { return spendvolume.InsertSystemUsers(ctx, db, systems) },
		func() error {
			return spendvolume.InsertTopUps(ctx, db, spendvolume.TopUps{Users: users, Every: 10, Amount: 5,
				Created: start, Expires: start.AddDate(1, 0, 0)})
		},
		func() error {
			return spendvolume.InsertPeriodRows(ctx, db, spendvolume.PeriodRows{Users: users, SystemUsers: systems,
				Start: start.UnixMilli(), End: start.AddDate(0, 1, 0).UnixMilli()})
		},
		func() error {
			return spendvolume.InsertLedger(ctx, db, spendvolume.Ledger{Rows: rows, Users: users, UserStride: 7919, Days: 5,
				FirstDay: firstDay, TracePrefix: seedPrefix, BatchRows: 700})
		},
		func() error { return spendvolume.InsertLedgerDetails(ctx, db, seedPrefix) },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("seed step %d: %v", i, err)
		}
	}
}

func TestGeneratorsFileTheSizedTenantAndRemoveTakesBackExactlyIt(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	before := map[string]int64{}
	tables := []string{"token_spend_ledger", "token_spend_ledger_details", "users", "budgets", "budget_assignments", "budget_top_ups",
		"token_spend_budget_period", "budget_alert_state"}
	start := time.Date(periodStart.Year(), periodStart.Month(), 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	bystander := "id:00000000-0000-7000-9000-000000000001"
	if _, err := db.ExecContext(ctx, `INSERT INTO budget_alert_state (subject_type, subject_ref, period_start, alerted_level, cap_key, updated_time)
		VALUES ('mesh', 'mesh', ?, 'limit', 'real-cap', 0), ('user', ?, ?, 'warn', 'real-cap', 0)`, start, bystander, start); err != nil {
		t.Fatalf("seed bystander alert states: %v", err)
	}
	if err := spendvolume.InsertLedger(ctx, db, spendvolume.Ledger{Rows: 30, Users: 3, Days: 1, FirstDay: firstDay, TracePrefix: keepPrefix}); err != nil {
		t.Fatalf("seed bystander: %v", err)
	}
	if err := spendvolume.InsertLedgerDetails(ctx, db, keepPrefix); err != nil {
		t.Fatalf("seed bystander details: %v", err)
	}
	for _, table := range tables {
		before[table] = count(t, db, table)
	}

	seed(t, db)
	if err := spendvolume.InsertAlertStatesFromPeriods(ctx, db); err != nil {
		t.Fatalf("insert alert states: %v", err)
	}

	if got := count(t, db, "token_spend_ledger") - before["token_spend_ledger"]; got != rows {
		t.Errorf("ledger calls filed = %d, want %d", got, rows)
	}
	if got := count(t, db, "token_spend_ledger_details") - before["token_spend_ledger_details"]; got != rows {
		t.Errorf("ledger details filed = %d, want %d", got, rows)
	}
	if got := count(t, db, "users") - before["users"]; got != users+systems {
		t.Errorf("users filed = %d, want %d", got, users+systems)
	}
	wantAssignments := int64(users - users/5 + systems)
	if got := count(t, db, "budget_assignments") - before["budget_assignments"]; got != wantAssignments {
		t.Errorf("assignments filed = %d, want %d", got, wantAssignments)
	}
	if got := count(t, db, "budget_top_ups") - before["budget_top_ups"]; got != users/10 {
		t.Errorf("top-ups filed = %d, want %d", got, users/10)
	}
	if got := count(t, db, "token_spend_budget_period") - before["token_spend_budget_period"]; got != users+systems+1 {
		t.Errorf("period rows filed = %d, want one per user and system user and the mesh's", got)
	}
	if got := count(t, db, "budget_alert_state") - before["budget_alert_state"]; got != users+systems {
		t.Errorf("alert states filed = %d, want one per generated user and system user, the mesh's already held", got)
	}

	removed, err := spendvolume.Remove(ctx, db, seedPrefix)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed.Ledger != rows {
		t.Errorf("removed ledger calls = %d, want %d", removed.Ledger, rows)
	}
	if removed.AlertStates != users+systems {
		t.Errorf("removed alert states = %d, want %d", removed.AlertStates, users+systems)
	}
	for _, table := range tables {
		if got := count(t, db, table); got != before[table] {
			t.Errorf("%s holds %d rows after remove, want the %d it held before", table, got, before[table])
		}
	}
	meshCap, err := dbutil.QueryVal[string](ctx, db, `SELECT cap_key FROM budget_alert_state WHERE subject_type = 'mesh' AND period_start = ?`, start)
	if err != nil || meshCap != "real-cap" {
		t.Errorf("mesh alert state cap after remove = %q (%v), want the real-cap it held before", meshCap, err)
	}
}

func TestInsertLedgerInBatchesMakesTheSameCallsAsOneStatement(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	l := spendvolume.Ledger{Rows: 1000, Users: 20, UserStride: 7919, Days: 4, FirstDay: firstDay}
	batched := l
	batched.TracePrefix, batched.BatchRows = "batched-", 300
	l.TracePrefix = "single-"
	for _, spec := range []spendvolume.Ledger{l, batched} {
		if err := spendvolume.InsertLedger(ctx, db, spec); err != nil {
			t.Fatalf("seed %s: %v", spec.TracePrefix, err)
		}
	}

	shape := func(prefix string) string {
		var out string
		err := db.QueryRowContext(ctx, `SELECT COUNT(*) || '/' || COUNT(DISTINCT trace_id) || '/' || MIN(completed_time) || '/' || MAX(completed_time)
			FROM token_spend_ledger WHERE trace_id LIKE '`+prefix+`%'`).Scan(&out)
		if err != nil {
			t.Fatalf("read %s: %v", prefix, err)
		}
		return out
	}
	if single, batch := shape("single-"), shape("batched-"); single != batch {
		t.Errorf("batched calls = %s, want the single statement's %s", batch, single)
	}
}

func TestInsertAlertStatesFromPeriodsFilesOneStatePerPeriodRowKeyedAsTheMonitorKeysIt(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	seed(t, db)

	if err := spendvolume.InsertAlertStatesFromPeriods(ctx, db); err != nil {
		t.Fatalf("insert alert states: %v", err)
	}

	if got, want := count(t, db, "budget_alert_state"), count(t, db, "token_spend_budget_period"); got != want {
		t.Errorf("alert states filed = %d, want one per period row (%d)", got, want)
	}
	start := time.Date(periodStart.Year(), periodStart.Month(), 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	states, err := store.NewBudgetAlertStatesRepo(db).AlertStates(ctx, start)
	if err != nil {
		t.Fatalf("read alert states: %v", err)
	}
	refs := map[string]bool{}
	for _, s := range states {
		refs[s.SubjectType+"/"+s.SubjectRef] = true
	}
	for _, want := range []string{"mesh/mesh", "user/id:" + spendvolume.UserID(0)} {
		if !refs[want] {
			t.Errorf("no alert state for %s among %d", want, len(states))
		}
	}
}

func TestInsertLedgerMeasuresEveryCallButEachActivitysFirst(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	l := spendvolume.Ledger{Rows: 800, Users: 10, Days: 2, FirstDay: firstDay, TracePrefix: "measured-",
		Cost: spendvolume.Cost{UncachedInput: 1000, Output: 1, CacheRead: 1, CacheWrite: 1}}
	if err := spendvolume.InsertLedger(ctx, db, l); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var measured, avoidedTokens, avoidedCost int64
	err := db.QueryRowContext(ctx, `SELECT COUNT(avoided_input_tokens), COALESCE(SUM(avoided_input_tokens), 0), COALESCE(SUM(avoided_input_cost), 0)
		FROM token_spend_ledger WHERE trace_id LIKE 'measured-%'`).Scan(&measured, &avoidedTokens, &avoidedCost)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := int64(l.Rows - l.Rows/8); measured != want {
		t.Errorf("measured calls = %d, want %d: all but the first of each 8-call activity", measured, want)
	}
	if avoidedTokens != measured*40 || avoidedCost != measured*400 {
		t.Errorf("avoided = %d tokens / %d cost over %d measured calls, want 40 tokens priced at the input rate (400) each", avoidedTokens, avoidedCost, measured)
	}
}

func TestInsertPeriodRowsFromLedgerBucketsCallsByUTCMonth(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	boundary := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	july, september := boundary.AddDate(0, -1, 0).UnixMilli(), boundary.AddDate(0, 1, 0).UnixMilli()
	l := spendvolume.Ledger{Rows: 400, Users: 2, Days: 2, FirstDay: boundary.AddDate(0, 0, -1).UnixMilli(), TracePrefix: "month-",
		Cost: spendvolume.Cost{UncachedInput: 7, Output: 3, CacheRead: 2, CacheWrite: 1}}
	if err := spendvolume.InsertLedger(ctx, db, l); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := spendvolume.InsertPeriodRowsFromLedger(ctx, db, "month-"); err != nil {
		t.Fatalf("insert period rows: %v", err)
	}

	type row struct {
		Kind  string `db:"subject_kind"`
		Ref   string `db:"subject_ref"`
		Start int64  `db:"period_start"`
		End   int64  `db:"period_end"`
		Calls int64  `db:"priced_calls"`
		Cost  int64  `db:"total_cost"`
	}
	rows, err := dbutil.QueryRows[row](ctx, db, `SELECT subject_kind, subject_ref, period_start, period_end, priced_calls, total_cost
		FROM token_spend_budget_period ORDER BY period_start, subject_kind, subject_ref`)
	if err != nil {
		t.Fatalf("read period rows: %v", err)
	}
	months := map[int64]int64{july: boundary.UnixMilli(), boundary.UnixMilli(): september}
	userCost, meshCost := map[int64]int64{}, map[int64]int64{}
	var calls, users int64
	for _, r := range rows {
		if end, ok := months[r.Start]; !ok || r.End != end {
			t.Errorf("%s/%s period = [%d, %d), want a UTC calendar month either side of %d", r.Kind, r.Ref, r.Start, r.End, boundary.UnixMilli())
		}
		if r.Kind == "mesh" {
			meshCost[r.Start] += r.Cost
			continue
		}
		users++
		calls += r.Calls
		userCost[r.Start] += r.Cost
	}
	if users != 4 || calls != int64(l.Rows) {
		t.Errorf("user period rows = %d holding %d calls, want one per user and month (4) holding all %d", users, calls, l.Rows)
	}
	for start := range months {
		if userCost[start] == 0 || meshCost[start] != userCost[start] {
			t.Errorf("month %d: mesh cost = %d, want the users' %d", start, meshCost[start], userCost[start])
		}
	}
}

func TestInsertLedgerFilesEveryNthActivityUnderAWorkflowTheUsageWorkflowsKindLists(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	const workflowEvery, workflowRoots = 4, 10
	agentsOnly := spendvolume.Ledger{Rows: 800, Users: 10, Days: 1, FirstDay: firstDay, TracePrefix: "agents-"}
	withWorkflows := agentsOnly
	withWorkflows.TracePrefix, withWorkflows.WorkflowEvery = "workflows-", workflowEvery
	for _, l := range []spendvolume.Ledger{agentsOnly, withWorkflows} {
		if err := spendvolume.InsertLedger(ctx, db, l); err != nil {
			t.Fatalf("seed %s: %v", l.TracePrefix, err)
		}
		if err := spendvolume.InsertLedgerDetails(ctx, db, l.TracePrefix); err != nil {
			t.Fatalf("seed %s details: %v", l.TracePrefix, err)
		}
	}
	if err := store.NewSpendRollupRepo(db).RefreshDay(ctx, firstDay, true); err != nil {
		t.Fatalf("roll up: %v", err)
	}

	list, err := store.NewSpendUsageRepo(db, nil).List(ctx, store.UsageWorkflows, store.UsageListQuery{
		FromDay: firstDay, ToDay: firstDay + 86_400_000, Sort: store.UsageSortCost, Descending: true, Limit: 100})
	if err != nil {
		t.Fatalf("list workflows: %v", err)
	}
	var calls int64
	for _, r := range list.Rows {
		calls += r.Totals.Calls
		if !strings.HasPrefix(r.Name, "Workflow ") {
			t.Errorf("workflow %v is named %q, want its resolved root name", r.SubjectID, r.Name)
		}
	}
	if len(list.Rows) != workflowRoots || calls != int64(withWorkflows.Rows/workflowEvery) {
		t.Errorf("workflows listed = %d holding %d calls, want %d holding every %dth activity's (%d)",
			len(list.Rows), calls, workflowRoots, workflowEvery, withWorkflows.Rows/workflowEvery)
	}

	removed, err := spendvolume.Remove(ctx, db, "workflows-")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed.Ledger != int64(withWorkflows.Rows) {
		t.Errorf("removed ledger calls = %d, want every one of the %d, workflow-rooted or not", removed.Ledger, withWorkflows.Rows)
	}
}
