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
	"unicode"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const subjectTypeReadOnlyHint = "cannot be changed; a budget's tier is fixed when it is created"

const (
	resourceBudget = "budget"

	isDefaultNotUpdatable = "cannot be set here; use the makeDefault action to make a budget its tier's default"

	budgetNameMinChars = 3
	budgetNameMaxChars = 255

	auditActionDefaultSet = "default_set"
)

type BudgetService struct {
	repo    *store.BudgetsRepo
	changed *BudgetsChangedSignal
	logger  *slog.Logger
}

func NewBudgetService(repo *store.BudgetsRepo, changed *BudgetsChangedSignal, logger *slog.Logger) *BudgetService {
	if logger == nil {
		logger = slog.Default()
	}
	return &BudgetService{repo: repo, changed: changed, logger: logger.With("logger_name", "budgets")}
}

func (s *BudgetService) Create(ctx context.Context, req CreateBudgetRequest, actor string) (*BudgetResponse, error) {
	var v budgetValidation
	name := v.name(req.Name)
	v.subjectType("subjectType", req.SubjectType)
	var amount *int64
	amountValid := false
	if !req.AmountSet {
		v.add("amount", "is required; send null for an unlimited budget")
	} else {
		amount, amountValid = v.amount(req.Amount)
	}
	v.warningPercentage(req.WarningPercentage)
	if amountValid {
		v.warnRequiresAmount(amount, req.WarningPercentage)
	}
	if err := v.err(); err != nil {
		return nil, err
	}

	b := &store.Budget{
		Name: name, SubjectType: req.SubjectType, Amount: amount, WarnPercentage: req.WarningPercentage,
		CreatedBy: actor, UpdatedBy: actor,
	}
	if err := s.repo.Create(ctx, b); err != nil {
		return nil, budgetWriteError(err, b)
	}
	s.changed.send(ctx)
	s.audit(auditActionCreated, b, actor, budgetValues(b)...)
	resp := toBudgetResponse(b)
	return &resp, nil
}

func (s *BudgetService) Get(ctx context.Context, id string) (*BudgetResponse, error) {
	b, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	resp := toBudgetResponse(b)
	return &resp, nil
}

type BudgetListQuery struct {
	PageNumber, PageSize int
	SubjectType, Name    string
	SortBy, SortDir      string
}

func (s *BudgetService) List(ctx context.Context, q BudgetListQuery) (*PaginatedResponse[BudgetResponse], error) {
	var v budgetValidation
	if q.SubjectType != "" {
		v.subjectType("subjectType", q.SubjectType)
	}
	sortBy := store.BudgetSort(q.SortBy)
	switch sortBy {
	case "", store.BudgetSortName, store.BudgetSortAmount:
	default:
		v.add("sort", "must be one of: name, amount")
	}
	if err := v.err(); err != nil {
		return nil, err
	}
	filter := store.BudgetFilter{SubjectType: q.SubjectType, Name: q.Name, SortBy: sortBy, Descending: q.SortDir == "desc"}
	total, err := s.repo.Count(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list budgets: %w", err)
	}
	filter.Limit, filter.Offset = q.PageSize, (q.PageNumber-1)*q.PageSize
	budgets, err := s.repo.List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("list budgets: %w", err)
	}
	data := make([]BudgetResponse, len(budgets))
	for i, b := range budgets {
		data[i] = toBudgetResponse(b)
	}
	return &PaginatedResponse[BudgetResponse]{
		Data: data,
		Meta: NewPaginationMeta(q.PageNumber, q.PageSize, int(total)),
	}, nil
}

func (s *BudgetService) Update(ctx context.Context, id string, req UpdateBudgetRequest, actor string) (*BudgetResponse, error) {
	cur, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}

	var v budgetValidation
	if req.SubjectType != nil {
		v.add("subjectType", subjectTypeReadOnlyHint)
	}
	if req.IsDefault != nil {
		v.add("isDefault", isDefaultNotUpdatable)
	}
	name := cur.Name
	switch {
	case req.Name != nil:
		name = v.name(*req.Name)
	case req.NameSet:
		v.add("name", "cannot be null")
	}
	amount, amountValid := cur.Amount, true
	if req.AmountSet {
		amount, amountValid = v.amount(req.Amount)
	}
	warn := cur.WarnPercentage
	if req.WarningPercentageSet {
		warn = req.WarningPercentage
		v.warningPercentage(warn)
	}
	if amountValid {
		v.warnRequiresAmount(amount, warn)
	}
	if err := v.err(); err != nil {
		return nil, err
	}

	if name == cur.Name && equalPtr(amount, cur.Amount) && equalPtr(warn, cur.WarnPercentage) {
		resp := toBudgetResponse(cur)
		return &resp, nil
	}

	updated := *cur
	updated.Name = name
	updated.Amount, updated.WarnPercentage = amount, warn
	updated.UpdatedBy, updated.UpdatedTime = actor, time.Now().UnixMilli()
	if err := s.repo.Update(ctx, store.BudgetUpdate{
		ID: cur.ID, Name: name, Amount: amount, WarnPercentage: warn,
		UpdatedBy: updated.UpdatedBy, UpdatedTime: updated.UpdatedTime, ExpectedUpdatedTime: cur.UpdatedTime,
	}); err != nil {
		return nil, budgetWriteError(err, &updated)
	}
	s.changed.send(ctx)
	s.audit(auditActionUpdated, &updated, actor, budgetChanges(cur, &updated)...)
	resp := toBudgetResponse(&updated)
	return &resp, nil
}

func (s *BudgetService) Delete(ctx context.Context, id, actor string) error {
	b, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	deleted, err := s.repo.Delete(ctx, b.ID)
	switch {
	case err == nil:
		s.changed.send(ctx)
		s.audit(auditActionDeleted, b, actor, append(budgetValues(b),
			"assignmentsDeleted", len(deleted.Assignments), "pendingAssignmentsDeleted", len(deleted.Pending))...)
		for i := range deleted.Assignments {
			logBudgetAssignmentAudit(s.logger, auditActionDeleted, &deleted.Assignments[i], actor, "reason", "budget_deleted")
		}
		for i := range deleted.Pending {
			logBudgetAssignmentAudit(s.logger, auditActionDeleted, pendingAsAssignment(&deleted.Pending[i]), actor, "reason", "budget_deleted")
		}
		return nil
	case errors.Is(err, store.ErrBudgetIsDefault):
		s.audit(auditActionRejected, b, actor, "reason", "tier_default")
		reason := BudgetConflictTierDefault
		if b.SubjectType == store.BudgetSubjectMesh {
			reason = BudgetConflictMeshUndeletable
		}
		return newBudgetConflict(reason, &ConflictError{Message: deleteDefaultMessage(b), LogMessage: "budget is its tier's default"})
	case errors.Is(err, store.ErrBudgetAssignedWhileDeleting):
		reason := "assigned_while_deleting"
		if dbutil.IsDeadlock(err) {
			reason = "deadlocked_with_assignment"
		}
		s.audit(auditActionRejected, b, actor, "reason", reason)
		return newBudgetConflict(BudgetConflictAssignedWhileDeleting, &ConflictError{Message: "This budget was assigned while it was being deleted. Try again.", LogMessage: "budget delete lost a race with an assignment"})
	case errors.Is(err, store.ErrBudgetNotFound):
		return &NotFoundError{Resource: resourceBudget, ID: id}
	default:
		return fmt.Errorf("delete budget: %w", err)
	}
}

func (s *BudgetService) SetDefault(ctx context.Context, id, actor string) (*BudgetResponse, error) {
	b, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	previous, promoted, err := s.repo.SetDefault(ctx, b.ID, actor, now)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrBudgetDefaultConflict):
		return nil, newBudgetConflict(BudgetConflictDefaultChanged, &ConflictError{Message: fmt.Sprintf(
			"The default for the %s tier changed while this request was running. Retry the request.", b.SubjectType)})
	case errors.Is(err, store.ErrBudgetNotFound):
		return nil, &NotFoundError{Resource: resourceBudget, ID: id}
	default:
		return nil, fmt.Errorf("set default budget: %w", err)
	}

	if !promoted {
		current, loadErr := s.load(ctx, id)
		if loadErr != nil {
			return nil, loadErr
		}
		resp := toBudgetResponse(current)
		return &resp, nil
	}
	if previous == "" {
		previous = "none"
	}
	s.changed.send(ctx)
	b.IsDefault, b.UpdatedBy, b.UpdatedTime = true, actor, now
	s.audit(auditActionDefaultSet, b, actor, "previousDefault", previous)
	resp := toBudgetResponse(b)
	return &resp, nil
}

func (s *BudgetService) load(ctx context.Context, id string) (*store.Budget, error) {
	b, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get budget: %w", err)
	}
	if b == nil {
		return nil, &NotFoundError{Resource: resourceBudget, ID: id}
	}
	return b, nil
}

func budgetWriteError(err error, b *store.Budget) error {
	switch {
	case errors.Is(err, store.ErrBudgetNameTaken):
		return newBudgetConflict(BudgetConflictNameTaken, &ConflictError{Message: fmt.Sprintf("A budget named %q already exists.", b.Name), LogMessage: "budget name already exists"})
	case errors.Is(err, store.ErrBudgetMeshExists):
		return newBudgetConflict(BudgetConflictMeshExists, &ConflictError{Message: "A mesh budget already exists. Only one is allowed.", LogMessage: "mesh budget already exists"})
	case errors.Is(err, store.ErrBudgetStale):
		return newBudgetConflict(BudgetConflictStale, &ConflictError{
			Message:    "This budget changed while your edit was being saved. Try again.",
			LogMessage: "budget changed since it was read",
		})
	case errors.Is(err, store.ErrBudgetNotFound):
		return &NotFoundError{Resource: resourceBudget, ID: b.ID}
	default:
		return fmt.Errorf("write budget: %w", err)
	}
}

func deleteDefaultMessage(b *store.Budget) string {
	if b.SubjectType == store.BudgetSubjectMesh {
		return "The Agent Mesh budget can't be deleted. Edit its limit instead."
	}
	return fmt.Sprintf("Budget %q is the default for the %s tier. Set another budget as the default before deleting it.", b.Name, b.SubjectType)
}

func (s *BudgetService) audit(action string, b *store.Budget, actor string, extra ...any) {
	args := []any{
		"event", "budget_mutation",
		"action", action,
		"resource", resourceBudget,
		"resourceId", b.ID,
		"actor", actor,
		"subjectType", b.SubjectType,
	}
	s.logger.Info("budget audit", append(args, extra...)...)
}

func budgetValues(b *store.Budget) []any {
	return []any{"amount", auditAmount(b.Amount), "warningPercentage", auditWarn(b.WarnPercentage)}
}

func budgetChanges(before, after *store.Budget) []any {
	var changes []any
	if before.Name != after.Name {
		changes = append(changes, "nameChanged", true)
	}
	if !equalPtr(before.Amount, after.Amount) {
		changes = append(changes, slog.Group("amount", "before", auditAmount(before.Amount), "after", auditAmount(after.Amount)))
	}
	if !equalPtr(before.WarnPercentage, after.WarnPercentage) {
		changes = append(changes, slog.Group("warningPercentage",
			"before", auditWarn(before.WarnPercentage), "after", auditWarn(after.WarnPercentage)))
	}
	return changes
}

func auditAmount(v *int64) string {
	if v == nil {
		return "unlimited"
	}
	return budget.FormatAmount(*v)
}

func auditWarn(v *int) string {
	if v == nil {
		return "none"
	}
	return fmt.Sprintf("%d", *v)
}

func equalPtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

const (
	msgIsRequired       = "is required"
	msgValidationFailed = "validation failed"
)

type budgetValidation struct {
	details map[string][]string
}

func (v *budgetValidation) add(field, msg string) {
	if v.details == nil {
		v.details = map[string][]string{}
	}
	v.details[field] = append(v.details[field], msg)
}

func (v *budgetValidation) err() error {
	if len(v.details) == 0 {
		return nil
	}
	return &ValidationError{Message: msgValidationFailed, Details: v.details}
}

func (v *budgetValidation) name(raw string) string {
	name := strings.TrimSpace(raw)
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		v.add("name", msgIsRequired)
	case n < budgetNameMinChars:
		v.add("name", fmt.Sprintf("must be at least %d characters", budgetNameMinChars))
	case n > budgetNameMaxChars:
		v.add("name", fmt.Sprintf("must be at most %d characters", budgetNameMaxChars))
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		v.add("name", "must not contain control characters")
	}
	return name
}

func (v *budgetValidation) subjectType(field, t string) {
	switch t {
	case store.BudgetSubjectMesh, store.BudgetSubjectUser:
	case "":
		v.add(field, msgIsRequired)
	case "agent":
		v.add(field, "agent budgets are not supported yet; must be one of: mesh, user")
	default:
		v.add(field, "must be one of: mesh, user")
	}
}

func (v *budgetValidation) amount(text *string) (*int64, bool) {
	if text == nil {
		return nil, true
	}
	parsed, err := budget.ParseAmount(*text)
	switch {
	case err == nil:
		return &parsed, true
	case errors.Is(err, budget.ErrNegativeAmount):
		v.add("amount", "must not be negative")
	case errors.Is(err, budget.ErrAmountTooPrecise):
		v.add("amount", fmt.Sprintf("must have at most %d decimal places", budget.InputDecimalPlaces))
	case errors.Is(err, budget.ErrAmountTooLarge):
		v.add("amount", "is larger than a budget may be")
	default:
		v.add("amount", `must be a decimal number of dollars, such as "250" or "19.99"`)
	}
	return nil, false
}

func (v *budgetValidation) warningPercentage(p *int) {
	if p != nil && (*p < 1 || *p > 100) {
		v.add("warningPercentage", "must be between 1 and 100")
	}
}

func (v *budgetValidation) warnRequiresAmount(amount *int64, warn *int) {
	if amount == nil && warn != nil {
		v.add("amount", "cannot be unlimited while a warning threshold is set")
		v.add("warningPercentage", "requires an amount; clear the threshold or set an amount")
	}
}
