// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	budgetSubjectResourceType = "budgetSubject"
	resourceBudgetSubject     = "budget_subject"
)

type BudgetSubjectResponse struct {
	ID              string                     `json:"id"`
	Type            string                     `json:"type"`
	SubjectType     string                     `json:"subjectType"`
	DisplayName     *string                    `json:"displayName"`
	User            *BudgetSubjectUserIdentity `json:"user,omitempty"`
	Budget          *BudgetSubjectBudget       `json:"budget"`
	TopUp           *BudgetSubjectTopUp        `json:"topUp"`
	EffectiveAmount *string                    `json:"effectiveAmount"`
}

type BudgetSubjectBudget struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Amount       *string `json:"amount"`
	Source       string  `json:"source"`
	AssignmentID *string `json:"assignmentId"`
}

type BudgetSubjectTopUp struct {
	Amount      string        `json:"amount"`
	ExpiresTime RESTTimestamp `json:"expiresTime"`
}

type subjectTopUp struct {
	amount, expiresTime int64
}

func toBudgetSubjectResponse(s *store.UserBudgetSubject, tierDefault *store.Budget) BudgetSubjectResponse {
	var assigned *store.Budget
	if s.BudgetID != nil {
		assigned = &store.Budget{ID: *s.BudgetID, Name: *s.BudgetName, Amount: s.BudgetAmount}
	}
	var topUp *subjectTopUp
	if s.TopUpID != nil {
		topUp = &subjectTopUp{amount: *s.TopUpAmount, expiresTime: *s.TopUpExpiresTime}
	}
	resp := BudgetSubjectResponse{
		ID:          s.ID,
		Type:        budgetSubjectResourceType,
		SubjectType: store.BudgetSubjectUser,
		DisplayName: s.DisplayName,
		User:        budgetSubjectUserIdentity(store.BudgetSubjectUser, s.Email, s.IsSystemUser),
	}
	applied, source := budget.Resolve(assigned, tierDefault)
	withBudget(&resp, applied, source, s.AssignmentID, topUp)
	return resp
}

func meshSubjectResponse(meshBudget *store.Budget, topUp *subjectTopUp) BudgetSubjectResponse {
	resp := BudgetSubjectResponse{
		ID:          store.BudgetMeshSubjectRef,
		Type:        budgetSubjectResourceType,
		SubjectType: store.BudgetSubjectMesh,
	}
	applied, source := budget.Resolve(nil, meshBudget)
	withBudget(&resp, applied, source, nil, topUp)
	return resp
}

func withBudget(resp *BudgetSubjectResponse, applied *store.Budget, source budget.Source, assignmentID *string, topUp *subjectTopUp) {
	var topUpAmount int64
	if topUp != nil {
		topUpAmount = topUp.amount
		resp.TopUp = &BudgetSubjectTopUp{
			Amount: budget.FormatAmount(topUp.amount), ExpiresTime: restTimestamp(topUp.expiresTime),
		}
	}
	if applied == nil {
		return
	}
	resp.Budget = &BudgetSubjectBudget{
		ID: applied.ID, Name: applied.Name, Amount: formatBudgetAmount(applied.Amount), Source: string(source),
		AssignmentID: assignmentID,
	}
	resp.EffectiveAmount = formatBudgetAmount(budget.EffectiveCap(applied.Amount, topUpAmount))
}

type ListBudgetSubjectsParams struct {
	PageNumber, PageSize int
	SubjectType          string
	Search               string
	Descending           bool
}

func (s *BudgetAssignmentService) ListSubjects(ctx context.Context, p ListBudgetSubjectsParams) (*PaginatedResponse[BudgetSubjectResponse], error) {
	var v budgetValidation
	v.subjectListType(p)
	if err := v.err(); err != nil {
		return nil, err
	}
	nowMs := s.now().UnixMilli()
	if p.SubjectType == store.BudgetSubjectMesh {
		return s.meshSubject(ctx, p, nowMs)
	}

	filter := store.UserBudgetSubjectFilter{
		Search: p.Search, HiddenSystemSubs: rbac.UserListingHiddenSubs(), Descending: p.Descending,
		NowMs: nowMs,
	}
	total, err := s.assignments.CountUserSubjects(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list budget subjects: %w", err)
	}
	tierDefault, err := tierDefaultBudget(ctx, s.budgets, store.BudgetSubjectUser)
	if err != nil {
		return nil, err
	}
	filter.Limit, filter.Offset = p.PageSize, (p.PageNumber-1)*p.PageSize
	subjects, err := s.assignments.ListUserSubjects(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list budget subjects: %w", err)
	}
	data := make([]BudgetSubjectResponse, len(subjects))
	for i, subject := range subjects {
		data[i] = toBudgetSubjectResponse(subject, tierDefault)
	}
	return &PaginatedResponse[BudgetSubjectResponse]{
		Data: data,
		Meta: NewPaginationMeta(p.PageNumber, p.PageSize, int(total)),
	}, nil
}

func appliedUserBudgets(ctx context.Context, assignments *store.BudgetAssignmentsRepo, budgets *store.BudgetsRepo, ids []string, nowMs int64) (map[string]BudgetSubjectResponse, error) {
	applied := make(map[string]BudgetSubjectResponse, len(ids))
	if len(ids) == 0 {
		return applied, nil
	}
	tierDefault, err := tierDefaultBudget(ctx, budgets, store.BudgetSubjectUser)
	if err != nil {
		return nil, err
	}
	subjects, err := assignments.ListUserSubjects(ctx, store.UserBudgetSubjectFilter{IDs: ids, Limit: len(ids), NowMs: nowMs})
	if err != nil {
		return nil, fmt.Errorf("resolve applied budgets: %w", err)
	}
	for _, subject := range subjects {
		applied[subject.ID] = toBudgetSubjectResponse(subject, tierDefault)
	}
	return applied, nil
}

func (s *BudgetAssignmentService) meshSubject(ctx context.Context, p ListBudgetSubjectsParams, nowMs int64) (*PaginatedResponse[BudgetSubjectResponse], error) {
	mesh, err := s.mesh(ctx, nowMs)
	if err != nil {
		return nil, err
	}
	data := make([]BudgetSubjectResponse, 0, 1)
	if p.PageNumber == 1 {
		data = append(data, mesh)
	}
	return &PaginatedResponse[BudgetSubjectResponse]{
		Data: data,
		Meta: NewPaginationMeta(p.PageNumber, p.PageSize, 1),
	}, nil
}

func (s *BudgetAssignmentService) mesh(ctx context.Context, nowMs int64) (BudgetSubjectResponse, error) {
	meshBudget, err := tierDefaultBudget(ctx, s.budgets, store.BudgetSubjectMesh)
	if err != nil {
		return BudgetSubjectResponse{}, err
	}
	active, err := s.topUps.Active(ctx, store.BudgetSubjectMesh, store.BudgetMeshSubjectRef, nowMs)
	if err != nil {
		return BudgetSubjectResponse{}, fmt.Errorf("read the mesh's top-up: %w", err)
	}
	var topUp *subjectTopUp
	if active != nil {
		topUp = &subjectTopUp{amount: active.Amount, expiresTime: active.ExpiresTime}
	}
	return meshSubjectResponse(meshBudget, topUp), nil
}

func (s *BudgetAssignmentService) GetSubject(ctx context.Context, id string) (*BudgetSubjectResponse, error) {
	nowMs := s.now().UnixMilli()
	if id == store.BudgetMeshSubjectRef {
		mesh, err := s.mesh(ctx, nowMs)
		if err != nil {
			return nil, err
		}
		return &mesh, nil
	}
	applied, err := appliedUserBudgets(ctx, s.assignments, s.budgets, []string{id}, nowMs)
	if err != nil {
		return nil, err
	}
	resp, ok := applied[id]
	if !ok {
		return nil, &NotFoundError{Resource: resourceBudgetSubject, ID: id}
	}
	return &resp, nil
}

func (v *budgetValidation) subjectListType(p ListBudgetSubjectsParams) {
	switch p.SubjectType {
	case store.BudgetSubjectUser:
	case store.BudgetSubjectMesh:
		if p.Search != "" {
			v.add("search", "is not supported for the mesh, which is one subject")
		}
	case "":
		v.add("subjectType", msgIsRequired)
	case "agent":
		v.add("subjectType", "agent budgets are not supported yet; must be one of: mesh, user")
	default:
		v.add("subjectType", "must be one of: mesh, user")
	}
}

func tierDefaultBudget(ctx context.Context, budgets *store.BudgetsRepo, subjectType string) (*store.Budget, error) {
	defaults, err := budgets.List(ctx, store.BudgetFilter{SubjectType: subjectType, DefaultOnly: true, Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("read the %s tier's default budget: %w", subjectType, err)
	}
	if len(defaults) == 0 {
		return nil, nil
	}
	return defaults[0], nil
}
