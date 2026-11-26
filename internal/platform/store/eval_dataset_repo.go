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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var ErrEvalNotFound = errors.New("eval entity not found")

var ErrEvalNameTaken = errors.New("eval entity name already taken")

var ErrEvalRunActive = errors.New("eval experiment has an active run")

type EvalDatasetWithCounts struct {
	*EvalDataset
	ExampleCount          int
	InputDataExampleCount int
	ExperimentCount       int
}

const evalExampleCountsJoin = `LEFT JOIN (
		    SELECT dataset_id, COUNT(*) AS cnt, COUNT(input_data) AS input_cnt FROM eval_examples GROUP BY dataset_id
		) ex ON ex.dataset_id = d.id`

const evalDatasetCountColumns = `COALESCE(ex.cnt, 0) AS example_count,
		       COALESCE(ex.input_cnt, 0) AS input_data_example_count,
		       COALESCE(exp.cnt, 0) AS experiment_count`

type evalExampleCounts struct {
	Total         int `db:"total"`
	WithInputData int `db:"with_input_data"`
}

type EvalDatasetRepo struct {
	db *DB
}

func NewEvalDatasetRepo(db *DB) *EvalDatasetRepo {
	return &EvalDatasetRepo{db: db}
}

func (r *EvalDatasetRepo) Create(ctx context.Context, d *EvalDataset) error {
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
	taken, err := r.nameTaken(ctx, d.Name, "")
	if err != nil {
		return fmt.Errorf("EvalDatasetRepo.Create check name: %w", err)
	}
	if taken {
		return fmt.Errorf("%w: %s", ErrEvalNameTaken, d.Name)
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO eval_datasets (id, name, description, is_default, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Name, d.Description, dbutil.BoolToInt(d.IsDefault), d.CreatedBy, d.UpdatedBy, d.CreatedTime, d.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("EvalDatasetRepo.Create: %w", err)
	}
	return nil
}

func (r *EvalDatasetRepo) GetByID(ctx context.Context, id string) (*EvalDataset, error) {
	row, err := dbutil.QueryRow[EvalDataset](ctx, r.db,
		`SELECT id, name, description, is_default, created_by, updated_by, created_time, updated_time
		 FROM eval_datasets WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("EvalDatasetRepo.GetByID(%s): %w", id, err)
	}
	if row == nil {
		return nil, ErrEvalNotFound
	}
	return row, nil
}

func (r *EvalDatasetRepo) GetDefault(ctx context.Context) (*EvalDataset, error) {
	row, err := dbutil.QueryRow[EvalDataset](ctx, r.db,
		`SELECT id, name, description, is_default, created_by, updated_by, created_time, updated_time
		 FROM eval_datasets WHERE is_default = 1 ORDER BY created_time ASC, id ASC LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("EvalDatasetRepo.GetDefault: %w", err)
	}
	if row == nil {
		return nil, ErrEvalNotFound
	}
	return row, nil
}

func (r *EvalDatasetRepo) GetWithCounts(ctx context.Context, id string) (*EvalDatasetWithCounts, error) {
	d, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	exCounts, err := r.exampleCounts(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("EvalDatasetRepo.GetWithCounts example count: %w", err)
	}
	expCount, err := r.experimentCount(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("EvalDatasetRepo.GetWithCounts experiment count: %w", err)
	}
	return &EvalDatasetWithCounts{EvalDataset: d, ExampleCount: exCounts.Total, InputDataExampleCount: exCounts.WithInputData, ExperimentCount: expCount}, nil
}

func (r *EvalDatasetRepo) Update(ctx context.Context, id string, name *string, description *string, updatedBy string) (*EvalDataset, error) {
	existing, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if name != nil && *name != existing.Name {
		taken, takenErr := r.nameTaken(ctx, *name, id)
		if takenErr != nil {
			return nil, fmt.Errorf("EvalDatasetRepo.Update check name: %w", takenErr)
		}
		if taken {
			return nil, fmt.Errorf("%w: %s", ErrEvalNameTaken, *name)
		}
		existing.Name = *name
	}
	if description != nil {
		existing.Description = description
	}
	existing.UpdatedBy = updatedBy
	existing.UpdatedTime = time.Now().UnixMilli()
	_, err = r.db.ExecContext(ctx,
		`UPDATE eval_datasets SET name = ?, description = ?, updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		existing.Name, existing.Description, existing.UpdatedBy, existing.UpdatedTime, id,
	)
	if err != nil {
		return nil, fmt.Errorf("EvalDatasetRepo.Update: %w", err)
	}
	return existing, nil
}

func (r *EvalDatasetRepo) Delete(ctx context.Context, id string, files EvalFileDeleteEvent) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := recordFileDeletes(ctx, tx, evalFileScope{exampleWhere: `dataset_id = ?`, exampleArgs: []any{id}}, files); err != nil {
			return fmt.Errorf("EvalDatasetRepo.Delete(%s): %w", id, err)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM eval_datasets WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("EvalDatasetRepo.Delete(%s): %w", id, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrEvalNotFound
		}
		return nil
	})
}

func (r *EvalDatasetRepo) NameExists(ctx context.Context, name string) (bool, error) {
	return r.nameTaken(ctx, name, "")
}

func (r *EvalDatasetRepo) List(ctx context.Context) ([]*EvalDataset, error) {
	return dbutil.QueryRowPtrs[EvalDataset](ctx, r.db,
		`SELECT id, name, description, is_default, created_by, updated_by, created_time, updated_time
		 FROM eval_datasets ORDER BY created_time DESC`)
}

func (r *EvalDatasetRepo) ListWithCounts(ctx context.Context) ([]*EvalDatasetWithCounts, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT d.id, d.name, d.description, d.is_default, d.created_by, d.updated_by, d.created_time, d.updated_time,
		       `+evalDatasetCountColumns+`
		FROM eval_datasets d
		`+evalExampleCountsJoin+`
		LEFT JOIN (
		    SELECT dataset_id, COUNT(*) AS cnt FROM eval_experiments WHERE dataset_id IS NOT NULL GROUP BY dataset_id
		) exp ON exp.dataset_id = d.id
		ORDER BY d.created_time DESC`)
	if err != nil {
		return nil, fmt.Errorf("EvalDatasetRepo.ListWithCounts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*EvalDatasetWithCounts
	for rows.Next() {
		row, err := scanEvalDatasetWithCounts(rows)
		if err != nil {
			return nil, fmt.Errorf("EvalDatasetRepo.ListWithCounts scan: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func scanEvalDatasetWithCounts(rows *sql.Rows) (*EvalDatasetWithCounts, error) {
	var d EvalDataset
	out := &EvalDatasetWithCounts{EvalDataset: &d}
	var isDefault int64
	if err := rows.Scan(&d.ID, &d.Name, &d.Description, &isDefault, &d.CreatedBy, &d.UpdatedBy,
		&d.CreatedTime, &d.UpdatedTime, &out.ExampleCount, &out.InputDataExampleCount, &out.ExperimentCount); err != nil {
		return nil, err
	}
	d.IsDefault = isDefault != 0
	return out, nil
}

type EvalDatasetSortField string

const (
	EvalDatasetSortCreatedTime     EvalDatasetSortField = "created_time"
	EvalDatasetSortUpdatedTime     EvalDatasetSortField = "updated_time"
	EvalDatasetSortName            EvalDatasetSortField = "name"
	EvalDatasetSortExampleCount    EvalDatasetSortField = "example_count"
	EvalDatasetSortExperimentCount EvalDatasetSortField = "experiment_count"
)

func (r *EvalDatasetRepo) ListPaginated(
	ctx context.Context,
	pageNumber, pageSize int,
	nameFilter string,
	sortBy EvalDatasetSortField,
	sortOrder string,
) ([]*EvalDatasetWithCounts, int, error) {
	if pageNumber < 1 {
		pageNumber = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	whereClause := ""
	args := []any{}
	if nameFilter != "" {
		pred, arg := dbutil.LikeContainsFold("d.name", nameFilter)
		whereClause = "WHERE " + pred
		args = append(args, arg)
	}

	totalQ := fmt.Sprintf(`SELECT COUNT(*) FROM eval_datasets d %s`, whereClause)
	total, err := dbutil.QueryVal[int](ctx, r.db, totalQ, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalDatasetRepo.ListPaginated count: %w", err)
	}

	sortColumn := evalDatasetSortColumn(sortBy)
	order := "DESC"
	if strings.EqualFold(sortOrder, "asc") {
		order = "ASC"
	}

	pageArgs := append([]any{}, args...)
	pageArgs = append(pageArgs, pageSize, (pageNumber-1)*pageSize)

	q := fmt.Sprintf(`
		SELECT d.id, d.name, d.description, d.is_default, d.created_by, d.updated_by, d.created_time, d.updated_time,
		       `+evalDatasetCountColumns+`
		FROM eval_datasets d
		`+evalExampleCountsJoin+`
		LEFT JOIN (
		    SELECT dataset_id, COUNT(*) AS cnt FROM eval_experiments WHERE dataset_id IS NOT NULL GROUP BY dataset_id
		) exp ON exp.dataset_id = d.id
		%s
		ORDER BY %s %s, d.id ASC
		LIMIT ? OFFSET ?`, whereClause, sortColumn, order)

	rows, err := r.db.QueryContext(ctx, q, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalDatasetRepo.ListPaginated query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*EvalDatasetWithCounts
	for rows.Next() {
		row, err := scanEvalDatasetWithCounts(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("EvalDatasetRepo.ListPaginated scan: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EvalDatasetRepo.ListPaginated rows: %w", err)
	}
	return out, total, nil
}

func evalDatasetSortColumn(s EvalDatasetSortField) string {
	switch s {
	case EvalDatasetSortName:
		return "d.name"
	case EvalDatasetSortUpdatedTime:
		return "d.updated_time"
	case EvalDatasetSortExampleCount:
		return "example_count"
	case EvalDatasetSortExperimentCount:
		return "experiment_count"
	default:
		return "d.created_time"
	}
}

func (r *EvalDatasetRepo) nameTaken(ctx context.Context, name, excludeID string) (bool, error) {
	var n int
	var err error
	if excludeID == "" {
		n, err = dbutil.QueryVal[int](ctx, r.db,
			`SELECT COUNT(*) FROM eval_datasets WHERE LOWER(name) = LOWER(?)`, name)
	} else {
		n, err = dbutil.QueryVal[int](ctx, r.db,
			`SELECT COUNT(*) FROM eval_datasets WHERE LOWER(name) = LOWER(?) AND id != ?`, name, excludeID)
	}
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (r *EvalDatasetRepo) exampleCounts(ctx context.Context, datasetID string) (evalExampleCounts, error) {
	row, err := dbutil.QueryRow[evalExampleCounts](ctx, r.db,
		`SELECT COUNT(*) AS total, COUNT(input_data) AS with_input_data FROM eval_examples WHERE dataset_id = ?`, datasetID)
	if err != nil || row == nil {
		return evalExampleCounts{}, err
	}
	return *row, nil
}

func (r *EvalDatasetRepo) experimentCount(ctx context.Context, datasetID string) (int, error) {
	return dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM eval_experiments WHERE dataset_id = ?`, datasetID)
}
