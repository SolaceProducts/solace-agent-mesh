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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

type TaskRecord struct {
	TaskID             string  `db:"task_id"`
	GatewayID          string  `db:"gateway_id"`
	AgentName          string  `db:"agent_name"`
	UserID             string  `db:"user_id"`
	SessionID          string  `db:"session_id"`
	TraceID            string  `db:"trace_id"`
	Trigger            string  `db:"trigger_kind"`
	SystemInitiated    bool    `db:"system_initiated"`
	MachineIngress     bool    `db:"machine_ingress"`
	Surface            string  `db:"surface"`
	AgentType          string  `db:"agent_type"`
	AnalyticsSessionID string  `db:"analytics_session_id"`
	ModelFamily        string  `db:"model_family"`
	ModelProvider      string  `db:"model_provider"`
	ModelServedFrom    string  `db:"model_served_from"`
	RunReported        bool    `db:"run_reported"`
	LastSeenAt         int64   `db:"last_seen_at"`
	CancelRequestedAt  *int64  `db:"cancel_requested_at"`
	CancelClaimedAt    *int64  `db:"cancel_claimed_at"`
	CreatedAt          int64   `db:"created_at"`
	GatewayInstanceID  string  `db:"gateway_instance_id"`
	WaitingRequestID   *string `db:"waiting_request_id"`
}

func (r *TaskRecord) WaitingOn() string {
	if r.WaitingRequestID == nil {
		return ""
	}
	return *r.WaitingRequestID
}

const taskRecordColumns = `task_id, gateway_id, agent_name, user_id, session_id, trace_id, trigger_kind,
	system_initiated, machine_ingress, surface, agent_type, analytics_session_id,
	model_family, model_provider, model_served_from, run_reported,
	last_seen_at, cancel_requested_at, cancel_claimed_at, created_at,
	gateway_instance_id, waiting_request_id`

const CancelClaimLease = 2 * time.Minute

type TaskRecordRepo struct {
	db        *DB
	gatewayID string
}

func NewTaskRecordRepo(db *DB, gatewayID string) *TaskRecordRepo {
	return &TaskRecordRepo{db: db, gatewayID: gatewayID}
}

func (r *TaskRecordRepo) Upsert(ctx context.Context, rec *TaskRecord) (*TaskRecord, error) {
	if rec.CreatedAt == 0 {
		rec.CreatedAt = time.Now().UnixMilli()
	}
	if rec.LastSeenAt == 0 {
		rec.LastSeenAt = rec.CreatedAt
	}
	rec.GatewayID = r.gatewayID
	row, err := dbutil.QueryRow[TaskRecord](ctx, r.db,
		`INSERT INTO task_records (`+taskRecordColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (task_id) DO UPDATE SET
		   last_seen_at         = EXCLUDED.last_seen_at,
		   trace_id             = CASE WHEN task_records.trace_id = ''     THEN EXCLUDED.trace_id             ELSE task_records.trace_id END,
		   session_id           = CASE WHEN task_records.session_id = ''   THEN EXCLUDED.session_id           ELSE task_records.session_id END,
		   trigger_kind         = CASE WHEN task_records.trigger_kind = '' THEN EXCLUDED.trigger_kind         ELSE task_records.trigger_kind END,
		   system_initiated     = CASE WHEN task_records.trigger_kind = '' THEN EXCLUDED.system_initiated     ELSE task_records.system_initiated END,
		   machine_ingress      = CASE WHEN task_records.trigger_kind = '' THEN EXCLUDED.machine_ingress      ELSE task_records.machine_ingress END,
		   surface              = CASE WHEN task_records.trigger_kind = '' THEN EXCLUDED.surface              ELSE task_records.surface END,
		   agent_type           = CASE WHEN task_records.trigger_kind = '' THEN EXCLUDED.agent_type           ELSE task_records.agent_type END,
		   analytics_session_id = CASE WHEN task_records.trigger_kind = '' THEN EXCLUDED.analytics_session_id ELSE task_records.analytics_session_id END
		 RETURNING `+taskRecordColumns,
		rec.TaskID, rec.GatewayID, rec.AgentName, rec.UserID, rec.SessionID, rec.TraceID, rec.Trigger,
		dbutil.BoolToInt(rec.SystemInitiated), dbutil.BoolToInt(rec.MachineIngress),
		rec.Surface, rec.AgentType, rec.AnalyticsSessionID,
		rec.ModelFamily, rec.ModelProvider, rec.ModelServedFrom,
		dbutil.BoolToInt(rec.RunReported), rec.LastSeenAt, rec.CancelRequestedAt, rec.CancelClaimedAt, rec.CreatedAt,
		rec.GatewayInstanceID, rec.WaitingRequestID,
	)
	if err != nil {
		return nil, fmt.Errorf("TaskRecordRepo.Upsert(%s): %w", rec.TaskID, err)
	}
	if row == nil {
		return nil, fmt.Errorf("TaskRecordRepo.Upsert(%s): no row returned", rec.TaskID)
	}
	return row, nil
}

func (r *TaskRecordRepo) Get(ctx context.Context, taskID string) (*TaskRecord, error) {
	rec, err := dbutil.QueryRow[TaskRecord](ctx, r.db,
		`SELECT `+taskRecordColumns+` FROM task_records WHERE task_id = ?`, taskID)
	if err != nil {
		return nil, fmt.Errorf("TaskRecordRepo.Get(%s): %w", taskID, err)
	}
	return rec, nil
}

func (r *TaskRecordRepo) SettleModel(ctx context.Context, taskID string, candidate usagefacts.Model) error {
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		selectSQL := `SELECT model_family, model_provider, model_served_from FROM task_records WHERE task_id = ?`
		if tx.Dialect() == "postgres" {
			selectSQL += ` FOR UPDATE`
		}
		var current usagefacts.Model
		row := tx.QueryRowContext(ctx, selectSQL, taskID)
		if err := row.Scan(&current.Family, &current.Provider, &current.ServedFrom); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		settled := usagefacts.SettleModel(current, candidate)
		if settled == current {
			return nil
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE task_records SET model_family = ?, model_provider = ?, model_served_from = ? WHERE task_id = ?`,
			settled.Family, settled.Provider, settled.ServedFrom, taskID)
		return err
	})
	if err != nil {
		return fmt.Errorf("TaskRecordRepo.SettleModel(%s): %w", taskID, err)
	}
	return nil
}

func (r *TaskRecordRepo) Exists(ctx context.Context, taskID string) (bool, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT 1 FROM task_records WHERE task_id = ?`, taskID)
	if err != nil {
		return false, fmt.Errorf("TaskRecordRepo.Exists(%s): %w", taskID, err)
	}
	return n == 1, nil
}

func (r *TaskRecordRepo) ClaimRunReport(ctx context.Context, taskID string) (*TaskRecord, error) {
	rec, err := dbutil.QueryRow[TaskRecord](ctx, r.db,
		`UPDATE task_records SET run_reported = 1
		 WHERE task_id = ? AND run_reported = 0
		 RETURNING `+taskRecordColumns, taskID)
	if err != nil {
		return nil, fmt.Errorf("TaskRecordRepo.ClaimRunReport(%s): %w", taskID, err)
	}
	return rec, nil
}

func (r *TaskRecordRepo) Delete(ctx context.Context, taskID string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM task_records WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("TaskRecordRepo.Delete(%s): %w", taskID, err)
	}
	return nil
}

func (r *TaskRecordRepo) StampSeen(ctx context.Context, ids []string) ([]string, error) {
	placeholders, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("TaskRecordRepo.StampSeen: %w", err)
	}
	if placeholders == "" {
		return nil, nil
	}
	seen, err := dbutil.QueryVals[string](ctx, r.db,
		`UPDATE task_records SET last_seen_at = ? WHERE task_id IN (`+placeholders+`) RETURNING task_id`,
		append([]any{time.Now().UnixMilli()}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("TaskRecordRepo.StampSeen: %w", err)
	}
	return seen, nil
}

func (r *TaskRecordRepo) ExistingIDs(ctx context.Context, ids []string) ([]string, error) {
	placeholders, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("TaskRecordRepo.ExistingIDs: %w", err)
	}
	if placeholders == "" {
		return nil, nil
	}
	existing, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT task_id FROM task_records WHERE task_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("TaskRecordRepo.ExistingIDs: %w", err)
	}
	return existing, nil
}

func (r *TaskRecordRepo) Take(ctx context.Context, taskID, instanceID string) (*TaskRecord, error) {
	rec, err := dbutil.QueryRow[TaskRecord](ctx, r.db,
		`UPDATE task_records SET gateway_instance_id = ?, last_seen_at = ?
		 WHERE task_id = ? AND gateway_id = ?
		 RETURNING `+taskRecordColumns, instanceID, time.Now().UnixMilli(), taskID, r.gatewayID)
	if err != nil {
		return nil, fmt.Errorf("TaskRecordRepo.Take(%s): %w", taskID, err)
	}
	return rec, nil
}

func (r *TaskRecordRepo) TakeAll(ctx context.Context, instanceID string) ([]TaskRecord, error) {
	rows, err := dbutil.QueryRows[TaskRecord](ctx, r.db,
		`UPDATE task_records SET gateway_instance_id = ?, last_seen_at = ?
		 WHERE gateway_id = ?
		 RETURNING `+taskRecordColumns, instanceID, time.Now().UnixMilli(), r.gatewayID)
	if err != nil {
		return nil, fmt.Errorf("TaskRecordRepo.TakeAll: %w", err)
	}
	return rows, nil
}

func (r *TaskRecordRepo) SetWaiting(ctx context.Context, taskID, requestID string) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE task_records SET waiting_request_id = ? WHERE task_id = ?`, requestID, taskID); err != nil {
		return fmt.Errorf("TaskRecordRepo.SetWaiting(%s): %w", taskID, err)
	}
	return nil
}

func (r *TaskRecordRepo) ClearWaiting(ctx context.Context, taskID, requestID string) (bool, error) {
	if requestID == "" {
		return false, nil
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE task_records SET waiting_request_id = ''
		 WHERE task_id = ? AND (waiting_request_id IS NULL OR waiting_request_id = ?)`,
		taskID, requestID)
	if err != nil {
		return false, fmt.Errorf("TaskRecordRepo.ClearWaiting(%s): %w", taskID, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("TaskRecordRepo.ClearWaiting(%s) rows affected: %w", taskID, err)
	}
	return n == 1, nil
}

func (r *TaskRecordRepo) ClaimCancels(ctx context.Context, limit int) ([]TaskRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	now := time.Now()
	expired := now.Add(-CancelClaimLease).UnixMilli()
	rows, err := dbutil.QueryRows[TaskRecord](ctx, r.db,
		`UPDATE task_records SET cancel_claimed_at = ?
		 WHERE task_id IN (
			SELECT task_id FROM task_records
			WHERE gateway_id = ? AND cancel_requested_at IS NOT NULL
			  AND (cancel_claimed_at IS NULL OR cancel_claimed_at < ?)
			LIMIT ?
		 )
		 AND (cancel_claimed_at IS NULL OR cancel_claimed_at < ?)
		 RETURNING `+taskRecordColumns, now.UnixMilli(), r.gatewayID, expired, limit, expired)
	if err != nil {
		return nil, fmt.Errorf("TaskRecordRepo.ClaimCancels: %w", err)
	}
	return rows, nil
}

func (r *TaskRecordRepo) DeleteUnseenBefore(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	beforeMs := before.UnixMilli()
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, fmt.Errorf("TaskRecordRepo.DeleteUnseenBefore: %w", err)
		}
		result, err := r.db.ExecContext(ctx,
			`DELETE FROM task_records WHERE task_id IN (
				SELECT task_id FROM task_records WHERE last_seen_at < ? LIMIT ?
			) AND last_seen_at < ?`, beforeMs, batchSize, beforeMs)
		if err != nil {
			return total, fmt.Errorf("TaskRecordRepo.DeleteUnseenBefore: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("TaskRecordRepo.DeleteUnseenBefore rows affected: %w", err)
		}
		total += n
		if n < int64(batchSize) {
			return total, nil
		}
	}
}
