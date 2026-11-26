// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/usage"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const (
	usageExpandDaily  = "daily"
	usageMaxNameBytes = spend.MaxFieldBytes
)

var usagePopulations = map[string]bool{"all": true, "spenders": false}

type UsageRow struct {
	ID              *string           `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e60"`
	Type            string            `json:"type" example:"agentUsage"`
	Name            *string           `json:"name" example:"Support triage"`
	BookedName      *string           `json:"bookedName" example:"support-triage"`
	Cost            string            `json:"cost" example:"44.2500000000"`
	Activities      int64             `json:"activities" example:"42"`
	CostPerActivity *string           `json:"costPerActivity" example:"1.0535714285"`
	ShareOfSpend    float64           `json:"shareOfSpend" example:"12.5"`
	CostByBucket    UsageCostByBucket `json:"costByBucket"`
	Tokens          UsageTokens       `json:"tokens"`
	Calls           int64             `json:"calls" example:"310"`
	UnpricedCalls   int64             `json:"unpricedCalls" example:"2"`
	Daily           []UsageDay        `json:"daily" nullable:"true"`
}

type ModelUsageRow struct {
	ID              *string            `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e70"`
	Type            string             `json:"type" example:"modelUsage"`
	Name            *string            `json:"name" example:"General purpose"`
	BookedName      *string            `json:"bookedName" example:"gpt-4o"`
	Cost            string             `json:"cost" example:"44.2500000000"`
	Activities      int64              `json:"activities" example:"42"`
	CostPerActivity *string            `json:"costPerActivity" example:"1.0535714285"`
	ShareOfSpend    float64            `json:"shareOfSpend" example:"12.5"`
	CostByBucket    UsageCostByBucket  `json:"costByBucket"`
	Tokens          UsageTokens        `json:"tokens"`
	Calls           int64              `json:"calls" example:"310"`
	UnpricedCalls   int64              `json:"unpricedCalls" example:"2"`
	Daily           []UsageDay         `json:"daily" nullable:"true"`
	Models          []UsageServedModel `json:"models"`
}

type UserUsageRow struct {
	ID              *string            `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type            string             `json:"type" example:"userUsage"`
	Name            *string            `json:"name" example:"Jane Doe"`
	Cost            string             `json:"cost" example:"44.2500000000"`
	Activities      int64              `json:"activities" example:"42"`
	CostPerActivity *string            `json:"costPerActivity" example:"1.0535714285"`
	ShareOfSpend    float64            `json:"shareOfSpend" example:"12.5"`
	CostByBucket    UsageCostByBucket  `json:"costByBucket"`
	Tokens          UsageTokens        `json:"tokens"`
	Calls           int64              `json:"calls" example:"310"`
	UnpricedCalls   int64              `json:"unpricedCalls" example:"2"`
	Daily           []UsageDay         `json:"daily" nullable:"true"`
	User            *UsageUser         `json:"user"`
	Budget          *UsageBudget       `json:"budget"`
	BudgetUsage     *UsageBudgetWindow `json:"budgetUsage"`
}

type listAgentUsageRequest struct {
	StrictRequest
	Page       Page      `json:"-"`
	Sort       SortOrder `sort:"cost,shareOfSpend,activities,costPerActivity,name,uncachedInputCost,outputCost,cacheReadCost,cacheWriteCost" json:"-"`
	Period     string    `query:"period,nonempty" json:"-" example:"last_30_days"`
	StartDate  string    `query:"startDate,nonempty" json:"-" example:"2026-10-01"`
	EndDate    string    `query:"endDate,nonempty" json:"-" example:"2026-10-15"`
	Search     string    `query:"search" json:"-" example:"support"`
	Population string    `query:"population,nonempty" json:"-" example:"spenders"`
	Expand     string    `query:"expand" json:"-" example:"daily"`
}

type listWorkflowUsageRequest struct {
	StrictRequest
	Page       Page      `json:"-"`
	Sort       SortOrder `sort:"cost,shareOfSpend,activities,costPerActivity,name,uncachedInputCost,outputCost,cacheReadCost,cacheWriteCost" json:"-"`
	Period     string    `query:"period,nonempty" json:"-" example:"last_30_days"`
	StartDate  string    `query:"startDate,nonempty" json:"-" example:"2026-10-01"`
	EndDate    string    `query:"endDate,nonempty" json:"-" example:"2026-10-15"`
	Search     string    `query:"search" json:"-" example:"support"`
	Population string    `query:"population,nonempty" json:"-" example:"spenders"`
	Expand     string    `query:"expand" json:"-" example:"daily"`
}

type listModelUsageRequest struct {
	StrictRequest
	Page       Page      `json:"-"`
	Sort       SortOrder `sort:"cost,shareOfSpend,activities,costPerActivity,name,uncachedInputCost,outputCost,cacheReadCost,cacheWriteCost,model" json:"-"`
	Period     string    `query:"period,nonempty" json:"-" example:"last_30_days"`
	StartDate  string    `query:"startDate,nonempty" json:"-" example:"2026-10-01"`
	EndDate    string    `query:"endDate,nonempty" json:"-" example:"2026-10-15"`
	Search     string    `query:"search" json:"-" example:"sonnet"`
	Population string    `query:"population,nonempty" json:"-" example:"spenders"`
	Expand     string    `query:"expand" json:"-" example:"daily"`
}

type listUserUsageRequest struct {
	StrictRequest
	Page       Page      `json:"-"`
	Sort       SortOrder `sort:"cost,shareOfSpend,activities,costPerActivity,name,uncachedInputCost,outputCost,cacheReadCost,cacheWriteCost,budget,budgetUsage" json:"-"`
	Period     string    `query:"period,nonempty" json:"-" example:"last_30_days"`
	StartDate  string    `query:"startDate,nonempty" json:"-" example:"2026-10-01"`
	EndDate    string    `query:"endDate,nonempty" json:"-" example:"2026-10-15"`
	Search     string    `query:"search" json:"-" example:"jane"`
	Population string    `query:"population,nonempty" json:"-" example:"spenders"`
	Expand     string    `query:"expand" json:"-" example:"daily"`
	BudgetID   string    `query:"budgetId,nonempty" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e50"`
}

type usageListQuery struct {
	page       Page
	sort       SortOrder
	window     usageWindowRequest
	search     string
	population string
	expand     string
	budgetID   string
}

func (r listAgentUsageRequest) query() usageListQuery {
	return usageListQuery{page: r.Page, sort: r.Sort, search: r.Search, population: r.Population, expand: r.Expand,
		window: usageWindowRequest{Period: r.Period, StartDate: r.StartDate, EndDate: r.EndDate}}
}

func (r listWorkflowUsageRequest) query() usageListQuery {
	return usageListQuery{page: r.Page, sort: r.Sort, search: r.Search, population: r.Population, expand: r.Expand,
		window: usageWindowRequest{Period: r.Period, StartDate: r.StartDate, EndDate: r.EndDate}}
}

func (r listModelUsageRequest) query() usageListQuery {
	return usageListQuery{page: r.Page, sort: r.Sort, search: r.Search, population: r.Population, expand: r.Expand,
		window: usageWindowRequest{Period: r.Period, StartDate: r.StartDate, EndDate: r.EndDate}}
}

func (r listUserUsageRequest) query() usageListQuery {
	return usageListQuery{page: r.Page, sort: r.Sort, search: r.Search, population: r.Population, expand: r.Expand,
		budgetID: r.BudgetID, window: usageWindowRequest{Period: r.Period, StartDate: r.StartDate, EndDate: r.EndDate}}
}

func (h *usageHandler) listAgents(ctx context.Context, req listAgentUsageRequest) (List[UsageRow], error) {
	rows, count, err := h.list(ctx, store.UsageAgents, usagePathAgents, req.query())
	if err != nil {
		return List[UsageRow]{}, err
	}
	return List[UsageRow]{Items: toUsageRows(entplatform.UsageRowDTOs(rows), usageTypeAgent), Total: count}, nil
}

func (h *usageHandler) listWorkflows(ctx context.Context, req listWorkflowUsageRequest) (List[UsageRow], error) {
	rows, count, err := h.list(ctx, store.UsageWorkflows, usagePathWorkflows, req.query())
	if err != nil {
		return List[UsageRow]{}, err
	}
	return List[UsageRow]{Items: toUsageRows(entplatform.UsageRowDTOs(rows), usageTypeWorkflow), Total: count}, nil
}

func (h *usageHandler) listModels(ctx context.Context, req listModelUsageRequest) (List[ModelUsageRow], error) {
	rows, count, err := h.list(ctx, store.UsageModels, usagePathModels, req.query())
	if err != nil {
		return List[ModelUsageRow]{}, err
	}
	dtos := entplatform.UsageModelRowDTOs(rows)
	items := make([]ModelUsageRow, len(dtos))
	for i, d := range dtos {
		r := d.UsageRowResponse
		items[i] = ModelUsageRow{
			ID: r.ID, Type: usageTypeModel, Name: r.Name, BookedName: r.BookedName, Cost: usageMoney(r.Cost),
			Activities: r.Activities, CostPerActivity: usageMoneyOrNil(r.CostPerActivity), ShareOfSpend: derefOrZero(r.ShareOfSpend),
			CostByBucket: toUsageCostByBucket(r.CostByBucket), Tokens: toUsageTokens(r.Tokens), Calls: r.Calls,
			UnpricedCalls: r.UnpricedCalls, Daily: toUsageDays(r.Daily), Models: toUsageServedModels(d.Models),
		}
	}
	return List[ModelUsageRow]{Items: items, Total: count}, nil
}

func (h *usageHandler) listUsers(ctx context.Context, req listUserUsageRequest) (List[UserUsageRow], error) {
	rows, count, err := h.list(ctx, store.UsageUsers, usagePathUsers, req.query())
	if err != nil {
		return List[UserUsageRow]{}, err
	}
	dtos := entplatform.UsageUserRowDTOs(rows)
	items := make([]UserUsageRow, len(dtos))
	for i, d := range dtos {
		r := d.UsageRowResponse
		items[i] = UserUsageRow{
			ID: r.ID, Type: usageTypeUser, Name: r.Name, Cost: usageMoney(r.Cost), Activities: r.Activities,
			CostPerActivity: usageMoneyOrNil(r.CostPerActivity), ShareOfSpend: derefOrZero(r.ShareOfSpend),
			CostByBucket: toUsageCostByBucket(r.CostByBucket), Tokens: toUsageTokens(r.Tokens), Calls: r.Calls,
			UnpricedCalls: r.UnpricedCalls, Daily: toUsageDays(r.Daily), User: toUsageUser(d.User),
			Budget: toUsageBudget(d.Budget), BudgetUsage: toUsageBudgetWindow(d.BudgetUsage),
		}
	}
	return List[UserUsageRow]{Items: items, Total: count}, nil
}

func (h *usageHandler) list(ctx context.Context, kind store.UsageKind, auditPath string, q usageListQuery) ([]usage.Row, int, error) {
	problems := newQueryProblems()
	req := usage.ListRequest{
		Window: problems.windowSyntax(q.window), Name: q.search, BudgetID: q.budgetID, AllSubjects: true,
		Sort: "cost", Descending: true, PageNumber: q.page.Number, PageSize: q.page.Size,
	}
	if q.sort.Field != "" {
		req.Sort, req.Descending = usage.ListSort(q.sort.Field), q.sort.Desc
	}
	if q.budgetID != "" && !IsCanonicalUUID(q.budgetID) {
		problems.addSyntax("budgetId", "must be a UUID")
	}
	problems.checkName("search", q.search)
	if q.population != "" {
		all, ok := usagePopulations[q.population]
		if !ok {
			problems.addSemantic("population", "must be one of all, spenders")
		}
		req.AllSubjects = all || !ok
	}
	daily, problem := apicontract.ExpandOnly(q.expand, usageExpandDaily)
	if problem != "" {
		problems.addSemantic("expand", problem)
	}
	req.Daily = daily
	problems.addService(h.svc.CheckList(kind, req))
	if err := problems.err(); err != nil {
		return nil, 0, err
	}
	got, err := h.svc.List(ctx, kind, req)
	if err != nil {
		return nil, 0, usageError(err)
	}
	h.audit(ctx, auditPath, "", "rowCount", len(got.Rows))
	return got.Rows, got.Count, nil
}

func toUsageRows(dtos []entplatform.UsageRowResponse, resourceType string) []UsageRow {
	items := make([]UsageRow, len(dtos))
	for i, r := range dtos {
		items[i] = UsageRow{
			ID: r.ID, Type: resourceType, Name: r.Name, BookedName: r.BookedName, Cost: usageMoney(r.Cost),
			Activities: r.Activities, CostPerActivity: usageMoneyOrNil(r.CostPerActivity), ShareOfSpend: derefOrZero(r.ShareOfSpend),
			CostByBucket: toUsageCostByBucket(r.CostByBucket), Tokens: toUsageTokens(r.Tokens), Calls: r.Calls,
			UnpricedCalls: r.UnpricedCalls, Daily: toUsageDays(r.Daily),
		}
	}
	return items
}

func toUsageBudgetWindow(w *entplatform.UsageBudgetWindow) *UsageBudgetWindow {
	if w == nil {
		return nil
	}
	return &UsageBudgetWindow{WindowBudget: usageMoney(w.WindowBudget), UsagePercent: w.UsagePercent}
}

func derefOrZero[T any](v *T) T {
	if v == nil {
		var zero T
		return zero
	}
	return *v
}
