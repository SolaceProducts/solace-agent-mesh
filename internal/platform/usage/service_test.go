// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type serviceFixture struct {
	t   *testing.T
	db  *store.DB
	svc *Service
	now time.Time
}

func newServiceFixture(t *testing.T, now time.Time) *serviceFixture {
	db := store.TestDB(t)
	f := &serviceFixture{t: t, db: db, now: now}
	f.svc = &Service{
		Usage:     store.NewSpendUsageRepo(db, nil),
		Budgets:   Budgets{Assignments: store.NewBudgetAssignmentsRepo(db), Budgets: store.NewBudgetsRepo(db), TopUps: store.NewBudgetTopUpsRepo(db)},
		Workflows: store.NewWorkflowRepo(db),
		Logger:    slog.Default(),
		Now:       func() time.Time { return f.now },
	}
	f.historyBuilt()
	return f
}

func (f *serviceFixture) historyBuilt() {
	f.t.Helper()
	rollups := store.NewSpendRollupRepo(f.db)
	if err := rollups.MarkRefreshed(f.t.Context(), f.now.Add(-time.Minute).UnixMilli()); err != nil {
		f.t.Fatalf("mark refreshed: %v", err)
	}
	if err := rollups.MarkReconciled(f.t.Context(), f.now.Add(-time.Hour).UnixMilli()); err != nil {
		f.t.Fatalf("mark reconciled: %v", err)
	}
}

func (f *serviceFixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), query, args...); err != nil {
		f.t.Fatalf("exec %q: %v", query, err)
	}
}

func (f *serviceFixture) user(name string) string {
	f.t.Helper()
	id := idgen.New()
	f.exec(`INSERT INTO users (id, issuer, sub, display_name, email, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'https://idp', ?, ?, ?, 'test', 'test', 0, 0)`, id, name, name, name+"@example.com")
	return id
}

func (f *serviceFixture) budget(userID string, dollarCap int64) {
	f.t.Helper()
	id := idgen.New()
	f.exec(`INSERT INTO budgets (id, name, name_lower, subject_type, amount, warn_percentage, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, 'user', ?, 80, 'test', 'test', 0, 0)`, id, id, id, dollars(dollarCap))
	f.exec(`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'user', ?, ?, 'test', 'test', 0, 0)`, idgen.New(), userID, id)
}

func (f *serviceFixture) topUp(userID string, dollarAmount int64, expires time.Time) {
	f.t.Helper()
	f.exec(`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'user', ?, ?, ?, 'test', 'test', 0, 0)`, idgen.New(), userID, dollars(dollarAmount), expires.UnixMilli())
}

func (f *serviceFixture) agent(name string) string {
	f.t.Helper()
	id := idgen.New()
	f.exec(`INSERT INTO agents (id, name, type, is_ephemeral, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, 'standard', 0, 'test', 'test', 0, 0)`, id, name)
	return id
}

func (f *serviceFixture) workflow(name, appConfig string) string {
	f.t.Helper()
	id := idgen.New()
	f.exec(`INSERT INTO workflows (id, name, app_config, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, 'test', 'test', 0, 0)`, id, name, appConfig)
	return id
}

type call struct {
	at                         time.Time
	cents                      int64
	userID                     string
	userSub                    string
	agentID                    string
	modelID                    string
	rootKind                   string
	rootID                     string
	rootName                   string
	rootNode                   string
	workflowID                 string
	servedName, servedProvider string
}

func (f *serviceFixture) file(calls ...call) {
	f.t.Helper()
	entries := make([]store.SpendLedgerEntry, len(calls))
	days := map[int64]bool{}
	for i, c := range calls {
		e := store.SpendLedgerEntry{
			ID: idgen.New(), CompletedTime: c.at.UnixMilli(), Surface: "agent",
			ModelName: new("m"), TraceID: new(idgen.New()), UncachedInputTokens: 1,
			Cost: &spend.Cost{UncachedInput: c.cents * 100_000_000},
		}
		switch {
		case c.userID != "":
			e.UserID = new(c.userID)
		case c.userSub != "":
			e.UserSub = new(c.userSub)
		default:
			e.UserSub = new("someone")
		}
		if c.agentID != "" {
			e.AgentID = new(c.agentID)
		}
		if c.modelID != "" {
			e.ModelName, e.ModelConfigID = nil, new(c.modelID)
		}
		switch {
		case c.rootKind != "" && c.rootID != "":
			e.RootKind, e.RootID = new(c.rootKind), new(c.rootID)
		case c.rootKind != "":
			e.RootKind, e.RootName = new(c.rootKind), new(c.rootName)
		}
		if c.rootNode != "" {
			e.RootNodeID = new(c.rootNode)
		}
		if c.workflowID != "" {
			e.WorkflowID = new(c.workflowID)
		}
		entries[i] = e
		days[startOfDay(c.at.UnixMilli())] = true
	}
	if _, err := store.NewSpendLedgerRepo(f.db).Insert(f.t.Context(), entries); err != nil {
		f.t.Fatalf("file calls: %v", err)
	}
	var details []store.SpendLedgerDetail
	for i, c := range calls {
		if c.servedName != "" {
			details = append(details, store.SpendLedgerDetail{ID: entries[i].ID, ResolvedModelName: new(c.servedName), ResolvedModelProvider: new(c.servedProvider)})
		}
	}
	if len(details) > 0 {
		if _, err := store.NewSpendLedgerRepo(f.db).InsertDetails(f.t.Context(), details); err != nil {
			f.t.Fatalf("file details: %v", err)
		}
	}
	rollups := store.NewSpendRollupRepo(f.db)
	for d := range days {
		if err := rollups.RefreshDay(f.t.Context(), d, true); err != nil {
			f.t.Fatalf("roll up %d: %v", d, err)
		}
	}
}

func cents(n int64) int64 { return n * 100_000_000 }

func (f *serviceFixture) users(req ListRequest) List {
	f.t.Helper()
	if req.Sort == "" {
		req.Sort = "cost"
	}
	if req.PageNumber == 0 {
		req.PageNumber, req.PageSize = 1, 25
	}
	list, err := f.svc.List(f.t.Context(), store.UsageUsers, req)
	if err != nil {
		f.t.Fatalf("list users: %v", err)
	}
	return list
}

func rowFor(list List, id string) *Row {
	for i := range list.Rows {
		if r := list.Rows[i]; r.SubjectID != nil && *r.SubjectID == id {
			return &list.Rows[i]
		}
	}
	return nil
}

func TestService_ThisMonthIsMeasuredAgainstTheWholeMonthsBudget(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	parminder := f.user("parminder")
	f.budget(parminder, 450)
	f.file(call{at: at(2026, 10, 2, 9, 0), cents: 9_000, userID: parminder})

	row := rowFor(f.users(ListRequest{Window: WindowRequest{Period: PeriodThisMonth}}), parminder)

	if row == nil || row.BudgetUsage == nil || *row.BudgetUsage.WindowBudget != dollars(450) || !near(*row.BudgetUsage.UsagePercent, 20) {
		t.Fatalf("row = %+v, want $90 against the whole $450", row)
	}
	if row.Budget.Standing.PeriodSpend != dollars(90) || row.Budget.Standing.Status != StatusOK {
		t.Errorf("standing = %+v, want $90 this period and ok", row.Budget.Standing)
	}
}

func TestService_ARollingWindowProratesEachMonthsBudget(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	topped := f.user("topped")
	f.budget(topped, 300)
	f.topUp(topped, 10, at(2026, 11, 1, 0, 0))
	f.file(call{at: at(2026, 9, 20, 9, 0), cents: 1_000, userID: topped})

	row := rowFor(f.users(ListRequest{Window: WindowRequest{Period: PeriodLast30Days}}), topped)

	want := dollars(150) + dollars(150)
	if row == nil || row.BudgetUsage == nil || *row.BudgetUsage.WindowBudget != want {
		t.Fatalf("window budget = %+v, want %d", row, want)
	}
	if row.Budget.Standing.PeriodSpend != 0 {
		t.Errorf("period spend = %d, want October's, none", row.Budget.Standing.PeriodSpend)
	}
}

func TestService_SummaryComparesWithTheSameSpanLastMonth(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	f.file(
		call{at: at(2026, 9, 10, 9, 0), cents: 1_000},
		call{at: at(2026, 9, 15, 18, 0), cents: 500},
		call{at: at(2026, 9, 15, 20, 0), cents: 700},
		call{at: at(2026, 10, 3, 9, 0), cents: 3_000},
	)

	sum, err := f.svc.Summary(t.Context(), WindowRequest{})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}

	if sum.Totals.Cost != cents(3_000) || sum.Compared == nil || sum.Compared.Cost != cents(1_500) || !near(*sum.Compared.ChangePercent, 100) {
		t.Fatalf("summary = %+v, compared %+v; want $30 against $15 before 19:20 on the 15th", sum.Totals, sum.Compared)
	}
	if len(sum.Daily) != 15 || sum.Daily[2].Cost != cents(3_000) || sum.Daily[0].Day != ms(at(2026, 10, 1, 0, 0)) {
		t.Errorf("daily = %+v, want October 1–15 with $30 on the 3rd", sum.Daily)
	}
	if sum.DataAsOf == nil || *sum.DataAsOf != f.now.Add(-time.Minute).UnixMilli() || !sum.HistoryComplete {
		t.Errorf("freshness = %+v, want refreshed a minute ago with the history built", sum.Freshness)
	}
}

func TestService_NothingIsComparedWhileTheHistoryIsBuilt(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	f.exec(`UPDATE token_spend_rollup_state SET reconciled_time = NULL`)
	f.file(call{at: at(2026, 9, 10, 9, 0), cents: 1_000})

	sum, err := f.svc.Summary(t.Context(), WindowRequest{})
	if err != nil || sum.Compared != nil || sum.HistoryComplete {
		t.Errorf("summary = %+v, %v; want no comparison while incomplete", sum, err)
	}
}

func TestService_SummaryCarriesTheMeshBudgetsStanding(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	f.exec(`UPDATE budgets SET amount = ?, warn_percentage = 80 WHERE subject_type = 'mesh' AND is_default = 1`, dollars(100))
	f.file(call{at: at(2026, 10, 2, 9, 0), cents: 8_500}, call{at: at(2026, 9, 30, 9, 0), cents: 9_900})

	sum, err := f.svc.Summary(t.Context(), WindowRequest{Period: PeriodLast7Days})
	if err != nil || sum.MeshBudget == nil {
		t.Fatalf("summary = %+v, %v; want a mesh budget", sum, err)
	}
	if got := sum.MeshBudget.Standing; got.PeriodSpend != cents(8_500) || got.Status != StatusApproaching {
		t.Errorf("mesh standing = %+v, want October's $85 of $100, approaching", got)
	}
}

func TestService_SummaryHasNoComparisonBeforeTheHistory(t *testing.T) {
	f := newServiceFixture(t, midOctober)

	sum, err := f.svc.Summary(t.Context(), WindowRequest{Period: PeriodLast90Days})
	if err != nil || sum.Compared != nil || len(sum.Daily) != 90 {
		t.Errorf("summary = %+v, %v; want 90 days and no comparison", sum, err)
	}
}

func TestService_BudgetUsageSortPagesAcrossEveryUser(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	low, high, none := f.user("low"), f.user("high"), f.user("none")
	f.budget(low, 100)
	f.budget(high, 100)
	f.exec(`UPDATE budgets SET amount = NULL WHERE subject_type = 'user' AND is_default = 1`)
	f.file(
		call{at: at(2026, 10, 2, 9, 0), cents: 1_000, userID: low},
		call{at: at(2026, 10, 2, 9, 0), cents: 9_000, userID: high},
		call{at: at(2026, 10, 2, 9, 0), cents: 50_000, userID: none},
	)

	first := f.users(ListRequest{Sort: ListSort(SortByBudgetUsage), Descending: true, PageNumber: 1, PageSize: 2})
	second := f.users(ListRequest{Sort: ListSort(SortByBudgetUsage), Descending: true, PageNumber: 2, PageSize: 2})

	if first.Count != 3 || len(first.Rows) != 2 || *first.Rows[0].SubjectID != high || *first.Rows[1].SubjectID != low {
		t.Errorf("page 1 = %+v, want high then low of 3", first.Rows)
	}
	if len(second.Rows) != 1 || *second.Rows[0].SubjectID != none || second.Rows[0].BudgetUsage != nil {
		t.Errorf("page 2 = %+v, want the unlimited user, with no budget usage", second.Rows)
	}
	for _, sort := range []BudgetSort{SortByBudgetUsage, SortByBudget} {
		past := f.users(ListRequest{Sort: ListSort(sort), Descending: true, PageNumber: 99, PageSize: 25})
		if past.Rows == nil || len(past.Rows) != 0 || past.Count != 3 {
			t.Errorf("%s page 99 = %d rows (nil %t) of %d, want an empty page that still counts 3", sort, len(past.Rows), past.Rows == nil, past.Count)
		}
	}
}

func TestService_ALastMonthListStandsUsersOnThisMonth(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	parminder := f.user("parminder")
	f.budget(parminder, 100)
	f.file(
		call{at: at(2026, 9, 20, 9, 0), cents: 9_000, userID: parminder},
		call{at: at(2026, 10, 2, 9, 0), cents: 3_000, userID: parminder},
		call{at: at(2026, 10, 3, 9, 0), cents: 1_000, userSub: "parminder"},
	)

	list := f.users(ListRequest{Window: WindowRequest{Period: PeriodLastMonth}})
	row := rowFor(list, parminder)

	if row == nil || row.Totals.Cost != cents(9_000) || row.Budget == nil || row.Budget.Standing.PeriodSpend != cents(3_000) {
		t.Fatalf("row = %+v, want September's $90 standing on October's $30", row)
	}
	f.file(call{at: at(2026, 9, 21, 9, 0), cents: 500, userSub: "parminder"})
	list = f.users(ListRequest{Window: WindowRequest{Period: PeriodLastMonth}})
	var bare *Row
	for i := range list.Rows {
		if r := &list.Rows[i]; r.SubjectID == nil && r.SubjectName != nil && *r.SubjectName == "parminder" {
			bare = r
		}
	}
	if bare == nil || bare.Budget == nil || bare.Budget.Budget.Source != budget.SourceDefault || bare.Budget.Standing.PeriodSpend != cents(1_000) {
		t.Errorf("bare subject = %+v, want the tier default standing on October's $10", bare)
	}
	if *row.BudgetUsage.WindowBudget != dollars(100) || !near(*row.BudgetUsage.UsagePercent, 90) {
		t.Errorf("budget usage = %+v, want $90 of September's whole $100", row.BudgetUsage)
	}
}

func TestService_RefusesABadPage(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	for name, tc := range map[string]struct {
		req   ListRequest
		field string
	}{
		"page 0":         {ListRequest{Sort: "cost", PageNumber: 0, PageSize: 25}, "pageNumber"},
		"no rows":        {ListRequest{Sort: "cost", PageNumber: 1, PageSize: 0}, "pageSize"},
		"too many rows":  {ListRequest{Sort: "cost", PageNumber: 1, PageSize: 101}, "pageSize"},
		"a budget sort":  {ListRequest{Sort: ListSort(SortByBudgetUsage), PageNumber: 1, PageSize: -1}, "pageSize"},
		"an unknown one": {ListRequest{Sort: "tokens", PageNumber: 1, PageSize: 25}, "sort"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := f.svc.List(t.Context(), store.UsageUsers, tc.req)
			if !namesField(err, tc.field) {
				t.Errorf("err = %v, want a validation error on %s", err, tc.field)
			}
		})
	}
}

func TestService_OnlyUsersSortByBudget(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	_, err := f.svc.List(t.Context(), store.UsageAgents, ListRequest{Sort: ListSort(SortByBudget), PageNumber: 1, PageSize: 25})
	if !namesField(err, "sort") {
		t.Errorf("err = %v, want a validation error on sort", err)
	}
}

func TestService_ListsShareTheDeploymentsSpend(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	a, b := f.agent("a"), f.agent("b")
	f.file(
		call{at: at(2026, 10, 2, 9, 0), cents: 3_000, agentID: a},
		call{at: at(2026, 10, 2, 9, 0), cents: 1_000, agentID: b},
	)

	list, err := f.svc.List(t.Context(), store.UsageAgents, ListRequest{Sort: "cost", Descending: true, PageNumber: 1, PageSize: 25})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Rows) != 2 || !near(*list.Rows[0].ShareOfSpend, 75) || *list.Rows[0].CostPerActivity != cents(3_000) {
		t.Errorf("rows = %+v, want a at 75%% and $30 per activity", list.Rows)
	}
}

const expenseAudit = `{"workflow": {"output_mapping": {"result": "{{review.output}}"}, "nodes": [
	{"id": "review", "type": "agent", "agent_name": "Policy Reviewer"},
	{"id": "notify", "type": "tool", "tool_name": "send_email"}]}}`

func TestService_WorkflowNodesFollowTheDefinition(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	wf := f.workflow("Expense Audit", expenseAudit)
	under := func(when time.Time, c int64, node string) call {
		return call{at: when, cents: c, rootKind: "workflow", rootID: wf, rootNode: node}
	}
	f.file(
		under(at(2026, 9, 3, 9, 0), 1_000, "review"),
		under(at(2026, 10, 3, 9, 0), 2_000, "review"),
		under(at(2026, 10, 3, 9, 0), 500, "old_step"),
	)

	got, err := f.svc.Workflow(t.Context(), store.UsageSubject{Kind: store.UsageWorkflows, ID: wf}, WindowRequest{})
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}

	if got.Info.Name != "Expense Audit" || len(got.Nodes) != 3 {
		t.Fatalf("workflow = %+v, nodes %+v; want three nodes", got.Info, got.Nodes)
	}
	review, notify, old := got.Nodes[0], got.Nodes[1], got.Nodes[2]
	if review.NodeID != "review" || review.Name != "Policy Reviewer" || *review.Kind != "agent" || review.Cost != cents(2_000) ||
		!near(review.ShareOfWorkflow, 80) || review.ChangePercent == nil || !near(*review.ChangePercent, 100) {
		t.Errorf("review = %+v, want $20, 80%% of the workflow, up 100%%", review)
	}
	if notify.Name != "send_email" || *notify.Kind != "tool" || notify.Cost != 0 || notify.ChangePercent != nil {
		t.Errorf("notify = %+v, want the tool at $0 with no change", notify)
	}
	if old.NodeID != "old_step" || old.Kind != nil || old.Cost != cents(500) {
		t.Errorf("old = %+v, want the removed node's $5 with no kind", old)
	}
}

func TestService_AConnectorNodeIsNamedByItsConnector(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	wf := f.workflow("Ticket Sync", `{"workflow": {"output_mapping": {"result": "{{sync.output}}"}, "nodes": [
		{"id": "sync", "type": "tool", "connector": "jira-cloud"}]}}`)

	got, err := f.svc.Workflow(t.Context(), store.UsageSubject{Kind: store.UsageWorkflows, ID: wf}, WindowRequest{})
	if err != nil || len(got.Nodes) != 1 || got.Nodes[0].Name != "jira-cloud" || *got.Nodes[0].Kind != "tool" {
		t.Errorf("nodes = %+v, %v; want the sync node named jira-cloud", got.Nodes, err)
	}
}

func TestService_WorkflowNodesCompareWithTheSameSpanLastMonth(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	wf := f.workflow("Expense Audit", expenseAudit)
	under := func(when time.Time, c int64) call {
		return call{at: when, cents: c, rootKind: "workflow", rootID: wf, rootNode: "review"}
	}
	f.file(under(at(2026, 9, 15, 18, 0), 1_000), under(at(2026, 9, 15, 20, 0), 5_000), under(at(2026, 10, 3, 9, 0), 1_500))

	got, err := f.svc.Workflow(t.Context(), store.UsageSubject{Kind: store.UsageWorkflows, ID: wf}, WindowRequest{})
	if err != nil || got.Nodes[0].ChangePercent == nil || !near(*got.Nodes[0].ChangePercent, 50) {
		t.Errorf("review = %+v, %v; want up 50%% on the $10 before 19:20 on the 15th", got.Nodes, err)
	}
}

func TestService_AnAgentStartedWorkflowHasItsSpendButNoNodeSplit(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	orchestrator, wf := f.agent("Orchestrator"), f.workflow("Expense Audit", expenseAudit)
	delegated := func(when time.Time, c int64) call {
		return call{at: when, cents: c, rootKind: "agent", rootID: orchestrator, workflowID: wf}
	}
	f.file(delegated(at(2026, 9, 15, 18, 0), 1_000), delegated(at(2026, 10, 3, 9, 0), 2_000))

	list, err := f.svc.List(t.Context(), store.UsageWorkflows, ListRequest{Sort: "cost", Descending: true, PageNumber: 1, PageSize: 25})
	if err != nil || len(list.Rows) != 1 || list.Rows[0].Name != "Expense Audit" || list.Rows[0].Totals.Cost != cents(2_000) {
		t.Errorf("workflows = %+v, %v; want Expense Audit with this month's $20", list.Rows, err)
	}
	got, err := f.svc.Workflow(t.Context(), store.UsageSubject{Kind: store.UsageWorkflows, ID: wf}, WindowRequest{})
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if got.Totals.Cost != cents(2_000) || got.Totals.Activities != 1 || got.Comparison == nil {
		t.Errorf("totals = %+v, comparison %+v; want $20 over one run, compared with last month", got.Totals, got.Comparison)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want the definition's two", got.Nodes)
	}
	for _, node := range got.Nodes {
		if node.Cost != 0 || node.ShareOfWorkflow != 0 || node.ChangePercent != nil {
			t.Errorf("node %s = %+v, want $0, 0%% and no change", node.NodeID, node)
		}
	}
}

func TestService_AYAMLWorkflowShowsTheNodesItSpentUnder(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	f.file(call{at: at(2026, 10, 3, 9, 0), cents: 700, rootKind: "workflow", rootName: "nightly-report", rootNode: "summarize"})

	got, err := f.svc.Workflow(t.Context(), store.UsageSubject{Kind: store.UsageWorkflows, Name: "nightly-report"}, WindowRequest{})
	if err != nil || got.Info.Name != "nightly-report" || len(got.Nodes) != 1 || got.Nodes[0].Kind != nil || got.Nodes[0].Cost != cents(700) {
		t.Errorf("workflow = %+v, %v; want summarize's $7 with no kind", got, err)
	}
}

func TestService_AModelReportsItsSpend(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	model := idgen.New()
	f.exec(`INSERT INTO model_configurations (id, alias, provider, model_name, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'General', 'openai', 'gpt', 'test', 'test', 0, 0)`, model)
	f.file(call{at: at(2026, 10, 3, 9, 0), cents: 400, modelID: model}, call{at: at(2026, 10, 3, 9, 0), cents: 600})

	got, err := f.svc.Subject(t.Context(), store.UsageSubject{Kind: store.UsageModels, ID: model}, WindowRequest{})
	if err != nil || got.Info.Name != "General" || got.Totals.Cost != cents(400) || !near(*got.Totals.ShareOfSpend, 40) || len(got.Daily) != 15 {
		t.Errorf("model = %+v, %v; want General's $4, 40%% of spend", got, err)
	}
}

func TestService_AnUpperCaseIDIsTheSameUser(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	parminder := f.user("parminder")
	f.budget(parminder, 450)
	f.file(call{at: at(2026, 10, 2, 9, 0), cents: 900, userID: parminder})
	upper := store.UsageSubject{Kind: store.UsageUsers, ID: strings.ToUpper(parminder)}

	user, err := f.svc.User(t.Context(), upper, WindowRequest{}, ScopeTenant)
	if err != nil || user.Budget == nil || user.Totals.Cost != cents(900) {
		t.Errorf("user = %+v, %v; want their $9 and their budget", user, err)
	}
	months, err := f.svc.Months(t.Context(), upper)
	if err != nil || months[2].BudgetAmount == nil {
		t.Errorf("months = %+v, %v; want their budget", months, err)
	}
}

func TestService_UserNamesTheirTopRootsAndSumsTheRest(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	parminder := f.user("parminder")
	f.budget(parminder, 450)
	var calls []call
	for i := range 7 {
		calls = append(calls, call{at: at(2026, 10, 2, 9, 0), cents: int64(100 * (i + 1)), userID: parminder, rootKind: "agent", rootID: idgen.New()})
	}
	f.file(calls...)
	subject := store.UsageSubject{Kind: store.UsageUsers, ID: parminder}

	user, err := f.svc.User(t.Context(), subject, WindowRequest{}, ScopeTenant)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if len(user.TopRoots) != 5 || user.TopRoots[0].Cost != cents(700) || user.OtherCost != cents(300) || !near(user.OtherShareOfUser, 300.0/2_800*100) {
		t.Errorf("roots = %+v, other %d; want the top five from $7 and $3 other", user.TopRoots, user.OtherCost)
	}
	if user.Budget == nil || user.Budget.Standing.PeriodSpend != cents(2_800) || user.Totals.ShareOfSpend == nil {
		t.Errorf("user = %+v, want a budget standing at $28 and a share", user)
	}

	self, err := f.svc.User(t.Context(), subject, WindowRequest{}, ScopeSelf)
	if err != nil || self.Totals.ShareOfSpend != nil {
		t.Errorf("self = %+v, %v; want no share of the deployment's spend", self.Totals, err)
	}
}

func TestService_AnUnknownSubjectIsNotFound(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	gone := f.agent("gone")
	f.file(call{at: at(2026, 10, 2, 9, 0), cents: 100, agentID: gone})
	f.exec(`DELETE FROM agents WHERE id = ?`, gone)

	_, err := f.svc.Subject(t.Context(), store.UsageSubject{Kind: store.UsageAgents, ID: idgen.New()}, WindowRequest{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown agent: err = %v, want not found", err)
	}
	deleted, err := f.svc.Subject(t.Context(), store.UsageSubject{Kind: store.UsageAgents, ID: gone}, WindowRequest{})
	if err != nil || deleted.Totals.Cost != cents(100) {
		t.Errorf("deleted agent with spend = %+v, %v; want its $1", deleted, err)
	}
	idle := f.agent("idle")
	if got, err := f.svc.Subject(t.Context(), store.UsageSubject{Kind: store.UsageAgents, ID: idle}, WindowRequest{}); err != nil || got.Info.Name != "idle" {
		t.Errorf("agent with no spend = %+v, %v; want found at $0", got, err)
	}
}

func TestService_MonthsAreTheLastThreeAgainstTheCurrentBudget(t *testing.T) {
	f := newServiceFixture(t, at(2026, 12, 31, 23, 0))
	parminder := f.user("parminder")
	f.budget(parminder, 250)
	f.topUp(parminder, 50, at(2027, 1, 1, 0, 0))
	f.file(
		call{at: at(2026, 10, 5, 9, 0), cents: 1_000, userID: parminder},
		call{at: at(2026, 12, 1, 9, 0), cents: 2_000, userID: parminder},
	)

	months, err := f.svc.Months(t.Context(), store.UsageSubject{Kind: store.UsageUsers, ID: parminder})
	if err != nil || len(months) != 3 {
		t.Fatalf("months = %+v, %v; want three", months, err)
	}
	oct, nov, dec := months[0], months[1], months[2]
	if oct.Start != ms(at(2026, 10, 1, 0, 0)) || oct.Cost != cents(1_000) || oct.PartialFrom == nil || *oct.PartialFrom != ms(at(2026, 10, 3, 0, 0)) {
		t.Errorf("october = %+v, want $10, partial from October 3", oct)
	}
	if nov.Cost != 0 || nov.PartialFrom != nil || *nov.BudgetAmount != dollars(250) {
		t.Errorf("november = %+v, want nothing spent against the $250 base", nov)
	}
	if dec.Cost != cents(2_000) || *dec.BudgetAmount != dollars(300) {
		t.Errorf("december = %+v, want $20 against the $300 effective cap", dec)
	}
}

func (f *serviceFixture) servedBy(c call, name, provider string) call {
	f.t.Helper()
	c.servedName, c.servedProvider = name, provider
	return c
}

func TestService_ModelsSortByTheProviderModelThatServedThem(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	model := func(alias string) string {
		id := idgen.New()
		f.exec(`INSERT INTO model_configurations (id, alias, provider, model_name, created_by, updated_by, created_time, updated_time)
			VALUES (?, ?, 'p', 'm', 'test', 'test', 0, 0)`, id, alias)
		return id
	}
	general, planning, unknown := model("General"), model("Planning"), model("Unknown")
	when := at(2026, 10, 2, 9, 0)
	f.file(
		f.servedBy(call{at: when, cents: 300, modelID: general}, "claude-sonnet-4-6", "anthropic"),
		f.servedBy(call{at: when, cents: 100, modelID: general}, "claude-sonnet-4-5", "anthropic"),
		f.servedBy(call{at: when, cents: 200, modelID: planning}, "gpt-4o", "openai"),
		call{at: when, cents: 50, modelID: unknown},
	)
	names := func(desc bool) []string {
		list, err := f.svc.List(t.Context(), store.UsageModels, ListRequest{Sort: "model", Descending: desc, PageNumber: 1, PageSize: 25})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		out := make([]string, len(list.Rows))
		for i, r := range list.Rows {
			out[i] = r.Name
		}
		return out
	}

	if got := names(false); len(got) != 3 || got[0] != "General" || got[1] != "Planning" || got[2] != "Unknown" {
		t.Errorf("ascending = %v, want claude's General, gpt's Planning, then the unknown", got)
	}
	if got := names(true); len(got) != 3 || got[0] != "Planning" || got[1] != "General" || got[2] != "Unknown" {
		t.Errorf("descending = %v, want gpt's Planning, claude's General, then the unknown", got)
	}
	second, err := f.svc.List(t.Context(), store.UsageModels, ListRequest{Sort: "model", PageNumber: 2, PageSize: 1})
	if err != nil || len(second.Rows) != 1 || second.Rows[0].Name != "Planning" || second.Count != 3 {
		t.Errorf("page 2 of 1 = %+v, %v; want Planning of 3", second, err)
	}
	panel, err := f.svc.Subject(t.Context(), store.UsageSubject{Kind: store.UsageModels, ID: general}, WindowRequest{})
	if err != nil || len(panel.Models) != 2 || *panel.Models[0].Name != "claude-sonnet-4-6" || panel.Models[0].Totals.Cost != cents(300) {
		t.Errorf("panel = %+v, %v; want 4-6's $3 first of two", panel.Models, err)
	}
	if _, err := f.svc.List(t.Context(), store.UsageAgents, ListRequest{Sort: "model", PageNumber: 1, PageSize: 25}); err == nil {
		t.Error("agents sorted by model, want refused")
	}
}

func TestService_DailyFollowsTheInMemorySorts(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	user := f.user("parminder")
	f.budget(user, 100)
	model := idgen.New()
	f.exec(`INSERT INTO model_configurations (id, alias, provider, model_name, created_by, updated_by, created_time, updated_time)
		VALUES (?, 'General', 'p', 'm', 'test', 'test', 0, 0)`, model)
	f.file(f.servedBy(call{at: at(2026, 10, 2, 9, 0), cents: 500, userID: user, modelID: model}, "claude", "anthropic"))

	for kind, sort := range map[store.UsageKind]ListSort{store.UsageUsers: ListSort(SortByBudgetUsage), store.UsageModels: "model"} {
		list, err := f.svc.List(t.Context(), kind, ListRequest{Sort: sort, Descending: true, Daily: true, PageNumber: 1, PageSize: 25})
		if err != nil || len(list.Rows) == 0 {
			t.Fatalf("%s: %+v, %v", kind, list, err)
		}
		row := list.Rows[0]
		if len(row.Daily) != 15 || row.Daily[1].Cost != cents(500) {
			t.Errorf("%s by %s: daily = %+v, want October 1–15 with $5 on the 2nd", kind, sort, row.Daily)
		}
	}
	_, err := f.svc.List(t.Context(), store.UsageModels, ListRequest{Sort: "cost", Daily: true, PageNumber: 1, PageSize: 26})
	if !namesField(err, "pageSize") {
		t.Errorf("a page of 26 days = %v, want refused on pageSize", err)
	}
}

func TestService_SelfIsTheCallersUsersRowAlone(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	parminder := f.user("parminder")
	f.budget(parminder, 450)
	f.file(
		call{at: at(2026, 10, 2, 9, 0), cents: 900, userID: parminder},
		call{at: at(2026, 10, 3, 9, 0), cents: 100, userSub: "parminder", rootKind: "agent", rootID: idgen.New()},
		call{at: at(2026, 10, 3, 9, 0), cents: 5_000, userSub: "someone-else"},
	)

	self, err := f.svc.Self(t.Context(), Caller{PlatformUserID: strings.ToUpper(parminder), Subject: "parminder"}, WindowRequest{})
	if err != nil {
		t.Fatalf("self: %v", err)
	}
	admin, err := f.svc.User(t.Context(), store.UsageSubject{Kind: store.UsageUsers, ID: parminder}, WindowRequest{}, ScopeTenant)
	if err != nil {
		t.Fatalf("admin view: %v", err)
	}
	if self.Totals.Cost != cents(900) || admin.Totals.Cost != cents(900) || self.Totals.ShareOfSpend != nil || admin.Totals.ShareOfSpend == nil ||
		self.Totals.Activities != 1 || admin.Totals.Activities != 1 {
		t.Errorf("self = %+v, admin = %+v; want the row's $9 for both, the share for the admin alone", self.Totals, admin.Totals)
	}
	if len(self.TopRoots) != 0 || len(self.Daily) != 15 || self.Daily[2].Cost != 0 {
		t.Errorf("self = roots %+v, daily %+v; want nothing from the subject's spend", self.TopRoots, self.Daily)
	}
	if self.Budget == nil || self.Budget.Standing.PeriodSpend != cents(900) || len(self.Months) != 3 ||
		self.Months[2].Cost != cents(900) || *self.Months[2].BudgetAmount != dollars(450) {
		t.Errorf("self = %+v, months %+v; want their budget standing and October on their $9", self.Budget, self.Months)
	}
}

func TestService_SelfBySubjectOnlyWhileNoUsersRowHasIt(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	alice := f.user("alice")
	for _, issuer := range []string{"https://idp-a", "https://idp-b"} {
		f.exec(`INSERT INTO users (id, issuer, sub, display_name, created_by, updated_by, created_time, updated_time)
			VALUES (?, ?, 'bob', 'Bob', 'test', 'test', 0, 0)`, idgen.New(), issuer)
	}
	f.file(
		call{at: at(2026, 10, 2, 9, 0), cents: 300, userSub: "sam_dev_user"},
		call{at: at(2026, 10, 2, 9, 0), cents: 400, userSub: "alice"},
		call{at: at(2026, 10, 2, 9, 0), cents: 500, userID: alice},
		call{at: at(2026, 10, 2, 9, 0), cents: 600, userSub: "bob"},
	)
	for name, tc := range map[string]struct {
		subject    string
		want       int64
		hasDefault bool
	}{
		"a subject no users row has, as the desktop's": {"sam_dev_user", cents(300), true},
		"a subject one users row has":                  {"alice", 0, false},
		"a subject several users rows share":           {"bob", 0, false},
		"a subject with no spend":                      {"nobody", 0, true},
	} {
		t.Run(name, func(t *testing.T) {
			self, err := f.svc.Self(t.Context(), Caller{Subject: tc.subject}, WindowRequest{})
			if err != nil || self.Totals.Cost != tc.want || self.Totals.ShareOfSpend != nil {
				t.Errorf("self = %+v, %v; want %d and no share", self.Totals, err, tc.want)
			}
			onDefault := self.Budget != nil && self.Budget.Budget.Source == budget.SourceDefault && self.Budget.Standing.PeriodSpend == tc.want
			if onDefault != tc.hasDefault {
				t.Errorf("self budget = %+v, want the tier default standing on %d: %t", self.Budget, tc.want, tc.hasDefault)
			}
			if len(self.Months) != 3 || len(self.Daily) != 15 || self.TopRoots == nil {
				t.Errorf("self = months %d, daily %d, roots %v; want a full report", len(self.Months), len(self.Daily), self.TopRoots)
			}
		})
	}
}

func TestService_SelfWithAUsersRowIgnoresTheSubject(t *testing.T) {
	f := newServiceFixture(t, midOctober)
	alice := f.user("alice")
	f.file(
		call{at: at(2026, 10, 2, 9, 0), cents: 300, userID: alice},
		call{at: at(2026, 10, 2, 9, 0), cents: 7_000, userSub: "unclaimed"},
	)

	self, err := f.svc.Self(t.Context(), Caller{PlatformUserID: alice, Subject: "unclaimed"}, WindowRequest{})
	if err != nil || self.Totals.Cost != cents(300) || self.Subject.Subject.ID != alice {
		t.Errorf("self = %+v, %v; want Alice's $3 alone", self.Totals, err)
	}
}

func TestService_SelfWithNothingToShowIsZeros(t *testing.T) {
	f := newServiceFixture(t, at(2026, 12, 31, 23, 0))
	for name, caller := range map[string]Caller{
		"a deleted users row": {PlatformUserID: idgen.New()},
		"no identity at all":  {},
	} {
		t.Run(name, func(t *testing.T) {
			self, err := f.svc.Self(t.Context(), caller, WindowRequest{})
			if err != nil || self.Totals.Cost != 0 || self.Budget != nil || len(self.Months) != 3 || self.Months[0].PartialFrom == nil {
				t.Errorf("self = %+v, %v; want zeros with October partial", self, err)
			}
		})
	}
}

func namesField(err error, field string) bool {
	for _, p := range Problems(err) {
		if p.Field == field {
			return true
		}
	}
	return false
}
