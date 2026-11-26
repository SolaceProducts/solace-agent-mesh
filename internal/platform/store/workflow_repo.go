// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/google/uuid"
)

type WorkflowRepo struct {
	db *DB
}

func NewWorkflowRepo(db *DB) *WorkflowRepo {
	return &WorkflowRepo{db: db}
}

func (r *WorkflowRepo) Create(ctx context.Context, w *Workflow, bindings WorkflowBindings) error {
	if w.ID == "" {
		w.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if w.CreatedTime == 0 {
		w.CreatedTime = now
	}
	if w.UpdatedTime == 0 {
		w.UpdatedTime = now
	}

	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO workflows (id, name, description, app_config, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			w.ID, w.Name, w.Description, w.AppConfig,
			w.CreatedBy, w.UpdatedBy, w.CreatedTime, w.UpdatedTime,
		)
		if err != nil {
			return fmt.Errorf("insert workflow: %w", err)
		}
		return r.setBindings(ctx, tx, w.ID, bindings)
	})
}

func (r *WorkflowRepo) GetByName(ctx context.Context, name string) (*Workflow, error) {
	return dbutil.QueryRow[Workflow](ctx, r.db,
		`SELECT id, name, description, app_config,
			created_by, updated_by, created_time, updated_time
		 FROM workflows WHERE LOWER(REPLACE(name, ' ', '')) = ?`, normalizeName(name),
	)
}

func (r *WorkflowRepo) NameTakenByOther(ctx context.Context, name, excludeID string) (bool, error) {
	rows, err := dbutil.QueryRows[workflowNameRow](ctx, r.db, `SELECT id, name FROM workflows`)
	if err != nil {
		return false, fmt.Errorf("WorkflowRepo.NameTakenByOther: %w", err)
	}
	wanted := normalizeName(name)
	for _, row := range rows {
		if row.ID != excludeID && normalizeName(row.Name) == wanted {
			return true, nil
		}
	}
	return false, nil
}

type workflowNameRow struct {
	ID   string `db:"id"`
	Name string `db:"name"`
}

func (r *WorkflowRepo) GetByID(ctx context.Context, id string) (*Workflow, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil
	}
	return dbutil.QueryRow[Workflow](ctx, r.db,
		`SELECT id, name, description, app_config,
			created_by, updated_by, created_time, updated_time
		 FROM workflows WHERE id = ?`, id,
	)
}

func (r *WorkflowRepo) ListPaginated(ctx context.Context, pageNumber, pageSize int) ([]Workflow, int, error) {
	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM workflows`)
	if err != nil {
		return nil, 0, fmt.Errorf("WorkflowRepo.ListPaginated count: %w", err)
	}

	offset := (pageNumber - 1) * pageSize
	workflows, err := dbutil.QueryRows[Workflow](ctx, r.db,
		`SELECT id, name, description, app_config,
			created_by, updated_by, created_time, updated_time
		 FROM workflows ORDER BY created_time DESC, id DESC LIMIT ? OFFSET ?`, pageSize, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("WorkflowRepo.ListPaginated query: %w", err)
	}

	return workflows, total, nil
}

func (r *WorkflowRepo) ListAll(ctx context.Context) ([]Workflow, error) {
	workflows, err := dbutil.QueryRows[Workflow](ctx, r.db,
		`SELECT id, name, description, app_config,
			created_by, updated_by, created_time, updated_time
		 FROM workflows ORDER BY created_time, id`,
	)
	if err != nil {
		return nil, fmt.Errorf("WorkflowRepo.ListAll: %w", err)
	}
	return workflows, nil
}

func (r *WorkflowRepo) Update(ctx context.Context, w *Workflow, bindings WorkflowBindings) error {
	w.UpdatedTime = time.Now().UnixMilli()
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		result, err := tx.ExecContext(ctx,
			`UPDATE workflows SET name = ?, description = ?, app_config = ?,
				updated_by = ?, updated_time = ?
			 WHERE id = ?`,
			w.Name, w.Description, w.AppConfig, w.UpdatedBy, w.UpdatedTime, w.ID,
		)
		if err != nil {
			return fmt.Errorf("update workflow: %w", err)
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return fmt.Errorf("workflow not found")
		}
		return r.setBindings(ctx, tx, w.ID, bindings)
	})
}

func (*WorkflowRepo) setBindings(ctx context.Context, q dbutil.Querier, workflowID string, bindings WorkflowBindings) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM workflow_connectors WHERE workflow_id = ?`, workflowID); err != nil {
		return fmt.Errorf("clear workflow_connectors: %w", err)
	}
	for _, b := range bindings.Connectors {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO workflow_connectors (workflow_id, connector_id, ref_name) VALUES (?, ?, ?)`,
			workflowID, b.ConnectorID, b.RefName,
		); err != nil {
			return fmt.Errorf("insert workflow_connectors: %w", err)
		}
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM workflow_toolsets WHERE workflow_id = ?`, workflowID); err != nil {
		return fmt.Errorf("clear workflow_toolsets: %w", err)
	}
	for _, ts := range bindings.Toolsets {
		if _, err := q.ExecContext(ctx,
			`INSERT INTO workflow_toolsets (workflow_id, toolset) VALUES (?, ?)`,
			workflowID, ts,
		); err != nil {
			return fmt.Errorf("insert workflow_toolsets: %w", err)
		}
	}
	return nil
}

func (r *WorkflowRepo) GetToolsets(ctx context.Context, workflowID string) ([]string, error) {
	names, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT toolset FROM workflow_toolsets WHERE workflow_id = ? ORDER BY toolset`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("WorkflowRepo.GetToolsets(%s): %w", workflowID, err)
	}
	return names, nil
}

func (r *WorkflowRepo) GetConnectorBindings(ctx context.Context, workflowID string) ([]WorkflowConnectorBinding, error) {
	bindings, err := dbutil.QueryRows[WorkflowConnectorBinding](ctx, r.db,
		`SELECT workflow_id, connector_id, ref_name FROM workflow_connectors WHERE workflow_id = ?`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("WorkflowRepo.GetConnectorBindings(%s): %w", workflowID, err)
	}
	return bindings, nil
}

func (r *WorkflowRepo) ListIDsByConnectorID(ctx context.Context, connectorID string) ([]string, error) {
	ids, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT workflow_id FROM workflow_connectors WHERE connector_id = ?`, connectorID)
	if err != nil {
		return nil, fmt.Errorf("WorkflowRepo.ListIDsByConnectorID(%s): %w", connectorID, err)
	}
	return ids, nil
}

func (r *WorkflowRepo) Delete(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM workflows WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("WorkflowRepo.Delete(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("workflow not found")
	}
	return nil
}

func (r *WorkflowRepo) GetDeploymentStatus(ctx context.Context, workflowID string) (string, error) {
	status, action, found, err := r.latestDeployment(ctx, workflowID)
	if err != nil {
		return "", fmt.Errorf("WorkflowRepo.GetDeploymentStatus(%s): %w", workflowID, err)
	}
	if !found {
		return "not_deployed", nil
	}
	return MapDeploymentDisplayStatus(status, action), nil
}

func (r *WorkflowRepo) latestDeployment(ctx context.Context, workflowID string) (status, action string, found bool, err error) {
	type row struct {
		Status string `db:"status"`
		Action string `db:"action"`
	}
	res, err := dbutil.QueryRow[row](ctx, r.db,
		`SELECT status, action FROM workflow_deployments
		 WHERE workflow_id = ? ORDER BY created_time DESC, id DESC LIMIT 1`, workflowID)
	if err != nil {
		return "", "", false, err
	}
	if res == nil {
		return "", "", false, nil
	}
	return res.Status, res.Action, true, nil
}

func (r *WorkflowRepo) HasPossiblyLiveDeployment(ctx context.Context, workflowID string) (bool, error) {
	status, action, found, err := r.latestDeployment(ctx, workflowID)
	if err != nil {
		return false, fmt.Errorf("WorkflowRepo.HasPossiblyLiveDeployment(%s): %w", workflowID, err)
	}
	if !found {
		return false, nil
	}
	return MayHaveLiveInstance(status, action), nil
}

func (r *WorkflowRepo) GetDeploymentStatuses(ctx context.Context, workflowIDs []string) (map[string]string, error) {
	latest, err := r.GetLatestDeployments(ctx, workflowIDs)
	statuses := make(map[string]string, len(latest))
	for workflowID, outcome := range latest {
		statuses[workflowID] = MapDeploymentDisplayStatus(outcome.Status, outcome.Action)
	}
	return statuses, err
}

func (r *WorkflowRepo) GetLatestDeployments(ctx context.Context, workflowIDs []string) (map[string]DeploymentOutcome, error) {
	if len(workflowIDs) == 0 {
		return map[string]DeploymentOutcome{}, nil
	}

	ph, args, err := dbutil.InClause(workflowIDs)
	if err != nil {
		return nil, fmt.Errorf("WorkflowRepo.GetLatestDeployments: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT d.id, d.workflow_id, d.status, d.action
		 FROM workflow_deployments d
		 INNER JOIN (
		   SELECT workflow_id, MAX(created_time) as max_time
		   FROM workflow_deployments
		   WHERE workflow_id IN (%s)
		   GROUP BY workflow_id
		 ) latest ON d.workflow_id = latest.workflow_id AND d.created_time = latest.max_time`,
		ph,
	)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("WorkflowRepo.GetLatestDeployments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]DeploymentOutcome)
	maxID := make(map[string]string)
	for rows.Next() {
		var id, workflowID, status, action string
		if scanErr := rows.Scan(&id, &workflowID, &status, &action); scanErr != nil {
			return nil, fmt.Errorf("WorkflowRepo.GetLatestDeployments scan: %w", scanErr)
		}
		if prev, ok := maxID[workflowID]; ok && prev >= id {
			continue
		}
		maxID[workflowID] = id
		result[workflowID] = DeploymentOutcome{Action: action, Status: status}
	}
	return result, rows.Err()
}

func (r *WorkflowRepo) IDByName(ctx context.Context, name string) (string, error) {
	query, arg := `SELECT id FROM workflows WHERE LOWER(REPLACE(name, ' ', '')) = ? ORDER BY id LIMIT 1`, normalizeName(name)
	if id, kind, ok := a2a.IDFromInstanceName(name); ok && kind == a2a.KindWorkflow {
		query, arg = `SELECT id FROM workflows WHERE id = ?`, id
	}
	id, err := dbutil.QueryVal[string](ctx, r.db, query, arg)
	if err != nil {
		return "", fmt.Errorf("WorkflowRepo.IDByName(%s): %w", name, err)
	}
	return id, nil
}
