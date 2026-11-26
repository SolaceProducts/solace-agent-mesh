// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type usageFixture struct {
	t     *testing.T
	db    *DB
	usage *SpendUsageRepo
}

func newUsageFixture(t *testing.T) *usageFixture {
	db := TestDB(t)
	return &usageFixture{t: t, db: db, usage: NewSpendUsageRepo(db, nil)}
}

func (f *usageFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

func (f *usageFixture) user(issuer, sub, displayName string, active bool, provisionedVia string) string {
	f.t.Helper()
	id := idgen.New()
	activeFlag := 0
	if active {
		activeFlag = 1
	}
	f.exec(`INSERT INTO users (id, issuer, sub, display_name, email, provisioned_via, active, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'test', 'test', 0, 0)`, id, issuer, sub, displayName, sub+"@example.com", provisionedVia, activeFlag)
	return id
}

func (f *usageFixture) agent(name, kind string, ephemeral bool) string {
	f.t.Helper()
	id := idgen.New()
	flag := 0
	if ephemeral {
		flag = 1
	}
	f.exec(`INSERT INTO agents (id, name, type, is_ephemeral, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, ?, 'test', 'test', 0, 0)`, id, name, kind, flag)
	return id
}

func (f *usageFixture) workflow(name string) string {
	f.t.Helper()
	id := idgen.New()
	f.exec(`INSERT INTO workflows (id, name, app_config, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, '{}', 'test', 'test', 0, 0)`, id, name)
	return id
}

func (f *usageFixture) model(alias string) string {
	f.t.Helper()
	id := idgen.New()
	f.exec(`INSERT INTO model_configurations (id, alias, provider, model_name, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, 'openai', 'gpt', 'test', 'test', 0, 0)`, id, alias)
	return id
}

func (f *usageFixture) calls(entries ...SpendLedgerEntry) {
	f.t.Helper()
	fileCalls(f.t, f.db, entries...)
	refreshDay(f.t, NewSpendRollupRepo(f.db), rollupTestDay, true)
}

func (f *usageFixture) list(kind UsageKind, q UsageListQuery) []UsageRow {
	f.t.Helper()
	return f.pageOf(kind, q).Rows
}

func (f *usageFixture) pageOf(kind UsageKind, q UsageListQuery) UsageList {
	f.t.Helper()
	if q.ToDay == 0 {
		q.FromDay, q.ToDay = rollupTestDay, rollupTestDay+SpendRollupDayMillis
	}
	if q.Sort == "" {
		q.Sort, q.Descending = UsageSortCost, true
	}
	if q.Limit == 0 {
		q.Unpaged = true
	}
	list, err := f.usage.List(f.t.Context(), kind, q)
	if err != nil {
		f.t.Fatalf("list %s: %v", kind, err)
	}
	return list
}

func (f *usageFixture) totals(s UsageSubject) UsageTotals {
	f.t.Helper()
	totals, err := f.usage.Totals(f.t.Context(), s, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		f.t.Fatalf("totals %+v: %v", s, err)
	}
	return totals
}

func costed(cost int64, edit func(*SpendLedgerEntry)) SpendLedgerEntry {
	return rollupCall(func(e *SpendLedgerEntry) {
		e.Cost = &spend.Cost{UncachedInput: cost}
		if edit != nil {
			edit(e)
		}
	})
}

func rowNamed(rows []UsageRow, name string) *UsageRow {
	for i := range rows {
		if rows[i].Name == name {
			return &rows[i]
		}
	}
	return nil
}

func rowBookedAs(rows []UsageRow, subjectName string) *UsageRow {
	for i := range rows {
		if n := rows[i].SubjectName; n != nil && *n == subjectName {
			return &rows[i]
		}
	}
	return nil
}

func TestSpendUsageRepo_TenantTotalsSumEveryCallAndCountActivitiesOnce(t *testing.T) {
	f := newUsageFixture(t)
	f.calls(
		costed(100, nil),
		costed(50, func(e *SpendLedgerEntry) { e.AgentName = new("helper") }),
		costed(25, func(e *SpendLedgerEntry) { e.TraceID = new("t2") }),
		rollupCall(func(e *SpendLedgerEntry) { e.Cost = nil; e.TraceID = new("t2") }),
	)

	got, _, err := f.usage.TenantTotals(t.Context(), rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		t.Fatal(err)
	}

	if got.Cost != 175 || got.UncachedInputCost != 175 || got.Calls != 4 || got.UnpricedCalls != 1 || got.UncachedInputTokens != 400 {
		t.Errorf("totals = %+v, want cost 175 over 4 calls, 1 unpriced, 400 input tokens", got)
	}
	if got.Activities != 2 {
		t.Errorf("activities = %d, want 2: two traces, however many agents each touched", got.Activities)
	}
}

func TestSpendUsageRepo_TenantTotalsGiveEachKindTheCostItsListCovers(t *testing.T) {
	f := newUsageFixture(t)
	f.calls(
		costed(100, nil),
		costed(40, func(e *SpendLedgerEntry) { e.RootKind, e.RootName = new("workflow"), new("invoices") }),
		costed(7, func(e *SpendLedgerEntry) { e.AgentName, e.RootKind, e.RootName = nil, nil, nil }),
	)

	totals, kinds, err := f.usage.TenantTotals(t.Context(), rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("tenant totals: %v", err)
	}

	if totals.Cost != 147 {
		t.Errorf("totals cost = %d, want 147", totals.Cost)
	}
	want := UsageKindCosts{Users: 147, Agents: 140, Workflows: 40, Models: 147}
	if kinds != want {
		t.Errorf("kind costs = %+v, want %+v: every call is a user's and a model's, 140 ran in an agent and 40 in a workflow", kinds, want)
	}
	for kind, cost := range map[UsageKind]int64{UsageUsers: kinds.Users, UsageAgents: kinds.Agents, UsageWorkflows: kinds.Workflows, UsageModels: kinds.Models} {
		var listed int64
		for _, row := range f.list(kind, UsageListQuery{}) {
			listed += row.Totals.Cost
		}
		if listed != cost {
			t.Errorf("%s rows sum to %d, want their kind's cost %d", kind, listed, cost)
		}
	}
}

func TestSpendUsageRepo_AUsersRowAndABareSubjectAreSeparateSubjects(t *testing.T) {
	f := newUsageFixture(t)
	alice := f.user("https://idp-a", "alice", "Alice", true, "jit")
	f.user("https://idp-a", "shared", "Shared A", true, "jit")
	f.user("https://idp-b", "shared", "Shared B", true, "jit")
	f.calls(
		costed(100, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(alice) }),
		costed(40, func(e *SpendLedgerEntry) { e.UserSub = new("alice"); e.TraceID = new("t2") }),
		costed(7, func(e *SpendLedgerEntry) { e.UserSub = new("shared") }),
		costed(3, func(e *SpendLedgerEntry) { e.UserSub = new("nobody") }),
	)

	rows := f.list(UsageUsers, UsageListQuery{})

	if len(rows) != 4 {
		t.Fatalf("users = %+v, want Alice's row and the subjects alice, shared and nobody", rows)
	}
	if got := rows[0]; got.SubjectID == nil || *got.SubjectID != alice || got.Name != "Alice" || got.Totals.Cost != 100 || got.Totals.Activities != 1 {
		t.Errorf("first user = %+v, want Alice's row with only its own booking: cost 100, 1 activity", got)
	}
	if got := rows[0]; got.Email == nil || *got.Email != "alice@example.com" {
		t.Errorf("Alice's email = %v, want alice@example.com", got.Email)
	}
	for sub, cost := range map[string]int64{"alice": 40, "shared": 7, "nobody": 3} {
		row := rowBookedAs(rows, sub)
		if row == nil || row.SubjectID != nil || row.Name != "" || row.Totals.Cost != cost {
			t.Errorf("%s = %+v, want a row by subject with no users row, unnamed, costing %d", sub, row, cost)
		}
		if got := f.totals(UsageSubject{Kind: UsageUsers, Name: sub}); got.Cost != cost {
			t.Errorf("totals for subject %s = %+v, want %d", sub, got, cost)
		}
	}
	if got := f.totals(UsageSubject{Kind: UsageUsers, ID: alice}); got.Cost != 100 || got.Activities != 1 {
		t.Errorf("Alice's totals = %+v, want her row's booking alone", got)
	}
}

func TestSpendUsageRepo_AUserSubjectIsNeitherFilteredNorSortedByName(t *testing.T) {
	f := newUsageFixture(t)
	bob := f.user("https://idp-a", "bob", "Bob", true, "jit")
	f.calls(
		costed(5, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(bob) }),
		costed(3, func(e *SpendLedgerEntry) { e.UserSub = new("aaron@example.com") }),
	)

	if rows := f.list(UsageUsers, UsageListQuery{Name: "aaron"}); len(rows) != 0 {
		t.Errorf("filtered by its subject = %+v, want nothing", rows)
	}
	for _, descending := range []bool{false, true} {
		rows := f.list(UsageUsers, UsageListQuery{Sort: UsageSortName, Descending: descending})
		if len(rows) != 2 || rows[0].Name != "Bob" || rows[1].SubjectName == nil {
			t.Errorf("by name, descending %v = %+v, want Bob, then the subject", descending, rows)
		}
	}
}

func TestSpendUsageRepo_UsersWithNoSpendAreTheActiveAndSystemUsers(t *testing.T) {
	f := newUsageFixture(t)
	idle := f.user("https://idp-a", "idle", "Idle", true, "jit")
	f.user("https://idp-a", "gone", "Gone", false, "jit")
	system := f.user("https://idp-a", "system:pipeline", "Pipeline", true, "system")
	f.calls(costed(10, nil))

	list := f.pageOf(UsageUsers, UsageListQuery{AllSubjects: true})
	rows := list.Rows

	ids := map[string]UsageRow{}
	for _, row := range rows {
		if row.SubjectID != nil {
			ids[*row.SubjectID] = row
		}
	}
	if _, ok := ids[idle]; !ok {
		t.Error("an active user with no spend is missing")
	}
	if row, ok := ids[system]; !ok || !row.SystemUser {
		t.Errorf("system user = %+v, %t; want it listed and marked a system user", row, ok)
	}
	if rowNamed(rows, "Gone") != nil {
		t.Error("an inactive user with no spend is listed")
	}
	if rows[0].Totals.Cost != 10 || list.Count != len(rows) {
		t.Errorf("first row = %+v, count %d of %d rows; want the spender first and the count of every row", rows[0], list.Count, len(rows))
	}
	if spenders := f.list(UsageUsers, UsageListQuery{}); len(spenders) != 1 {
		t.Errorf("spenders = %d, want only the one who spent", len(spenders))
	}
}

func TestSpendUsageRepo_AUserTheListingsHideIsListedOnlyWithSpend(t *testing.T) {
	f := newUsageFixture(t)
	f.user("https://idp-a", "idle", "Idle", true, "jit")
	hiddenIdle := f.user("https://idp-a", "system:hidden-idle", "Hidden Idle", true, "system")
	hiddenSpender := f.user("https://idp-a", "system:hidden-spender", "Hidden Spender", true, "system")
	f.usage = NewSpendUsageRepo(f.db, []string{hiddenIdle, hiddenSpender})
	f.calls(costed(10, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(hiddenSpender) }))

	rows := f.list(UsageUsers, UsageListQuery{AllSubjects: true})
	if rowNamed(rows, "Idle") == nil {
		t.Error("a listed user with no spend is missing")
	}
	if row := rowNamed(rows, "Hidden Idle"); row != nil {
		t.Errorf("a hidden user with no spend is listed: %+v", row)
	}
	if row := rowNamed(rows, "Hidden Spender"); row == nil || row.Totals.Cost != 10 {
		t.Errorf("hidden user with spend = %+v, want it listed with its cost", row)
	}
}

func TestSpendUsageRepo_AgentsAreNamedLiveAndKeepTheirLabelOnceDeleted(t *testing.T) {
	f := newUsageFixture(t)
	renamed := f.agent("qa", "standard", false)
	deleted := f.agent("old", "standard", false)
	f.agent("idle", "standard", false)
	f.agent("remote", "a2a_proxy", false)
	f.agent("scratch", "standard", true)
	gone := costed(20, func(e *SpendLedgerEntry) { e.AgentName, e.AgentID = nil, new(deleted) })
	fileCalls(t, f.db, gone)
	if _, err := NewSpendLedgerRepo(f.db).InsertDetails(t.Context(), []SpendLedgerDetail{{ID: gone.ID, ResolvedAgentName: new("Old Agent")}}); err != nil {
		t.Fatalf("file details: %v", err)
	}
	f.calls(
		costed(30, func(e *SpendLedgerEntry) { e.AgentName, e.AgentID = nil, new(renamed) }),
		costed(10, func(e *SpendLedgerEntry) { e.AgentName = new("yaml-agent") }),
	)
	f.exec(`UPDATE agents SET name = 'QA Tester' WHERE id = ?`, renamed)
	f.exec(`DELETE FROM agents WHERE id = ?`, deleted)

	rows := f.list(UsageAgents, UsageListQuery{AllSubjects: true})

	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Name
	}
	want := []string{"QA Tester", "Old Agent", "yaml-agent", "idle"}
	if len(names) != len(want) {
		t.Fatalf("agents = %v, want %v: no remote proxy or ephemeral agent at $0", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("agents = %v, want %v", names, want)
			break
		}
	}
	if yaml := rowNamed(rows, "yaml-agent"); yaml == nil || yaml.SubjectID != nil || *yaml.SubjectName != "yaml-agent" {
		t.Errorf("YAML agent = %+v, want it named by the name it was booked under", yaml)
	}
}

func TestSpendUsageRepo_ListSortsPagesAndFiltersStably(t *testing.T) {
	f := newUsageFixture(t)
	f.calls(
		costed(30, func(e *SpendLedgerEntry) { e.ModelName = new("Alpha") }),
		costed(30, func(e *SpendLedgerEntry) { e.ModelName = new("bravo") }),
		costed(10, func(e *SpendLedgerEntry) { e.ModelName = new("Charlie"); e.TraceID = nil }),
		costed(50, func(e *SpendLedgerEntry) { e.ModelName = new("delta") }),
	)
	names := func(rows []UsageRow) []string {
		out := make([]string, len(rows))
		for i, row := range rows {
			out[i] = row.Name
		}
		return out
	}
	for name, tc := range map[string]struct {
		q    UsageListQuery
		want []string
	}{
		"cost, most first, ties by subject": {q: UsageListQuery{Sort: UsageSortCost, Descending: true}, want: []string{"delta", "Alpha", "bravo", "Charlie"}},
		"cost, least first":                 {q: UsageListQuery{Sort: UsageSortCost}, want: []string{"Charlie", "Alpha", "bravo", "delta"}},
		"name, case-insensitive":            {q: UsageListQuery{Sort: UsageSortName}, want: []string{"Alpha", "bravo", "Charlie", "delta"}},
		"cost per activity, none last":      {q: UsageListQuery{Sort: UsageSortCostPerActivity}, want: []string{"Alpha", "bravo", "delta", "Charlie"}},
		"second page of two":                {q: UsageListQuery{Sort: UsageSortCost, Descending: true, Limit: 2, Offset: 2}, want: []string{"bravo", "Charlie"}},
		"name filter, any case":             {q: UsageListQuery{Sort: UsageSortName, Name: "A"}, want: []string{"Alpha", "bravo", "Charlie", "delta"}},
		"name filter narrowing":             {q: UsageListQuery{Sort: UsageSortName, Name: "rav"}, want: []string{"bravo"}},
	} {
		t.Run(name, func(t *testing.T) {
			list := f.pageOf(UsageModels, tc.q)
			got := names(list.Rows)
			if len(got) != len(tc.want) {
				t.Fatalf("models = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("models = %v, want %v", got, tc.want)
				}
			}
			if tc.q.Limit > 0 && list.Count != 4 {
				t.Errorf("count = %d on a page, want every matching subject (4)", list.Count)
			}
		})
	}
}

func TestSpendUsageRepo_ModelsAreNamedByTheirConfigurationAlias(t *testing.T) {
	f := newUsageFixture(t)
	general := f.model("General")
	f.model("Unused")
	f.calls(costed(10, func(e *SpendLedgerEntry) { e.ModelName, e.ModelConfigID = nil, new(general) }))

	rows := f.list(UsageModels, UsageListQuery{AllSubjects: true})

	if len(rows) != 2 || rows[0].Name != "General" || rows[0].Totals.Cost != 10 || rows[1].Name != "Unused" || rows[1].Totals.Cost != 0 {
		t.Errorf("models = %+v, want General with its spend, then Unused at zero", rows)
	}
	if got := f.totals(UsageSubject{Kind: UsageModels, ID: general}); got.Cost != 10 || got.Activities != 1 {
		t.Errorf("General's totals = %+v, want cost 10 and 1 activity", got)
	}
}

func TestSpendUsageRepo_AllSubjectsListsOnlyTheSubjectsCreatedBeforeTheWindowEnded(t *testing.T) {
	windowEnd := rollupTestDay + SpendRollupDayMillis
	for _, tc := range []struct {
		kind  UsageKind
		table string
		add   func(f *usageFixture, name string) string
	}{
		{UsageAgents, "agents", func(f *usageFixture, name string) string { return f.agent(name, "standard", false) }},
		{UsageWorkflows, "workflows", (*usageFixture).workflow},
		{UsageModels, "model_configurations", (*usageFixture).model},
		{UsageUsers, "users", func(f *usageFixture, name string) string { return f.user("https://idp-a", name, name, true, "jit") }},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			f := newUsageFixture(t)
			createdAt := func(name string, millis int64) {
				f.exec(`UPDATE `+tc.table+` SET created_time = ? WHERE id = ?`, millis, tc.add(f, name))
			}
			createdAt("existed", windowEnd-1)
			createdAt("later", windowEnd)

			rows := f.list(tc.kind, UsageListQuery{AllSubjects: true})

			if rowNamed(rows, "existed") == nil || rowNamed(rows, "later") != nil {
				t.Errorf("%s = %+v, want the one created before the window ended and not the one created as it ended", tc.kind, rows)
			}
		})
	}
}

func TestSpendUsageRepo_WorkflowsAreRootsAndSplitByNode(t *testing.T) {
	f := newUsageFixture(t)
	audit := f.workflow("expense-audit")
	f.workflow("idle-workflow")
	underAudit := func(cost int64, node string) SpendLedgerEntry {
		return costed(cost, func(e *SpendLedgerEntry) {
			e.RootKind, e.RootName, e.RootID, e.RootNodeID = new("workflow"), nil, new(audit), new(node)
		})
	}
	f.calls(underAudit(30, "review"), underAudit(10, "review"), underAudit(5, "notify"), costed(100, nil))

	rows := f.list(UsageWorkflows, UsageListQuery{AllSubjects: true})
	if len(rows) != 2 || rows[0].Name != "expense-audit" || rows[0].Totals.Cost != 45 || rows[1].Totals.Cost != 0 {
		t.Errorf("workflows = %+v, want expense-audit with 45 and the idle workflow at zero, and no agent root", rows)
	}

	subject := UsageSubject{Kind: UsageWorkflows, ID: audit}
	nodes, err := f.usage.WorkflowNodeCosts(t.Context(), subject, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("node costs: %v", err)
	}
	if len(nodes) != 2 || nodes[0] != (UsageNodeCost{NodeID: "notify", Cost: 5}) || nodes[1] != (UsageNodeCost{NodeID: "review", Cost: 40}) {
		t.Errorf("node costs = %+v, want notify 5 and review 40", nodes)
	}
	ledgerNodes, err := f.usage.LedgerWorkflowNodeCosts(t.Context(), subject, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil || len(ledgerNodes) != 2 || ledgerNodes[1].Cost != 40 {
		t.Errorf("ledger node costs = %+v, %v; want the rollup's", ledgerNodes, err)
	}
}

func TestSpendUsageRepo_UserRootCostsAreTheMostFirstAndNamedLive(t *testing.T) {
	f := newUsageFixture(t)
	alice := f.user("https://idp-a", "alice", "Alice", true, "jit")
	support := f.agent("customer-support", "standard", false)
	audit := f.workflow("expense-audit")
	byAlice := func(cost int64, edit func(*SpendLedgerEntry)) SpendLedgerEntry {
		return costed(cost, func(e *SpendLedgerEntry) {
			e.UserSub, e.UserID = nil, new(alice)
			edit(e)
		})
	}
	f.calls(
		byAlice(60, func(e *SpendLedgerEntry) { e.RootName, e.RootID = nil, new(support) }),
		byAlice(25, func(e *SpendLedgerEntry) { e.RootKind, e.RootName, e.RootID = new("workflow"), nil, new(audit) }),
		byAlice(5, func(e *SpendLedgerEntry) { e.RootName = new("yaml-root") }),
		byAlice(80, func(e *SpendLedgerEntry) { e.RootKind, e.RootName = new("forged"), new("forged-root") }),
		costed(99, nil),
	)

	roots, err := f.usage.UserRootCosts(t.Context(), UsageSubject{Kind: UsageUsers, ID: alice}, rollupTestDay, rollupTestDay+SpendRollupDayMillis, 2)
	if err != nil {
		t.Fatalf("root costs: %v", err)
	}
	if len(roots) != 2 || roots[0].Name != "customer-support" || roots[0].Kind != "agent" || roots[0].Cost != 60 ||
		roots[1].Name != "expense-audit" || roots[1].Kind != "workflow" || roots[1].Cost != 25 {
		t.Errorf("root costs = %+v, want customer-support 60 then expense-audit 25, not the forged kind", roots)
	}
}

func TestSpendUsageRepo_DailyHasOnlyDaysWithSpendOrActivity(t *testing.T) {
	f := newUsageFixture(t)
	next := rollupTestDay + 2*SpendRollupDayMillis
	cached := func(e *SpendLedgerEntry) { e.CacheReadTokens, e.CacheWriteTokens = 7, 3 }
	fileCalls(t, f.db, costed(10, func(e *SpendLedgerEntry) { e.CompletedTime = next + 1; cached(e) }))
	refreshDay(t, NewSpendRollupRepo(f.db), next, false)
	f.calls(costed(5, cached), costed(5, func(e *SpendLedgerEntry) { e.TraceID = new("t2"); cached(e) }))

	days, err := f.usage.Daily(t.Context(), UsageSubject{}, rollupTestDay, next+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	want := []UsageDay{
		{Day: rollupTestDay, Cost: 10, UncachedInputTokens: 200, OutputTokens: 20, CacheReadTokens: 14, CacheWriteTokens: 6, Activities: 2},
		{Day: next, Cost: 10, UncachedInputTokens: 100, OutputTokens: 10, CacheReadTokens: 7, CacheWriteTokens: 3, Activities: 1},
	}
	if len(days) != len(want) || days[0] != want[0] || days[1] != want[1] {
		t.Errorf("daily = %+v, want %+v", days, want)
	}
}

func TestSpendUsageRepo_LedgerCostCoversPartOfADay(t *testing.T) {
	f := newUsageFixture(t)
	fileCalls(t, f.db,
		costed(10, func(e *SpendLedgerEntry) { e.CompletedTime = rollupTestDay + 1_000 }),
		costed(20, func(e *SpendLedgerEntry) { e.CompletedTime = rollupTestDay + 5_000; e.TraceID = new("t2") }),
	)

	got, err := f.usage.LedgerCost(t.Context(), UsageSubject{}, rollupTestDay, rollupTestDay+2_000)
	if err != nil || got != 10 {
		t.Errorf("ledger cost = %d, %v; want only the call before the cut", got, err)
	}
}

func TestSpendUsageRepo_RollupStateIsEmptyUntilTheJobsRun(t *testing.T) {
	f := newUsageFixture(t)
	if state, err := f.usage.RollupState(t.Context()); err != nil || state.RefreshedTime != nil || state.ReconciledTime != nil {
		t.Fatalf("state before any run = %+v, %v; want neither time", state, err)
	}
	rollups := NewSpendRollupRepo(f.db)
	if err := rollups.MarkRefreshed(t.Context(), 7); err != nil {
		t.Fatalf("mark refreshed: %v", err)
	}
	state, err := f.usage.RollupState(t.Context())
	if err != nil || state.RefreshedTime == nil || *state.RefreshedTime != 7 || state.ReconciledTime != nil {
		t.Errorf("state = %+v, %v; want refreshed at 7 and not yet reconciled", state, err)
	}
}

func TestSpendUsageRepo_RefusesAnUnknownKindOrSort(t *testing.T) {
	f := newUsageFixture(t)
	if _, err := f.usage.List(t.Context(), "teams", UsageListQuery{Sort: UsageSortCost, Limit: 1}); err == nil {
		t.Error("an unknown kind was listed")
	}
	if _, err := f.usage.List(t.Context(), UsageAgents, UsageListQuery{Sort: "budget", Limit: 1}); err == nil {
		t.Error("a sort the store does not know was accepted")
	}
	if _, err := f.usage.List(t.Context(), UsageAgents, UsageListQuery{Sort: UsageSortCost}); err == nil {
		t.Error("a list with no limit that does not ask for every subject was accepted")
	}
	if _, err := f.usage.Totals(t.Context(), UsageSubject{Kind: UsageAgents, ID: idgen.New(), Name: "qa"}, 0, 1); err == nil {
		t.Error("a subject naming both an id and a name was accepted")
	}
	if _, err := f.usage.Totals(t.Context(), UsageSubject{Kind: UsageAgents}, 0, 1); err == nil {
		t.Error("a subject with neither id nor name was accepted")
	}
	if n, err := dbutil.QueryVal[int](t.Context(), f.db, `SELECT COUNT(*) FROM token_spend_daily`); err != nil || n != 0 {
		t.Errorf("rollup rows = %d, %v; want none written", n, err)
	}
}

func TestSpendUsageRepo_APageAfterTheLastStillCountsEverySubject(t *testing.T) {
	f := newUsageFixture(t)
	f.calls(costed(10, nil), costed(20, func(e *SpendLedgerEntry) { e.ModelName = new("other") }))

	list := f.pageOf(UsageModels, UsageListQuery{Limit: 5, Offset: 10})

	if len(list.Rows) != 0 || list.Count != 2 {
		t.Errorf("page past the end = %d rows, count %d; want none and 2", len(list.Rows), list.Count)
	}
}

func TestSpendUsageRepo_UsersAreNamedByAnyNameTheyHave(t *testing.T) {
	f := newUsageFixture(t)
	blank := f.user("https://idp-a", "blank", "", true, "jit")
	pipeline := f.user("https://idp-a", "system:pipeline", "Pipeline", true, "system")
	person := f.user("https://idp-a", "gone", "Gone Person", true, "jit")
	nameless := f.user("https://idp-a", "nameless", "", true, "jit")
	f.exec(`UPDATE users SET email = NULL WHERE id = ?`, nameless)
	byBlank := func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(blank) }
	f.calls(
		costed(10, byBlank),
		costed(20, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(pipeline) }),
		costed(12, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(person) }),
		costed(3, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(nameless) }),
	)
	f.exec(`DELETE FROM users WHERE id IN (?, ?)`, pipeline, person)
	f.calls(costed(5, byBlank))

	rows := f.list(UsageUsers, UsageListQuery{})

	if len(rows) != 4 || rows[0].Name != "Pipeline" || rows[1].Name != "blank@example.com" ||
		*rows[2].SubjectID != person || rows[2].Name != "" || *rows[3].SubjectID != nameless || rows[3].Name != "" {
		t.Errorf("users = %+v, want the deleted Pipeline by its rolled-up name, blank by email, then the deleted person and the nameless user unnamed, not by id", rows)
	}
	for _, id := range []string{pipeline, person, nameless} {
		info, found, err := f.usage.SubjectInfo(t.Context(), UsageSubject{Kind: UsageUsers, ID: id}, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
		if want := map[string]string{pipeline: "Pipeline"}[id]; err != nil || !found || info.Name != want {
			t.Errorf("info of %s = %+v, %v, %v; want named %q", id, info, found, err, want)
		}
	}
}

func TestSpendUsageRepo_AnEmptyEmailIsNone(t *testing.T) {
	f := newUsageFixture(t)
	id := f.user("https://idp-a", "noemail", "No Email", true, "jit")
	f.exec(`UPDATE users SET email = '' WHERE id = ?`, id)
	f.calls(costed(10, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(id) }))

	if row := rowNamed(f.list(UsageUsers, UsageListQuery{}), "No Email"); row == nil || row.Email != nil {
		t.Errorf("row = %+v, want listed with no email", row)
	}
}

func TestSpendUsageRepo_AnInactiveUserWhoSpentIsListed(t *testing.T) {
	f := newUsageFixture(t)
	gone := f.user("https://idp-a", "gone", "Gone", false, "jit")
	f.calls(costed(10, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(gone) }))

	if row := rowNamed(f.list(UsageUsers, UsageListQuery{AllSubjects: true}), "Gone"); row == nil || row.Totals.Cost != 10 {
		t.Errorf("inactive spender = %+v, want listed with its spend", row)
	}
}

func TestSpendUsageRepo_ABudgetFilterKeepsTheUsersItAppliesTo(t *testing.T) {
	f := newUsageFixture(t)
	eng := &Budget{Name: "Engineering", SubjectType: BudgetSubjectUser, Amount: new(int64(100)), CreatedBy: "x", UpdatedBy: "x"}
	if err := NewBudgetsRepo(f.db).Create(t.Context(), eng); err != nil {
		t.Fatal(err)
	}
	alice := f.user("https://idp-a", "alice", "Alice", true, "jit")
	f.user("https://idp-a", "bob", "Bob", true, "jit")
	carol := f.user("https://idp-a", "carol", "Carol", true, "jit")
	insertOrphanAssignment(t, f.db, alice, eng.ID)
	insertOrphanAssignment(t, f.db, carol, SeededDefaultUserBudgetID)
	f.calls(
		costed(10, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(alice) }),
		costed(5, func(e *SpendLedgerEntry) { e.UserSub = new("unknown") }),
		costed(3, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(idgen.New()) }),
	)

	for _, c := range []struct {
		name   string
		budget UserBudgetMatch
		want   []string
	}{
		{"Engineering", UserBudgetMatch{BudgetID: eng.ID}, []string{"Alice"}},
		{"the default", UserBudgetMatch{BudgetID: SeededDefaultUserBudgetID, IsDefault: true}, []string{"Bob", "Carol", "bare"}},
	} {
		page := f.pageOf(UsageUsers, UsageListQuery{AllSubjects: true, Budget: &c.budget, Sort: UsageSortName})
		var names []string
		for _, row := range page.Rows {
			switch {
			case row.SubjectID == nil:
				names = append(names, "bare")
			case row.Name == "Alice" || row.Name == "Bob" || row.Name == "Carol" || row.Name == "":
				names = append(names, row.Name)
			}
		}
		if fmt.Sprint(names) != fmt.Sprint(c.want) || page.Count != len(page.Rows) {
			t.Errorf("%s's users = %v (count %d of %d rows), want %v", c.name, names, page.Count, len(page.Rows), c.want)
		}
	}
	if _, err := f.usage.List(t.Context(), UsageAgents, UsageListQuery{Budget: &UserBudgetMatch{BudgetID: eng.ID}, Limit: 1, Sort: UsageSortCost}); err == nil {
		t.Error("a budget filter on agents was accepted, want an error")
	}
}

func TestSpendUsageRepo_UserSubjectOwnershipTellsOwnedSharedAndUnknown(t *testing.T) {
	f := newUsageFixture(t)
	alice := f.user("https://idp-a", "alice", "Alice", true, "jit")
	f.user("https://idp-a", "shared", "A", true, "jit")
	f.user("https://idp-b", "shared", "B", true, "jit")
	for sub, want := range map[string]UserSubjectOwner{
		"alice":  {UserID: alice},
		"shared": {Shared: true},
		"nobody": {},
	} {
		got, err := f.usage.UserSubjectOwnership(t.Context(), sub)
		if err != nil || got != want {
			t.Errorf("ownership of %s = %+v, %v; want %+v", sub, got, err, want)
		}
	}
}

func TestSpendUsageRepo_AnUpperCaseIDNamesTheSameSubject(t *testing.T) {
	f := newUsageFixture(t)
	alice := f.user("https://idp-a", "alice", "Alice", true, "jit")
	agent := f.agent("qa", "standard", false)
	f.calls(
		costed(100, func(e *SpendLedgerEntry) {
			e.UserSub, e.UserID = nil, new(alice)
			e.AgentName, e.AgentID = nil, new(agent)
		}),
		costed(40, func(e *SpendLedgerEntry) { e.UserSub = new("alice") }),
	)

	if got := f.totals(UsageSubject{Kind: UsageUsers, ID: strings.ToUpper(alice)}); got.Cost != 100 {
		t.Errorf("Alice by an upper-case id = %d, want her row's 100", got.Cost)
	}
	if got := f.totals(UsageSubject{Kind: UsageAgents, ID: strings.ToUpper(agent)}); got.Cost != 100 {
		t.Errorf("agent by an upper-case id = %d, want 100", got.Cost)
	}
}

func TestSpendUsageRepo_SpendWithNoAgentIsInNoAgentsRow(t *testing.T) {
	f := newUsageFixture(t)
	f.calls(costed(10, nil), costed(5, func(e *SpendLedgerEntry) { e.AgentName = nil }))

	rows := f.list(UsageAgents, UsageListQuery{})

	if len(rows) != 1 || rows[0].Name != "qa" || rows[0].Totals.Cost != 10 {
		t.Errorf("agents = %+v, want only qa with its 10", rows)
	}
}

func TestSpendUsageRepo_AYAMLWorkflowIsListedByItsName(t *testing.T) {
	f := newUsageFixture(t)
	f.calls(costed(10, func(e *SpendLedgerEntry) { e.RootKind, e.RootName = new("workflow"), new("nightly-report") }))

	rows := f.list(UsageWorkflows, UsageListQuery{})

	if len(rows) != 1 || rows[0].Name != "nightly-report" || rows[0].SubjectID != nil || rows[0].Totals.Cost != 10 {
		t.Errorf("workflows = %+v, want nightly-report by name", rows)
	}
	if got := f.totals(UsageSubject{Kind: UsageWorkflows, Name: "nightly-report"}); got.Cost != 10 || got.Activities != 1 {
		t.Errorf("nightly-report totals = %+v, want 10 and 1 activity", got)
	}
}

func TestSpendUsageRepo_LedgerCostSelectsAUserAsTheRollupsDo(t *testing.T) {
	f := newUsageFixture(t)
	alice := f.user("https://idp-a", "alice", "Alice", true, "jit")
	fileCalls(t, f.db,
		costed(100, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(alice) }),
		costed(40, func(e *SpendLedgerEntry) { e.UserSub = new("alice"); e.TraceID = new("t2") }),
		costed(7, func(e *SpendLedgerEntry) { e.UserSub = new("bob") }),
	)

	for _, c := range []struct {
		subject UsageSubject
		want    int64
	}{
		{UsageSubject{Kind: UsageUsers, ID: alice}, 100},
		{UsageSubject{Kind: UsageUsers, Name: "alice"}, 40},
	} {
		got, err := f.usage.LedgerCost(t.Context(), c.subject, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
		if err != nil || got != c.want {
			t.Errorf("ledger cost of %+v = %d, %v; want %d", c.subject, got, err, c.want)
		}
	}
}

func TestSpendUsageRepo_ModelsSplitByTheProviderModelThatServed(t *testing.T) {
	f := newUsageFixture(t)
	general := f.model("General")
	onGeneral := func(cost int64, trace string) SpendLedgerEntry {
		return costed(cost, func(e *SpendLedgerEntry) {
			e.ModelName, e.ModelConfigID, e.TraceID = nil, new(general), new(trace)
		})
	}
	sonnet45, sonnet46, unrecorded := onGeneral(30, "t1"), onGeneral(50, "t2"), onGeneral(5, "t3")
	f.calls(sonnet45, sonnet46, unrecorded)
	details := []SpendLedgerDetail{
		{ID: sonnet45.ID, ModelKey: new("General"), ResolvedModelName: new("claude-sonnet-4-5"), ResolvedModelProvider: new("anthropic")},
		{ID: sonnet46.ID, ModelKey: new("General"), ResolvedModelName: new("claude-sonnet-4-6"), ResolvedModelProvider: new("anthropic")},
	}
	if _, err := NewSpendLedgerRepo(f.db).InsertDetails(t.Context(), details); err != nil {
		t.Fatalf("insert details: %v", err)
	}
	refreshDay(t, NewSpendRollupRepo(f.db), rollupTestDay, true)

	row := UsageRow{SubjectID: new(general)}
	served, err := f.usage.ServedModels(t.Context(), []UsageRow{row}, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("served models: %v", err)
	}
	got := served[UsageRowKey(row)]
	if len(got) != 3 || *got[0].Name != "claude-sonnet-4-6" || got[0].Totals.Cost != 50 || *got[0].Provider != "anthropic" || got[0].Totals.Activities != 1 ||
		*got[1].Name != "claude-sonnet-4-5" || got[1].Totals.Cost != 30 || got[2].Name != nil || got[2].Totals.Cost != 5 {
		t.Fatalf("served = %+v, want 4-6's $50, then 4-5's $30, then $5 unrecorded", got)
	}

	f.exec(`DELETE FROM model_configurations WHERE id = ?`, general)
	if rows := f.list(UsageModels, UsageListQuery{}); len(rows) != 1 || rows[0].Name != "General" {
		t.Errorf("deleted configuration = %+v, want it named by its alias", rows)
	}
}

func TestSpendUsageRepo_LateDetailsMoveACallOutOfTheUnrecordedBucket(t *testing.T) {
	f := newUsageFixture(t)
	general := f.model("General")
	call := costed(40, func(e *SpendLedgerEntry) { e.ModelName, e.ModelConfigID = nil, new(general) })
	fileCalls(t, f.db, call)
	rollups := NewSpendRollupRepo(f.db)
	refreshDay(t, rollups, rollupTestDay, false)
	if _, err := NewSpendLedgerRepo(f.db).InsertDetails(t.Context(), []SpendLedgerDetail{
		{ID: call.ID, ResolvedModelName: new("claude-sonnet-4-6"), ResolvedModelProvider: new("anthropic")},
	}); err != nil {
		t.Fatalf("insert details: %v", err)
	}
	refreshDay(t, rollups, rollupTestDay, false)

	row := UsageRow{SubjectID: new(general)}
	served, err := f.usage.ServedModels(t.Context(), []UsageRow{row}, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("served models: %v", err)
	}
	if got := served[UsageRowKey(row)]; len(got) != 1 || got[0].Name == nil || got[0].Totals.Cost != 40 {
		t.Errorf("served = %+v, want only claude-sonnet-4-6's 40", got)
	}
}

func TestSpendUsageRepo_ServedModelsAcrossInlineModelsAndDays(t *testing.T) {
	f := newUsageFixture(t)
	general := f.model("General")
	nextDay := rollupTestDay + SpendRollupDayMillis
	inline := costed(7, func(e *SpendLedgerEntry) { e.ModelName = new("gpt-4o-mini") })
	monday := costed(10, func(e *SpendLedgerEntry) { e.ModelName, e.ModelConfigID = nil, new(general) })
	tuesday := costed(20, func(e *SpendLedgerEntry) {
		e.ModelName, e.ModelConfigID, e.CompletedTime = nil, new(general), nextDay+3_600_000
	})
	fileCalls(t, f.db, inline, monday, tuesday)
	if _, err := NewSpendLedgerRepo(f.db).InsertDetails(t.Context(), []SpendLedgerDetail{
		{ID: monday.ID, ModelKey: new(general), ResolvedModelName: new("claude-sonnet-4-5"), ResolvedModelProvider: new("anthropic")},
		{ID: tuesday.ID, ModelKey: new(general), ResolvedModelName: new("claude-sonnet-4-6"), ResolvedModelProvider: new("anthropic")},
	}); err != nil {
		t.Fatalf("insert details: %v", err)
	}
	rollups := NewSpendRollupRepo(f.db)
	refreshDay(t, rollups, rollupTestDay, true)
	refreshDay(t, rollups, nextDay, true)

	inlineRow, configRow := UsageRow{SubjectName: new("gpt-4o-mini")}, UsageRow{SubjectID: new(general)}
	served, err := f.usage.ServedModels(t.Context(), []UsageRow{inlineRow, configRow}, rollupTestDay, nextDay+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("served models: %v", err)
	}
	if got := served[UsageRowKey(inlineRow)]; len(got) != 1 || *got[0].Name != "gpt-4o-mini" || got[0].Provider != nil {
		t.Errorf("inline = %+v, want served by gpt-4o-mini, no provider", got)
	}
	if got := served[UsageRowKey(configRow)]; len(got) != 2 || *got[0].Name != "claude-sonnet-4-6" || *got[1].Name != "claude-sonnet-4-5" {
		t.Errorf("configuration = %+v, want 4-6's 20 then 4-5's 10", got)
	}

	f.exec(`DELETE FROM model_configurations WHERE id = ?`, general)
	rows, err := f.usage.List(t.Context(), UsageModels, UsageListQuery{FromDay: rollupTestDay, ToDay: rollupTestDay + SpendRollupDayMillis, Sort: UsageSortCost, Limit: 5})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if row := rowWithID(rows.Rows, general); row == nil || row.Name != "claude-sonnet-4-5" {
		t.Errorf("deleted configuration named by its id = %+v, want its provider model", row)
	}
}

func rowWithID(rows []UsageRow, id string) *UsageRow {
	for i := range rows {
		if rows[i].SubjectID != nil && *rows[i].SubjectID == id {
			return &rows[i]
		}
	}
	return nil
}

func TestSpendUsageRepo_AUserIsReadFromTheRowsBookedUnderTheirID(t *testing.T) {
	f := newUsageFixture(t)
	id := f.user("https://idp-a", "parminder", "Parminder", true, "jit")
	f.calls(
		costed(10, func(e *SpendLedgerEntry) { e.UserSub, e.UserID, e.TraceID = nil, new(id), new("by-id") }),
		costed(5, func(e *SpendLedgerEntry) {
			e.UserSub, e.TraceID, e.RootName = new("parminder"), new("by-sub"), new("other-root")
		}),
	)
	from, to := rollupTestDay, rollupTestDay+SpendRollupDayMillis
	window := func(s UsageSubject) (UsageTotals, []UsageDay, []UsageRootCost) {
		t.Helper()
		totals := f.totals(s)
		days, err := f.usage.Daily(t.Context(), s, from, to)
		if err != nil {
			t.Fatalf("daily: %v", err)
		}
		roots, err := f.usage.UserRootCosts(t.Context(), s, from, to, 5)
		if err != nil {
			t.Fatalf("roots: %v", err)
		}
		return totals, days, roots
	}

	byID, idDays, idRoots := window(UsageSubject{Kind: UsageUsers, ID: id})
	bySub, subDays, subRoots := window(UsageSubject{Kind: UsageUsers, Name: "parminder"})

	if byID.Cost != 10 || byID.Activities != 1 || len(idDays) != 1 || idDays[0].Cost != 10 || idDays[0].Activities != 1 || len(idRoots) != 1 {
		t.Errorf("by id = %+v, %+v, %+v; want the call booked under the id alone", byID, idDays, idRoots)
	}
	if bySub.Cost != 5 || bySub.Activities != 1 || len(subDays) != 1 || subDays[0].Cost != 5 || len(subRoots) != 1 {
		t.Errorf("by subject = %+v, %+v, %+v; want the call booked under the subject alone", bySub, subDays, subRoots)
	}
	cost, err := f.usage.Cost(t.Context(), UsageSubject{Kind: UsageUsers, ID: id}, from, to)
	if err != nil || cost != 10 {
		t.Errorf("cost by id = %d, %v; want 10", cost, err)
	}
	users, err := f.usage.UserCosts(t.Context(), []string{strings.ToUpper(id)}, from, to)
	if err != nil || len(users) != 1 || users[id] != 10 {
		t.Errorf("user costs = %v, %v; want %s: 10", users, err, id)
	}
	subs, err := f.usage.SubCosts(t.Context(), []string{"parminder", "nobody"}, from, to)
	if err != nil || len(subs) != 1 || subs["parminder"] != 5 {
		t.Errorf("subject costs = %v, %v; want parminder: 5", subs, err)
	}
}

func TestSpendUsageRepo_DailyByRowAddsUpToEachRow(t *testing.T) {
	f := newUsageFixture(t)
	support, audit, general := f.agent("customer-support", "standard", false), f.workflow("expense-audit"), f.model("general")
	alice := f.user("https://idp-a", "alice", "Alice", true, "jit")
	f.user("https://idp-a", "shared", "Shared A", true, "jit")
	f.user("https://idp-b", "shared", "Shared B", true, "jit")
	trace := 0
	call := func(cost int64, edit func(*SpendLedgerEntry)) SpendLedgerEntry {
		trace++
		return costed(cost, func(e *SpendLedgerEntry) {
			e.TraceID = new(fmt.Sprintf("trace-%d", trace))
			e.CacheReadTokens, e.CacheWriteTokens = cost+1, cost/2
			edit(e)
		})
	}
	f.calls(
		call(10, func(e *SpendLedgerEntry) { e.AgentName, e.AgentID = nil, new(support) }),
		call(20, func(e *SpendLedgerEntry) {
			e.RootKind, e.RootName, e.RootID = new("workflow"), nil, new(audit)
			e.ModelName, e.ModelConfigID = nil, new(general)
		}),
		call(30, func(e *SpendLedgerEntry) { e.RootKind, e.RootName = new("workflow"), new("nightly-report") }),
		call(40, func(e *SpendLedgerEntry) { e.UserSub, e.UserID = nil, new(alice) }),
		call(50, func(e *SpendLedgerEntry) { e.UserSub = new("alice") }),
		call(60, func(e *SpendLedgerEntry) { e.UserSub = new("shared") }),
	)

	for _, kind := range []UsageKind{UsageWorkflows, UsageAgents, UsageModels, UsageUsers} {
		rows := f.list(kind, UsageListQuery{})
		daily, err := f.usage.DailyByRow(t.Context(), kind, rows, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
		if err != nil {
			t.Fatalf("%s daily by row: %v", kind, err)
		}
		var ids, names int
		for _, row := range rows {
			if row.Totals.Cost == 0 {
				continue
			}
			if row.SubjectID != nil {
				ids++
			} else {
				names++
			}
			var cost, activities, input, output, cacheRead, cacheWrite int64
			for _, d := range daily[UsageRowKey(row)] {
				cost, activities = cost+d.Cost, activities+d.Activities
				input, output = input+d.UncachedInputTokens, output+d.OutputTokens
				cacheRead, cacheWrite = cacheRead+d.CacheReadTokens, cacheWrite+d.CacheWriteTokens
			}
			if cost != row.Totals.Cost || activities != row.Totals.Activities {
				t.Errorf("%s %s: daily sums to %d over %d activities, want the row's %d over %d",
					kind, UsageRowKey(row), cost, activities, row.Totals.Cost, row.Totals.Activities)
			}
			if input != row.Totals.UncachedInputTokens || output != row.Totals.OutputTokens ||
				cacheRead != row.Totals.CacheReadTokens || cacheWrite != row.Totals.CacheWriteTokens {
				t.Errorf("%s %s: daily tokens sum to %d in, %d out, %d cache read, %d cache write; want the row's %d, %d, %d, %d",
					kind, UsageRowKey(row), input, output, cacheRead, cacheWrite,
					row.Totals.UncachedInputTokens, row.Totals.OutputTokens, row.Totals.CacheReadTokens, row.Totals.CacheWriteTokens)
			}
		}
		if ids == 0 || names == 0 {
			t.Errorf("%s page = %d rows with an id and %d by name, want both", kind, ids, names)
		}
	}
	if got := rowBookedAs(f.list(UsageUsers, UsageListQuery{}), "shared"); got == nil || got.Totals.Cost != 60 {
		t.Errorf("shared subject = %+v, want its own 60", got)
	}
}

func TestSpendUsageRepo_ReadsAreBoundedOnPostgres(t *testing.T) {
	f := newUsageFixture(t)
	if f.db.Dialect() != "postgres" {
		t.Skip("SQLite has no statement timeout")
	}
	type settings struct {
		Timeout string `db:"timeout"`
		Plans   string `db:"plans"`
	}
	got, err := readRow[settings](t.Context(), f.usage, `SELECT current_setting('statement_timeout') AS timeout,
		current_setting('plan_cache_mode') AS plans`)
	if err != nil || got == nil || got.Timeout != "15s" || got.Plans != "force_custom_plan" {
		t.Errorf("read settings = %+v, %v; want a 15s timeout and custom plans", got, err)
	}
}
