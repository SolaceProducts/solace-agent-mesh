// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type EvalExperimentEnriched struct {
	*EvalExperiment
	DatasetName        *string
	DatasetUpdatedTime *int64
	EvaluatorIDs       []string
	LatestRunStatus    *string
	LatestRunTime      *int64
}

type EvalExperimentRepo struct {
	db *DB
}

func NewEvalExperimentRepo(db *DB) *EvalExperimentRepo {
	return &EvalExperimentRepo{db: db}
}

func (r *EvalExperimentRepo) Create(ctx context.Context, e *EvalExperiment, evaluatorIDs []string) error {
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
	if e.RunsPerExample == 0 {
		e.RunsPerExample = 1
	}
	if e.MaxWorkers == 0 {
		e.MaxWorkers = 4
	}
	if e.Models == "" {
		e.Models = "[]"
	}
	if e.TargetType == "" {
		e.TargetType = EvalTargetTypeAgent
	}
	taken, err := r.nameTaken(ctx, e.Name, "")
	if err != nil {
		return fmt.Errorf("EvalExperimentRepo.Create check name: %w", err)
	}
	if taken {
		return fmt.Errorf("%w: %s", ErrEvalNameTaken, e.Name)
	}
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO eval_experiments
			 (id, name, description, dataset_id, target_agent, target_type, models, runs_per_example, max_workers,
			  primary_evaluator_id, is_default, run_as, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, e.Name, e.Description, e.DatasetID, e.TargetAgent, e.TargetType, e.Models, e.RunsPerExample, e.MaxWorkers,
			e.PrimaryEvaluatorID, dbutil.BoolToInt(e.IsDefault), e.RunAs, e.CreatedBy, e.UpdatedBy, e.CreatedTime, e.UpdatedTime,
		); err != nil {
			return fmt.Errorf("insert experiment: %w", err)
		}
		return r.attachEvaluatorsTx(ctx, tx, e.ID, evaluatorIDs)
	})
}

func (r *EvalExperimentRepo) GetByID(ctx context.Context, id string) (*EvalExperiment, error) {
	row, err := dbutil.QueryRow[EvalExperiment](ctx, r.db, evalExperimentSelect+` WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("EvalExperimentRepo.GetByID(%s): %w", id, err)
	}
	if row == nil {
		return nil, ErrEvalNotFound
	}
	return row, nil
}

func (r *EvalExperimentRepo) GetDefault(ctx context.Context) (*EvalExperiment, error) {
	row, err := dbutil.QueryRow[EvalExperiment](ctx, r.db,
		evalExperimentSelect+` WHERE is_default = 1 ORDER BY created_time ASC, id ASC LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("EvalExperimentRepo.GetDefault: %w", err)
	}
	if row == nil {
		return nil, ErrEvalNotFound
	}
	return row, nil
}

func (r *EvalExperimentRepo) GetEnriched(ctx context.Context, id string) (*EvalExperimentEnriched, error) {
	exp, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	enriched, err := r.enrich(ctx, exp)
	if err != nil {
		return nil, err
	}
	return enriched, nil
}

func (r *EvalExperimentRepo) List(
	ctx context.Context,
	datasetID, evaluatorID, targetAgent string,
) ([]*EvalExperimentEnriched, error) {
	where, args := EvalExperimentFilter{DatasetID: datasetID, EvaluatorID: evaluatorID, TargetAgent: targetAgent}.where()
	q := fmt.Sprintf(`%s %s ORDER BY created_time DESC`, evalExperimentSelect, where)
	rows, err := dbutil.QueryRowPtrs[EvalExperiment](ctx, r.db, q, args...)
	if err != nil {
		return nil, fmt.Errorf("EvalExperimentRepo.List: %w", err)
	}
	return r.enrichAll(ctx, rows)
}

type EvalExperimentFilter struct {
	DatasetID   string
	EvaluatorID string
	TargetAgent string
}

func (f EvalExperimentFilter) where() (string, []any) {
	var clauses []string
	var args []any
	if f.DatasetID != "" {
		clauses = append(clauses, "dataset_id = ?")
		args = append(args, f.DatasetID)
	}
	if f.TargetAgent != "" {
		clauses = append(clauses, "target_agent = ? AND target_type = ?")
		args = append(args, f.TargetAgent, EvalTargetTypeAgent)
	}
	if f.EvaluatorID != "" {
		clauses = append(clauses, "id IN (SELECT experiment_id FROM eval_experiment_evaluators WHERE evaluator_id = ?)")
		args = append(args, f.EvaluatorID)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

type EvalExperimentSortField string

const (
	EvalExperimentSortCreatedTime EvalExperimentSortField = "created_time"
	EvalExperimentSortName        EvalExperimentSortField = "name"
)

func (r *EvalExperimentRepo) ListPaginated(
	ctx context.Context,
	filter EvalExperimentFilter,
	pageNumber, pageSize int,
	sortBy EvalExperimentSortField,
	desc bool,
) ([]*EvalExperimentEnriched, int, error) {
	where, args := filter.where()
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM eval_experiments `+where, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalExperimentRepo.ListPaginated count: %w", err)
	}
	column := string(EvalExperimentSortCreatedTime)
	if sortBy == EvalExperimentSortName {
		column = "LOWER(name)"
	}
	order := "ASC"
	if desc {
		order = "DESC"
	}
	q := fmt.Sprintf(`%s %s ORDER BY %s %s, id ASC LIMIT ? OFFSET ?`, evalExperimentSelect, where, column, order)
	rows, err := dbutil.QueryRowPtrs[EvalExperiment](ctx, r.db, q, append(args, pageSize, (pageNumber-1)*pageSize)...)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalExperimentRepo.ListPaginated: %w", err)
	}
	enriched, err := r.enrichAll(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return enriched, total, nil
}

func (r *EvalExperimentRepo) enrichAll(ctx context.Context, rows []*EvalExperiment) ([]*EvalExperimentEnriched, error) {
	out := make([]*EvalExperimentEnriched, 0, len(rows))
	for _, exp := range rows {
		enriched, err := r.enrich(ctx, exp)
		if err != nil {
			return nil, err
		}
		out = append(out, enriched)
	}
	return out, nil
}

type EvalExperimentUpdate struct {
	Name               *string
	Description        *string
	DatasetID          *string
	TargetAgent        *string
	TargetType         *string
	Models             *string
	RunsPerExample     *int
	MaxWorkers         *int
	PrimaryEvaluatorID *string
	RunAs              *string
	UpdatedBy          string
}

func (r *EvalExperimentRepo) Update(ctx context.Context, id string, in EvalExperimentUpdate) (*EvalExperiment, error) {
	existing, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil && *in.Name != existing.Name {
		taken, takenErr := r.nameTaken(ctx, *in.Name, id)
		if takenErr != nil {
			return nil, fmt.Errorf("EvalExperimentRepo.Update check name: %w", takenErr)
		}
		if taken {
			return nil, fmt.Errorf("%w: %s", ErrEvalNameTaken, *in.Name)
		}
		existing.Name = *in.Name
	}
	if in.Description != nil {
		existing.Description = in.Description
	}
	if in.DatasetID != nil {
		if *in.DatasetID == "" {
			existing.DatasetID = nil
		} else {
			v := *in.DatasetID
			existing.DatasetID = &v
		}
	}
	if in.TargetAgent != nil {
		existing.TargetAgent = *in.TargetAgent
	}
	if in.TargetType != nil {
		existing.TargetType = *in.TargetType
	}
	if in.Models != nil {
		existing.Models = *in.Models
	}
	if in.RunsPerExample != nil {
		existing.RunsPerExample = *in.RunsPerExample
	}
	if in.MaxWorkers != nil {
		existing.MaxWorkers = *in.MaxWorkers
	}
	if in.PrimaryEvaluatorID != nil {
		if *in.PrimaryEvaluatorID == "" {
			existing.PrimaryEvaluatorID = nil
		} else {
			v := *in.PrimaryEvaluatorID
			existing.PrimaryEvaluatorID = &v
		}
	}
	if in.RunAs != nil {
		if *in.RunAs == "" {
			existing.RunAs = nil
		} else {
			v := *in.RunAs
			existing.RunAs = &v
		}
	}
	existing.UpdatedBy = in.UpdatedBy
	existing.UpdatedTime = time.Now().UnixMilli()
	_, err = r.db.ExecContext(ctx,
		`UPDATE eval_experiments
		 SET name = ?, description = ?, dataset_id = ?, target_agent = ?, target_type = ?, models = ?,
		     runs_per_example = ?, max_workers = ?, primary_evaluator_id = ?,
		     run_as = ?, updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		existing.Name, existing.Description, existing.DatasetID, existing.TargetAgent, existing.TargetType, existing.Models,
		existing.RunsPerExample, existing.MaxWorkers, existing.PrimaryEvaluatorID,
		existing.RunAs, existing.UpdatedBy, existing.UpdatedTime, id,
	)
	if err != nil {
		return nil, fmt.Errorf("EvalExperimentRepo.Update: %w", err)
	}
	return existing, nil
}

func (r *EvalExperimentRepo) Delete(ctx context.Context, id string, files EvalFileDeleteEvent) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		if err := recordFileDeletes(ctx, tx, evalFileScope{runWhere: `experiment_id = ?`, runArgs: []any{id}}, files); err != nil {
			return fmt.Errorf("EvalExperimentRepo.Delete(%s): %w", id, err)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM eval_experiments WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("EvalExperimentRepo.Delete(%s): %w", id, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrEvalNotFound
		}
		return nil
	})
}

func (r *EvalExperimentRepo) AttachEvaluators(ctx context.Context, experimentID string, evaluatorIDs []string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return r.attachEvaluatorsTx(ctx, tx, experimentID, evaluatorIDs)
	})
}

func (r *EvalExperimentRepo) DetachAllEvaluators(ctx context.Context, experimentID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM eval_experiment_evaluators WHERE experiment_id = ?`, experimentID)
	if err != nil {
		return fmt.Errorf("EvalExperimentRepo.DetachAllEvaluators(%s): %w", experimentID, err)
	}
	return nil
}

func (r *EvalExperimentRepo) GetEvaluatorIDs(ctx context.Context, experimentID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT evaluator_id FROM eval_experiment_evaluators WHERE experiment_id = ?`, experimentID)
}

func (r *EvalExperimentRepo) attachEvaluatorsTx(ctx context.Context, tx *dbutil.Tx, experimentID string, evaluatorIDs []string) error {
	for _, evID := range evaluatorIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO eval_experiment_evaluators (experiment_id, evaluator_id) VALUES (?, ?)`,
			experimentID, evID); err != nil {
			return fmt.Errorf("attach evaluator %s: %w", evID, err)
		}
	}
	return nil
}

func (r *EvalExperimentRepo) enrich(ctx context.Context, exp *EvalExperiment) (*EvalExperimentEnriched, error) {
	out := &EvalExperimentEnriched{EvalExperiment: exp}

	if exp.DatasetID != nil {
		var name string
		var updatedTime int64
		err := r.db.QueryRowContext(ctx,
			`SELECT name, updated_time FROM eval_datasets WHERE id = ?`, *exp.DatasetID).Scan(&name, &updatedTime)
		if err == nil {
			out.DatasetName = &name
			out.DatasetUpdatedTime = &updatedTime
		}
	}

	evIDs, err := r.GetEvaluatorIDs(ctx, exp.ID)
	if err != nil {
		return nil, fmt.Errorf("enrich evaluator ids: %w", err)
	}
	out.EvaluatorIDs = evIDs

	type latestRow struct {
		Status      string `db:"status"`
		StartedTime int64  `db:"started_time"`
	}
	latest, err := dbutil.QueryRow[latestRow](ctx, r.db,
		`SELECT status, started_time FROM eval_runs WHERE experiment_id = ?
		 ORDER BY started_time DESC LIMIT 1`, exp.ID)
	if err != nil {
		return nil, fmt.Errorf("enrich latest run: %w", err)
	}
	if latest != nil {
		s := latest.Status
		t := latest.StartedTime
		out.LatestRunStatus = &s
		out.LatestRunTime = &t
	}

	return out, nil
}

func (r *EvalExperimentRepo) nameTaken(ctx context.Context, name, excludeID string) (bool, error) {
	var n int
	var err error
	if excludeID == "" {
		n, err = dbutil.QueryVal[int](ctx, r.db,
			`SELECT COUNT(*) FROM eval_experiments WHERE LOWER(name) = LOWER(?)`, name)
	} else {
		n, err = dbutil.QueryVal[int](ctx, r.db,
			`SELECT COUNT(*) FROM eval_experiments WHERE LOWER(name) = LOWER(?) AND id != ?`, name, excludeID)
	}
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

const evalExperimentSelect = `SELECT id, name, description, dataset_id, target_agent, target_type, models,
	runs_per_example, max_workers, primary_evaluator_id, is_default, run_as,
	created_by, updated_by, created_time, updated_time
	FROM eval_experiments`
