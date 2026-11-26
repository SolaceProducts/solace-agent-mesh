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
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const evalExampleColumns = `id, dataset_id, sequence_number, prompt, expected_response, input_data, expected_path, expected_facts, created_time, updated_time`

const evalExampleInsert = `INSERT INTO eval_examples (` + evalExampleColumns + `)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func evalExampleInsertArgs(e *EvalExample) []any {
	return []any{
		e.ID, e.DatasetID, e.SequenceNumber, e.Prompt, e.ExpectedResponse, e.InputData, e.ExpectedPath, e.ExpectedFacts,
		e.CreatedTime, e.UpdatedTime,
	}
}

type EvalExampleRepo struct {
	db *DB
}

func NewEvalExampleRepo(db *DB) *EvalExampleRepo {
	return &EvalExampleRepo{db: db}
}

func touchDataset(ctx context.Context, q dbutil.Querier, datasetID string) error {
	_, err := q.ExecContext(ctx,
		`UPDATE eval_datasets SET updated_time = ? WHERE id = ?`,
		time.Now().UnixMilli(), datasetID)
	if err != nil {
		return fmt.Errorf("touch dataset %s: %w", datasetID, err)
	}
	return nil
}

func (r *EvalExampleRepo) CountByDataset(ctx context.Context, datasetID string) (int, error) {
	return dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM eval_examples WHERE dataset_id = ?`, datasetID)
}

func (r *EvalExampleRepo) NextSequenceNumber(ctx context.Context, datasetID string) (int, error) {
	var max *int
	err := r.db.QueryRowContext(ctx,
		`SELECT MAX(sequence_number) FROM eval_examples WHERE dataset_id = ?`, datasetID).Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("EvalExampleRepo.NextSequenceNumber: %w", err)
	}
	if max == nil {
		return 1, nil
	}
	return *max + 1, nil
}

func (r *EvalExampleRepo) ListByDataset(ctx context.Context, datasetID string) ([]*EvalExample, error) {
	rows, err := dbutil.QueryRowPtrs[EvalExample](ctx, r.db,
		`SELECT `+evalExampleColumns+`
		 FROM eval_examples WHERE dataset_id = ? ORDER BY sequence_number ASC`, datasetID)
	if err != nil {
		return nil, err
	}
	if err := r.hydrateArtifacts(ctx, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *EvalExampleRepo) ListIDsByDataset(ctx context.Context, datasetID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT id FROM eval_examples WHERE dataset_id = ? ORDER BY sequence_number ASC`, datasetID)
}

type EvalExampleSortField string

const (
	EvalExampleSortSequenceNumber EvalExampleSortField = "sequence_number"
	EvalExampleSortCreatedTime    EvalExampleSortField = "created_time"
	EvalExampleSortUpdatedTime    EvalExampleSortField = "updated_time"
)

func (r *EvalExampleRepo) ListByDatasetPaginated(
	ctx context.Context,
	datasetID string,
	pageNumber, pageSize int,
	contentFilter string,
	sortBy EvalExampleSortField,
	desc bool,
) ([]*EvalExample, int, error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	args := []any{datasetID}
	whereExtra := ""
	if contentFilter != "" {
		promptPred, needle := dbutil.LikeContainsFold("prompt", contentFilter)
		responsePred, _ := dbutil.LikeContainsFold("expected_response", contentFilter)
		whereExtra = "AND (" + promptPred + " OR " + responsePred + ")"
		args = append(args, needle, needle)
	}

	total, err := dbutil.QueryVal[int](ctx, r.db,
		fmt.Sprintf(`SELECT COUNT(*) FROM eval_examples WHERE dataset_id = ? %s`, whereExtra), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalExampleRepo.ListByDatasetPaginated count: %w", err)
	}

	pageArgs := append([]any{}, args...)
	pageArgs = append(pageArgs, pageSize, (pageNumber-1)*pageSize)

	column := EvalExampleSortSequenceNumber
	if sortBy == EvalExampleSortCreatedTime || sortBy == EvalExampleSortUpdatedTime {
		column = sortBy
	}
	direction := "ASC"
	if desc {
		direction = "DESC"
	}
	rows, err := dbutil.QueryRowPtrs[EvalExample](ctx, r.db,
		fmt.Sprintf(`SELECT `+evalExampleColumns+`
		 FROM eval_examples WHERE dataset_id = ? %s
		 ORDER BY %s %s, id %s LIMIT ? OFFSET ?`, whereExtra, column, direction, direction), pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalExampleRepo.ListByDatasetPaginated query: %w", err)
	}
	if err := r.hydrateArtifacts(ctx, rows); err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

func (r *EvalExampleRepo) GetByID(ctx context.Context, id string) (*EvalExample, error) {
	row, err := dbutil.QueryRow[EvalExample](ctx, r.db,
		`SELECT `+evalExampleColumns+`
		 FROM eval_examples WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("EvalExampleRepo.GetByID(%s): %w", id, err)
	}
	if row == nil {
		return nil, ErrEvalNotFound
	}
	if err := r.hydrateArtifacts(ctx, []*EvalExample{row}); err != nil {
		return nil, err
	}
	return row, nil
}

func (r *EvalExampleRepo) hydrateArtifacts(ctx context.Context, examples []*EvalExample) error {
	if len(examples) == 0 {
		return nil
	}
	ids := make([]string, len(examples))
	for i, e := range examples {
		ids[i] = e.ID
	}
	byExample := make(map[string][]*EvalExampleArtifact)
	for chunk := range slices.Chunk(ids, dbutil.InClauseCap) {
		ph, args, err := dbutil.InClause(chunk)
		if err != nil {
			return fmt.Errorf("EvalExampleRepo.hydrateArtifacts: %w", err)
		}
		rows, err := dbutil.QueryRowPtrs[EvalExampleArtifact](ctx, r.db,
			fmt.Sprintf(`SELECT %s FROM eval_example_artifacts WHERE example_id IN (%s) ORDER BY example_id, role`,
				evalExampleArtifactColumns, ph), args...)
		if err != nil {
			return fmt.Errorf("EvalExampleRepo.hydrateArtifacts: %w", err)
		}
		for _, a := range rows {
			byExample[a.ExampleID] = append(byExample[a.ExampleID], a)
		}
	}
	for _, e := range examples {
		e.Artifacts = byExample[e.ID]
	}
	return nil
}

func (r *EvalExampleRepo) Create(ctx context.Context, e *EvalExample) error {
	if e.ID == "" {
		e.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if e.CreatedTime == 0 {
		e.CreatedTime = now
	}
	if e.UpdatedTime == 0 {
		e.UpdatedTime = now
	}
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		_, err := tx.ExecContext(ctx, evalExampleInsert, evalExampleInsertArgs(e)...)
		if err != nil {
			return fmt.Errorf("EvalExampleRepo.Create: %w", err)
		}
		return touchDataset(ctx, tx, e.DatasetID)
	})
}

func (r *EvalExampleRepo) BulkCreate(ctx context.Context, items []*EvalExample) error {
	if len(items) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		for _, e := range items {
			if e.ID == "" {
				e.ID = idgen.New()
			}
			if e.CreatedTime == 0 {
				e.CreatedTime = now
			}
			if e.UpdatedTime == 0 {
				e.UpdatedTime = now
			}
			_, err := tx.ExecContext(ctx, evalExampleInsert, evalExampleInsertArgs(e)...)
			if err != nil {
				return fmt.Errorf("insert example: %w", err)
			}
		}
		touched := make(map[string]struct{}, 1)
		for _, e := range items {
			if _, ok := touched[e.DatasetID]; ok {
				continue
			}
			touched[e.DatasetID] = struct{}{}
			if err := touchDataset(ctx, tx, e.DatasetID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *EvalExampleRepo) Update(ctx context.Context, id string, prompt, expectedResponse *string, clearExpected bool) (*EvalExample, error) {
	return r.UpdateWith(ctx, id, EvalExampleUpdate{Prompt: prompt, ExpectedResponse: expectedResponse, ClearExpected: clearExpected})
}

type EvalExampleUpdate struct {
	Prompt           *string
	ExpectedResponse *string
	ClearExpected    bool
	InputData        *string
	ClearInputData   bool
	ExpectedPath     *string
	ClearPath        bool
	ExpectedFacts    *string
	ClearFacts       bool
}

func (r *EvalExampleRepo) UpdateWith(ctx context.Context, id string, in EvalExampleUpdate) (*EvalExample, error) {
	existing, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Prompt != nil {
		existing.Prompt = *in.Prompt
	}
	if in.ClearExpected {
		existing.ExpectedResponse = nil
	} else if in.ExpectedResponse != nil {
		existing.ExpectedResponse = in.ExpectedResponse
	}
	if in.ClearInputData {
		existing.InputData = nil
	} else if in.InputData != nil {
		existing.InputData = in.InputData
	}
	if in.ClearPath {
		existing.ExpectedPath = nil
	} else if in.ExpectedPath != nil {
		existing.ExpectedPath = in.ExpectedPath
	}
	if in.ClearFacts {
		existing.ExpectedFacts = nil
	} else if in.ExpectedFacts != nil {
		existing.ExpectedFacts = in.ExpectedFacts
	}
	existing.UpdatedTime = time.Now().UnixMilli()
	err = r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, execErr := tx.ExecContext(ctx,
			`UPDATE eval_examples SET prompt = ?, expected_response = ?, input_data = ?, expected_path = ?, expected_facts = ?, updated_time = ? WHERE id = ?`,
			existing.Prompt, existing.ExpectedResponse, existing.InputData, existing.ExpectedPath, existing.ExpectedFacts, existing.UpdatedTime, id,
		); execErr != nil {
			return fmt.Errorf("EvalExampleRepo.Update: %w", execErr)
		}
		return touchDataset(ctx, tx, existing.DatasetID)
	})
	if err != nil {
		return nil, err
	}
	return existing, nil
}

const errFmtEvalExampleDelete = "EvalExampleRepo.Delete(%s): %w"

func (r *EvalExampleRepo) Delete(ctx context.Context, id string, files EvalFileDeleteEvent) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var datasetID string
		err := tx.QueryRowContext(ctx,
			`SELECT dataset_id FROM eval_examples WHERE id = ?`, id).Scan(&datasetID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEvalNotFound
		}
		if err != nil {
			return fmt.Errorf(errFmtEvalExampleDelete, id, err)
		}
		if err = recordFileDeletes(ctx, tx, evalFileScope{exampleWhere: `id = ?`, exampleArgs: []any{id}}, files); err != nil {
			return fmt.Errorf(errFmtEvalExampleDelete, id, err)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM eval_examples WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf(errFmtEvalExampleDelete, id, err)
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrEvalNotFound
		}
		return touchDataset(ctx, tx, datasetID)
	})
}

func (r *EvalExampleRepo) DeleteByDataset(ctx context.Context, datasetID string) (int, error) {
	var n int64
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM eval_examples WHERE dataset_id = ?`, datasetID)
		if err != nil {
			return fmt.Errorf("EvalExampleRepo.DeleteByDataset(%s): %w", datasetID, err)
		}
		n, _ = res.RowsAffected()
		if n == 0 {
			return nil
		}
		return touchDataset(ctx, tx, datasetID)
	})
	return int(n), err
}
