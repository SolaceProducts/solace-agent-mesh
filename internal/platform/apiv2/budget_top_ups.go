// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type BudgetTopUp struct {
	Amount      *string `json:"amount" example:"50.00"`
	ExpiresTime Instant `json:"expiresTime"`
	CreatedBy   *string `json:"createdBy" example:"jane.doe@example.com"`
	CreatedTime Instant `json:"createdTime"`
	UpdatedBy   *string `json:"updatedBy" example:"jane.doe@example.com"`
	UpdatedTime Instant `json:"updatedTime"`
}

type SetBudgetTopUpRequest struct {
	ID     string `path:"id,builtin" json:"-"`
	Amount string `json:"amount" required:"true" example:"50.00"`
}

type budgetSubjectTopUpHandler struct {
	subjects *entplatform.BudgetAssignmentService
	topUps   *entplatform.BudgetTopUpService
}

func budgetSubjectTopUpRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.BudgetAssignmentSvc == nil || deps.BudgetTopUpSvc == nil {
		return nil
	}
	h := &budgetSubjectTopUpHandler{subjects: deps.BudgetAssignmentSvc, topUps: deps.BudgetTopUpSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/budgetSubjects/{id}/topUp", Scope: rbac.ScopeBudgetRead, Stability: StabilityDraft,
			Doc: Doc{ID: "getBudgetSubjectTopUp", Summary: "Get a budget subject's top-up",
				Text: "Returns the active top-up of one user, or of the mesh, with every field null when it has none.", Tag: budgetSubjectsTag},
			Handler: Handle(h.get, "Unable to get budget top-up")},
		{Method: http.MethodPut, Path: "/budgetSubjects/{id}/topUp", Gate: GateBudgetAssign, Stability: StabilityDraft,
			Doc: Doc{ID: "setBudgetSubjectTopUp", Summary: "Add or replace a budget subject's top-up",
				Text: "Raises the budget of one user, or of the mesh, by the amount until the end of the current budget period. The amount replaces that of an active top-up. Returns the top-up, with 201 when it created one and 200 otherwise. An amount of 0 removes the active top-up, if any, and returns every field null.", Tag: budgetSubjectsTag},
			Handler: Handle(h.set, "Unable to set budget top-up")},
	}
}

func (h *budgetSubjectTopUpHandler) get(ctx context.Context, req getBudgetSubjectRequest) (BudgetTopUp, error) {
	subject, err := h.subjects.GetSubject(ctx, req.ID)
	if err != nil {
		return BudgetTopUp{}, err
	}
	active, err := h.topUps.Active(ctx, subject.SubjectType, subject.ID)
	if err != nil {
		return BudgetTopUp{}, err
	}
	return toBudgetTopUp(active), nil
}

func (h *budgetSubjectTopUpHandler) set(ctx context.Context, req SetBudgetTopUpRequest) (apicontract.Upserted[BudgetTopUp], error) {
	subject, err := h.subjects.GetSubject(ctx, req.ID)
	if err != nil {
		return apicontract.Upserted[BudgetTopUp]{}, err
	}
	actor := entplatform.UserIDFromContext(ctx)
	if amount, parseErr := budget.ParseAmount(req.Amount); parseErr == nil && amount == 0 {
		return apicontract.Upserted[BudgetTopUp]{}, h.topUps.CancelForSubject(ctx, subject.SubjectType, subject.ID, actor)
	}
	grant := entplatform.CreateBudgetTopUpRequest{SubjectType: subject.SubjectType, Amount: req.Amount}
	if subject.SubjectType == store.BudgetSubjectUser {
		grant.SubjectID = subject.ID
	}
	resp, created, err := h.topUps.Grant(ctx, grant, actor)
	if err != nil {
		return apicontract.Upserted[BudgetTopUp]{}, explainBudgetConflict(err, "Unable to set budget top-up")
	}
	return apicontract.Upserted[BudgetTopUp]{Resource: toBudgetTopUp(resp), Created: created}, nil
}

func toBudgetTopUp(t *entplatform.BudgetTopUpResponse) BudgetTopUp {
	if t == nil {
		return BudgetTopUp{}
	}
	return BudgetTopUp{
		Amount:      &t.Amount,
		ExpiresTime: Instant(t.ExpiresTime),
		CreatedBy:   &t.CreatedBy,
		CreatedTime: Instant(t.CreatedTime),
		UpdatedBy:   &t.UpdatedBy,
		UpdatedTime: Instant(t.UpdatedTime),
	}
}
