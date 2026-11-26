// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type EvalEvaluatorWithCount struct {
	*EvalEvaluator
	ExperimentCount int
}

type EvalEvaluatorRepo struct {
	db *DB
}

func NewEvalEvaluatorRepo(db *DB) *EvalEvaluatorRepo {
	return &EvalEvaluatorRepo{db: db}
}

func (r *EvalEvaluatorRepo) Create(ctx context.Context, e *EvalEvaluator) error {
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
	taken, err := r.nameTaken(ctx, e.Name, "")
	if err != nil {
		return fmt.Errorf("EvalEvaluatorRepo.Create check name: %w", err)
	}
	if taken {
		return fmt.Errorf("%w: %s", ErrEvalNameTaken, e.Name)
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO eval_evaluators
		 (id, name, description, type, heuristic_type, is_default, model, prompt_template, choice_scores,
		  decision_config, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Name, e.Description, e.Type, e.HeuristicType, dbutil.BoolToInt(e.IsDefault), e.Model, e.PromptTemplate, e.ChoiceScores,
		e.DecisionConfig, e.CreatedBy, e.UpdatedBy, e.CreatedTime, e.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("EvalEvaluatorRepo.Create: %w", err)
	}
	return nil
}

func (r *EvalEvaluatorRepo) GetByID(ctx context.Context, id string) (*EvalEvaluator, error) {
	row, err := dbutil.QueryRow[EvalEvaluator](ctx, r.db, evalEvaluatorSelect+` WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("EvalEvaluatorRepo.GetByID(%s): %w", id, err)
	}
	if row == nil {
		return nil, ErrEvalNotFound
	}
	return row, nil
}

func (r *EvalEvaluatorRepo) GetByName(ctx context.Context, name string) (*EvalEvaluator, error) {
	row, err := dbutil.QueryRow[EvalEvaluator](ctx, r.db, evalEvaluatorSelect+` WHERE name = ?`, name)
	if err != nil {
		return nil, fmt.Errorf("EvalEvaluatorRepo.GetByName(%q): %w", name, err)
	}
	if row == nil {
		return nil, ErrEvalNotFound
	}
	return row, nil
}

func (r *EvalEvaluatorRepo) GetByIDs(ctx context.Context, ids []string) ([]*EvalEvaluator, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("EvalEvaluatorRepo.GetByIDs: %w", err)
	}
	q := fmt.Sprintf(`%s WHERE id IN (%s)`, evalEvaluatorSelect, ph)
	rows, err := dbutil.QueryRowPtrs[EvalEvaluator](ctx, r.db, q, args...)
	if err != nil {
		return nil, fmt.Errorf("EvalEvaluatorRepo.GetByIDs: %w", err)
	}
	return rows, nil
}

func (r *EvalEvaluatorRepo) List(ctx context.Context) ([]*EvalEvaluator, error) {
	return dbutil.QueryRowPtrs[EvalEvaluator](ctx, r.db,
		evalEvaluatorSelect+` ORDER BY created_time ASC`)
}

const evalEvaluatorWithCountSelect = `
	SELECT e.id, e.name, e.description, e.type, e.heuristic_type, e.is_default, e.model,
	       e.prompt_template, e.choice_scores, e.decision_config, e.created_by, e.updated_by, e.created_time, e.updated_time,
	       COALESCE(c.cnt, 0) AS experiment_count
	FROM eval_evaluators e
	LEFT JOIN (
	    SELECT evaluator_id, COUNT(*) AS cnt FROM eval_experiment_evaluators GROUP BY evaluator_id
	) c ON c.evaluator_id = e.id`

func (r *EvalEvaluatorRepo) ListWithCounts(ctx context.Context) ([]*EvalEvaluatorWithCount, error) {
	out, err := r.queryWithCounts(ctx, evalEvaluatorWithCountSelect+` ORDER BY e.created_time ASC`)
	if err != nil {
		return nil, fmt.Errorf("EvalEvaluatorRepo.ListWithCounts: %w", err)
	}
	return out, nil
}

type EvalEvaluatorSortField string

const (
	EvalEvaluatorSortCreatedTime EvalEvaluatorSortField = "created_time"
	EvalEvaluatorSortName        EvalEvaluatorSortField = "name"
)

func (r *EvalEvaluatorRepo) ListWithCountsPaginated(
	ctx context.Context,
	pageNumber, pageSize int,
	sortBy EvalEvaluatorSortField,
	desc bool,
) ([]*EvalEvaluatorWithCount, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM eval_evaluators`)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalEvaluatorRepo.ListWithCountsPaginated count: %w", err)
	}
	order := "e.is_default DESC, e.created_time ASC, e.id ASC"
	if sortBy != "" {
		column := "e.created_time"
		if sortBy == EvalEvaluatorSortName {
			column = "LOWER(e.name)"
		}
		direction := "ASC"
		if desc {
			direction = "DESC"
		}
		order = column + " " + direction + ", e.id ASC"
	}
	out, err := r.queryWithCounts(ctx, evalEvaluatorWithCountSelect+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		pageSize, (pageNumber-1)*pageSize)
	if err != nil {
		return nil, 0, fmt.Errorf("EvalEvaluatorRepo.ListWithCountsPaginated: %w", err)
	}
	return out, total, nil
}

func (r *EvalEvaluatorRepo) CountExperiments(ctx context.Context, id string) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM eval_experiment_evaluators WHERE evaluator_id = ?`, id)
	if err != nil {
		return 0, fmt.Errorf("EvalEvaluatorRepo.CountExperiments(%s): %w", id, err)
	}
	return n, nil
}

func (r *EvalEvaluatorRepo) queryWithCounts(ctx context.Context, query string, args ...any) ([]*EvalEvaluatorWithCount, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*EvalEvaluatorWithCount
	for rows.Next() {
		var e EvalEvaluator
		var cnt int
		var isDefault int64
		if err := rows.Scan(&e.ID, &e.Name, &e.Description, &e.Type, &e.HeuristicType, &isDefault,
			&e.Model, &e.PromptTemplate, &e.ChoiceScores, &e.DecisionConfig, &e.CreatedBy, &e.UpdatedBy,
			&e.CreatedTime, &e.UpdatedTime, &cnt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		e.IsDefault = isDefault != 0
		out = append(out, &EvalEvaluatorWithCount{EvalEvaluator: &e, ExperimentCount: cnt})
	}
	return out, rows.Err()
}

type EvalEvaluatorUpdate struct {
	Name              *string
	Description       *string
	Model             *string
	ClearModel        bool
	PromptTemplate    *string
	ChoiceScores      *string
	ClearChoiceScores bool
	DecisionConfig    *string
	UpdatedBy         string
}

func (r *EvalEvaluatorRepo) Update(ctx context.Context, id string, in EvalEvaluatorUpdate) (*EvalEvaluator, error) {
	existing, err := r.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Name != nil && *in.Name != existing.Name {
		taken, takenErr := r.nameTaken(ctx, *in.Name, id)
		if takenErr != nil {
			return nil, fmt.Errorf("EvalEvaluatorRepo.Update check name: %w", takenErr)
		}
		if taken {
			return nil, fmt.Errorf("%w: %s", ErrEvalNameTaken, *in.Name)
		}
		existing.Name = *in.Name
	}
	if in.Description != nil {
		existing.Description = in.Description
	}
	if in.Model != nil {
		existing.Model = in.Model
	} else if in.ClearModel {
		existing.Model = nil
	}
	if in.PromptTemplate != nil {
		existing.PromptTemplate = in.PromptTemplate
	}
	if in.ChoiceScores != nil {
		existing.ChoiceScores = in.ChoiceScores
	} else if in.ClearChoiceScores {
		existing.ChoiceScores = nil
	}
	if in.DecisionConfig != nil {
		existing.DecisionConfig = in.DecisionConfig
	}
	existing.UpdatedBy = in.UpdatedBy
	existing.UpdatedTime = time.Now().UnixMilli()
	_, err = r.db.ExecContext(ctx,
		`UPDATE eval_evaluators
		 SET name = ?, description = ?, model = ?, prompt_template = ?, choice_scores = ?,
		     decision_config = ?, updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		existing.Name, existing.Description, existing.Model, existing.PromptTemplate, existing.ChoiceScores,
		existing.DecisionConfig, existing.UpdatedBy, existing.UpdatedTime, id,
	)
	if err != nil {
		return nil, fmt.Errorf("EvalEvaluatorRepo.Update: %w", err)
	}
	return existing, nil
}

func (r *EvalEvaluatorRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM eval_evaluators WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("EvalEvaluatorRepo.Delete(%s): %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrEvalNotFound
	}
	return nil
}

func (r *EvalEvaluatorRepo) nameTaken(ctx context.Context, name, excludeID string) (bool, error) {
	var n int
	var err error
	if excludeID == "" {
		n, err = dbutil.QueryVal[int](ctx, r.db,
			`SELECT COUNT(*) FROM eval_evaluators WHERE LOWER(name) = LOWER(?)`, name)
	} else {
		n, err = dbutil.QueryVal[int](ctx, r.db,
			`SELECT COUNT(*) FROM eval_evaluators WHERE LOWER(name) = LOWER(?) AND id != ?`, name, excludeID)
	}
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

const evalEvaluatorSelect = `SELECT id, name, description, type, heuristic_type, is_default, model,
	prompt_template, choice_scores, decision_config, created_by, updated_by, created_time, updated_time
	FROM eval_evaluators`
