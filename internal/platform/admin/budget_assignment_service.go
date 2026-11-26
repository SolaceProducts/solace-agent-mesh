// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	resourceBudgetAssignment = "budget_assignment"
	fieldIdentityValue       = "identity.value"

	maxBudgetAssignmentChanges = 1000
)

type BudgetAssignmentService struct {
	assignments *store.BudgetAssignmentsRepo
	budgets     *store.BudgetsRepo
	users       *store.UsersRepo
	topUps      *store.BudgetTopUpsRepo
	changed     *BudgetsChangedSignal
	logger      *slog.Logger
	now         func() time.Time
}

func NewBudgetAssignmentService(assignments *store.BudgetAssignmentsRepo, budgets *store.BudgetsRepo, users *store.UsersRepo, topUps *store.BudgetTopUpsRepo, changed *BudgetsChangedSignal, logger *slog.Logger) *BudgetAssignmentService {
	if logger == nil {
		logger = slog.Default()
	}
	return &BudgetAssignmentService{
		assignments: assignments, budgets: budgets, users: users, topUps: topUps, changed: changed,
		logger: logger.With("logger_name", "budget_assignments"),
		now:    time.Now,
	}
}

type ListBudgetAssignmentsParams struct {
	PageNumber, PageSize int
	BudgetID             string
	SubjectType          string
	SubjectID            string
	State                string
	Search               string
	ExpandIdentity       bool
	SearchIdentity       bool
}

const auditActionSuperseded = "superseded"

func (s *BudgetAssignmentService) Create(ctx context.Context, req CreateBudgetAssignmentRequest, actor string) (*BudgetAssignmentResponse, error) {
	var v budgetValidation
	typeValid := v.assignmentSubjectType("subjectType", req.SubjectType)
	var ref string
	var identity *BudgetAssignmentIdentity
	switch {
	case req.Identity != nil && req.SubjectID != "":
		v.add("identity", "give subjectId or identity, not both")
	case req.Identity != nil:
		identity = v.identity("identity", req.Identity)
	case req.SubjectID == "":
		v.add("subjectId", "is required, unless identity is given")
	default:
		ref, _ = v.userRef("subjectId", req.SubjectID)
	}
	b, err := s.requireBudget(ctx, &v, req.BudgetID)
	if err != nil {
		return nil, err
	}
	if typeValid && b != nil {
		v.tierMatches("budgetId", b, req.SubjectType)
	}
	if verr := v.err(); verr != nil {
		return nil, verr
	}

	a := &store.BudgetAssignment{
		SubjectType: req.SubjectType, SubjectRef: ref, BudgetID: b.ID,
		CreatedBy: actor, UpdatedBy: actor,
	}
	if identity != nil {
		a.IdentityType, a.IdentityValue, a.IdentityIssuer = identity.Type, identity.Value, identity.Issuer
		if a.SubjectRef, err = s.resolveIdentity(ctx, identity); err != nil {
			return nil, err
		}
		if a.SubjectRef == "" {
			return s.createPending(ctx, a, actor)
		}
	}
	superseded, err := s.assignments.Create(ctx, a)
	if err != nil {
		return nil, s.createError(ctx, err, a, actor)
	}
	s.changed.send(ctx)
	a.State = store.BudgetAssignmentConcrete
	s.audit(auditActionCreated, a, actor)
	s.auditSuperseded(superseded, a.ID, actor)
	return s.Get(ctx, a.ID, false)
}

func (s *BudgetAssignmentService) resolveIdentity(ctx context.Context, identity *BudgetAssignmentIdentity) (string, error) {
	if identity.Type != store.BudgetIdentitySystem {
		return assignmentIdentity.resolveIdentity(ctx, s.users, identity.Type, identity.Value, identity.Issuer)
	}
	u, _, err := s.users.GetByIssuerSub(ctx, rbac.SystemUserIssuer, rbac.SystemUserPrincipal(identity.Value))
	if err != nil {
		return "", fmt.Errorf("look up service account for assignment: %w", err)
	}
	if u == nil {
		return "", &ValidationError{Message: msgValidationFailed, Details: map[string][]string{fieldIdentityValue: {"no service account with this name"}}}
	}
	return u.ID, nil
}

func (s *BudgetAssignmentService) createPending(ctx context.Context, a *store.BudgetAssignment, actor string) (*BudgetAssignmentResponse, error) {
	p := &store.BudgetPendingAssignment{
		SubjectType: a.SubjectType, IdentityType: a.IdentityType, IdentityValue: a.IdentityValue, Issuer: a.IdentityIssuer,
		BudgetID: a.BudgetID, CreatedBy: actor, UpdatedBy: actor,
	}
	switch err := s.assignments.CreatePending(ctx, p); {
	case errors.Is(err, store.ErrBudgetAssignmentExists):
		s.auditPending(auditActionRejected, p, actor, "reason", "identity_pending")
		return nil, newBudgetConflict(BudgetConflictIdentityPending, &ConflictError{Message: "This identity already has a pending budget assignment."})
	case errors.Is(err, store.ErrBudgetAssignmentNoBudget):
		return nil, noBudgetError()
	case err != nil:
		return nil, fmt.Errorf("create pending budget assignment: %w", err)
	}
	s.changed.send(ctx)
	s.auditPending(auditActionCreated, p, actor)
	return s.Get(ctx, p.ID, false)
}

func (s *BudgetAssignmentService) Get(ctx context.Context, id string, expandIdentity bool) (*BudgetAssignmentResponse, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	resp := toBudgetAssignmentResponse(a, expandIdentity)
	return &resp, nil
}

func (s *BudgetAssignmentService) List(ctx context.Context, p ListBudgetAssignmentsParams) (*PaginatedResponse[BudgetAssignmentResponse], error) {
	var v budgetValidation
	if p.SubjectType != "" {
		v.assignmentSubjectType("subjectType", p.SubjectType)
	}
	ref := ""
	if p.SubjectID != "" {
		ref, _ = v.userRef("subjectId", p.SubjectID)
	}
	if err := v.err(); err != nil {
		return nil, err
	}

	filter := store.BudgetAssignmentFilter{BudgetID: p.BudgetID, SubjectType: p.SubjectType, SubjectRef: ref, State: p.State, Search: p.Search, SearchIdentity: p.SearchIdentity}
	if ref == "" {
		filter.HiddenSubjectRefs = rbac.UserListingHiddenIDs()
	}
	total, err := s.assignments.Count(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list budget assignments: %w", err)
	}
	filter.Limit, filter.Offset = p.PageSize, (p.PageNumber-1)*p.PageSize
	as, err := s.assignments.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list budget assignments: %w", err)
	}
	data := make([]BudgetAssignmentResponse, len(as))
	for i, a := range as {
		data[i] = toBudgetAssignmentResponse(a, p.ExpandIdentity)
	}
	return &PaginatedResponse[BudgetAssignmentResponse]{
		Data: data,
		Meta: NewPaginationMeta(p.PageNumber, p.PageSize, int(total)),
	}, nil
}

func (s *BudgetAssignmentService) Update(ctx context.Context, id string, req UpdateBudgetAssignmentRequest, actor string) (*BudgetAssignmentResponse, error) {
	cur, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	var v budgetValidation
	var b *store.Budget
	switch {
	case req.BudgetID != "":
		if b, err = s.requireBudget(ctx, &v, req.BudgetID); err != nil {
			return nil, err
		}
		if b != nil {
			v.tierMatches("budgetId", b, cur.SubjectType)
		}
	case req.Identity == nil:
		v.add("budgetId", "is required, unless identity is given")
	}
	identity, err := s.identityToRecord(ctx, &v, cur, req.Identity)
	if err != nil {
		return nil, err
	}
	if verr := v.err(); verr != nil {
		return nil, verr
	}

	update := store.BudgetAssignmentUpdate{Identity: identity, Actor: actor, NowMs: time.Now().UnixMilli()}
	if b != nil {
		update.BudgetID = b.ID
	}
	res, err := s.assignments.Update(ctx, cur.ID, update)
	switch {
	case errors.Is(err, store.ErrBudgetAssignmentNotFound):
		return nil, &NotFoundError{Resource: resourceBudgetAssignment, ID: id}
	case errors.Is(err, store.ErrBudgetAssignmentNoBudget):
		return nil, noBudgetError()
	case errors.Is(err, store.ErrBudgetAssignmentPendingIdentity):
		return nil, pendingIdentityError()
	case err != nil:
		return nil, fmt.Errorf("update budget assignment: %w", err)
	}
	if res.Moved {
		s.changed.send(ctx)
	}
	if res.Moved || res.Rerecorded {
		updated := *cur
		updated.BudgetID = res.PreviousBudgetID
		var extra []any
		if res.Moved {
			updated.BudgetID = b.ID
			extra = append(extra, "previousBudgetId", res.PreviousBudgetID)
		}
		if res.Rerecorded {
			updated.IdentityType = identity.Type
			extra = append(extra, "previousIdentityType", res.PreviousIdentity.Type)
		}
		s.audit(auditActionUpdated, &updated, actor, extra...)
	}
	return s.Get(ctx, cur.ID, false)
}

func (s *BudgetAssignmentService) identityToRecord(ctx context.Context, v *budgetValidation, cur *store.BudgetAssignment, raw *BudgetAssignmentIdentity) (*store.BudgetIdentity, error) {
	if raw == nil {
		return nil, nil
	}
	if cur.State == store.BudgetAssignmentPending {
		v.add("identity", pendingIdentityMessage)
		return nil, nil
	}
	identity := v.identity("identity", raw)
	if identity == nil {
		return nil, nil
	}
	ref, err := s.resolveIdentity(ctx, identity)
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		for field, msgs := range verr.Details {
			for _, msg := range msgs {
				v.add(field, msg)
			}
		}
		if verr.Message != msgValidationFailed {
			v.add("identity", verr.Message)
		}
		return nil, nil
	case err != nil:
		return nil, err
	case ref == "":
		v.add(fieldIdentityValue, "no user has this identity, so it does not name this assignment's user")
		return nil, nil
	case !strings.EqualFold(ref, cur.SubjectRef):
		v.add(fieldIdentityValue, "names another user; to give that user this budget, delete this assignment and assign them")
		return nil, nil
	}
	return &store.BudgetIdentity{Type: identity.Type, Value: identity.Value, Issuer: identity.Issuer}, nil
}

const pendingIdentityMessage = "a pending assignment binds to the user its identity names, so the identity cannot change; delete it and assign the other identity"

func pendingIdentityError() error {
	return &ValidationError{Message: msgValidationFailed, Details: map[string][]string{"identity": {pendingIdentityMessage}}}
}

func (s *BudgetAssignmentService) Delete(ctx context.Context, id, actor string) error {
	cur, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	budgetID, err := s.assignments.Delete(ctx, cur.ID)
	switch {
	case errors.Is(err, store.ErrBudgetAssignmentNotFound):
		return &NotFoundError{Resource: resourceBudgetAssignment, ID: id}
	case err != nil:
		return fmt.Errorf("delete budget assignment: %w", err)
	}
	s.changed.send(ctx)
	deleted := *cur
	deleted.BudgetID = budgetID
	s.audit(auditActionDeleted, &deleted, actor)
	return nil
}

func (s *BudgetAssignmentService) UpdateAssignments(ctx context.Context, budgetID string, req UpdateBudgetAssignmentsRequest, actor string) (*UpdateBudgetAssignmentsResponse, error) {
	if n := len(req.Add) + len(req.Remove); n > maxBudgetAssignmentChanges {
		return nil, &ValidationError{Field: "add", Message: fmt.Sprintf(
			"A save can change at most %d assignments; this one has %d.", maxBudgetAssignmentChanges, n)}
	}
	b, err := s.budgets.GetByID(ctx, budgetID)
	if err != nil {
		return nil, fmt.Errorf("look up budget for assignments: %w", err)
	}
	if b == nil {
		return nil, &NotFoundError{Resource: resourceBudget, ID: budgetID}
	}

	var v budgetValidation
	if v.assignmentSubjectType("subjectType", req.SubjectType) && b.SubjectType != req.SubjectType {
		v.add("subjectType", fmt.Sprintf("the budget is a %s budget; a %s can only be assigned a %s budget",
			b.SubjectType, req.SubjectType, req.SubjectType))
	}
	add := v.changeRefs("add", req.Add, nil)
	remove := v.changeRefs("remove", req.Remove, add)
	if verr := v.err(); verr != nil {
		return nil, verr
	}

	changes, err := s.assignments.ApplyBudgetChanges(ctx, store.BudgetAssignmentChanges{
		BudgetID: b.ID, SubjectType: req.SubjectType,
		Add: add.refs, Remove: remove.refs,
		Actor: actor, NowMs: time.Now().UnixMilli(),
	})
	if err != nil {
		return nil, s.changesError(err, b, add)
	}
	if len(changes) > 0 {
		s.changed.send(ctx)
	}
	return s.reportChanges(changes, b, req.SubjectType, actor), nil
}

func (v *budgetValidation) changeRefs(list string, raw []string, other *refList) *refList {
	out := &refList{index: make(map[string]int, len(raw))}
	for i, r := range raw {
		field := fmt.Sprintf("%s[%d]", list, i)
		parsed, err := uuid.Parse(r)
		if err != nil {
			v.add(field, "must be a user id")
			continue
		}
		ref := parsed.String()
		if j, seen := out.index[ref]; seen {
			v.add(field, fmt.Sprintf("repeats %s[%d]", list, j))
			continue
		}
		if other != nil {
			if j, both := other.index[ref]; both {
				v.add(field, fmt.Sprintf("is also in add[%d]", j))
				continue
			}
		}
		out.index[ref] = i
		out.refs = append(out.refs, ref)
	}
	return out
}

type refList struct {
	refs  []string
	index map[string]int
}

func (s *BudgetAssignmentService) changesError(err error, b *store.Budget, add *refList) error {
	var missing *store.BudgetAssignmentMissingSubjectsError
	switch {
	case errors.As(err, &missing):
		var v budgetValidation
		for _, ref := range missing.Refs {
			v.add(fmt.Sprintf("add[%d]", add.index[ref]), "no user with this id")
		}
		return v.err()
	case errors.Is(err, store.ErrBudgetAssignmentNoBudget):
		return &NotFoundError{Resource: resourceBudget, ID: b.ID}
	case errors.Is(err, store.ErrBudgetAssignmentConflict):
		return newBudgetConflict(BudgetConflictAssignmentsRaced, &ConflictError{
			Message:    "These assignments changed while the save was in progress. Reload them and save again.",
			LogMessage: "budget assignment save lost a race",
		})
	default:
		return fmt.Errorf("update budget assignments: %w", err)
	}
}

func (s *BudgetAssignmentService) reportChanges(changes []store.BudgetAssignmentChange, b *store.Budget, subjectType, actor string) *UpdateBudgetAssignmentsResponse {
	resp := &UpdateBudgetAssignmentsResponse{
		Added:     []BudgetAssignmentChangeEntry{},
		Moved:     []BudgetAssignmentMovedEntry{},
		Removed:   []BudgetAssignmentChangeEntry{},
		Unchanged: []BudgetAssignmentUnchangedEntry{},
	}
	changeSetID := idgen.New()
	for _, c := range changes {
		subject := BudgetAssignmentSubjectRef{ID: c.SubjectRef}
		a := &store.BudgetAssignment{ID: c.AssignmentID, SubjectType: subjectType, SubjectRef: c.SubjectRef, BudgetID: b.ID}
		s.auditSuperseded(c.Superseded, c.AssignmentID, actor)
		switch c.Outcome {
		case store.BudgetAssignmentAdded:
			s.audit(auditActionCreated, a, actor, "changeSetId", changeSetID)
			resp.Added = append(resp.Added, BudgetAssignmentChangeEntry{AssignmentID: c.AssignmentID, Subject: subject})
		case store.BudgetAssignmentMoved:
			s.audit(auditActionUpdated, a, actor, "previousBudgetId", c.FromBudgetID, "changeSetId", changeSetID)
			resp.Moved = append(resp.Moved, BudgetAssignmentMovedEntry{
				AssignmentID: c.AssignmentID, Subject: subject,
				FromBudget: BudgetAssignmentBudget{ID: c.FromBudgetID, Name: c.FromBudgetName},
			})
		case store.BudgetAssignmentRemoved:
			s.audit(auditActionDeleted, a, actor, "changeSetId", changeSetID)
			resp.Removed = append(resp.Removed, BudgetAssignmentChangeEntry{AssignmentID: c.AssignmentID, Subject: subject})
		case store.BudgetAssignmentUnchanged:
			var id *string
			if c.AssignmentID != "" {
				id = new(c.AssignmentID)
			}
			resp.Unchanged = append(resp.Unchanged, BudgetAssignmentUnchangedEntry{AssignmentID: id, Subject: subject, Reason: c.Reason})
		}
	}
	return resp
}

func (s *BudgetAssignmentService) load(ctx context.Context, id string) (*store.BudgetAssignment, error) {
	a, err := s.assignments.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get budget assignment: %w", err)
	}
	if a == nil {
		return nil, &NotFoundError{Resource: resourceBudgetAssignment, ID: id}
	}
	return a, nil
}

func (s *BudgetAssignmentService) requireBudget(ctx context.Context, v *budgetValidation, id string) (*store.Budget, error) {
	if id == "" {
		v.add("budgetId", msgIsRequired)
		return nil, nil
	}
	b, err := s.budgets.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("look up budget for assignment: %w", err)
	}
	if b == nil {
		v.add("budgetId", "no budget with this id")
	}
	return b, nil
}

func (s *BudgetAssignmentService) createError(ctx context.Context, err error, a *store.BudgetAssignment, actor string) error {
	switch {
	case errors.Is(err, store.ErrBudgetAssignmentExists):
		existing, lookupErr := s.assignments.List(ctx, store.BudgetAssignmentFilter{
			SubjectType: a.SubjectType, SubjectRef: a.SubjectRef, Limit: 1,
		})
		existingID := ""
		if lookupErr != nil {
			s.logger.WarnContext(ctx, "budget assignment conflict lookup failed; omitting the existing id", "error", lookupErr)
		} else if len(existing) > 0 {
			existingID = existing[0].ID
		}
		rejected := *a
		rejected.ID = existingID
		s.audit(auditActionRejected, &rejected, actor, "reason", "subject_assigned")
		return duplicateAssignmentError()
	case errors.Is(err, store.ErrBudgetAssignmentNoBudget):
		return noBudgetError()
	case errors.Is(err, store.ErrBudgetAssignmentConflict):
		return newBudgetConflict(BudgetConflictAssignmentRaced, &ConflictError{Message: "This user's budget assignment changed while saving. Reload and try again.", LogMessage: "budget assignment create lost a race"})
	case errors.Is(err, store.ErrBudgetAssignmentNoSubject):
		if a.IdentityType != "" {
			return &ValidationError{Message: msgValidationFailed, Details: map[string][]string{fieldIdentityValue: {"no user with this identity"}}}
		}
		return &ValidationError{Message: msgValidationFailed, Details: map[string][]string{"subjectId": {"no user with this id"}}}
	default:
		return fmt.Errorf("create budget assignment: %w", err)
	}
}

func duplicateAssignmentError() error {
	return newBudgetConflict(BudgetConflictSubjectAssigned, &ConflictError{Message: "This user already has a budget assignment."})
}

func noBudgetError() error {
	return &ValidationError{Message: msgValidationFailed, Details: map[string][]string{"budgetId": {"no budget with this id"}}}
}

func (s *BudgetAssignmentService) audit(action string, a *store.BudgetAssignment, actor string, extra ...any) {
	logBudgetAssignmentAudit(s.logger, action, a, actor, extra...)
}

func logBudgetAssignmentAudit(logger *slog.Logger, action string, a *store.BudgetAssignment, actor string, extra ...any) {
	args := []any{
		"event", "budget_mutation",
		"action", action,
		"resource", resourceBudgetAssignment,
		"resourceId", a.ID,
		"actor", actor,
		"state", a.State,
		"subjectType", a.SubjectType,
		"subjectId", a.SubjectRef,
		"identityType", a.IdentityType,
		"budgetId", a.BudgetID,
	}
	logger.Info("budget assignment audit", append(args, extra...)...)
}

func (s *BudgetAssignmentService) auditPending(action string, p *store.BudgetPendingAssignment, actor string, extra ...any) {
	s.audit(action, pendingAsAssignment(p), actor, extra...)
}

func (s *BudgetAssignmentService) auditSuperseded(superseded []store.BudgetPendingAssignment, assignmentID, actor string) {
	for i := range superseded {
		s.auditPending(auditActionSuperseded, &superseded[i], actor, "assignmentId", assignmentID)
	}
}

func pendingAsAssignment(p *store.BudgetPendingAssignment) *store.BudgetAssignment {
	return &store.BudgetAssignment{
		ID: p.ID, State: store.BudgetAssignmentPending, SubjectType: p.SubjectType,
		IdentityType: p.IdentityType, BudgetID: p.BudgetID,
	}
}

func (v *budgetValidation) assignmentSubjectType(field, t string) bool {
	switch t {
	case store.BudgetSubjectUser:
		return true
	case "":
		v.add(field, msgIsRequired)
	case store.BudgetSubjectMesh:
		v.add(field, "the mesh budget applies to the whole deployment and has no assignments; must be: user")
	case "agent":
		v.add(field, "agent assignments are not supported yet; must be: user")
	default:
		v.add(field, "must be: user")
	}
	return false
}

func (v *budgetValidation) identity(field string, raw *BudgetAssignmentIdentity) *BudgetAssignmentIdentity {
	id := &BudgetAssignmentIdentity{
		Type:   raw.Type,
		Value:  NormalizeGrantSubjectValue(raw.Type, raw.Value),
		Issuer: strings.TrimSpace(raw.Issuer),
	}
	ok := true
	fail := func(sub, msg string) {
		v.add(field+"."+sub, msg)
		ok = false
	}
	switch id.Type {
	case store.BudgetIdentityEmail, store.BudgetIdentitySub, store.BudgetIdentitySystem:
	case "":
		fail("type", msgIsRequired)
	default:
		fail("type", "must be one of: email, sub, system")
	}
	if id.Type == store.BudgetIdentitySystem {
		id.Value = rbac.SystemUserName(id.Value)
	}
	if id.Value == "" {
		fail("value", msgIsRequired)
	}
	if id.Type == store.BudgetIdentitySub && id.Issuer == "" {
		fail("issuer", "is required when type is sub")
	}
	if id.Type != store.BudgetIdentitySub && id.Issuer != "" {
		fail("issuer", "is only for type sub")
	}
	if (id.Type == store.BudgetIdentityEmail || id.Type == store.BudgetIdentitySub) && rbac.IsSystemPrincipal(id.Value) {
		fail("type", "a service account is named with type system")
	}
	if !ok {
		return nil
	}
	return id
}

func (v *budgetValidation) userRef(field, raw string) (string, bool) {
	if raw == "" {
		v.add(field, msgIsRequired)
		return "", false
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		v.add(field, "must be a user id")
		return "", false
	}
	return parsed.String(), true
}

func (v *budgetValidation) tierMatches(field string, b *store.Budget, subjectType string) {
	if b.SubjectType != subjectType {
		v.add(field, fmt.Sprintf("is a %s budget; a %s can only be assigned a %s budget", b.SubjectType, subjectType, subjectType))
	}
}
