// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var (
	ErrBudgetTopUpNotFound      = errors.New("budget top-up: not found")
	ErrBudgetTopUpNoSubject     = errors.New("budget top-up: no user with that id")
	ErrBudgetTopUpUnlimited     = errors.New("budget top-up: the subject's budget is unlimited")
	ErrBudgetTopUpNoBudget      = errors.New("budget top-up: no budget applies to the subject")
	ErrBudgetTopUpPeriodChanged = errors.New("budget top-up: a grant for the next period has already been made")
)

var errNoNow = errors.New("the time to read top-ups at is required")

type BudgetTopUpUnlimitedError struct {
	BudgetID   string
	BudgetName string
}

func (e *BudgetTopUpUnlimitedError) Error() string {
	return fmt.Sprintf("budget top-up: budget %s is unlimited", e.BudgetID)
}

func (e *BudgetTopUpUnlimitedError) Unwrap() error { return ErrBudgetTopUpUnlimited }

const budgetTopUpSelect = `SELECT t.id, t.subject_type, t.subject_ref, t.amount, t.expires_time,
	NULLIF(u.display_name, '') AS subject_display_name,
	NULLIF(u.email, '') AS subject_email,
	CASE WHEN u.provisioned_via = '` + provisionedViaSystem + `' THEN 1 ELSE 0 END AS subject_is_system_user,
	t.created_by, t.updated_by, t.created_time, t.updated_time
	FROM budget_top_ups t
	LEFT JOIN users u ON t.subject_type = '` + BudgetSubjectUser + `' AND CAST(u.id AS TEXT) = t.subject_ref`

type BudgetTopUpsRepo struct {
	db *DB
}

func NewBudgetTopUpsRepo(db *DB) *BudgetTopUpsRepo {
	return &BudgetTopUpsRepo{db: db}
}

func (r *BudgetTopUpsRepo) Grant(ctx context.Context, g BudgetTopUpGrant) (*BudgetTopUpGranted, error) {
	wrap := func(err error) error { return fmt.Errorf("BudgetTopUpsRepo.Grant: %w", err) }
	var granted *BudgetTopUpGranted
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		granted = nil
		if g.SubjectType == BudgetSubjectUser {
			found, err := lockUsers(ctx, tx, []string{g.SubjectRef})
			if err != nil {
				return err
			}
			if len(found) == 0 {
				return ErrBudgetTopUpNoSubject
			}
		}
		if err := requireLimitedBudget(ctx, tx, g.SubjectType, g.SubjectRef); err != nil {
			return err
		}
		id, previous, err := writeTopUp(ctx, tx, g)
		if err != nil {
			return err
		}
		topUp, err := dbutil.QueryRow[BudgetTopUp](ctx, tx, budgetTopUpSelect+` WHERE t.id = ?`, id)
		if err != nil {
			return fmt.Errorf("read the granted top-up: %w", err)
		}
		granted = &BudgetTopUpGranted{TopUp: topUp, Created: previous == nil, PreviousAmount: previous}
		return nil
	})
	if err != nil {
		return nil, wrap(err)
	}
	return granted, nil
}

func writeTopUp(ctx context.Context, tx *dbutil.Tx, g BudgetTopUpGrant) (string, *int64, error) {
	cur, err := lockActiveTopUp(ctx, tx, g.SubjectType, g.SubjectRef, g.NowMs)
	if err != nil {
		return "", nil, err
	}
	if cur == nil {
		id := idgen.New()
		result, err := tx.ExecContext(ctx,
			`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time,
			                             created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (subject_type, subject_ref, expires_time) DO NOTHING`,
			id, g.SubjectType, g.SubjectRef, g.Amount, g.ExpiresTime, g.Actor, g.Actor, g.NowMs, g.NowMs)
		if err != nil {
			return "", nil, fmt.Errorf("insert top-up: %w", err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return "", nil, fmt.Errorf("count the inserted top-up: %w", err)
		}
		if inserted == 1 {
			return id, nil, nil
		}
		if cur, err = lockActiveTopUp(ctx, tx, g.SubjectType, g.SubjectRef, g.NowMs); err != nil {
			return "", nil, err
		}
		if cur == nil {
			return "", nil, errors.New("insert top-up: conflicted with a top-up no longer active")
		}
	}
	if supersededBy(cur, g) {
		return "", nil, ErrBudgetTopUpPeriodChanged
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE budget_top_ups SET amount = ?, expires_time = ?, updated_by = ?, updated_time = ? WHERE id = ?`,
		g.Amount, g.ExpiresTime, g.Actor, g.NowMs, cur.ID,
	); err != nil {
		return "", nil, fmt.Errorf("replace top-up: %w", err)
	}
	return cur.ID, &cur.Amount, nil
}

type lockedTopUp struct {
	ID          string `db:"id"`
	Amount      int64  `db:"amount"`
	ExpiresTime int64  `db:"expires_time"`
	UpdatedTime int64  `db:"updated_time"`
}

func supersededBy(cur *lockedTopUp, g BudgetTopUpGrant) bool {
	return cur.ExpiresTime > g.ExpiresTime && cur.UpdatedTime > g.NowMs
}

func lockActiveTopUp(ctx context.Context, tx *dbutil.Tx, subjectType, subjectRef string, nowMs int64) (*lockedTopUp, error) {
	query := `SELECT id, amount, expires_time, updated_time FROM budget_top_ups
		WHERE subject_type = ? AND subject_ref = ? AND expires_time > ?
		ORDER BY expires_time DESC LIMIT 1`
	if tx.Dialect() == "postgres" {
		query += ` FOR UPDATE`
	}
	rows, err := dbutil.QueryRowPtrs[lockedTopUp](ctx, tx, query, subjectType, subjectRef, nowMs)
	if err != nil {
		return nil, fmt.Errorf("lock the active top-up: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	rows[0].ID = strings.ToLower(rows[0].ID)
	return rows[0], nil
}

func requireLimitedBudget(ctx context.Context, tx *dbutil.Tx, subjectType, subjectRef string) error {
	var b struct {
		ID     string `db:"id"`
		Name   string `db:"name"`
		Amount *int64 `db:"amount"`
	}
	err := tx.QueryRowContext(ctx,
		`SELECT id, name, amount FROM budgets
		 WHERE id = COALESCE(
		     (SELECT budget_id FROM budget_assignments WHERE subject_type = ? AND subject_ref = ?),
		     (SELECT id FROM budgets WHERE subject_type = ? AND is_default = 1))`,
		subjectType, subjectRef, subjectType,
	).Scan(&b.ID, &b.Name, &b.Amount)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrBudgetTopUpNoBudget
	case err != nil:
		return fmt.Errorf("read the subject's budget: %w", err)
	case b.Amount == nil:
		return &BudgetTopUpUnlimitedError{BudgetID: strings.ToLower(b.ID), BudgetName: b.Name}
	}
	return nil
}

func (r *BudgetTopUpsRepo) Active(ctx context.Context, subjectType, subjectRef string, nowMs int64) (*BudgetTopUp, error) {
	if nowMs <= 0 {
		return nil, fmt.Errorf("BudgetTopUpsRepo.Active: %w", errNoNow)
	}
	topUps, err := dbutil.QueryRowPtrs[BudgetTopUp](ctx, r.db, budgetTopUpSelect+
		` WHERE t.subject_type = ? AND t.subject_ref = ? AND t.expires_time > ? ORDER BY t.expires_time DESC LIMIT 1`,
		subjectType, subjectRef, nowMs)
	if err != nil {
		return nil, fmt.Errorf("BudgetTopUpsRepo.Active: %w", err)
	}
	if len(topUps) == 0 {
		return nil, nil
	}
	return topUps[0], nil
}

func (r *BudgetTopUpsRepo) Delete(ctx context.Context, id string, nowMs int64) (*BudgetTopUp, error) {
	if nowMs <= 0 {
		return nil, fmt.Errorf("BudgetTopUpsRepo.Delete: %w", errNoNow)
	}
	canonical, ok := canonicalBudgetID(id)
	if !ok {
		return nil, ErrBudgetTopUpNotFound
	}
	var deleted *BudgetTopUp
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		lock := `SELECT id FROM budget_top_ups WHERE id = ? AND expires_time > ?`
		if tx.Dialect() == "postgres" {
			lock += ` FOR UPDATE`
		}
		ids, err := dbutil.QueryVals[string](ctx, tx, lock, canonical, nowMs)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return ErrBudgetTopUpNotFound
		}
		if deleted, err = dbutil.QueryRow[BudgetTopUp](ctx, tx, budgetTopUpSelect+` WHERE t.id = ?`, canonical); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM budget_top_ups WHERE id = ?`, canonical)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("BudgetTopUpsRepo.Delete(%s): %w", canonical, err)
	}
	return deleted, nil
}

func (r *BudgetTopUpsRepo) DeleteExpired(ctx context.Context, nowMs int64) (int64, error) {
	if nowMs <= 0 {
		return 0, fmt.Errorf("BudgetTopUpsRepo.DeleteExpired: %w", errNoNow)
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM budget_top_ups WHERE expires_time <= ?`, nowMs)
	if err != nil {
		return 0, fmt.Errorf("BudgetTopUpsRepo.DeleteExpired: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("BudgetTopUpsRepo.DeleteExpired: rows affected: %w", err)
	}
	return n, nil
}
