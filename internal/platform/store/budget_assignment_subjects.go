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

const userBudgetSubjectSelect = `SELECT u.id,
	NULLIF(u.display_name, '') AS display_name,
	NULLIF(u.email, '') AS email,
	CASE WHEN u.provisioned_via = '` + provisionedViaSystem + `' THEN 1 ELSE 0 END AS is_system_user,
	a.id AS assignment_id, b.id AS budget_id, b.name AS budget_name, b.amount AS budget_amount,
	t.id AS top_up_id, t.amount AS top_up_amount, t.expires_time AS top_up_expires_time
	FROM users u
	LEFT JOIN budget_assignments a
	       ON a.subject_type = '` + BudgetSubjectUser + `' AND a.subject_ref = CAST(u.id AS TEXT)
	LEFT JOIN budgets b ON b.id = a.budget_id
	LEFT JOIN budget_top_ups t ON t.id = (
	       SELECT active.id FROM budget_top_ups active
	        WHERE active.subject_type = '` + BudgetSubjectUser + `' AND active.subject_ref = CAST(u.id AS TEXT)
	          AND active.expires_time > ?
	        ORDER BY active.expires_time DESC LIMIT 1)`

const userBudgetSubjectOrder = `LOWER(COALESCE(u.display_name, u.email, ''))`

func (r *BudgetAssignmentsRepo) ListUserSubjects(ctx context.Context, f UserBudgetSubjectFilter) ([]*UserBudgetSubject, error) {
	wrap := func(err error) error { return fmt.Errorf("BudgetAssignmentsRepo.ListUserSubjects: %w", err) }
	if f.NowMs <= 0 {
		return nil, wrap(errNoNow)
	}
	where, filterArgs, err := buildUserBudgetSubjectFilter(f)
	if err != nil {
		return nil, wrap(err)
	}
	args := append([]any{f.NowMs}, filterArgs...)
	direction := "ASC"
	if f.Descending {
		direction = "DESC"
	}
	query := userBudgetSubjectSelect + where +
		` ORDER BY ` + userBudgetSubjectOrder + ` ` + direction + `, u.id ` + direction
	if f.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	}
	subjects, err := dbutil.QueryRowPtrs[UserBudgetSubject](ctx, r.db, query, args...)
	if err != nil {
		return nil, wrap(err)
	}
	return subjects, nil
}

func (r *BudgetAssignmentsRepo) CountUserSubjects(ctx context.Context, f UserBudgetSubjectFilter) (int64, error) {
	where, args, err := buildUserBudgetSubjectFilter(f)
	if err != nil {
		return 0, fmt.Errorf("BudgetAssignmentsRepo.CountUserSubjects: %w", err)
	}
	n, err := dbutil.QueryVal[int64](ctx, r.db, `SELECT COUNT(*) FROM users u`+where, args...)
	if err != nil {
		return 0, fmt.Errorf("BudgetAssignmentsRepo.CountUserSubjects: %w", err)
	}
	return n, nil
}

func buildUserBudgetSubjectFilter(f UserBudgetSubjectFilter) (string, []any, error) {
	var clauses []string
	var args []any
	if s := strings.TrimSpace(f.Search); s != "" {
		namePred, like := dbutil.LikeContainsFold("u.display_name", s)
		emailPred, _ := dbutil.LikeContainsFold("u.email", s)
		clauses = append(clauses, "("+namePred+" OR "+emailPred+")")
		args = append(args, like, like)
	}
	if len(f.IDs) > 0 {
		var canonical []string
		for _, id := range f.IDs {
			if c, ok := canonicalBudgetID(id); ok {
				canonical = append(canonical, c)
			}
		}
		if len(canonical) == 0 {
			return " WHERE 1 = 0", nil, nil
		}
		in, ids, err := dbutil.InClause(canonical)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, `u.id IN (`+in+`)`)
		args = append(args, ids...)
	}
	if len(f.HiddenSystemSubs) > 0 {
		in, subs, err := dbutil.InClause(f.HiddenSystemSubs)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, `NOT (u.provisioned_via = '`+provisionedViaSystem+`' AND u.sub IN (`+in+`))`)
		args = append(args, subs...)
	}
	if len(clauses) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args, nil
}
