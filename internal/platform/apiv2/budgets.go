// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	budgetResourceType = "budget"
	budgetPathByID     = "/budgets/{id}"
	msgUpdateBudget    = "Unable to update budget"
)

var nullJSON = json.RawMessage("null")

type Budget struct {
	ID                  string  `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e50"`
	Type                string  `json:"type" example:"budget"`
	Name                string  `json:"name" example:"Engineering monthly"`
	SubjectType         string  `json:"subjectType" example:"user"`
	Amount              *string `json:"amount" example:"250.00"`
	WarningPercentage   *int    `json:"warningPercentage" example:"80"`
	IsDefault           bool    `json:"isDefault" example:"false"`
	AssignedCount       int     `json:"assignedCount" example:"12"`
	HiddenAssignedCount int     `json:"hiddenAssignedCount" example:"0"`
	PendingCount        int     `json:"pendingCount" example:"2"`
	CreatedBy           string  `json:"createdBy" example:"jane.doe@example.com"`
	CreatedTime         Instant `json:"createdTime"`
	UpdatedBy           string  `json:"updatedBy" example:"jane.doe@example.com"`
	UpdatedTime         Instant `json:"updatedTime"`
}

type CreateBudgetRequest struct {
	Name              string        `json:"name" required:"true" example:"Engineering monthly"`
	SubjectType       string        `json:"subjectType" required:"true" example:"user"`
	Amount            Patch[string] `json:"amount" required:"true" clearable:"true" example:"250.00"`
	WarningPercentage *int          `json:"warningPercentage" example:"80"`
}

type UpdateBudgetRequest struct {
	ID                string        `path:"id" json:"-"`
	Name              Patch[string] `json:"name" example:"Engineering monthly"`
	Amount            Patch[string] `json:"amount" clearable:"true" example:"300.00"`
	WarningPercentage Patch[int]    `json:"warningPercentage" clearable:"true" example:"90"`
	SubjectType       Patch[string] `json:"subjectType" example:"user"`
	IsDefault         Patch[bool]   `json:"isDefault" example:"true"`
}

type listBudgetsRequest struct {
	StrictRequest
	Page        Page      `json:"-"`
	SubjectType string    `query:"subjectType,nonempty" json:"-" example:"user"`
	Search      string    `query:"search" json:"-" example:"engineering"`
	Sort        SortOrder `sort:"name,amount" json:"-"`
}

type budgetHandler struct {
	svc *entplatform.BudgetService
}

func budgetRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.BudgetSvc == nil {
		return nil
	}
	h := &budgetHandler{svc: deps.BudgetSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/budgets", Scope: rbac.ScopeBudgetRead, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "listBudgets", Summary: "Get a list of budgets",
				Text: "Returns one page of budgets, optionally only those of one tier or whose name contains a search term.", Tag: budgetsTag},
			Handler: Handle(h.list, "Unable to list budgets")},
		{Method: http.MethodPost, Path: "/budgets", Scope: rbac.ScopeBudgetCreate, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "createBudget", Summary: "Create a budget",
				Text: "Creates a budget and returns it. A user budget applies to nobody until it is assigned or made its tier's default.", Tag: budgetsTag},
			Handler: Handle(h.create, "Unable to create budget")},
		{Method: http.MethodGet, Path: budgetPathByID, Scope: rbac.ScopeBudgetRead, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "getBudget", Summary: "Get a budget",
				Text: "Returns one budget.", Tag: budgetsTag},
			Handler: Handle(h.get, "Unable to get budget")},
		{Method: http.MethodPatch, Path: budgetPathByID, Scope: rbac.ScopeBudgetUpdate, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "updateBudget", Summary: "Update a budget",
				Text: "Changes the budget's name, amount or warning threshold. An empty body changes nothing and returns the budget.", Tag: budgetsTag},
			Handler: Handle(h.update, msgUpdateBudget)},
		{Method: http.MethodDelete, Path: budgetPathByID, Gate: GateBudgetDelete, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "deleteBudget", Summary: "Delete a budget",
				Text: "Deletes a budget that is not its tier's default, with its assignments, pending ones included, so its users revert to the default. The mesh budget cannot be deleted.", Tag: budgetsTag},
			Handler: Handle(h.delete, "Unable to delete budget")},
		{Method: http.MethodPut, Path: "/budgets/{id}/makeDefault", Scope: rbac.ScopeBudgetAssign, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "makeBudgetDefault", Summary: "Update a budget to be its tier's default",
				Text: "Makes the budget the one that applies to every subject of its tier with no assignment of its own, in place of the previous default, and returns it. Making the current default the default changes nothing.", Tag: budgetsTag},
			Handler: Handle(h.makeDefault, msgUpdateBudget)},
	}
}

func (h *budgetHandler) list(ctx context.Context, req listBudgetsRequest) (List[Budget], error) {
	query := entplatform.BudgetListQuery{
		PageNumber: req.Page.Number, PageSize: req.Page.Size,
		SubjectType: req.SubjectType, Name: strings.TrimSpace(req.Search), SortBy: req.Sort.Field,
	}
	if req.Sort.Desc {
		query.SortDir = "desc"
	}
	resp, err := h.svc.List(ctx, query)
	if err != nil {
		return List[Budget]{}, err
	}
	return ListOf(resp, toBudget), nil
}

func (h *budgetHandler) create(ctx context.Context, req CreateBudgetRequest) (Budget, error) {
	create := entplatform.CreateBudgetRequest{
		Name: req.Name, SubjectType: req.SubjectType,
		AmountSet: req.Amount.Set, WarningPercentage: req.WarningPercentage,
	}
	if req.Amount.Set && !req.Amount.Null {
		create.Amount = &req.Amount.Value
	}
	resp, err := h.svc.Create(ctx, create, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Budget{}, explainBudgetConflict(err, "Unable to create budget")
	}
	return toBudget(resp), nil
}

type getBudgetRequest struct {
	StrictRequest
	ID string `path:"id" json:"-"`
}

func (h *budgetHandler) get(ctx context.Context, req getBudgetRequest) (Budget, error) {
	resp, err := h.svc.Get(ctx, req.ID)
	if err != nil {
		return Budget{}, err
	}
	return toBudget(resp), nil
}

func (h *budgetHandler) update(ctx context.Context, req UpdateBudgetRequest) (Budget, error) {
	resp, err := h.svc.Update(ctx, req.ID, req.toV1(), entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Budget{}, explainBudgetConflict(err, msgUpdateBudget)
	}
	return toBudget(resp), nil
}

func (r UpdateBudgetRequest) toV1() entplatform.UpdateBudgetRequest {
	update := entplatform.UpdateBudgetRequest{
		NameSet: r.Name.Set, AmountSet: r.Amount.Set, WarningPercentageSet: r.WarningPercentage.Set,
	}
	if r.Name.Set && !r.Name.Null {
		update.Name = &r.Name.Value
	}
	if r.Amount.Set && !r.Amount.Null {
		update.Amount = &r.Amount.Value
	}
	if r.WarningPercentage.Set && !r.WarningPercentage.Null {
		update.WarningPercentage = &r.WarningPercentage.Value
	}
	if r.SubjectType.Set {
		update.SubjectType = nullJSON
	}
	if r.IsDefault.Set {
		update.IsDefault = nullJSON
	}
	return update
}

func (h *budgetHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	err := h.svc.Delete(ctx, req.ID, entplatform.UserIDFromContext(ctx))
	return struct{}{}, explainBudgetConflict(err, "Unable to delete budget")
}

func (h *budgetHandler) makeDefault(ctx context.Context, req IDRequest) (Budget, error) {
	resp, err := h.svc.SetDefault(ctx, req.ID, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Budget{}, explainBudgetConflict(err, msgUpdateBudget)
	}
	return toBudget(resp), nil
}

func toBudget(b *entplatform.BudgetResponse) Budget {
	return Budget{
		ID:                  b.ID,
		Type:                budgetResourceType,
		Name:                b.Name,
		SubjectType:         b.SubjectType,
		Amount:              b.Amount,
		WarningPercentage:   b.WarningPercentage,
		IsDefault:           b.IsDefault,
		AssignedCount:       b.AssignedCount,
		HiddenAssignedCount: b.HiddenAssignedCount,
		PendingCount:        b.PendingCount,
		CreatedBy:           b.CreatedBy,
		CreatedTime:         Instant(b.CreatedTime),
		UpdatedBy:           b.UpdatedBy,
		UpdatedTime:         Instant(b.UpdatedTime),
	}
}
