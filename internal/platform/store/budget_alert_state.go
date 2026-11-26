// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type BudgetAlertState struct {
	SubjectType  string `db:"subject_type"`
	SubjectRef   string `db:"subject_ref"`
	PeriodStart  int64  `db:"period_start"`
	AlertedLevel string `db:"alerted_level"`
	CapKey       string `db:"cap_key"`
	UpdatedTime  int64  `db:"updated_time"`
}

type BudgetAlertStatesRepo struct {
	db *DB
}

func NewBudgetAlertStatesRepo(db *DB) *BudgetAlertStatesRepo {
	return &BudgetAlertStatesRepo{db: db}
}

func (r *BudgetAlertStatesRepo) AlertStates(ctx context.Context, periodStart int64) ([]BudgetAlertState, error) {
	states, err := dbutil.QueryRows[BudgetAlertState](ctx, r.db,
		`SELECT subject_type, subject_ref, period_start, alerted_level, cap_key, updated_time
		FROM budget_alert_state WHERE period_start = ?`, periodStart)
	if err != nil {
		return nil, fmt.Errorf("BudgetAlertStatesRepo.AlertStates: %w", err)
	}
	return states, nil
}

func (r *BudgetAlertStatesRepo) SetAlertState(ctx context.Context, s BudgetAlertState) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO budget_alert_state (subject_type, subject_ref, period_start, alerted_level, cap_key, updated_time)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (subject_type, subject_ref, period_start)
		DO UPDATE SET alerted_level = excluded.alerted_level, cap_key = excluded.cap_key, updated_time = excluded.updated_time`,
		s.SubjectType, s.SubjectRef, s.PeriodStart, s.AlertedLevel, s.CapKey, s.UpdatedTime); err != nil {
		return fmt.Errorf("BudgetAlertStatesRepo.SetAlertState: %w", err)
	}
	return nil
}

func (r *BudgetAlertStatesRepo) DeleteAlertStatesBefore(ctx context.Context, periodStart int64) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM budget_alert_state WHERE period_start < ?`, periodStart)
	if err != nil {
		return 0, fmt.Errorf("BudgetAlertStatesRepo.DeleteAlertStatesBefore: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("BudgetAlertStatesRepo.DeleteAlertStatesBefore: %w", err)
	}
	return n, nil
}
