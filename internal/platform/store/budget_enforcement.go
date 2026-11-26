// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type LimitingBudget struct {
	ID             string `db:"id"`
	Amount         *int64 `db:"amount"`
	WarnPercentage *int   `db:"warn_percentage"`
}

type UserBudgetLimits struct {
	Default       LimitingBudget
	Assigned      map[string]LimitingBudget
	TopUps        map[string]int64
	SystemUserIDs map[string]string
	Pending       map[string]LimitingBudget
}

func (r *BudgetsRepo) UserLimits(ctx context.Context, nowMs int64) (UserBudgetLimits, error) {
	if nowMs <= 0 {
		return UserBudgetLimits{}, fmt.Errorf("BudgetsRepo.UserLimits: %w", errNoNow)
	}
	var limits UserBudgetLimits
	defaults, err := dbutil.QueryRows[LimitingBudget](ctx, r.db,
		`SELECT id, amount, warn_percentage FROM budgets WHERE subject_type = ? AND is_default = 1`, BudgetSubjectUser)
	if err != nil {
		return limits, fmt.Errorf("BudgetsRepo.UserLimits: default: %w", err)
	}
	if len(defaults) > 0 {
		limits.Default = defaults[0]
	}

	type assignment struct {
		Ref            string `db:"subject_ref"`
		ID             string `db:"id"`
		Amount         *int64 `db:"amount"`
		WarnPercentage *int   `db:"warn_percentage"`
	}
	assignments, err := dbutil.QueryRows[assignment](ctx, r.db,
		`SELECT a.subject_ref, b.id, b.amount, b.warn_percentage FROM budget_assignments a
		JOIN budgets b ON b.id = a.budget_id
		WHERE a.subject_type = ?`, BudgetSubjectUser)
	if err != nil {
		return limits, fmt.Errorf("BudgetsRepo.UserLimits: assignments: %w", err)
	}
	limits.Assigned = make(map[string]LimitingBudget, len(assignments))
	for _, a := range assignments {
		limits.Assigned[strings.ToLower(a.Ref)] = LimitingBudget{ID: a.ID, Amount: a.Amount, WarnPercentage: a.WarnPercentage}
	}

	type topUp struct {
		Ref    string `db:"subject_ref"`
		Amount int64  `db:"amount"`
	}
	topUps, err := dbutil.QueryRows[topUp](ctx, r.db,
		`SELECT subject_ref, amount FROM budget_top_ups
		WHERE subject_type = ? AND expires_time > ? ORDER BY expires_time`, BudgetSubjectUser, nowMs)
	if err != nil {
		return limits, fmt.Errorf("BudgetsRepo.UserLimits: top-ups: %w", err)
	}
	limits.TopUps = make(map[string]int64, len(topUps))
	for _, t := range topUps {
		limits.TopUps[strings.ToLower(t.Ref)] = t.Amount
	}

	type systemUser struct {
		ID  string `db:"id"`
		Sub string `db:"sub"`
	}
	systemUsers, err := dbutil.QueryRows[systemUser](ctx, r.db,
		`SELECT id, sub FROM users WHERE provisioned_via = ?`, provisionedViaSystem)
	if err != nil {
		return limits, fmt.Errorf("BudgetsRepo.UserLimits: system users: %w", err)
	}
	limits.SystemUserIDs = make(map[string]string, len(systemUsers))
	for _, u := range systemUsers {
		limits.SystemUserIDs[u.Sub] = strings.ToLower(u.ID)
	}

	type pending struct {
		IdentityType   string `db:"identity_type"`
		IdentityValue  string `db:"identity_value"`
		ID             string `db:"id"`
		Amount         *int64 `db:"amount"`
		WarnPercentage *int   `db:"warn_percentage"`
	}
	pendings, err := dbutil.QueryRows[pending](ctx, r.db,
		`SELECT p.identity_type, p.identity_value, b.id, b.amount, b.warn_percentage FROM budget_pending_assignments p
		JOIN budgets b ON b.id = p.budget_id
		WHERE p.subject_type = ? AND p.identity_type IN (?, ?)
		ORDER BY p.identity_type = ?`, BudgetSubjectUser, BudgetIdentityEmail, BudgetIdentitySub, BudgetIdentitySub)
	if err != nil {
		return limits, fmt.Errorf("BudgetsRepo.UserLimits: pending assignments: %w", err)
	}
	limits.Pending = make(map[string]LimitingBudget, len(pendings))
	for _, p := range pendings {
		limits.Pending[p.IdentityValue] = LimitingBudget{ID: p.ID, Amount: p.Amount, WarnPercentage: p.WarnPercentage}
	}
	return limits, nil
}
