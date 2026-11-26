// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package usage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/governor"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/workflow"
)

var ErrNotFound = errors.New("usage subject not found")

type Scope string

const (
	ScopeTenant Scope = "tenant"
	ScopeSelf   Scope = "self"
)

const MaxPageSize = 100

const MaxDailyPageSize = 25

const topRootCount = 5

type Service struct {
	Usage     *store.SpendUsageRepo
	Budgets   Budgets
	Workflows *store.WorkflowRepo
	Logger    *slog.Logger
	Now       func() time.Time

	unparsedDefinitions sync.Map
}

type Freshness struct {
	DataAsOf        *int64
	HistoryComplete bool
}

type Compared struct {
	Window        Window
	Cost          int64
	ChangePercent *float64
}

type BudgetStanding struct {
	Budget   Budget
	Standing Standing
}

type Summary struct {
	Freshness
	Window     Window
	Totals     store.UsageTotals
	KindCosts  store.UsageKindCosts
	Compared   *Compared
	MeshBudget *BudgetStanding
	Daily      []store.UsageDay
}

type Savings struct {
	Freshness
	Window Window
	store.UsageSavings
}

func (s *Service) Savings(ctx context.Context, req WindowRequest) (Savings, error) {
	return s.savings(ctx, store.UsageSubject{}, true, req)
}

func (s *Service) SelfSavings(ctx context.Context, caller Caller, req WindowRequest) (Savings, error) {
	subject, ok, err := s.callerSubject(ctx, caller)
	if err != nil {
		return Savings{}, err
	}
	return s.savings(ctx, subject, ok, req)
}

func (s *Service) savings(ctx context.Context, subject store.UsageSubject, read bool, req WindowRequest) (Savings, error) {
	w, err := ResolveWindow(req, s.Now())
	if err != nil {
		return Savings{}, err
	}
	fresh, err := s.freshness(ctx)
	if err != nil {
		return Savings{}, err
	}
	out := Savings{Freshness: fresh, Window: w}
	if !read {
		return out, nil
	}
	from, to := w.rollupDays()
	if out.UsageSavings, err = s.Usage.Savings(ctx, subject, from, to); err != nil {
		return Savings{}, err
	}
	return out, nil
}

type SubjectTotals struct {
	store.UsageTotals
	CostPerActivity *int64
	ShareOfSpend    *float64
}

type Row struct {
	store.UsageRow
	CostPerActivity *int64
	ShareOfSpend    *float64
	Budget          *BudgetStanding
	BudgetUsage     *WindowUsage
	Daily           []store.UsageDay
	Models          []store.UsageServedModel
}

type List struct {
	Window Window
	Rows   []Row
	Count  int
}

type ListSort string

var listSorts = map[ListSort]store.UsageSort{
	"cost":                      store.UsageSortCost,
	"shareOfSpend":              store.UsageSortCost,
	"activities":                store.UsageSortActivities,
	"costPerActivity":           store.UsageSortCostPerActivity,
	"name":                      store.UsageSortName,
	"uncachedInputCost":         store.UsageSortUncachedInputCost,
	"outputCost":                store.UsageSortOutputCost,
	"cacheReadCost":             store.UsageSortCacheReadCost,
	"cacheWriteCost":            store.UsageSortCacheWriteCost,
	ListSort(SortByBudget):      store.UsageSortCost,
	ListSort(SortByBudgetUsage): store.UsageSortCost,
	sortByServedModel:           store.UsageSortServedModel,
}

const sortByServedModel ListSort = "model"

type ListRequest struct {
	Window               WindowRequest
	Name                 string
	BudgetID             string
	AllSubjects          bool
	Daily                bool
	Sort                 ListSort
	Descending           bool
	PageNumber, PageSize int
}

type Subject struct {
	Freshness
	Window  Window
	Subject store.UsageSubject
	Info    store.UsageSubjectInfo
	Totals  SubjectTotals
	Daily   []store.UsageDay
	Models  []store.UsageServedModel
}

type Node struct {
	NodeID          string
	Name            string
	Kind            *string
	Cost            int64
	ShareOfWorkflow float64
	ChangePercent   *float64
}

type Workflow struct {
	Subject
	Comparison *Window
	Nodes      []Node
}

type Root struct {
	Kind        string
	ID          *string
	BookedName  *string
	Name        string
	Cost        int64
	ShareOfUser float64
}

type User struct {
	Subject
	Budget           *BudgetStanding
	TopRoots         []Root
	OtherCost        int64
	OtherShareOfUser float64
}

type Month struct {
	Start        int64
	Cost         int64
	BudgetAmount *int64
	PartialFrom  *int64
}

func (s *Service) Summary(ctx context.Context, req WindowRequest) (Summary, error) {
	now := s.Now()
	w, err := ResolveWindow(req, now)
	if err != nil {
		return Summary{}, err
	}
	fresh, err := s.freshness(ctx)
	if err != nil {
		return Summary{}, err
	}
	tenant := store.UsageSubject{}
	from, to := w.rollupDays()
	totals, kindCosts, err := s.Usage.TenantTotals(ctx, from, to)
	if err != nil {
		return Summary{}, err
	}
	daily, err := s.daily(ctx, tenant, w)
	if err != nil {
		return Summary{}, err
	}
	var compared *Compared
	if fresh.HistoryComplete {
		if compared, err = s.compare(ctx, tenant, w, totals.Cost, now); err != nil {
			return Summary{}, err
		}
	}
	sum := Summary{Freshness: fresh, Window: w, Totals: totals, KindCosts: kindCosts, Compared: compared, Daily: daily}
	mesh, err := s.Budgets.Mesh(ctx, now)
	if err != nil {
		return Summary{}, err
	}
	if mesh == nil {
		return sum, nil
	}
	spent, err := s.periodSpend(ctx, tenant, w, totals.Cost, now)
	if err != nil {
		return Summary{}, err
	}
	sum.MeshBudget = &BudgetStanding{Budget: *mesh, Standing: mesh.Standing(spent, now)}
	return sum, nil
}

func (s *Service) CheckList(kind store.UsageKind, req ListRequest) ValidationErrors {
	return checkList(kind, req, s.Now())
}

func checkList(kind store.UsageKind, req ListRequest, now time.Time) ValidationErrors {
	var problems ValidationErrors
	if _, err := ResolveWindow(req.Window, now); err != nil {
		problems = append(problems, Problems(err)...)
	}
	if _, ok := listSorts[req.Sort]; !ok || (kind != store.UsageUsers && isBudgetSort(req.Sort)) || (kind != store.UsageModels && req.Sort == sortByServedModel) {
		problems = append(problems, &ValidationError{"sort", "sort must be one of " + strings.Join(sortFields(kind), ", ")})
	}
	switch {
	case req.BudgetID == "":
	case kind != store.UsageUsers:
		problems = append(problems, &ValidationError{"budgetId", "budgetId filters users only"})
	case !store.IsCanonicalUUID(req.BudgetID):
		problems = append(problems, &ValidationError{"budgetId", "budgetId must be a UUID"})
	}
	if req.PageNumber < 1 {
		problems = append(problems, &ValidationError{"pageNumber", "pageNumber must be at least 1"})
	}
	switch {
	case req.PageSize < 1 || req.PageSize > MaxPageSize:
		problems = append(problems, &ValidationError{"pageSize", fmt.Sprintf("pageSize must be from 1 to %d", MaxPageSize)})
	case req.Daily && req.PageSize > MaxDailyPageSize:
		problems = append(problems, &ValidationError{"pageSize", fmt.Sprintf("with expand=daily, pageSize must be at most %d", MaxDailyPageSize)})
	}
	return problems
}

func (s *Service) List(ctx context.Context, kind store.UsageKind, req ListRequest) (List, error) {
	now := s.Now()
	if err := checkList(kind, req, now).Err(); err != nil {
		return List{}, err
	}
	w, err := ResolveWindow(req.Window, now)
	if err != nil {
		return List{}, err
	}
	sort := listSorts[req.Sort]
	from, to := w.rollupDays()
	q := store.UsageListQuery{
		FromDay: from, ToDay: to, Name: req.Name, AllSubjects: req.AllSubjects,
		Sort: sort, Descending: req.Descending,
		Offset: (req.PageNumber - 1) * req.PageSize, Limit: req.PageSize,
	}
	if req.BudgetID != "" {
		if q.Budget, err = s.Budgets.Budgets.UserMatch(ctx, req.BudgetID); err != nil {
			return List{}, err
		}
		if q.Budget == nil {
			return List{Window: w, Rows: []Row{}}, nil
		}
	}
	var page store.UsageList
	var budgets *UserBudgets
	var served map[string][]store.UsageServedModel
	switch {
	case isBudgetSort(req.Sort):
		page, budgets, err = s.budgetSortedPage(ctx, q, BudgetSort(req.Sort), w, now)
	default:
		page, err = s.Usage.List(ctx, kind, q)
	}
	if err != nil {
		return List{}, err
	}
	if kind == store.UsageModels {
		if served, err = s.Usage.ServedModels(ctx, page.Rows, from, to); err != nil {
			return List{}, err
		}
	}
	tenantCost, err := s.Usage.Cost(ctx, store.UsageSubject{}, from, to)
	if err != nil {
		return List{}, err
	}
	rows := make([]Row, len(page.Rows))
	for i, r := range page.Rows {
		rows[i] = Row{UsageRow: r, CostPerActivity: costPerActivity(r.Totals), ShareOfSpend: new(share(r.Totals.Cost, tenantCost))}
		if kind == store.UsageModels {
			rows[i].Models = nonNil(served[store.UsageRowKey(r)])
		}
	}
	if kind == store.UsageUsers {
		if err := s.decorateUsers(ctx, rows, budgets, w, now); err != nil {
			return List{}, err
		}
	}
	if req.Daily {
		byRow, err := s.Usage.DailyByRow(ctx, kind, page.Rows, from, to)
		if err != nil {
			return List{}, err
		}
		for i := range rows {
			rows[i].Daily = fillDays(byRow[store.UsageRowKey(rows[i].UsageRow)], from, to)
		}
	}
	return List{Window: w, Rows: rows, Count: page.Count}, nil
}

func sortFields(kind store.UsageKind) []string {
	fields := make([]string, 0, len(listSorts))
	for f := range listSorts {
		switch {
		case isBudgetSort(f) && kind != store.UsageUsers, f == sortByServedModel && kind != store.UsageModels:
			continue
		}
		fields = append(fields, string(f))
	}
	slices.Sort(fields)
	return fields
}

func nonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

func isBudgetSort(sort ListSort) bool {
	return sort == ListSort(SortByBudget) || sort == ListSort(SortByBudgetUsage)
}

func (s *Service) budgetSortedPage(ctx context.Context, q store.UsageListQuery, by BudgetSort, w Window, now time.Time) (store.UsageList, *UserBudgets, error) {
	all := q
	all.Unpaged, all.Offset, all.Limit = true, 0, 0
	every, err := s.Usage.List(ctx, store.UsageUsers, all)
	if err != nil {
		return store.UsageList{}, nil, err
	}
	budgets, err := s.Budgets.ForUsers(ctx, nil, now)
	if err != nil {
		return store.UsageList{}, nil, err
	}
	usage := make(map[string]WindowUsage, len(every.Rows))
	for _, r := range every.Rows {
		if b, ok := budgets.For(r); ok {
			usage[store.UsageRowKey(r)] = b.WindowUsage(r.Totals.Cost, w.Start, w.DayEnd, now)
		}
	}
	sorted := SortUsers(every.Rows, budgets, usage, by, q.Descending)
	start := min(q.Offset, len(sorted))
	return store.UsageList{Rows: sorted[start:min(start+q.Limit, len(sorted))], Count: len(sorted)}, &budgets, nil
}

func (s *Service) decorateUsers(ctx context.Context, rows []Row, budgets *UserBudgets, w Window, now time.Time) error {
	if len(rows) == 0 {
		return nil
	}
	if budgets == nil {
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			if r.SubjectID != nil {
				ids = append(ids, *r.SubjectID)
			}
		}
		read, err := s.Budgets.ForUsers(ctx, ids, now)
		if err != nil {
			return err
		}
		budgets = &read
	}
	spent, err := s.usersPeriodSpend(ctx, rows, w, now)
	if err != nil {
		return err
	}
	for i := range rows {
		r := &rows[i]
		b, ok := budgets.For(r.UsageRow)
		if !ok {
			continue
		}
		r.Budget = &BudgetStanding{Budget: b, Standing: b.Standing(spent[store.UsageRowKey(r.UsageRow)], now)}
		if u := b.WindowUsage(r.Totals.Cost, w.Start, w.DayEnd, now); u.WindowBudget != nil {
			r.BudgetUsage = &u
		}
	}
	return nil
}

func (s *Service) usersPeriodSpend(ctx context.Context, rows []Row, w Window, now time.Time) (map[string]int64, error) {
	spent := make(map[string]int64, len(rows))
	if isPeriod(w, now) {
		for _, r := range rows {
			spent[store.UsageRowKey(r.UsageRow)] = r.Totals.Cost
		}
		return spent, nil
	}
	var ids, subs []string
	for _, r := range rows {
		switch {
		case r.SubjectID != nil:
			ids = append(ids, *r.SubjectID)
		case r.SubjectName != nil:
			subs = append(subs, *r.SubjectName)
		}
	}
	from, to := budget.PeriodStart(now).UnixMilli(), store.SpendRollupDayEnd(now.UnixMilli())
	byID, err := s.Usage.UserCosts(ctx, ids, from, to)
	if err != nil {
		return nil, err
	}
	bySub, err := s.Usage.SubCosts(ctx, subs, from, to)
	if err != nil {
		return nil, err
	}
	for id, cost := range byID {
		spent[store.UsageRowKey(store.UsageRow{SubjectID: &id})] = cost
	}
	for sub, cost := range bySub {
		spent[store.UsageRowKey(store.UsageRow{SubjectName: &sub})] = cost
	}
	return spent, nil
}

func isPeriod(w Window, now time.Time) bool {
	return w.Start == budget.PeriodStart(now).UnixMilli() && w.End == now.UnixMilli()
}

func (s *Service) periodSpend(ctx context.Context, subject store.UsageSubject, w Window, cost int64, now time.Time) (int64, error) {
	if isPeriod(w, now) {
		return cost, nil
	}
	return s.Usage.Cost(ctx, subject, budget.PeriodStart(now).UnixMilli(), store.SpendRollupDayEnd(now.UnixMilli()))
}

func (s *Service) Subject(ctx context.Context, subject store.UsageSubject, req WindowRequest) (Subject, error) {
	subject, now := canonical(subject), s.Now()
	w, err := ResolveWindow(req, now)
	if err != nil {
		return Subject{}, err
	}
	return s.subject(ctx, subject, w, ScopeTenant, now)
}

func (s *Service) subject(ctx context.Context, subject store.UsageSubject, w Window, scope Scope, now time.Time) (Subject, error) {
	info, found, err := s.Usage.SubjectInfo(ctx, subject, governor.RetentionFloor(now), store.SpendRollupDayEnd(now.UnixMilli()))
	if err != nil {
		return Subject{}, err
	}
	if !found {
		return Subject{}, ErrNotFound
	}
	fresh, err := s.freshness(ctx)
	if err != nil {
		return Subject{}, err
	}
	from, to := w.rollupDays()
	totals, err := s.Usage.Totals(ctx, subject, from, to)
	if err != nil {
		return Subject{}, err
	}
	daily, err := s.daily(ctx, subject, w)
	if err != nil {
		return Subject{}, err
	}
	t := SubjectTotals{UsageTotals: totals, CostPerActivity: costPerActivity(totals)}
	if scope != ScopeSelf {
		tenantCost, err := s.Usage.Cost(ctx, store.UsageSubject{}, from, to)
		if err != nil {
			return Subject{}, err
		}
		t.ShareOfSpend = new(share(totals.Cost, tenantCost))
	}
	got := Subject{Freshness: fresh, Window: w, Subject: subject, Info: info, Totals: t, Daily: daily}
	if subject.Kind == store.UsageModels {
		row := store.UsageRow{}
		if subject.ID != "" {
			row.SubjectID = new(subject.ID)
		} else {
			row.SubjectName = new(subject.Name)
		}
		served, err := s.Usage.ServedModels(ctx, []store.UsageRow{row}, from, to)
		if err != nil {
			return Subject{}, err
		}
		got.Models = nonNil(served[store.UsageRowKey(row)])
	}
	return got, nil
}

func (s *Service) Workflow(ctx context.Context, subject store.UsageSubject, req WindowRequest) (Workflow, error) {
	subject, now := canonical(subject), s.Now()
	w, err := ResolveWindow(req, now)
	if err != nil {
		return Workflow{}, err
	}
	base, err := s.subject(ctx, subject, w, ScopeTenant, now)
	if err != nil {
		return Workflow{}, err
	}
	from, to := w.rollupDays()
	costs, err := s.Usage.WorkflowNodeCosts(ctx, subject, from, to)
	if err != nil {
		return Workflow{}, err
	}
	var comparison *Window
	if base.HistoryComplete {
		comparison = Comparison(w, now)
	}
	var before map[string]int64
	if comparison != nil {
		if before, err = s.nodeCostsOver(ctx, subject, *comparison); err != nil {
			return Workflow{}, err
		}
	}
	definition, err := s.workflowNodes(ctx, subject)
	if err != nil {
		return Workflow{}, err
	}
	return Workflow{Subject: base, Comparison: comparison, Nodes: nodesWithSpend(definition, costs, before, base.Totals.Cost)}, nil
}

func (s *Service) nodeCostsOver(ctx context.Context, subject store.UsageSubject, w Window) (map[string]int64, error) {
	lastDay := startOfDay(w.End)
	costs, err := s.Usage.WorkflowNodeCosts(ctx, subject, w.Start, lastDay)
	if err != nil {
		return nil, err
	}
	byNode := make(map[string]int64, len(costs))
	for _, c := range costs {
		byNode[c.NodeID] += c.Cost
	}
	if lastDay < w.End {
		partial, err := s.Usage.LedgerWorkflowNodeCosts(ctx, subject, lastDay, w.End)
		if err != nil {
			return nil, err
		}
		for _, c := range partial {
			byNode[c.NodeID] += c.Cost
		}
	}
	return byNode, nil
}

type definedNode struct {
	id, name, kind string
}

func (s *Service) workflowNodes(ctx context.Context, subject store.UsageSubject) ([]definedNode, error) {
	if subject.ID == "" {
		return nil, nil
	}
	row, err := s.Workflows.GetByID(ctx, subject.ID)
	if err != nil || row == nil {
		return nil, err
	}
	var appConfig map[string]any
	if err = json.Unmarshal([]byte(row.AppConfig), &appConfig); err != nil {
		s.warnUnparsed(row.ID, "usage: stored workflow definition is not JSON; showing nodes from spend alone; open the workflow's definition to see why")
		return nil, nil
	}
	body, nested := appConfig["workflow"].(map[string]any)
	if !nested || body == nil {
		body = appConfig
	}
	cfg, err := workflow.ParseWorkflowConfig(row.Name, "", workflow.WithConnectorNodePlaceholders(body))
	if err != nil {
		s.warnUnparsed(row.ID, "usage: stored workflow definition did not parse; showing nodes from spend alone; open the workflow's definition to see why")
		return nil, nil
	}
	nodes := make([]definedNode, 0, len(cfg.Nodes))
	for _, n := range cfg.Nodes {
		nodes = append(nodes, definedNode{id: n.ID, name: nodeName(n), kind: n.Type})
	}
	return nodes, nil
}

func (s *Service) warnUnparsed(workflowID, msg string) {
	if _, logged := s.unparsedDefinitions.LoadOrStore(workflowID, true); !logged {
		s.Logger.Warn(msg, "workflowID", workflowID)
	}
}

func nodeName(n *workflow.NodeConfig) string {
	for _, name := range []string{n.AgentName, n.WorkflowName, n.Connector, n.ToolName} {
		if name != "" {
			return name
		}
	}
	return n.ID
}

func nodesWithSpend(definition []definedNode, costs []store.UsageNodeCost, before map[string]int64, total int64) []Node {
	byNode := make(map[string]int64, len(costs))
	for _, c := range costs {
		byNode[c.NodeID] = c.Cost
	}
	node := func(id, name string, kind *string) Node {
		cost := byNode[id]
		n := Node{NodeID: id, Name: name, Kind: kind, Cost: cost, ShareOfWorkflow: share(cost, total)}
		if prev := before[id]; prev > 0 {
			n.ChangePercent = new(change(cost, prev))
		}
		return n
	}
	nodes := make([]Node, 0, len(definition)+len(costs))
	defined := make(map[string]bool, len(definition))
	for _, d := range definition {
		defined[d.id] = true
		nodes = append(nodes, node(d.id, d.name, new(d.kind)))
	}
	for _, c := range costs {
		if !defined[c.NodeID] {
			nodes = append(nodes, node(c.NodeID, c.NodeID, nil))
		}
	}
	return nodes
}

func (s *Service) User(ctx context.Context, subject store.UsageSubject, req WindowRequest, scope Scope) (User, error) {
	now := s.Now()
	w, err := ResolveWindow(req, now)
	if err != nil {
		return User{}, err
	}
	return s.user(ctx, canonical(subject), w, scope, now)
}

func (s *Service) user(ctx context.Context, subject store.UsageSubject, w Window, scope Scope, now time.Time) (User, error) {
	base, err := s.subject(ctx, subject, w, scope, now)
	if err != nil {
		return User{}, err
	}
	from, to := w.rollupDays()
	roots, err := s.Usage.UserRootCosts(ctx, subject, from, to, topRootCount)
	if err != nil {
		return User{}, err
	}
	user := User{Subject: base, TopRoots: make([]Root, len(roots))}
	other := base.Totals.Cost
	for i, r := range roots {
		user.TopRoots[i] = Root{Kind: r.Kind, ID: r.RootID, Name: r.Name, Cost: r.Cost, ShareOfUser: share(r.Cost, base.Totals.Cost)}
		if r.RootID == nil {
			user.TopRoots[i].BookedName = r.Root
		}
		other -= r.Cost
	}
	other = max(other, 0)
	user.OtherCost, user.OtherShareOfUser = other, share(other, base.Totals.Cost)
	b, err := s.userBudget(ctx, subject, now)
	if err != nil {
		return User{}, err
	}
	if b == nil {
		return user, nil
	}
	spent, err := s.periodSpend(ctx, subject, w, base.Totals.Cost, now)
	if err != nil {
		return User{}, err
	}
	user.Budget = &BudgetStanding{Budget: *b, Standing: b.Standing(spent, now)}
	return user, nil
}

func (s *Service) userBudget(ctx context.Context, subject store.UsageSubject, now time.Time) (*Budget, error) {
	if subject.ID == "" {
		return s.Budgets.Default(ctx)
	}
	budgets, err := s.Budgets.Users(ctx, []string{subject.ID}, now)
	if err != nil {
		return nil, err
	}
	b, ok := budgets[subject.ID]
	if !ok {
		return nil, nil
	}
	return &b, nil
}

type Caller struct {
	PlatformUserID string
	Subject        string
}

type Self struct {
	User
	Months []Month
}

func (s *Service) Self(ctx context.Context, caller Caller, req WindowRequest) (Self, error) {
	now := s.Now()
	w, err := ResolveWindow(req, now)
	if err != nil {
		return Self{}, err
	}
	subject, ok, err := s.callerSubject(ctx, caller)
	if err != nil {
		return Self{}, err
	}
	if !ok {
		return s.emptySelf(ctx, w, now)
	}
	user, err := s.user(ctx, subject, w, ScopeSelf, now)
	if errors.Is(err, ErrNotFound) {
		return s.unspentSelf(ctx, subject, w, now)
	}
	if err != nil {
		return Self{}, err
	}
	months, err := s.months(ctx, subject, now)
	if err != nil {
		return Self{}, err
	}
	return Self{User: user, Months: months}, nil
}

func (s *Service) callerSubject(ctx context.Context, caller Caller) (store.UsageSubject, bool, error) {
	if caller.PlatformUserID != "" {
		return canonical(store.UsageSubject{Kind: store.UsageUsers, ID: caller.PlatformUserID}), true, nil
	}
	if caller.Subject == "" {
		return store.UsageSubject{}, false, nil
	}
	owner, err := s.Usage.UserSubjectOwnership(ctx, caller.Subject)
	if err != nil {
		return store.UsageSubject{}, false, err
	}
	if owner.Shared || owner.UserID != "" {
		attrs := []any{"reason", "owned", "ownerUserID", owner.UserID}
		if owner.Shared {
			attrs = []any{"reason", "shared"}
		}
		s.Logger.DebugContext(ctx, "usage: self report shows no spend booked under the caller's subject", attrs...)
		return store.UsageSubject{}, false, nil
	}
	return store.UsageSubject{Kind: store.UsageUsers, Name: caller.Subject}, true, nil
}

func (s *Service) unspentSelf(ctx context.Context, subject store.UsageSubject, w Window, now time.Time) (Self, error) {
	self, err := s.emptySelf(ctx, w, now)
	if err != nil {
		return Self{}, err
	}
	b, err := s.userBudget(ctx, subject, now)
	if err != nil || b == nil {
		return self, err
	}
	self.Budget = &BudgetStanding{Budget: *b, Standing: b.Standing(0, now)}
	if self.Months, err = s.months(ctx, subject, now); err != nil {
		return Self{}, err
	}
	return self, nil
}

func (s *Service) emptySelf(ctx context.Context, w Window, now time.Time) (Self, error) {
	fresh, err := s.freshness(ctx)
	if err != nil {
		return Self{}, err
	}
	from, to := w.rollupDays()
	user := User{
		Subject:  Subject{Freshness: fresh, Window: w, Daily: fillDays(nil, from, to)},
		TopRoots: []Root{},
	}
	floor, current := governor.RetentionFloor(now), budget.PeriodStart(now)
	months := make([]Month, 0, monthsShown)
	for back := monthsShown - 1; back >= 0; back-- {
		m := Month{Start: current.AddDate(0, -back, 0).UnixMilli()}
		if m.Start < floor {
			m.PartialFrom = new(floor)
		}
		months = append(months, m)
	}
	return Self{User: user, Months: months}, nil
}

const monthsShown = 3

func (s *Service) Months(ctx context.Context, subject store.UsageSubject) ([]Month, error) {
	return s.months(ctx, canonical(subject), s.Now())
}

func (s *Service) months(ctx context.Context, subject store.UsageSubject, now time.Time) ([]Month, error) {
	b, err := s.userBudget(ctx, subject, now)
	if err != nil {
		return nil, err
	}
	floor, today := governor.RetentionFloor(now), store.SpendRollupDayEnd(now.UnixMilli())
	current := budget.PeriodStart(now)
	months := make([]Month, 0, monthsShown)
	for back := monthsShown - 1; back >= 0; back-- {
		start := current.AddDate(0, -back, 0)
		m := Month{Start: start.UnixMilli()}
		from, to := max(m.Start, floor), min(start.AddDate(0, 1, 0).UnixMilli(), today)
		if m.Start < floor {
			m.PartialFrom = new(floor)
		}
		if m.Cost, err = s.Usage.Cost(ctx, subject, from, to); err != nil {
			return nil, err
		}
		if b != nil {
			m.BudgetAmount = b.Amount
			if back == 0 {
				m.BudgetAmount = b.EffectiveAmount
			}
		}
		months = append(months, m)
	}
	return months, nil
}

func (s *Service) compare(ctx context.Context, subject store.UsageSubject, w Window, cost int64, now time.Time) (*Compared, error) {
	c := Comparison(w, now)
	if c == nil {
		return nil, nil
	}
	lastDay := startOfDay(c.End)
	before, err := s.Usage.Cost(ctx, subject, c.Start, lastDay)
	if err != nil {
		return nil, err
	}
	if lastDay < c.End {
		partial, err := s.Usage.LedgerCost(ctx, subject, lastDay, c.End)
		if err != nil {
			return nil, err
		}
		before += partial
	}
	compared := &Compared{Window: *c, Cost: before}
	if before > 0 {
		compared.ChangePercent = new(change(cost, before))
	}
	return compared, nil
}

func (s *Service) daily(ctx context.Context, subject store.UsageSubject, w Window) ([]store.UsageDay, error) {
	from, to := w.rollupDays()
	days, err := s.Usage.Daily(ctx, subject, from, to)
	if err != nil {
		return nil, err
	}
	return fillDays(days, from, to), nil
}

func fillDays(days []store.UsageDay, from, to int64) []store.UsageDay {
	byDay := make(map[int64]store.UsageDay, len(days))
	for _, d := range days {
		byDay[d.Day] = d
	}
	filled := make([]store.UsageDay, 0, (to-from)/dayMillis)
	for day := from; day < to; day += dayMillis {
		d := byDay[day]
		d.Day = day
		filled = append(filled, d)
	}
	return filled
}

func canonical(subject store.UsageSubject) store.UsageSubject {
	subject.ID = strings.ToLower(subject.ID)
	return subject
}

func (s *Service) freshness(ctx context.Context) (Freshness, error) {
	state, err := s.Usage.RollupState(ctx)
	if err != nil {
		return Freshness{}, err
	}
	return Freshness{DataAsOf: state.RefreshedTime, HistoryComplete: state.ReconciledTime != nil}, nil
}

func costPerActivity(t store.UsageTotals) *int64 {
	if t.Activities == 0 {
		return nil
	}
	return new(t.Cost / t.Activities)
}

func share(part, whole int64) float64 {
	if whole == 0 {
		return 0
	}
	return min(float64(part)/float64(whole)*100, 100)
}

func change(now, before int64) float64 {
	return float64(now-before) / float64(before) * 100
}
