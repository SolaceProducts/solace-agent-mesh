// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var ErrEvalGenerationNotInProgress = errors.New("eval generation operation is no longer in progress")

func (r *EvalExampleRepo) SaveGeneratedDataset(ctx context.Context, operationID string, d *EvalDataset, examples []*EvalExample) error {
	if d.ID == "" {
		d.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if d.CreatedTime == 0 {
		d.CreatedTime = now
	}
	if d.UpdatedTime == 0 {
		d.UpdatedTime = now
	}
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		taken, err := dbutil.QueryVal[int](ctx, tx,
			`SELECT COUNT(*) FROM eval_datasets WHERE LOWER(name) = LOWER(?)`, d.Name)
		if err != nil {
			return fmt.Errorf("SaveGeneratedDataset check name: %w", err)
		}
		if taken > 0 {
			return fmt.Errorf("%w: %s", ErrEvalNameTaken, d.Name)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO eval_datasets (id, name, description, is_default, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			d.ID, d.Name, d.Description, dbutil.BoolToInt(d.IsDefault), d.CreatedBy, d.UpdatedBy, d.CreatedTime, d.UpdatedTime); err != nil {
			return fmt.Errorf("SaveGeneratedDataset insert dataset: %w", err)
		}
		if err := insertGeneratedExamples(ctx, tx, d.ID, 1, examples, now); err != nil {
			return err
		}
		return succeedGeneration(ctx, tx, operationID, d.ID, now)
	})
}

func (r *EvalExampleRepo) SaveGeneratedExamples(ctx context.Context, operationID, datasetID string, examples []*EvalExample) error {
	now := time.Now().UnixMilli()
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var highest *int
		if err := tx.QueryRowContext(ctx,
			`SELECT MAX(sequence_number) FROM eval_examples WHERE dataset_id = ?`, datasetID).Scan(&highest); err != nil {
			return fmt.Errorf("SaveGeneratedExamples next sequence: %w", err)
		}
		next := 1
		if highest != nil {
			next = *highest + 1
		}
		if err := insertGeneratedExamples(ctx, tx, datasetID, next, examples, now); err != nil {
			return err
		}
		if len(examples) > 0 {
			if err := touchDataset(ctx, tx, datasetID); err != nil {
				return err
			}
		}
		return succeedGeneration(ctx, tx, operationID, datasetID, now)
	})
}

func insertGeneratedExamples(ctx context.Context, tx *dbutil.Tx, datasetID string, firstSequence int, examples []*EvalExample, now int64) error {
	for i, e := range examples {
		if e.ID == "" {
			e.ID = idgen.New()
		}
		e.DatasetID = datasetID
		e.SequenceNumber = firstSequence + i
		if e.CreatedTime == 0 {
			e.CreatedTime = now
		}
		if e.UpdatedTime == 0 {
			e.UpdatedTime = now
		}
		if _, err := tx.ExecContext(ctx, evalExampleInsert, evalExampleInsertArgs(e)...); err != nil {
			return fmt.Errorf("insert generated example: %w", err)
		}
	}
	return nil
}

func succeedGeneration(ctx context.Context, tx *dbutil.Tx, operationID, datasetID string, now int64) error {
	if operationID == "" {
		return nil
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE eval_generation_operations SET status = ?, dataset_id = ?, completed_time = ?
		 WHERE id = ? AND status = ?`,
		EvalGenerationSucceeded, datasetID, now, operationID, EvalGenerationInProgress)
	if err != nil {
		return fmt.Errorf("succeed generation %s: %w", operationID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("succeed generation %s rows affected: %w", operationID, err)
	}
	if n == 0 {
		return ErrEvalGenerationNotInProgress
	}
	return nil
}
