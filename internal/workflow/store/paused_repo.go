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
)

func nowMillis() int64 { return time.Now().UnixMilli() }

type PausedWorkflow struct {
	RequestID       string `db:"request_id"`
	ExecutionID     string `db:"execution_id"`
	WorkflowName    string `db:"workflow_name"`
	NodeID          string `db:"node_id"`
	RequesterAddr   string `db:"requester_addr"`
	RecipientUserID string `db:"recipient_user_id"`
	StateSnapshot   string `db:"state_snapshot"`
	ExecContext     string `db:"exec_context"`
	Routing         string `db:"routing"`
	Status          string `db:"status"`
	ExpiresAt       *int64 `db:"expires_at"`
	CreatedAt       int64  `db:"created_at"`
	ResolvedAt      *int64 `db:"resolved_at"`

	TaskID string `db:"task_id"`

	Answer *string `db:"answer"`
}

const (
	StatusPaused   = "paused"
	StatusAnswered = "answered"
)

const openStatuses = "('paused', 'answered')"

const pausedColumns = `request_id, execution_id, workflow_name, node_id, requester_addr,
		        recipient_user_id, state_snapshot, exec_context, routing, status,
		        expires_at, created_at, resolved_at, answer`

type PausedRepo struct {
	db *DB
}

func NewPausedRepo(db *DB) *PausedRepo { return &PausedRepo{db: db} }

func (r *PausedRepo) Insert(ctx context.Context, p PausedWorkflow) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO workflow_paused
		   (request_id, execution_id, task_id, workflow_name, node_id, requester_addr,
		    recipient_user_id, state_snapshot, exec_context, routing, status,
		    expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'paused', ?, ?)`,
		p.RequestID, p.ExecutionID, p.TaskID, p.WorkflowName, p.NodeID, p.RequesterAddr,
		p.RecipientUserID, p.StateSnapshot, p.ExecContext, p.Routing,
		p.ExpiresAt, nowMillis(),
	)
	if err != nil {
		return fmt.Errorf("insert paused workflow: %w", err)
	}
	return nil
}

func (r *PausedRepo) Get(ctx context.Context, requestID string) (*PausedWorkflow, error) {
	p, err := dbutil.QueryRow[PausedWorkflow](ctx, r.db,
		`SELECT `+pausedColumns+`
		 FROM workflow_paused WHERE request_id = ?`, requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get paused workflow: %w", err)
	}
	return p, nil
}

func (r *PausedRepo) Claim(ctx context.Context, requestID, terminalStatus string) (*PausedWorkflow, bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE workflow_paused SET status = ?, resolved_at = ?
		 WHERE request_id = ? AND status = 'paused'`,
		terminalStatus, nowMillis(), requestID,
	)
	if err != nil {
		return nil, false, fmt.Errorf("claim paused workflow: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, false, fmt.Errorf("claim paused workflow rows affected: %w", err)
	}
	if affected == 0 {
		return nil, false, nil
	}
	p, err := r.Get(ctx, requestID)
	if err != nil {
		return nil, false, err
	}
	return p, true, nil
}

func (r *PausedRepo) ClaimAllForExecution(ctx context.Context, executionID, terminalStatus string) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE workflow_paused SET status = ?, resolved_at = ?
		 WHERE execution_id = ? AND status IN `+openStatuses,
		terminalStatus, nowMillis(), executionID,
	)
	if err != nil {
		return 0, fmt.Errorf("claim paused workflows for execution: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("claim paused workflows for execution rows affected: %w", err)
	}
	return n, nil
}

type TaskPause struct {
	RequestID   string `db:"request_id"`
	ExecutionID string `db:"execution_id"`
	Status      string `db:"status"`
}

func (r *PausedRepo) PausesForTask(ctx context.Context, taskID string) ([]TaskPause, error) {
	rows, err := dbutil.QueryRows[TaskPause](ctx, r.db,
		`SELECT request_id, execution_id, status FROM workflow_paused WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("paused workflows for task: %w", err)
	}
	return rows, nil
}

func (r *PausedRepo) CloseUnlessCarried(ctx context.Context, taskID, closedStatus string, carried func([]TaskPause) bool) (pauses []TaskPause, closed int64, isCarried bool, err error) {
	selectRows := `SELECT request_id, execution_id, status FROM workflow_paused WHERE task_id = ?`
	if r.db.Dialect() == "postgres" {
		selectRows += " FOR UPDATE"
	}
	err = r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		pauses, err = dbutil.QueryRows[TaskPause](ctx, tx, selectRows, taskID)
		if err != nil {
			return err
		}
		if len(pauses) == 0 {
			return nil
		}
		if isCarried = carried(pauses); isCarried {
			return nil
		}
		res, execErr := tx.ExecContext(ctx,
			`UPDATE workflow_paused SET status = ?, resolved_at = ?
			 WHERE task_id = ? AND status IN `+openStatuses,
			closedStatus, nowMillis(), taskID)
		if execErr != nil {
			return execErr
		}
		closed, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return nil, 0, false, fmt.Errorf("close paused workflows for task: %w", err)
	}
	return pauses, closed, isCarried, nil
}

func (r *PausedRepo) Expired(ctx context.Context, workflowName string, nowMs int64, limit int) ([]PausedWorkflow, error) {
	return dbutil.QueryRows[PausedWorkflow](ctx, r.db,
		`SELECT `+pausedColumns+`
		 FROM workflow_paused
		 WHERE workflow_name = ? AND status = 'paused' AND expires_at IS NOT NULL AND expires_at <= ?
		 ORDER BY expires_at ASC LIMIT ?`,
		workflowName, nowMs, limit,
	)
}

func (r *PausedRepo) PausedForExecution(ctx context.Context, executionID string) ([]PausedWorkflow, error) {
	return dbutil.QueryRows[PausedWorkflow](ctx, r.db,
		`SELECT `+pausedColumns+`
		 FROM workflow_paused
		 WHERE execution_id = ? AND status IN `+openStatuses+`
		 ORDER BY created_at ASC`,
		executionID,
	)
}

func (r *PausedRepo) StatusesFor(ctx context.Context, requestIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(requestIDs))
	for chunk := range slices.Chunk(requestIDs, dbutil.InClauseCap) {
		if err := r.appendStatuses(ctx, chunk, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *PausedRepo) appendStatuses(ctx context.Context, requestIDs []string, out map[string]string) error {
	if len(requestIDs) == 0 {
		return nil
	}
	placeholders, args, err := dbutil.InClause(requestIDs)
	if err != nil {
		return fmt.Errorf("statuses for paused workflows: %w", err)
	}
	type statusRow struct {
		RequestID string `db:"request_id"`
		Status    string `db:"status"`
	}
	rows, err := dbutil.QueryRows[statusRow](ctx, r.db,
		`SELECT request_id, status FROM workflow_paused WHERE request_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return fmt.Errorf("statuses for paused workflows: %w", err)
	}
	for i := range rows {
		out[rows[i].RequestID] = rows[i].Status
	}
	return nil
}

func (r *PausedRepo) DeleteResolvedOlderThan(ctx context.Context, cutoff int64, limit int) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM workflow_paused WHERE request_id IN (
			SELECT request_id FROM workflow_paused
			WHERE status != 'paused' AND resolved_at IS NOT NULL AND resolved_at < ? LIMIT ?)`,
		cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("delete resolved paused workflows: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}
