// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type BudgetStateRepo struct {
	db *DB
}

func NewBudgetStateRepo(db *DB) *BudgetStateRepo {
	return &BudgetStateRepo{db: db}
}

var _ budget.StateStore = (*BudgetStateRepo)(nil)

func (r *BudgetStateRepo) LoadBudgetState(ctx context.Context) (*budget.EnforcementState, error) {
	rows, err := dbutil.QueryVals[string](ctx, r.db, `SELECT state FROM gateway_budget_state WHERE id = 1`)
	if err != nil {
		return nil, fmt.Errorf("BudgetStateRepo.Load: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	state, err := budget.DecodeState([]byte(rows[0]))
	if err != nil {
		return nil, fmt.Errorf("BudgetStateRepo.Load: %w", err)
	}
	return &state, nil
}

func (r *BudgetStateRepo) SaveBudgetState(ctx context.Context, state budget.EnforcementState) error {
	encoded, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("BudgetStateRepo.Save: encode: %w", err)
	}
	if _, err := r.db.ExecContext(ctx,
		`INSERT INTO gateway_budget_state (id, state, updated_time) VALUES (1, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET state = excluded.state, updated_time = excluded.updated_time`,
		string(encoded), time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("BudgetStateRepo.Save: %w", err)
	}
	return nil
}
