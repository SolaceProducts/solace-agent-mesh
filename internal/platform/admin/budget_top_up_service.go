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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const resourceBudgetTopUp = "budget_top_up"

var maxBudgetTopUpAmount = mustParseAmount("100000.00")

func mustParseAmount(text string) int64 {
	amount, err := budget.ParseAmount(text)
	if err != nil {
		panic(err)
	}
	return amount
}

type BudgetTopUpService struct {
	topUps  *store.BudgetTopUpsRepo
	changed *BudgetsChangedSignal
	logger  *slog.Logger
	now     func() time.Time
}

func NewBudgetTopUpService(topUps *store.BudgetTopUpsRepo, changed *BudgetsChangedSignal, logger *slog.Logger) *BudgetTopUpService {
	if logger == nil {
		logger = slog.Default()
	}
	return &BudgetTopUpService{
		topUps:  topUps,
		changed: changed,
		logger:  logger.With("logger_name", "budget_top_ups"),
		now:     time.Now,
	}
}

func (s *BudgetTopUpService) Grant(ctx context.Context, req CreateBudgetTopUpRequest, actor string) (resp *BudgetTopUpResponse, created bool, err error) {
	var v budgetValidation
	ref := v.topUpSubject(req)
	amount := v.topUpAmount(req.Amount)
	if verr := v.err(); verr != nil {
		return nil, false, verr
	}

	now := s.now()
	granted, err := s.topUps.Grant(ctx, store.BudgetTopUpGrant{
		SubjectType: req.SubjectType, SubjectRef: ref, Amount: amount,
		ExpiresTime: budget.PeriodEnd(now).UnixMilli(), Actor: actor, NowMs: now.UnixMilli(),
	})
	if err != nil {
		return nil, false, s.grantError(err, req.SubjectType, ref, amount, actor)
	}
	s.changed.send(ctx)
	if granted.Created {
		s.audit(auditActionCreated, granted.TopUp, actor)
	} else {
		s.audit(auditActionUpdated, granted.TopUp, actor, "previousAmount", budget.FormatAmount(*granted.PreviousAmount))
	}
	out := toBudgetTopUpResponse(granted.TopUp)
	return &out, granted.Created, nil
}

func (s *BudgetTopUpService) grantError(err error, subjectType, ref string, amount int64, actor string) error {
	if unlimited, ok := errors.AsType[*store.BudgetTopUpUnlimitedError](err); ok {
		s.audit(auditActionRejected, &store.BudgetTopUp{SubjectType: subjectType, SubjectRef: ref, Amount: amount}, actor,
			"reason", "budget_unlimited", "budgetId", unlimited.BudgetID)
		return newBudgetConflict(BudgetConflictTopUpUnlimited, &ConflictError{
			Message:    unlimitedTopUpMessage(subjectType, unlimited.BudgetName),
			LogMessage: "budget top-up refused: the subject's budget is unlimited",
		})
	}
	switch {
	case errors.Is(err, store.ErrBudgetTopUpNoSubject):
		return &NotFoundError{Resource: resourceBudgetSubject, ID: ref}
	case errors.Is(err, store.ErrBudgetTopUpPeriodChanged):
		s.audit(auditActionRejected, &store.BudgetTopUp{SubjectType: subjectType, SubjectRef: ref, Amount: amount}, actor,
			"reason", "period_changed")
		return newBudgetConflict(BudgetConflictTopUpPeriodChanged, &ConflictError{
			Message:    "A new budget period started while this was being saved, and an additional amount has already been granted for it. Saving again replaces that amount for the new period.",
			LogMessage: "budget top-up refused: a grant for the next period was already made",
		})
	case errors.Is(err, store.ErrBudgetTopUpNoBudget):
		return newBudgetConflict(BudgetConflictTopUpNoBudget, &ConflictError{
			Message:    "No budget applies to this subject, so there is no limit to add to. Set a default budget first.",
			LogMessage: "budget top-up refused: no budget applies to the subject",
		})
	default:
		return fmt.Errorf("grant budget top-up: %w", err)
	}
}

func unlimitedTopUpMessage(subjectType, budgetName string) string {
	whose := "This user's budget"
	if subjectType == store.BudgetSubjectMesh {
		whose = "The mesh budget"
	}
	return fmt.Sprintf("%s, %s, is unlimited, so a top-up would have no effect. Set an amount on it first.", whose, budgetName)
}

func (s *BudgetTopUpService) Active(ctx context.Context, subjectType, subjectRef string) (*BudgetTopUpResponse, error) {
	t, err := s.topUps.Active(ctx, subjectType, subjectRef, s.now().UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("get active budget top-up: %w", err)
	}
	if t == nil {
		return nil, nil
	}
	resp := toBudgetTopUpResponse(t)
	return &resp, nil
}

func (s *BudgetTopUpService) CancelForSubject(ctx context.Context, subjectType, subjectRef, actor string) error {
	active, err := s.Active(ctx, subjectType, subjectRef)
	if err != nil || active == nil {
		return err
	}
	deleted, err := s.topUps.Delete(ctx, active.ID, s.now().UnixMilli())
	switch {
	case errors.Is(err, store.ErrBudgetTopUpNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("delete budget top-up: %w", err)
	}
	s.changed.send(ctx)
	s.audit(auditActionDeleted, deleted, actor)
	return nil
}

func (s *BudgetTopUpService) audit(action string, t *store.BudgetTopUp, actor string, extra ...any) {
	args := []any{
		"event", "budget_mutation",
		"action", action,
		"resource", resourceBudgetTopUp,
		"resourceId", t.ID,
		"actor", actor,
		"subjectType", t.SubjectType,
		"subjectId", t.SubjectRef,
		"amount", budget.FormatAmount(t.Amount),
	}
	if t.ExpiresTime != 0 {
		args = append(args, "expiresTime", t.ExpiresTime)
	}
	s.logger.Info("budget top-up audit", append(args, extra...)...)
}

func (v *budgetValidation) topUpSubject(req CreateBudgetTopUpRequest) string {
	if !v.topUpSubjectType(req.SubjectType) {
		return ""
	}
	if req.SubjectType == store.BudgetSubjectMesh {
		return store.BudgetMeshSubjectRef
	}
	ref, _ := v.userRef("subjectId", req.SubjectID)
	return ref
}

func (v *budgetValidation) topUpSubjectType(t string) bool {
	switch t {
	case store.BudgetSubjectUser, store.BudgetSubjectMesh:
		return true
	case "":
		v.add("subjectType", msgIsRequired)
	case "agent":
		v.add("subjectType", "agent top-ups are not supported yet; must be one of: mesh, user")
	default:
		v.add("subjectType", "must be one of: mesh, user")
	}
	return false
}

func (v *budgetValidation) topUpAmount(text string) int64 {
	if text == "" {
		v.add("amount", msgIsRequired)
		return 0
	}
	amount, ok := v.amount(&text)
	switch {
	case !ok:
		return 0
	case *amount == 0:
		v.add("amount", "must be greater than 0; to remove a top-up, cancel it")
	case *amount > maxBudgetTopUpAmount:
		v.add("amount", "must be at most "+budget.FormatAmount(maxBudgetTopUpAmount))
	}
	return *amount
}
