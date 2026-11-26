// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const rollupTestDay int64 = 1_768_435_200_000

type dailyRollupRow struct {
	GrainKey          string  `db:"grain_key"`
	UserID            *string `db:"user_id"`
	UserSub           *string `db:"user_sub"`
	AgentID           *string `db:"agent_id"`
	AgentName         *string `db:"agent_name"`
	RootKind          *string `db:"root_kind"`
	RootID            *string `db:"root_id"`
	RootNodeID        *string `db:"root_node_id"`
	ModelConfigID     *string `db:"model_config_id"`
	ModelName         *string `db:"model_name"`
	AgentLabel        *string `db:"agent_label"`
	ModelLabel        *string `db:"model_label"`
	Calls             int64   `db:"calls"`
	UnpricedCalls     int64   `db:"unpriced_calls"`
	UncachedInputTok  int64   `db:"uncached_input_tokens"`
	OutputTokens      int64   `db:"output_tokens"`
	UncachedInputCost int64   `db:"uncached_input_cost"`
	OutputCost        int64   `db:"output_cost"`
	CacheReadCost     int64   `db:"cache_read_cost"`
	CacheWriteCost    int64   `db:"cache_write_cost"`
	TotalCost         int64   `db:"total_cost"`
	AvoidedTokens     int64   `db:"avoided_input_tokens"`
	AvoidedCost       int64   `db:"avoided_input_cost"`
	MeasuredCalls     int64   `db:"measured_calls"`
	PricedMeasured    int64   `db:"priced_measured_calls"`
}

type activityRollupRow struct {
	Grouping      string  `db:"grouping"`
	GrainKey      string  `db:"grain_key"`
	UserID        *string `db:"user_id"`
	UserSub       *string `db:"user_sub"`
	RootID        *string `db:"root_id"`
	AgentID       *string `db:"agent_id"`
	AgentName     *string `db:"agent_name"`
	ModelConfigID *string `db:"model_config_id"`
	ModelName     *string `db:"model_name"`
	Activities    int64   `db:"activities"`
}

func dailyRollupRows(t *testing.T, db *DB, day int64) []dailyRollupRow {
	t.Helper()
	rows, err := dbutil.QueryRows[dailyRollupRow](t.Context(), db, `SELECT grain_key, user_id, user_sub, agent_id, agent_name,
		root_kind, root_id, root_node_id, model_config_id, model_name, agent_label, model_label, calls, unpriced_calls,
		uncached_input_tokens, output_tokens, uncached_input_cost, output_cost, cache_read_cost, cache_write_cost, total_cost,
		avoided_input_tokens, avoided_input_cost, measured_calls, priced_measured_calls
		FROM token_spend_daily WHERE day = ? ORDER BY grain_key`, day)
	if err != nil {
		t.Fatalf("read daily rollup: %v", err)
	}
	return rows
}

func activityRollupRows(t *testing.T, db *DB, day int64) []activityRollupRow {
	t.Helper()
	rows, err := dbutil.QueryRows[activityRollupRow](t.Context(), db, `SELECT grouping, grain_key, user_id, user_sub, root_id, agent_id,
		agent_name, model_config_id, model_name, activities
		FROM token_spend_daily_activities WHERE day = ? ORDER BY grouping, grain_key`, day)
	if err != nil {
		t.Fatalf("read activity rollup: %v", err)
	}
	return rows
}

func rollupCall(edit func(*SpendLedgerEntry)) SpendLedgerEntry {
	e := SpendLedgerEntry{
		ID:                  idgen.New(),
		CompletedTime:       rollupTestDay + 3_600_000,
		UserSub:             new("user-1"),
		AgentName:           new("qa"),
		RootKind:            new("agent"),
		RootName:            new("qa"),
		Surface:             "agent",
		ModelName:           new("m"),
		TraceID:             new("t1"),
		UncachedInputTokens: 100,
		OutputTokens:        10,
		Cost:                &spend.Cost{UncachedInput: 1_000, Output: 200, CacheRead: 30, CacheWrite: 4},
	}
	if edit != nil {
		edit(&e)
	}
	return e
}

func fileCalls(t *testing.T, db *DB, entries ...SpendLedgerEntry) {
	t.Helper()
	if _, err := NewSpendLedgerRepo(db).Insert(t.Context(), entries); err != nil {
		t.Fatalf("file ledger calls: %v", err)
	}
}

func refreshDay(t *testing.T, repo *SpendRollupRepo, day int64, removeStale bool) {
	t.Helper()
	if err := repo.RefreshDay(t.Context(), day, removeStale); err != nil {
		t.Fatalf("refresh day: %v", err)
	}
}

func TestSpendRollupRepo_RefreshDaySumsEachGroupOfTheDay(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	fileCalls(t, db,
		rollupCall(nil),
		rollupCall(func(e *SpendLedgerEntry) { e.TraceID = new("t2") }),
		rollupCall(func(e *SpendLedgerEntry) { e.Cost = nil }),
		rollupCall(func(e *SpendLedgerEntry) { e.ModelName = new("other") }),
		rollupCall(func(e *SpendLedgerEntry) { e.CompletedTime = rollupTestDay - 1 }),
		rollupCall(func(e *SpendLedgerEntry) { e.CompletedTime = rollupTestDay + SpendRollupDayMillis }),
	)

	refreshDay(t, repo, rollupTestDay, false)

	rows := dailyRollupRows(t, db, rollupTestDay)
	if len(rows) != 2 {
		t.Fatalf("daily rows = %d, want one per model: %+v", len(rows), rows)
	}
	byModel := map[string]dailyRollupRow{}
	for _, row := range rows {
		byModel[*row.ModelName] = row
	}
	m := byModel["m"]
	if m.Calls != 3 || m.UnpricedCalls != 1 || m.UncachedInputTok != 300 || m.OutputTokens != 30 {
		t.Errorf("model m = %d calls, %d unpriced, %d/%d tokens; want 3, 1, 300/30", m.Calls, m.UnpricedCalls, m.UncachedInputTok, m.OutputTokens)
	}
	if m.UncachedInputCost != 2_000 || m.OutputCost != 400 || m.CacheReadCost != 60 || m.CacheWriteCost != 8 || m.TotalCost != 2_468 {
		t.Errorf("model m costs = %d/%d/%d/%d total %d; want the two priced calls' sums", m.UncachedInputCost, m.OutputCost, m.CacheReadCost, m.CacheWriteCost, m.TotalCost)
	}
	if other := byModel["other"]; other.Calls != 1 || other.TotalCost != 1_234 {
		t.Errorf("model other = %d calls, total %d; want 1 and 1234", other.Calls, other.TotalCost)
	}
}

func TestSpendRollupRepo_RefreshDayKeepsEveryDistinctGrainApart(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	agentRow := idgen.New()
	fileCalls(t, db,
		rollupCall(nil),
		rollupCall(func(e *SpendLedgerEntry) { e.AgentName, e.AgentID = nil, new(agentRow) }),
		rollupCall(func(e *SpendLedgerEntry) { e.AgentName = nil }),
		rollupCall(func(e *SpendLedgerEntry) { e.AgentName = new("~") }),
		rollupCall(func(e *SpendLedgerEntry) { e.AgentName = new("q|a") }),
		rollupCall(func(e *SpendLedgerEntry) { e.RootNodeID = new("node-1") }),
	)

	refreshDay(t, repo, rollupTestDay, false)

	if rows := dailyRollupRows(t, db, rollupTestDay); len(rows) != 6 {
		t.Errorf("daily rows = %d, want 6, one per distinct grain: %+v", len(rows), rows)
	}
}

func TestSpendRollupRepo_RefreshDayLabelsASubjectByTheNameTheLedgerResolved(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	agentRow := idgen.New()
	call := rollupCall(func(e *SpendLedgerEntry) { e.AgentName, e.AgentID = nil, new(agentRow) })
	fileCalls(t, db, call, rollupCall(nil))
	if _, err := NewSpendLedgerRepo(db).InsertDetails(t.Context(), []SpendLedgerDetail{{ID: call.ID, ResolvedAgentName: new("QA Tester")}}); err != nil {
		t.Fatalf("file details: %v", err)
	}

	refreshDay(t, repo, rollupTestDay, false)

	labels := map[bool]string{}
	for _, row := range dailyRollupRows(t, db, rollupTestDay) {
		labels[row.AgentID != nil] = *row.AgentLabel
	}
	if labels[true] != "QA Tester" || labels[false] != "qa" {
		t.Errorf("labels = id-keyed %q, name-keyed %q; want the resolved name and the booked name", labels[true], labels[false])
	}
}

func TestSpendRollupRepo_RefreshDayCountsDistinctTracesPerGrouping(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	fileCalls(t, db,
		rollupCall(nil),
		rollupCall(func(e *SpendLedgerEntry) { e.AgentName = new("helper") }),
		rollupCall(func(e *SpendLedgerEntry) { e.TraceID = new("t2"); e.ModelName = new("other") }),
		rollupCall(func(e *SpendLedgerEntry) { e.TraceID = nil }),
	)

	refreshDay(t, repo, rollupTestDay, false)

	got := map[string]int64{}
	for _, row := range activityRollupRows(t, db, rollupTestDay) {
		key := row.Grouping
		switch {
		case row.AgentName != nil:
			key += ":" + *row.AgentName
		case row.ModelName != nil:
			key += ":" + *row.ModelName
		}
		got[key] = row.Activities
	}
	want := map[string]int64{"root": 2, "agent:qa": 2, "agent:helper": 1, "model:m": 1, "model:other": 1}
	if len(got) != len(want) {
		t.Fatalf("activity rows = %v, want %v", got, want)
	}
	for key, n := range want {
		if got[key] != n {
			t.Errorf("%s activities = %d, want %d (all: %v)", key, got[key], n, got)
		}
	}
}

func TestSpendRollupRepo_RefreshDayOfOnlyUntracedCallsHasNoActivities(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	fileCalls(t, db, rollupCall(func(e *SpendLedgerEntry) { e.TraceID = nil }))

	refreshDay(t, repo, rollupTestDay, true)

	if rows := dailyRollupRows(t, db, rollupTestDay); len(rows) != 1 || rows[0].Calls != 1 {
		t.Errorf("daily rows = %+v, want the one call", rows)
	}
	if rows := activityRollupRows(t, db, rollupTestDay); len(rows) != 0 {
		t.Errorf("activity rows = %+v, want none", rows)
	}
}

func TestSpendRollupRepo_RefreshDayTwiceLeavesTheSameRows(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	fileCalls(t, db, rollupCall(nil), rollupCall(func(e *SpendLedgerEntry) { e.ModelName = new("other") }))

	refreshDay(t, repo, rollupTestDay, true)
	first, firstActivities := dailyRollupRows(t, db, rollupTestDay), activityRollupRows(t, db, rollupTestDay)
	refreshDay(t, repo, rollupTestDay, true)
	refreshDay(t, repo, rollupTestDay, false)

	assertSameRollup(t, db, first, firstActivities)
}

func TestSpendRollupRepo_RefreshDayPicksUpLateCalls(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	fileCalls(t, db, rollupCall(nil))
	refreshDay(t, repo, rollupTestDay, false)

	fileCalls(t, db, rollupCall(nil), rollupCall(func(e *SpendLedgerEntry) { e.TraceID = new("t2") }))
	refreshDay(t, repo, rollupTestDay, false)

	if rows := dailyRollupRows(t, db, rollupTestDay); len(rows) != 1 || rows[0].Calls != 3 {
		t.Errorf("daily rows = %+v, want the three calls in one row", rows)
	}
}

func TestSpendRollupRepo_RefreshDayRemovesStaleGroupsOnlyWhenAsked(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	gone := rollupCall(func(e *SpendLedgerEntry) { e.ModelName = new("gone"); e.TraceID = new("t9") })
	fileCalls(t, db, rollupCall(nil), gone)
	refreshDay(t, repo, rollupTestDay, false)
	if _, err := db.ExecContext(t.Context(), `DELETE FROM token_spend_ledger WHERE id = ?`, gone.ID); err != nil {
		t.Fatalf("delete ledger row: %v", err)
	}

	refreshDay(t, repo, rollupTestDay, false)
	if rows := dailyRollupRows(t, db, rollupTestDay); len(rows) != 2 {
		t.Fatalf("daily rows after a plain refresh = %d, want both still there", len(rows))
	}

	refreshDay(t, repo, rollupTestDay, true)
	rows := dailyRollupRows(t, db, rollupTestDay)
	if len(rows) != 1 || *rows[0].ModelName != "m" {
		t.Errorf("daily rows after a rebuild = %+v, want only model m", rows)
	}
	for _, row := range activityRollupRows(t, db, rollupTestDay) {
		if row.ModelName != nil && *row.ModelName == "gone" {
			t.Errorf("activity row for the removed model survived the rebuild: %+v", row)
		}
		if row.Grouping == "root" && row.Activities != 1 {
			t.Errorf("root activities = %d after the rebuild, want 1", row.Activities)
		}
	}
}

func TestSpendRollupRepo_RebuildRemovesAStaleWorkflowActivity(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	moved := rollupCall(func(e *SpendLedgerEntry) { e.WorkflowName = new("old-flow") })
	fileCalls(t, db, moved)
	refreshDay(t, repo, rollupTestDay, false)
	if _, err := db.ExecContext(t.Context(), `DELETE FROM token_spend_ledger WHERE id = ?`, moved.ID); err != nil {
		t.Fatalf("delete ledger row: %v", err)
	}
	fileCalls(t, db, rollupCall(func(e *SpendLedgerEntry) { e.WorkflowName = new("new-flow") }))

	refreshDay(t, repo, rollupTestDay, true)
	workflows, err := dbutil.QueryRows[struct {
		Name string `db:"workflow_name"`
	}](t.Context(), db, `SELECT workflow_name FROM token_spend_daily_activities
		WHERE day = ? AND grouping = 'workflow' ORDER BY workflow_name`, rollupTestDay)
	if err != nil {
		t.Fatalf("read workflow activities: %v", err)
	}
	if len(workflows) != 1 || workflows[0].Name != "new-flow" {
		t.Errorf("workflow activities after a rebuild = %v, want only new-flow", workflows)
	}
}

func TestSpendRollupRepo_ConcurrentRebuildsOfADayConverge(t *testing.T) {
	db := TestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("SQLite runs every statement on its one connection, so rebuilds cannot overlap")
	}
	repo := NewSpendRollupRepo(db)
	var calls []SpendLedgerEntry
	for i := range 2000 {
		calls = append(calls, rollupCall(func(e *SpendLedgerEntry) {
			e.UserSub = new(fmt.Sprintf("user-%d", i%13))
			e.ModelName = new(fmt.Sprintf("model-%d", i%7))
			e.AgentName = new(fmt.Sprintf("agent-%d", i%11))
			e.TraceID = new(fmt.Sprintf("trace-%d", i%97))
		}))
	}
	for chunk := range slices.Chunk(calls, MaxSpendLedgerBatch) {
		fileCalls(t, db, chunk...)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() { errs <- repo.RefreshDay(context.Background(), rollupTestDay, true) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent rebuild: %v", err)
		}
	}
	got, gotActivities := dailyRollupRows(t, db, rollupTestDay), activityRollupRows(t, db, rollupTestDay)

	refreshDay(t, repo, rollupTestDay, true)
	assertSameRollup(t, db, got, gotActivities)
}

func assertSameRollup(t *testing.T, db *DB, daily []dailyRollupRow, activities []activityRollupRow) {
	t.Helper()
	if got := dailyRollupRows(t, db, rollupTestDay); !reflect.DeepEqual(got, daily) {
		t.Errorf("daily rows = %+v, want %+v", got, daily)
	}
	if got := activityRollupRows(t, db, rollupTestDay); !reflect.DeepEqual(got, activities) {
		t.Errorf("activity rows = %+v, want %+v", got, activities)
	}
}

func TestSpendRollupRepo_OverlappingRefreshesWhileCallsArriveSettleOnTheLedger(t *testing.T) {
	db := TestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("SQLite runs every statement on its one connection, so refreshes cannot overlap")
	}
	repo := NewSpendRollupRepo(db)
	call := func(i int) SpendLedgerEntry {
		return rollupCall(func(e *SpendLedgerEntry) {
			e.UserSub = new(fmt.Sprintf("user-%d", i%13))
			e.ModelName = new(fmt.Sprintf("model-%d", i%7))
			e.AgentName = new(fmt.Sprintf("agent-%d", i%11))
			e.TraceID = new(fmt.Sprintf("trace-%d", i%97))
			e.OutputTokens = int64(i % 5)
		})
	}
	var first, later []SpendLedgerEntry
	for i := range 1000 {
		first = append(first, call(i))
		later = append(later, call(i+1000))
	}
	for chunk := range slices.Chunk(first, MaxSpendLedgerBatch) {
		fileCalls(t, db, chunk...)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := range 6 {
		wg.Go(func() { errs <- repo.RefreshDay(context.Background(), rollupTestDay, i%2 == 0) })
	}
	wg.Go(func() {
		for chunk := range slices.Chunk(later, MaxSpendLedgerBatch) {
			if _, err := NewSpendLedgerRepo(db).Insert(context.Background(), chunk); err != nil {
				errs <- err
			}
		}
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("overlapping refresh: %v", err)
		}
	}
	refreshDay(t, repo, rollupTestDay, true)

	type sums struct {
		Calls        int64 `db:"calls"`
		OutputTokens int64 `db:"output_tokens"`
		TotalCost    int64 `db:"total_cost"`
	}
	read := func(query string) sums {
		got, err := dbutil.QueryRow[sums](t.Context(), db, query, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
		if err != nil || got == nil {
			t.Fatalf("read sums: %v", err)
		}
		return *got
	}
	ledger := read(`SELECT COUNT(*) AS calls, CAST(SUM(output_tokens) AS BIGINT) AS output_tokens,
		CAST(COALESCE(SUM(total_cost), 0) AS BIGINT) AS total_cost
		FROM token_spend_ledger WHERE completed_time >= ? AND completed_time < ?`)
	for _, table := range []string{"token_spend_daily", "token_spend_daily_models"} {
		got := read(`SELECT CAST(SUM(calls) AS BIGINT) AS calls, CAST(SUM(output_tokens) AS BIGINT) AS output_tokens,
			CAST(SUM(total_cost) AS BIGINT) AS total_cost FROM ` + table + ` WHERE day >= ? AND day < ?`)
		if got != ledger || ledger.Calls != 2000 {
			t.Errorf("%s sums = %+v, want the ledger's %+v over 2000 calls", table, got, ledger)
		}
	}
	want, err := dbutil.QueryVal[int64](t.Context(), db, `SELECT COUNT(*) FROM (SELECT DISTINCT user_sub, trace_id
		FROM token_spend_ledger WHERE completed_time >= ? AND completed_time < ?) d`, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("count the ledger's traces: %v", err)
	}
	got, err := dbutil.QueryVal[int64](t.Context(), db, `SELECT CAST(SUM(activities) AS BIGINT) FROM token_spend_daily_activities
		WHERE day = ? AND grouping = 'root'`, rollupTestDay)
	if err != nil || got != want {
		t.Errorf("root activities = %d, %v; want the ledger's %d traces per user under the one root", got, err, want)
	}
}

func TestSpendRollupRepo_CallsByDayCountBothSides(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	next := rollupTestDay + SpendRollupDayMillis
	fileCalls(t, db, rollupCall(nil), rollupCall(nil), rollupCall(func(e *SpendLedgerEntry) { e.CompletedTime = next + 5 }))
	refreshDay(t, repo, rollupTestDay, false)

	ledger, err := repo.LedgerCallsByDay(t.Context(), rollupTestDay, next+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("ledger calls: %v", err)
	}
	rolled, err := repo.RollupCallsByDay(t.Context(), rollupTestDay, next+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("rollup calls: %v", err)
	}
	if len(ledger) != 2 || ledger[rollupTestDay] != 2 || ledger[next] != 1 {
		t.Errorf("ledger calls by day = %v, want 2 on the day and 1 the next", ledger)
	}
	if len(rolled) != 1 || rolled[rollupTestDay] != 2 {
		t.Errorf("rollup calls by day = %v, want 2 on the refreshed day only", rolled)
	}
}

func TestSpendRollupRepo_DeleteDaysBeforeKeepsTheDayItNames(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	previous := rollupTestDay - SpendRollupDayMillis
	fileCalls(t, db, rollupCall(nil), rollupCall(func(e *SpendLedgerEntry) { e.CompletedTime = previous }))
	refreshDay(t, repo, previous, false)
	refreshDay(t, repo, rollupTestDay, false)

	deleted, err := repo.DeleteDaysBefore(t.Context(), rollupTestDay)
	if err != nil {
		t.Fatalf("delete days: %v", err)
	}
	if deleted == 0 || len(dailyRollupRows(t, db, previous)) != 0 || len(activityRollupRows(t, db, previous)) != 0 {
		t.Errorf("deleted %d; the previous day still has rows", deleted)
	}
	if len(dailyRollupRows(t, db, rollupTestDay)) != 1 || len(activityRollupRows(t, db, rollupTestDay)) == 0 {
		t.Error("the named day lost its rows")
	}
}

func TestSpendRollupRepo_DeleteDaysBeforeClearsAWholeBacklogAndCountsEveryTable(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	backlog := []int64{rollupTestDay - 3*SpendRollupDayMillis, rollupTestDay - 2*SpendRollupDayMillis, rollupTestDay - SpendRollupDayMillis}
	for _, day := range append(backlog, rollupTestDay) {
		fileCalls(t, db, rollupCall(func(e *SpendLedgerEntry) { e.CompletedTime = day }))
		refreshDay(t, repo, day, false)
	}
	var want int64
	for _, day := range backlog {
		want += rollupRowsOn(t, db, day)
	}

	deleted, err := repo.DeleteDaysBefore(t.Context(), rollupTestDay)
	if err != nil {
		t.Fatalf("delete days: %v", err)
	}

	if deleted != want {
		t.Errorf("deleted = %d, want %d, every backlog row across the tables", deleted, want)
	}
	for _, day := range backlog {
		if rollupRowsOn(t, db, day) != 0 {
			t.Errorf("day %d kept rollup rows", day)
		}
	}
	if len(dailyRollupRows(t, db, rollupTestDay)) != 1 {
		t.Error("the named day lost its rows")
	}
}

func rollupRowsOn(t *testing.T, db *DB, day int64) int64 {
	t.Helper()
	var n int64
	for _, table := range rollupTables {
		c, err := dbutil.QueryVal[int64](t.Context(), db, `SELECT COUNT(*) FROM `+table+` WHERE day = ?`, day)
		if err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		n += c
	}
	return n
}

func TestSpendRollupRepo_EnsureGrainVersionClearsOnlyRowsOfAnotherGrain(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	fileCalls(t, db, rollupCall(nil))

	if cleared, err := repo.EnsureGrainVersion(t.Context()); err != nil || cleared {
		t.Fatalf("first ensure = cleared %t, %v; want nothing to clear", cleared, err)
	}
	refreshDay(t, repo, rollupTestDay, false)
	if cleared, err := repo.EnsureGrainVersion(t.Context()); err != nil || cleared {
		t.Fatalf("ensure at the current grain = cleared %t, %v; want nothing cleared", cleared, err)
	}
	if len(dailyRollupRows(t, db, rollupTestDay)) != 1 {
		t.Fatal("rows at the current grain were cleared")
	}

	if _, err := db.ExecContext(t.Context(), `UPDATE token_spend_rollup_state SET grain_version = ?`, SpendRollupGrainVersion-1); err != nil {
		t.Fatalf("age the grain: %v", err)
	}
	if cleared, err := repo.EnsureGrainVersion(t.Context()); err != nil || !cleared {
		t.Fatalf("ensure at an older grain = cleared %t, %v; want cleared", cleared, err)
	}
	if len(dailyRollupRows(t, db, rollupTestDay)) != 0 || len(activityRollupRows(t, db, rollupTestDay)) != 0 {
		t.Error("rows built at an older grain survived")
	}
	version, err := dbutil.QueryVal[int](t.Context(), db, `SELECT grain_version FROM token_spend_rollup_state WHERE id = 1`)
	if err != nil || version != SpendRollupGrainVersion {
		t.Errorf("recorded grain = %d, %v; want %d", version, err, SpendRollupGrainVersion)
	}
}

func TestSpendRollupRepo_AGrainClearLeavesNoOldKeys(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	fileCalls(t, db, rollupCall(nil))
	if _, err := repo.EnsureGrainVersion(t.Context()); err != nil {
		t.Fatalf("ensure grain: %v", err)
	}
	refreshDay(t, repo, rollupTestDay, false)
	for _, table := range rollupTables {
		if _, err := db.ExecContext(t.Context(), `UPDATE `+table+` SET grain_key = 'old:' || grain_key`); err != nil {
			t.Fatalf("rekey %s as an older grain: %v", table, err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE token_spend_rollup_state SET grain_version = ?`, SpendRollupGrainVersion-1); err != nil {
		t.Fatalf("age the grain: %v", err)
	}

	if _, err := repo.EnsureGrainVersion(t.Context()); err != nil {
		t.Fatalf("ensure grain: %v", err)
	}
	refreshDay(t, repo, rollupTestDay, true)

	for _, table := range rollupTables {
		old, err := dbutil.QueryVal[int](t.Context(), db, `SELECT COUNT(*) FROM `+table+` WHERE grain_key LIKE 'old:%'`)
		if err != nil || old != 0 {
			t.Errorf("%s keeps %d rows of the older grain, %v; want none", table, old, err)
		}
	}
	calls, err := dbutil.QueryVal[int64](t.Context(), db, `SELECT CAST(SUM(calls) AS BIGINT) FROM token_spend_daily`)
	if err != nil || calls != 1 {
		t.Errorf("rolled calls = %d, %v; want the one call", calls, err)
	}
}

func TestSpendRollupRepo_EnsureGrainVersionRefusesANewerGrain(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	fileCalls(t, db, rollupCall(nil))
	refreshDay(t, repo, rollupTestDay, false)
	if err := repo.MarkRefreshed(t.Context(), 1_000); err != nil {
		t.Fatalf("mark refreshed: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE token_spend_rollup_state SET grain_version = ?`, SpendRollupGrainVersion+1); err != nil {
		t.Fatalf("raise the grain: %v", err)
	}

	if cleared, err := repo.EnsureGrainVersion(t.Context()); err == nil || cleared {
		t.Errorf("ensure under a newer grain = cleared %t, %v; want refused", cleared, err)
	}
	if len(dailyRollupRows(t, db, rollupTestDay)) != 1 {
		t.Error("the newer grain's rows were cleared")
	}
	version, err := dbutil.QueryVal[int](t.Context(), db, `SELECT grain_version FROM token_spend_rollup_state WHERE id = 1`)
	if err != nil || version != SpendRollupGrainVersion+1 {
		t.Errorf("recorded grain = %d, %v; want the newer grain kept", version, err)
	}
}

func TestSpendRollupRepo_MarkReconciledIsClearedWithTheGrain(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	if _, err := repo.EnsureGrainVersion(t.Context()); err != nil {
		t.Fatalf("ensure grain: %v", err)
	}
	if err := repo.MarkReconciled(t.Context(), 5_000); err != nil {
		t.Fatalf("mark reconciled: %v", err)
	}
	if err := repo.MarkRefreshed(t.Context(), 6_000); err != nil {
		t.Fatalf("mark refreshed: %v", err)
	}
	type state struct {
		RefreshedTime  *int64 `db:"refreshed_time"`
		ReconciledTime *int64 `db:"reconciled_time"`
	}
	read := func() *state {
		got, err := dbutil.QueryRow[state](t.Context(), db, `SELECT refreshed_time, reconciled_time FROM token_spend_rollup_state WHERE id = 1`)
		if err != nil || got == nil {
			t.Fatalf("read state: %v", err)
		}
		return got
	}
	if got := read(); got.ReconciledTime == nil || *got.ReconciledTime != 5_000 || got.RefreshedTime == nil || *got.RefreshedTime != 6_000 {
		t.Fatalf("state = %+v, want reconciled at 5000 and refreshed at 6000, each kept by the other's mark", got)
	}

	if _, err := db.ExecContext(t.Context(), `UPDATE token_spend_rollup_state SET grain_version = ?`, SpendRollupGrainVersion-1); err != nil {
		t.Fatalf("age the grain: %v", err)
	}
	if _, err := repo.EnsureGrainVersion(t.Context()); err != nil {
		t.Fatalf("ensure grain: %v", err)
	}
	if got := read(); got.ReconciledTime != nil || got.RefreshedTime != nil {
		t.Errorf("state after a grain clear = %+v, want neither time until the rollups are rebuilt", got)
	}
}

func TestSpendRollupRepo_MarkRefreshedKeepsTheGrain(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendRollupRepo(db)
	if err := repo.MarkRefreshed(t.Context(), 1_000); err != nil {
		t.Fatalf("first mark: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE token_spend_rollup_state SET grain_version = 99`); err != nil {
		t.Fatalf("set grain: %v", err)
	}
	if err := repo.MarkRefreshed(t.Context(), 2_000); err != nil {
		t.Fatalf("second mark: %v", err)
	}
	type state struct {
		GrainVersion  int   `db:"grain_version"`
		RefreshedTime int64 `db:"refreshed_time"`
	}
	got, err := dbutil.QueryRow[state](t.Context(), db, `SELECT grain_version, refreshed_time FROM token_spend_rollup_state WHERE id = 1`)
	if err != nil || got == nil || got.GrainVersion != 99 || got.RefreshedTime != 2_000 {
		t.Errorf("state = %+v, %v; want grain 99 kept and refreshed at 2000", got, err)
	}
}

func TestSpendRollupRepo_RefreshDayRefusesATimeThatIsNotADayStart(t *testing.T) {
	if err := NewSpendRollupRepo(TestDB(t)).RefreshDay(t.Context(), rollupTestDay+1, false); err == nil {
		t.Error("RefreshDay accepted a time that is not a UTC midnight")
	}
}
