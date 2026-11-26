// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

func (f *usageFixture) workflowReads(subject UsageSubject) (totals UsageTotals, cost, ledger int64, daily []UsageDay) {
	f.t.Helper()
	ctx, from, to := f.t.Context(), rollupTestDay, rollupTestDay+SpendRollupDayMillis
	totals = f.totals(subject)
	var err error
	if cost, err = f.usage.Cost(ctx, subject, from, to); err != nil {
		f.t.Fatalf("cost: %v", err)
	}
	if ledger, err = f.usage.LedgerCost(ctx, subject, from, to); err != nil {
		f.t.Fatalf("ledger cost: %v", err)
	}
	if daily, err = f.usage.Daily(ctx, subject, from, to); err != nil {
		f.t.Fatalf("daily: %v", err)
	}
	return totals, cost, ledger, daily
}

func (f *usageFixture) nodeCosts(workflow string) []UsageNodeCost {
	f.t.Helper()
	nodes, err := f.usage.WorkflowNodeCosts(f.t.Context(), UsageSubject{Kind: UsageWorkflows, ID: workflow}, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		f.t.Fatalf("node costs: %v", err)
	}
	return nodes
}

func (f *usageFixture) workflowsCost() int64 {
	f.t.Helper()
	_, kinds, err := f.usage.TenantTotals(f.t.Context(), rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		f.t.Fatalf("tenant totals: %v", err)
	}
	return kinds.Workflows
}

func TestSpendUsageRepo_AWorkflowAnAgentStartedHasItsNodesSpend(t *testing.T) {
	f := newUsageFixture(t)
	orchestrator := f.agent("orchestrator", "standard", false)
	stories := f.workflow("story-generator")
	underOrchestrator := func(cost int64, edit func(*SpendLedgerEntry)) SpendLedgerEntry {
		return costed(cost, func(e *SpendLedgerEntry) {
			e.RootKind, e.RootName, e.RootID = new("agent"), nil, new(orchestrator)
			e.TraceID = new("story-run")
			if edit != nil {
				edit(e)
			}
		})
	}
	node := func(cost int64, agent string) SpendLedgerEntry {
		return underOrchestrator(cost, func(e *SpendLedgerEntry) { e.AgentName, e.WorkflowID = new(agent), new(stories) })
	}
	f.calls(underOrchestrator(100, nil), node(7, "outliner"), node(8, "writer"), node(9, "editor"))

	rows := f.list(UsageWorkflows, UsageListQuery{})
	if len(rows) != 1 || rows[0].Name != "story-generator" || rows[0].Totals.Cost != 24 || rows[0].Totals.Activities != 1 {
		t.Errorf("workflows = %+v, want story-generator with its nodes' 24 over one run, and not the orchestrator's own call", rows)
	}
	totals, cost, ledger, daily := f.workflowReads(UsageSubject{Kind: UsageWorkflows, ID: stories})
	if totals.Cost != 24 || totals.Activities != 1 || cost != 24 || ledger != 24 {
		t.Errorf("totals %+v, cost %d, ledger %d; want 24 over one run from each", totals, cost, ledger)
	}
	if len(daily) != 1 || daily[0].Cost != 24 || daily[0].Activities != 1 {
		t.Errorf("daily = %+v, want the day's 24 over one run", daily)
	}
	byRow, err := f.usage.DailyByRow(t.Context(), UsageWorkflows, rows, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if days := byRow[UsageRowKey(rows[0])]; err != nil || len(days) != 1 || days[0].Cost != 24 || days[0].Activities != 1 {
		t.Errorf("daily by row = %+v, %v; want the day's 24 over one run", days, err)
	}
	if got := f.workflowsCost(); got != 24 {
		t.Errorf("workflows' kind cost = %d, want 24", got)
	}
	if nodes := f.nodeCosts(stories); len(nodes) != 0 {
		t.Errorf("node costs = %+v, want none: the orchestrator's run has no workflow nodes", nodes)
	}
}

func TestSpendUsageRepo_ACallUnderOneWorkflowBothWaysCountsOnce(t *testing.T) {
	f := newUsageFixture(t)
	audit := f.workflow("expense-audit")
	underAudit := func(cost int64, node string, dispatched bool) SpendLedgerEntry {
		return costed(cost, func(e *SpendLedgerEntry) {
			e.RootKind, e.RootName, e.RootID, e.RootNodeID = new("workflow"), nil, new(audit), new(node)
			if dispatched {
				e.WorkflowID = new(audit)
			}
		})
	}
	startedByAnAgent := costed(2, func(e *SpendLedgerEntry) { e.WorkflowID, e.TraceID = new(audit), new("t2") })
	f.calls(underAudit(30, "review", true), underAudit(5, "notify", false), startedByAnAgent)

	rows := f.list(UsageWorkflows, UsageListQuery{})
	if len(rows) != 1 || rows[0].Totals.Cost != 37 || rows[0].Totals.Activities != 2 {
		t.Errorf("workflows = %+v, want expense-audit with 37 over two runs", rows)
	}
	totals, cost, ledger, _ := f.workflowReads(UsageSubject{Kind: UsageWorkflows, ID: audit})
	if totals.Cost != 37 || totals.Activities != 2 || cost != 37 || ledger != 37 {
		t.Errorf("totals %+v, cost %d, ledger %d; want 37 over two runs from each", totals, cost, ledger)
	}
	if got := f.workflowsCost(); got != 37 {
		t.Errorf("workflows' kind cost = %d, want 37", got)
	}
	nodes := f.nodeCosts(audit)
	if len(nodes) != 2 || nodes[0] != (UsageNodeCost{NodeID: "notify", Cost: 5}) || nodes[1] != (UsageNodeCost{NodeID: "review", Cost: 30}) {
		t.Errorf("node costs = %+v, want notify 5 and review 30, and nothing from the run an agent started", nodes)
	}
}

func TestSpendUsageRepo_NestedWorkflowsEachHaveTheCallsUnderThem(t *testing.T) {
	f := newUsageFixture(t)
	monthEnd, audit := f.workflow("month-end"), f.workflow("expense-audit")
	underMonthEnd := func(cost int64, node, dispatchedBy string) SpendLedgerEntry {
		return costed(cost, func(e *SpendLedgerEntry) {
			e.RootKind, e.RootName, e.RootID, e.RootNodeID = new("workflow"), nil, new(monthEnd), new(node)
			e.WorkflowID = new(dispatchedBy)
		})
	}
	f.calls(underMonthEnd(10, "run-audit", audit), underMonthEnd(4, "close", monthEnd))

	rows := f.list(UsageWorkflows, UsageListQuery{})
	outer, inner := rowNamed(rows, "month-end"), rowNamed(rows, "expense-audit")
	if len(rows) != 2 || outer == nil || inner == nil || outer.Totals.Cost != 14 || inner.Totals.Cost != 10 ||
		outer.Totals.Activities != 1 || inner.Totals.Activities != 1 {
		t.Errorf("workflows = %+v, want month-end 14 and expense-audit 10, one run each", rows)
	}
	if got := f.workflowsCost(); got != 14 {
		t.Errorf("workflows' kind cost = %d, want each call once: 14", got)
	}
	if totals, _, ledger, _ := f.workflowReads(UsageSubject{Kind: UsageWorkflows, ID: audit}); totals.Cost != 10 || ledger != 10 {
		t.Errorf("expense-audit totals %+v, ledger %d; want 10 from each", totals, ledger)
	}
	if nodes := f.nodeCosts(audit); len(nodes) != 0 {
		t.Errorf("expense-audit node costs = %+v, want none: run-audit is month-end's node", nodes)
	}
	if nodes := f.nodeCosts(monthEnd); len(nodes) != 2 || nodes[1] != (UsageNodeCost{NodeID: "run-audit", Cost: 10}) {
		t.Errorf("month-end node costs = %+v, want close 4 and run-audit 10", nodes)
	}

	daily, err := f.usage.DailyByRow(t.Context(), UsageWorkflows, rows, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil {
		t.Fatalf("daily by row: %v", err)
	}
	for _, row := range rows {
		days := daily[UsageRowKey(row)]
		if len(days) != 1 || days[0].Cost != row.Totals.Cost || days[0].Activities != row.Totals.Activities {
			t.Errorf("%s daily = %+v, want the row's %d over %d", row.Name, days, row.Totals.Cost, row.Totals.Activities)
		}
	}
}

func TestSpendUsageRepo_ADispatchingWorkflowIsNamedLiveByNameOrByItsLabel(t *testing.T) {
	f := newUsageFixture(t)
	retired, renamed := f.workflow("retired"), f.workflow("draft")
	gone := costed(6, func(e *SpendLedgerEntry) { e.WorkflowID = new(retired) })
	fileCalls(t, f.db, gone)
	if _, err := NewSpendLedgerRepo(f.db).InsertDetails(t.Context(), []SpendLedgerDetail{{ID: gone.ID, ResolvedWorkflowName: new("Retired Flow")}}); err != nil {
		t.Fatalf("file details: %v", err)
	}
	f.calls(
		costed(3, func(e *SpendLedgerEntry) { e.WorkflowName = new("nightly-report") }),
		costed(2, func(e *SpendLedgerEntry) { e.WorkflowID = new(renamed) }),
	)
	f.exec(`DELETE FROM workflows WHERE id = ?`, retired)
	f.exec(`UPDATE workflows SET name = 'Release Notes' WHERE id = ?`, renamed)

	rows := f.list(UsageWorkflows, UsageListQuery{})
	if got := rowNamed(rows, "Retired Flow"); got == nil || got.Totals.Cost != 6 {
		t.Errorf("deleted workflow = %+v, want it named by its resolved name with 6", got)
	}
	if got := rowNamed(rows, "Release Notes"); got == nil || got.Totals.Cost != 2 {
		t.Errorf("renamed workflow = %+v, want its live name with 2", got)
	}
	if got := rowBookedAs(rows, "nightly-report"); got == nil || got.SubjectID != nil || got.Totals.Cost != 3 {
		t.Errorf("YAML workflow = %+v, want it by the name it was booked under with 3", got)
	}
	if got := f.totals(UsageSubject{Kind: UsageWorkflows, Name: "nightly-report"}); got.Cost != 3 || got.Activities != 1 {
		t.Errorf("YAML workflow totals = %+v, want 3 over one run", got)
	}
	if ledger, err := f.usage.LedgerCost(t.Context(), UsageSubject{Kind: UsageWorkflows, Name: "nightly-report"}, rollupTestDay, rollupTestDay+SpendRollupDayMillis); err != nil || ledger != 3 {
		t.Errorf("YAML workflow ledger cost = %d, %v; want 3", ledger, err)
	}
	info, found, err := f.usage.SubjectInfo(t.Context(), UsageSubject{Kind: UsageWorkflows, ID: retired}, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil || !found || info.Name != "Retired Flow" || info.Live {
		t.Errorf("deleted workflow info = %+v, %v, %v; want it found as Retired Flow with no live row", info, found, err)
	}
}

func TestSpendUsageRepo_AnAgentStartedWorkflowCountsEachUsersRunsOnEachDay(t *testing.T) {
	f := newUsageFixture(t)
	orchestrator := f.agent("orchestrator", "standard", false)
	stories := f.workflow("story-generator")
	nextDay := rollupTestDay + SpendRollupDayMillis
	node := func(cost int64, user, trace string, day int64) SpendLedgerEntry {
		return costed(cost, func(e *SpendLedgerEntry) {
			e.RootKind, e.RootName, e.RootID = new("agent"), nil, new(orchestrator)
			e.UserSub, e.TraceID, e.WorkflowID = new(user), new(trace), new(stories)
			e.CompletedTime = day + 3_600_000
		})
	}
	fileCalls(t, f.db,
		node(7, "alice", "alice-run", rollupTestDay), node(8, "alice", "alice-run", rollupTestDay),
		node(5, "bob", "bob-run", rollupTestDay),
		node(4, "alice", "alice-next-run", nextDay))
	rollups := NewSpendRollupRepo(f.db)
	refreshDay(t, rollups, rollupTestDay, true)
	refreshDay(t, rollups, nextDay, true)

	window := UsageListQuery{FromDay: rollupTestDay, ToDay: nextDay + SpendRollupDayMillis}
	rows := f.list(UsageWorkflows, window)
	if len(rows) != 1 || rows[0].Totals.Cost != 24 || rows[0].Totals.Activities != 3 {
		t.Errorf("workflows = %+v, want story-generator with 24 over three runs", rows)
	}
	subject := UsageSubject{Kind: UsageWorkflows, ID: stories}
	daily, err := f.usage.Daily(t.Context(), subject, window.FromDay, window.ToDay)
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	if len(daily) != 2 || daily[0].Cost != 20 || daily[0].Activities != 2 || daily[1].Cost != 4 || daily[1].Activities != 1 {
		t.Errorf("daily = %+v, want alice's and bob's runs for 20 on the first day and alice's 4 on the next", daily)
	}
	byRow, err := f.usage.DailyByRow(t.Context(), UsageWorkflows, rows, window.FromDay, window.ToDay)
	if days := byRow[UsageRowKey(rows[0])]; err != nil || len(days) != 2 || days[0].Activities != 2 || days[1].Activities != 1 {
		t.Errorf("daily by row = %+v, %v; want two runs then one", days, err)
	}
	if totals, err := f.usage.Totals(t.Context(), subject, window.FromDay, window.ToDay); err != nil || totals.Cost != 24 || totals.Activities != 3 {
		t.Errorf("totals = %+v, %v; want 24 over three runs", totals, err)
	}
}

func TestSpendUsageRepo_ACallUnderOneYAMLWorkflowBothWaysCountsOnce(t *testing.T) {
	f := newUsageFixture(t)
	underNightly := func(cost int64, dispatched bool) SpendLedgerEntry {
		return costed(cost, func(e *SpendLedgerEntry) {
			e.RootKind, e.RootName, e.RootID, e.RootNodeID = new("workflow"), new("nightly-report"), nil, new("collect")
			if dispatched {
				e.WorkflowName = new("nightly-report")
			}
		})
	}
	f.calls(underNightly(30, true), underNightly(5, false))

	rows := f.list(UsageWorkflows, UsageListQuery{})
	if got := rowBookedAs(rows, "nightly-report"); len(rows) != 1 || got == nil || got.Totals.Cost != 35 || got.Totals.Activities != 1 {
		t.Errorf("workflows = %+v, want nightly-report once with 35 over one run", rows)
	}
	totals, cost, ledger, _ := f.workflowReads(UsageSubject{Kind: UsageWorkflows, Name: "nightly-report"})
	if totals.Cost != 35 || totals.Activities != 1 || cost != 35 || ledger != 35 {
		t.Errorf("totals %+v, cost %d, ledger %d; want 35 over one run from each", totals, cost, ledger)
	}
	if got := f.workflowsCost(); got != 35 {
		t.Errorf("workflows' kind cost = %d, want 35", got)
	}
}

func TestSpendUsageRepo_ADeletedRootWorkflowIsNamedByItsLabel(t *testing.T) {
	f := newUsageFixture(t)
	retired := f.workflow("retired")
	call := costed(6, func(e *SpendLedgerEntry) {
		e.RootKind, e.RootName, e.RootID, e.RootNodeID = new("workflow"), nil, new(retired), new("draft")
	})
	fileCalls(t, f.db, call)
	if _, err := NewSpendLedgerRepo(f.db).InsertDetails(t.Context(), []SpendLedgerDetail{{ID: call.ID, ResolvedRootName: new("Retired Flow")}}); err != nil {
		t.Fatalf("file details: %v", err)
	}
	refreshDay(t, NewSpendRollupRepo(f.db), rollupTestDay, true)
	f.exec(`DELETE FROM workflows WHERE id = ?`, retired)

	rows := f.list(UsageWorkflows, UsageListQuery{})
	if got := rowNamed(rows, "Retired Flow"); len(rows) != 1 || got == nil || got.Totals.Cost != 6 {
		t.Errorf("workflows = %+v, want the deleted root named Retired Flow with 6", rows)
	}
	info, found, err := f.usage.SubjectInfo(t.Context(), UsageSubject{Kind: UsageWorkflows, ID: retired}, rollupTestDay, rollupTestDay+SpendRollupDayMillis)
	if err != nil || !found || info.Name != "Retired Flow" || info.Live {
		t.Errorf("deleted root info = %+v, %v, %v; want it found as Retired Flow with no live row", info, found, err)
	}
}
