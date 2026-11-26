// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const budgetSubjectResourceType = "budgetSubject"

type BudgetSubject struct {
	ID              string             `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type            string             `json:"type" example:"budgetSubject"`
	SubjectType     string             `json:"subjectType" example:"user"`
	DisplayName     *string            `json:"displayName" example:"Jane Doe"`
	User            *BudgetSubjectUser `json:"user"`
	Budget          *AppliedBudget     `json:"budget"`
	TopUp           *ActiveTopUp       `json:"topUp"`
	EffectiveAmount *string            `json:"effectiveAmount" example:"300.00"`
}

type BudgetSubjectUser struct {
	Email            *string `json:"email" example:"jane.doe@example.com"`
	IsSystemUser     bool    `json:"isSystemUser" example:"false"`
	IsServiceAccount bool    `json:"isServiceAccount" example:"false"`
}

type listBudgetSubjectsRequest struct {
	StrictRequest
	Page        Page      `json:"-"`
	SubjectType string    `query:"subjectType,nonempty" required:"true" json:"-" example:"user"`
	Search      string    `query:"search" json:"-" example:"jane"`
	Sort        SortOrder `sort:"displayName" json:"-"`
}

type getBudgetSubjectRequest struct {
	StrictRequest
	ID string `path:"id,builtin" json:"-"`
}

type budgetSubjectHandler struct {
	svc *entplatform.BudgetAssignmentService
}

func budgetSubjectRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.BudgetAssignmentSvc == nil {
		return nil
	}
	h := &budgetSubjectHandler{svc: deps.BudgetAssignmentSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/budgetSubjects", Scope: rbac.ScopeBudgetRead, Stability: StabilityDraft,
			Deprecation: isSystemUserFieldDeprecation,
			Doc: Doc{ID: "listBudgetSubjects", Summary: "Get a list of budget subjects",
				Text: "Returns one page of the subjects of one tier, each with the budget that applies to it and its active top-up.", Tag: budgetSubjectsTag},
			Handler: Handle(h.list, "Unable to list budget subjects")},
		{Method: http.MethodGet, Path: "/budgetSubjects/{id}", Scope: rbac.ScopeBudgetRead, Stability: StabilityDraft,
			Deprecation: isSystemUserFieldDeprecation,
			Doc: Doc{ID: "getBudgetSubject", Summary: "Get a budget subject",
				Text: "Returns one user, or the mesh, with the budget that applies to it and its active top-up.", Tag: budgetSubjectsTag},
			Handler: Handle(h.get, "Unable to get budget subject")},
	}
}

func (h *budgetSubjectHandler) list(ctx context.Context, req listBudgetSubjectsRequest) (List[BudgetSubject], error) {
	resp, err := h.svc.ListSubjects(ctx, entplatform.ListBudgetSubjectsParams{
		PageNumber: req.Page.Number, PageSize: req.Page.Size,
		SubjectType: req.SubjectType, Search: strings.TrimSpace(req.Search), Descending: req.Sort.Desc,
	})
	if err != nil {
		return List[BudgetSubject]{}, err
	}
	return ListOf(resp, toBudgetSubject), nil
}

func (h *budgetSubjectHandler) get(ctx context.Context, req getBudgetSubjectRequest) (BudgetSubject, error) {
	resp, err := h.svc.GetSubject(ctx, req.ID)
	if err != nil {
		return BudgetSubject{}, err
	}
	return toBudgetSubject(resp), nil
}

func toBudgetSubject(s *entplatform.BudgetSubjectResponse) BudgetSubject {
	return BudgetSubject{
		ID:              s.ID,
		Type:            budgetSubjectResourceType,
		SubjectType:     s.SubjectType,
		DisplayName:     s.DisplayName,
		User:            toBudgetSubjectUser(s.User),
		Budget:          toAppliedBudget(s.Budget),
		TopUp:           toActiveTopUp(s.TopUp),
		EffectiveAmount: s.EffectiveAmount,
	}
}

func toBudgetSubjectUser(u *entplatform.BudgetSubjectUserIdentity) *BudgetSubjectUser {
	if u == nil {
		return nil
	}
	return &BudgetSubjectUser{Email: u.Email, IsSystemUser: u.IsSystemUser, IsServiceAccount: u.IsSystemUser}
}
