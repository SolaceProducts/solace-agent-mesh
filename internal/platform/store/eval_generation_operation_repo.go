// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	EvalGenerationDataset  = "generate_dataset"
	EvalGenerationExamples = "generate_examples"

	EvalGenerationInProgress = "inProgress"
	EvalGenerationSucceeded  = "succeeded"
	EvalGenerationFailed     = "failed"
)

var ErrEvalGenerationActive = errors.New("eval dataset has an example generation in progress")

type EvalGenerationOperation struct {
	ID            string  `db:"id"`
	OperationType string  `db:"operation_type"`
	DatasetID     *string `db:"dataset_id"`
	Status        string  `db:"status"`
	ErrorMessage  *string `db:"error_message"`
	ErrorID       *string `db:"error_id"`
	CreatedBy     string  `db:"created_by"`
	CreatedTime   int64   `db:"created_time"`
	CompletedTime *int64  `db:"completed_time"`
	Deadline      int64   `db:"deadline"`
	Request       *string `db:"request"`
	Takeovers     int     `db:"takeovers"`
}

const evalGenerationOperationSelect = `SELECT id, operation_type, dataset_id, status, error_message, error_id,
	created_by, created_time, completed_time, deadline, request, takeovers FROM eval_generation_operations`

type EvalGenerationOperationRepo struct {
	db *DB
}

func NewEvalGenerationOperationRepo(db *DB) *EvalGenerationOperationRepo {
	return &EvalGenerationOperationRepo{db: db}
}

func (r *EvalGenerationOperationRepo) Create(ctx context.Context, op *EvalGenerationOperation) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO eval_generation_operations
		 (id, operation_type, dataset_id, status, created_by, created_time, deadline, request)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		op.ID, op.OperationType, op.DatasetID, op.Status, op.CreatedBy, op.CreatedTime, op.Deadline, op.Request)
	if dbutil.IsUniqueViolation(err) {
		return ErrEvalGenerationActive
	}
	if err != nil {
		return fmt.Errorf("EvalGenerationOperationRepo.Create: %w", err)
	}
	return nil
}

func (r *EvalGenerationOperationRepo) Get(ctx context.Context, id string) (*EvalGenerationOperation, error) {
	row, err := dbutil.QueryRow[EvalGenerationOperation](ctx, r.db, evalGenerationOperationSelect+` WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("EvalGenerationOperationRepo.Get(%s): %w", id, err)
	}
	if row == nil {
		return nil, ErrEvalNotFound
	}
	return row, nil
}

func (r *EvalGenerationOperationRepo) ListByDataset(ctx context.Context, datasetID string, pageNumber, pageSize int, oldestFirst bool) ([]*EvalGenerationOperation, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM eval_generation_operations WHERE dataset_id = ?`, datasetID)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalGenerationOperationRepo.ListByDataset count: %w", err)
	}
	direction := "DESC"
	if oldestFirst {
		direction = "ASC"
	}
	rows, err := dbutil.QueryRowPtrs[EvalGenerationOperation](ctx, r.db,
		evalGenerationOperationSelect+` WHERE dataset_id = ? ORDER BY created_time `+direction+`, id `+direction+` LIMIT ? OFFSET ?`,
		datasetID, pageSize, (pageNumber-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalGenerationOperationRepo.ListByDataset: %w", err)
	}
	return rows, total, nil
}

func (r *EvalGenerationOperationRepo) Fail(ctx context.Context, id, message, errorID string, completedTime int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE eval_generation_operations SET status = ?, error_message = ?, error_id = ?, completed_time = ?
		 WHERE id = ? AND status = ?`,
		EvalGenerationFailed, message, errorID, completedTime, id, EvalGenerationInProgress)
	if err != nil {
		return fmt.Errorf("EvalGenerationOperationRepo.Fail(%s): %w", id, err)
	}
	return nil
}

func (r *EvalGenerationOperationRepo) FailExpired(ctx context.Context, now int64, message string) ([]string, error) {
	ids, err := dbutil.QueryVals[string](ctx, r.db,
		`UPDATE eval_generation_operations SET status = ?, error_message = ?, error_id = CAST(id AS TEXT), completed_time = deadline
		 WHERE status = ? AND deadline < ? RETURNING id`,
		EvalGenerationFailed, message, EvalGenerationInProgress, now)
	if err != nil {
		return nil, fmt.Errorf("EvalGenerationOperationRepo.FailExpired: %w", err)
	}
	return ids, nil
}

func (r *EvalGenerationOperationRepo) ListInProgressBefore(ctx context.Context, cutoff int64) ([]*EvalGenerationOperation, error) {
	rows, err := dbutil.QueryRowPtrs[EvalGenerationOperation](ctx, r.db,
		evalGenerationOperationSelect+` WHERE status = ? AND created_time < ?`, EvalGenerationInProgress, cutoff)
	if err != nil {
		return nil, fmt.Errorf("EvalGenerationOperationRepo.ListInProgressBefore: %w", err)
	}
	return rows, nil
}

func (r *EvalGenerationOperationRepo) TakeOver(ctx context.Context, id string, deadline int64) (int, bool, error) {
	counts, err := dbutil.QueryVals[int](ctx, r.db,
		`UPDATE eval_generation_operations SET takeovers = takeovers + 1, deadline = ?
		 WHERE id = ? AND status = ? RETURNING takeovers`, deadline, id, EvalGenerationInProgress)
	if err != nil {
		return 0, false, fmt.Errorf("EvalGenerationOperationRepo.TakeOver(%s): %w", id, err)
	}
	if len(counts) == 0 {
		return 0, false, nil
	}
	return counts[0], true, nil
}
