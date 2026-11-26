// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	budgetAssignmentResourceType   = "budgetAssignment"
	budgetAssignmentPathByID       = "/budgetAssignments/{id}"
	budgetAssignmentExpandIdentity = "identity"
	subjectNotChangeable           = "cannot be changed; delete the assignment and create one for the other subject"
)

type BudgetAssignment struct {
	ID          string                  `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e51"`
	Type        string                  `json:"type" example:"budgetAssignment"`
	State       string                  `json:"state" example:"concrete"`
	Subject     BudgetAssignmentSubject `json:"subject"`
	Budget      BudgetRef               `json:"budget"`
	CreatedBy   string                  `json:"createdBy" example:"jane.doe@example.com"`
	CreatedTime Instant                 `json:"createdTime"`
	UpdatedBy   string                  `json:"updatedBy" example:"jane.doe@example.com"`
	UpdatedTime Instant                 `json:"updatedTime"`
}

type BudgetAssignmentSubject struct {
	Type        string                    `json:"type" example:"user"`
	ID          *string                   `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	DisplayName *string                   `json:"displayName" example:"Jane Doe"`
	User        *BudgetAssignmentUser     `json:"user"`
	Identity    *BudgetAssignmentIdentity `json:"identity"`
}

type BudgetAssignmentUser struct {
	Email            *string `json:"email" example:"jane.doe@example.com"`
	IsSystemUser     bool    `json:"isSystemUser" example:"false"`
	IsServiceAccount bool    `json:"isServiceAccount" example:"false"`
	EmailShared      *bool   `json:"emailShared" example:"false"`
	Sub              *string `json:"sub" example:"00u1abcd2EFGH3ijk4l5"`
	Issuer           *string `json:"issuer" example:"https://idp.example.com"`
}

type BudgetAssignmentIdentity struct {
	Type   string  `json:"type" example:"email"`
	Value  string  `json:"value" example:"jane.doe@example.com"`
	Issuer *string `json:"issuer" example:"https://idp.example.com"`
}

type BudgetRef struct {
	ID   string `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e50"`
	Name string `json:"name" example:"Engineering monthly"`
}

type BudgetAssignmentIdentityRequest struct {
	Type   string  `json:"type" required:"true" example:"email"`
	Value  string  `json:"value" required:"true" example:"jane.doe@example.com"`
	Issuer *string `json:"issuer" example:"https://idp.example.com"`
}

type CreateBudgetAssignmentRequest struct {
	SubjectType string                           `json:"subjectType" required:"true" example:"user"`
	SubjectID   *string                          `json:"subjectId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Identity    *BudgetAssignmentIdentityRequest `json:"identity"`
	BudgetID    string                           `json:"budgetId" required:"true" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e50"`
}

type UpdateBudgetAssignmentRequest struct {
	ID          string                                 `path:"id" json:"-"`
	BudgetID    Patch[string]                          `json:"budgetId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e50"`
	Identity    Patch[BudgetAssignmentIdentityRequest] `json:"identity"`
	SubjectType Patch[string]                          `json:"subjectType" example:"user"`
	SubjectID   Patch[string]                          `json:"subjectId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
}

type listBudgetAssignmentsRequest struct {
	StrictRequest
	Page        Page   `json:"-"`
	BudgetID    string `query:"budgetId,nonempty" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e50"`
	SubjectType string `query:"subjectType,nonempty" json:"-" example:"user"`
	SubjectID   string `query:"subjectId,nonempty" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	State       string `query:"state,nonempty" json:"-" example:"pending"`
	Search      string `query:"search" json:"-" example:"jane"`
	Expand      string `query:"expand" json:"-" example:"identity"`
}

type getBudgetAssignmentRequest struct {
	StrictRequest
	ID     string `path:"id" json:"-"`
	Expand string `query:"expand" json:"-" example:"identity"`
}

type UpdateBudgetAssignmentsRequest struct {
	ID          string   `path:"id" json:"-"`
	SubjectType string   `json:"subjectType" required:"true" example:"user"`
	Add         []string `json:"add" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f\"]"`
	Remove      []string `json:"remove" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
}

type BudgetAssignmentChanges struct {
	Added     []AddedBudgetAssignment     `json:"added"`
	Moved     []MovedBudgetAssignment     `json:"moved"`
	Removed   []RemovedBudgetAssignment   `json:"removed"`
	Unchanged []UnchangedBudgetAssignment `json:"unchanged"`
}

type BudgetSubjectRef struct {
	ID string `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
}

type AddedBudgetAssignment struct {
	AssignmentID string           `json:"assignmentId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e51"`
	Subject      BudgetSubjectRef `json:"subject"`
}

type MovedBudgetAssignment struct {
	AssignmentID string           `json:"assignmentId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e51"`
	Subject      BudgetSubjectRef `json:"subject"`
	FromBudget   BudgetRef        `json:"fromBudget"`
}

type RemovedBudgetAssignment struct {
	AssignmentID string           `json:"assignmentId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e51"`
	Subject      BudgetSubjectRef `json:"subject"`
}

type UnchangedBudgetAssignment struct {
	AssignmentID *string          `json:"assignmentId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e51"`
	Subject      BudgetSubjectRef `json:"subject"`
	Reason       string           `json:"reason" example:"already_assigned"`
}

type budgetAssignmentHandler struct {
	svc *entplatform.BudgetAssignmentService
}

func budgetAssignmentRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.BudgetAssignmentSvc == nil {
		return nil
	}
	h := &budgetAssignmentHandler{svc: deps.BudgetAssignmentSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/budgetAssignments", Scope: rbac.ScopeBudgetRead, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Deprecation: isSystemUserFieldDeprecation,
			Doc: Doc{ID: "listBudgetAssignments", Summary: "Get a list of budget assignments",
				Text: "Returns one page of budget assignments, pending ones included, optionally filtered by budget, subject, state or a search term. Hidden service accounts are left out unless named by subjectId.", Tag: budgetAssignmentsTag},
			Handler: Handle(h.list, "Unable to list budget assignments")},
		{Method: http.MethodPost, Path: "/budgetAssignments", Gate: GateBudgetAssign, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Deprecation: isSystemUserFieldDeprecation,
			Doc: Doc{ID: "createBudgetAssignment", Summary: "Create a budget assignment",
				Text: "Assigns a budget to a user, or to a person by their identity before they first sign in, and returns the assignment.", Tag: budgetAssignmentsTag},
			Handler: Handle(h.create, "Unable to create budget assignment")},
		{Method: http.MethodGet, Path: budgetAssignmentPathByID, Scope: rbac.ScopeBudgetRead, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Deprecation: isSystemUserFieldDeprecation,
			Doc: Doc{ID: "getBudgetAssignment", Summary: "Get a budget assignment",
				Text: "Returns one budget assignment.", Tag: budgetAssignmentsTag},
			Handler: Handle(h.get, "Unable to get budget assignment")},
		{Method: http.MethodPatch, Path: budgetAssignmentPathByID, Gate: GateBudgetAssign, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Deprecation: isSystemUserFieldDeprecation,
			Doc: Doc{ID: "updateBudgetAssignment", Summary: "Update a budget assignment",
				Text: "Moves the assignment to another budget at the subject's tier, re-records the identity its user is named by, or both, in one change. An empty body changes nothing and returns the assignment.", Tag: budgetAssignmentsTag},
			Handler: Handle(h.update, "Unable to update budget assignment")},
		{Method: http.MethodDelete, Path: budgetAssignmentPathByID, Gate: GateBudgetAssign, AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "deleteBudgetAssignment", Summary: "Delete a budget assignment",
				Text: "Deletes the assignment; the subject falls back to their tier's default budget.", Tag: budgetAssignmentsTag},
			Handler: Handle(h.delete, "Unable to delete budget assignment")},
		{Method: http.MethodPut, Path: "/budgets/{id}/updateAssignments", Gate: GateBudgetAssign, Stability: StabilityDraft,
			Doc: Doc{ID: "updateBudgetAssignments", Summary: "Update the assignments of a budget",
				Text: "Adds users to the budget and removes users from it in one change, applied together or not at all, and reports what it did for each user.", Tag: budgetsTag},
			Handler: Handle(h.updateAssignments, "Unable to update budget assignments")},
	}
}

func (h *budgetAssignmentHandler) list(ctx context.Context, req listBudgetAssignmentsRequest) (List[BudgetAssignment], error) {
	problems := newQueryProblems()
	for _, q := range []struct{ name, value string }{{"budgetId", req.BudgetID}, {"subjectId", req.SubjectID}} {
		if q.value != "" && !IsCanonicalUUID(q.value) {
			problems.addSyntax(q.name, "must be a UUID")
		}
	}
	CheckOneOfQuery(problems.semantic, "state", req.State, []string{store.BudgetAssignmentConcrete, store.BudgetAssignmentPending})
	expand := problems.assignmentExpand(req.Expand)
	if err := problems.err(); err != nil {
		return List[BudgetAssignment]{}, err
	}
	if expand && !canReadIdentities(ctx) {
		return List[BudgetAssignment]{}, &CallerError{Status: http.StatusForbidden, Message: msgNoPermission}
	}
	resp, err := h.svc.List(ctx, entplatform.ListBudgetAssignmentsParams{
		PageNumber: req.Page.Number, PageSize: req.Page.Size,
		BudgetID: req.BudgetID, SubjectType: req.SubjectType, SubjectID: req.SubjectID, State: req.State,
		Search: req.Search, ExpandIdentity: expand, SearchIdentity: canReadIdentities(ctx),
	})
	if err != nil {
		return List[BudgetAssignment]{}, err
	}
	return ListOf(resp, toBudgetAssignment), nil
}

func (h *budgetAssignmentHandler) get(ctx context.Context, req getBudgetAssignmentRequest) (BudgetAssignment, error) {
	expand, err := parseAssignmentExpand(ctx, req.Expand)
	if err != nil {
		return BudgetAssignment{}, err
	}
	resp, err := h.svc.Get(ctx, req.ID, expand)
	if err != nil {
		return BudgetAssignment{}, err
	}
	return toBudgetAssignment(resp), nil
}

func (h *budgetAssignmentHandler) create(ctx context.Context, req CreateBudgetAssignmentRequest) (BudgetAssignment, error) {
	create := entplatform.CreateBudgetAssignmentRequest{SubjectType: req.SubjectType, BudgetID: req.BudgetID}
	if req.SubjectID != nil {
		create.SubjectID = *req.SubjectID
	}
	if req.Identity != nil {
		create.Identity = toAssignmentIdentity(*req.Identity)
	}
	resp, err := h.svc.Create(ctx, create, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return BudgetAssignment{}, explainBudgetConflict(err, "Unable to create budget assignment")
	}
	return toBudgetAssignment(resp), nil
}

func (h *budgetAssignmentHandler) update(ctx context.Context, req UpdateBudgetAssignmentRequest) (BudgetAssignment, error) {
	details := map[string][]string{}
	budgetID, moving := RequiredPatch(details, "budgetId", req.BudgetID)
	if req.Identity.Set && req.Identity.Null {
		details["identity"] = []string{"cannot be null"}
	}
	if req.SubjectType.Set {
		details["subjectType"] = []string{subjectNotChangeable}
	}
	if req.SubjectID.Set {
		details["subjectId"] = []string{subjectNotChangeable}
	}
	if len(details) > 0 {
		return BudgetAssignment{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgValidationFailed, Details: details}
	}
	rerecording := req.Identity.Set
	if !moving && !rerecording {
		return h.get(ctx, getBudgetAssignmentRequest{ID: req.ID})
	}
	var update entplatform.UpdateBudgetAssignmentRequest
	if moving {
		update.BudgetID = *budgetID
	}
	if rerecording {
		update.Identity = toAssignmentIdentity(req.Identity.Value)
	}
	resp, err := h.svc.Update(ctx, req.ID, update, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return BudgetAssignment{}, explainBudgetConflict(err, "Unable to update budget assignment")
	}
	return toBudgetAssignment(resp), nil
}

func toAssignmentIdentity(req BudgetAssignmentIdentityRequest) *entplatform.BudgetAssignmentIdentity {
	identity := &entplatform.BudgetAssignmentIdentity{Type: req.Type, Value: req.Value}
	if req.Issuer != nil {
		identity.Issuer = *req.Issuer
	}
	return identity
}

func (h *budgetAssignmentHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	return struct{}{}, h.svc.Delete(ctx, req.ID, entplatform.UserIDFromContext(ctx))
}

func (h *budgetAssignmentHandler) updateAssignments(ctx context.Context, req UpdateBudgetAssignmentsRequest) (BudgetAssignmentChanges, error) {
	resp, err := h.svc.UpdateAssignments(ctx, req.ID, entplatform.UpdateBudgetAssignmentsRequest{
		SubjectType: req.SubjectType, Add: req.Add, Remove: req.Remove,
	}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return BudgetAssignmentChanges{}, explainBudgetConflict(err, "Unable to update budget assignments")
	}
	return toBudgetAssignmentChanges(resp), nil
}

func parseAssignmentExpand(ctx context.Context, expand string) (bool, error) {
	problems := newQueryProblems()
	identity := problems.assignmentExpand(expand)
	if err := problems.err(); err != nil {
		return false, err
	}
	if identity && !canReadIdentities(ctx) {
		return false, &CallerError{Status: http.StatusForbidden, Message: msgNoPermission}
	}
	return identity, nil
}

func (p queryProblems) assignmentExpand(expand string) bool {
	asked, problem := apicontract.ExpandOnly(expand, budgetAssignmentExpandIdentity)
	if problem != "" {
		p.addSemantic("expand", problem)
	}
	return asked
}

func canReadIdentities(ctx context.Context) bool {
	return coreauth.HasScope(entplatform.ScopesFromContext(ctx), rbac.ScopeRBACRead)
}

func toBudgetAssignment(a *entplatform.BudgetAssignmentResponse) BudgetAssignment {
	subject := BudgetAssignmentSubject{Type: a.Subject.Type, ID: a.Subject.ID, DisplayName: a.Subject.DisplayName}
	if u := a.Subject.User; u != nil {
		subject.User = &BudgetAssignmentUser{
			Email: u.Email, IsSystemUser: u.IsSystemUser, IsServiceAccount: u.IsSystemUser, EmailShared: u.EmailShared, Sub: u.Sub, Issuer: u.Issuer,
		}
	}
	if i := a.Subject.Identity; i != nil {
		subject.Identity = &BudgetAssignmentIdentity{Type: i.Type, Value: i.Value, Issuer: NullIfZero(i.Issuer)}
	}
	return BudgetAssignment{
		ID:          a.ID,
		Type:        budgetAssignmentResourceType,
		State:       a.State,
		Subject:     subject,
		Budget:      BudgetRef{ID: a.Budget.ID, Name: a.Budget.Name},
		CreatedBy:   a.CreatedBy,
		CreatedTime: Instant(a.CreatedTime),
		UpdatedBy:   a.UpdatedBy,
		UpdatedTime: Instant(a.UpdatedTime),
	}
}

func toBudgetAssignmentChanges(r *entplatform.UpdateBudgetAssignmentsResponse) BudgetAssignmentChanges {
	changes := BudgetAssignmentChanges{
		Added:     make([]AddedBudgetAssignment, len(r.Added)),
		Moved:     make([]MovedBudgetAssignment, len(r.Moved)),
		Removed:   make([]RemovedBudgetAssignment, len(r.Removed)),
		Unchanged: make([]UnchangedBudgetAssignment, len(r.Unchanged)),
	}
	for i, e := range r.Added {
		changes.Added[i] = AddedBudgetAssignment{AssignmentID: e.AssignmentID, Subject: BudgetSubjectRef{ID: e.Subject.ID}}
	}
	for i, e := range r.Moved {
		changes.Moved[i] = MovedBudgetAssignment{
			AssignmentID: e.AssignmentID, Subject: BudgetSubjectRef{ID: e.Subject.ID},
			FromBudget: BudgetRef{ID: e.FromBudget.ID, Name: e.FromBudget.Name},
		}
	}
	for i, e := range r.Removed {
		changes.Removed[i] = RemovedBudgetAssignment{AssignmentID: e.AssignmentID, Subject: BudgetSubjectRef{ID: e.Subject.ID}}
	}
	for i, e := range r.Unchanged {
		changes.Unchanged[i] = UnchangedBudgetAssignment{AssignmentID: e.AssignmentID, Subject: BudgetSubjectRef{ID: e.Subject.ID}, Reason: e.Reason}
	}
	return changes
}
