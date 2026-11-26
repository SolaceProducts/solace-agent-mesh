// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	RunHeld   = "held"
	RunParked = "parked"
	RunEnded  = "ended"
)

const HeldByRequest = ""

var ErrNotHolder = errors.New("workflow run is not held by the caller")

type Holder struct {
	Key   string
	Epoch int64
}

func (h Holder) stored() int64 { return max(h.Epoch, 1) }

func (h Holder) holds(run *WorkflowRun) bool {
	return run != nil && run.Status == RunHeld && run.HeldBy == h.Key && run.HolderEpoch == h.stored()
}

var ErrPauseClaimed = errors.New("a pause listed as open was claimed")

type WorkflowRun struct {
	ExecutionID    string `db:"execution_id"`
	TaskID         string `db:"task_id"`
	WorkflowName   string `db:"workflow_name"`
	DefinitionHash string `db:"definition_hash"`
	StateSnapshot  string `db:"state_snapshot"`
	ExecContext    string `db:"exec_context"`
	DeadlineAt     *int64 `db:"deadline_at"`
	WakeAt         *int64 `db:"wake_at"`
	Status         string `db:"status"`
	HeldBy         string `db:"held_by"`
	HolderEpoch    int64  `db:"holder_epoch"`
	OpenPauses     string `db:"open_pauses"`
	CreatedAt      int64  `db:"created_at"`
	UpdatedAt      int64  `db:"updated_at"`
	AnsweredAt     *int64 `db:"answered_at"`
	EndedAt        *int64 `db:"ended_at"`
}

type RunState struct {
	TaskID         string
	WorkflowName   string
	DefinitionHash string
	StateSnapshot  string
	ExecContext    string
	DeadlineAt     *int64
	WakeAt         *int64
	OpenPauses     []string
}

func (s RunState) openPausesJSON() (string, error) {
	ids := s.OpenPauses
	if ids == nil {
		ids = []string{}
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "", fmt.Errorf("encode open pauses: %w", err)
	}
	return string(b), nil
}

func (r *WorkflowRun) OpenPauseIDs() ([]string, error) {
	var ids []string
	if err := json.Unmarshal([]byte(r.OpenPauses), &ids); err != nil {
		return nil, fmt.Errorf("decode open pauses of run %s: %w", r.ExecutionID, err)
	}
	return ids, nil
}

const runColumns = `execution_id, task_id, workflow_name, definition_hash, state_snapshot,
		        exec_context, deadline_at, wake_at, status, held_by, holder_epoch, open_pauses, created_at,
		        updated_at, answered_at, ended_at`

func (r *WorkflowRun) Holder() Holder { return Holder{Key: r.HeldBy, Epoch: r.HolderEpoch} }

type RunRepo struct {
	db *DB
}

func NewRunRepo(db *DB) *RunRepo { return &RunRepo{db: db} }

func (r *PausedRepo) Runs() *RunRepo { return &RunRepo{db: r.db} }

func (r *RunRepo) lockRun(ctx context.Context, tx *dbutil.Tx, executionID string) (*WorkflowRun, error) {
	q := `SELECT ` + runColumns + ` FROM workflow_checkpoint WHERE execution_id = ?`
	if r.db.Dialect() == "postgres" {
		q += " FOR UPDATE"
	}
	run, err := dbutil.QueryRow[WorkflowRun](ctx, tx, q, executionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return run, err
}

func (r *RunRepo) lockedPause(ctx context.Context, tx *dbutil.Tx, requestID string) (*PausedWorkflow, *WorkflowRun, error) {
	pause, err := getPause(ctx, tx, requestID)
	if err != nil || pause == nil {
		return nil, nil, err
	}
	run, err := r.lockRun(ctx, tx, pause.ExecutionID)
	return pause, run, err
}

func getPause(ctx context.Context, q dbutil.Querier, requestID string) (*PausedWorkflow, error) {
	p, err := dbutil.QueryRow[PausedWorkflow](ctx, q,
		`SELECT `+pausedColumns+` FROM workflow_paused WHERE request_id = ?`, requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

type RunWake struct {
	Status string `db:"status"`
	WakeAt *int64 `db:"wake_at"`
}

func (r *RunRepo) WakeState(ctx context.Context, executionID string) (*RunWake, error) {
	run, err := dbutil.QueryRow[RunWake](ctx, r.db,
		`SELECT status, wake_at FROM workflow_checkpoint WHERE execution_id = ?`, executionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow run wake state: %w", err)
	}
	return run, nil
}

func (r *RunRepo) Get(ctx context.Context, executionID string) (*WorkflowRun, error) {
	run, err := dbutil.QueryRow[WorkflowRun](ctx, r.db,
		`SELECT `+runColumns+` FROM workflow_checkpoint WHERE execution_id = ?`, executionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow run: %w", err)
	}
	return run, nil
}

func createRun(ctx context.Context, q dbutil.Querier, executionID, taskID, workflowName string, h Holder) (bool, error) {
	now := nowMillis()
	res, err := q.ExecContext(ctx,
		`INSERT INTO workflow_checkpoint (execution_id, task_id, workflow_name, status, held_by, holder_epoch, created_at, updated_at)
		 VALUES (?, ?, ?, 'held', ?, ?, ?, ?)
		 ON CONFLICT (execution_id) DO NOTHING`,
		executionID, taskID, workflowName, h.Key, h.stored(), now, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (r *RunRepo) EnsureRun(ctx context.Context, executionID, taskID, workflowName, heldBy string) (Holder, error) {
	created, err := createRun(ctx, r.db, executionID, taskID, workflowName, Holder{Key: heldBy})
	if err != nil {
		return Holder{}, fmt.Errorf("ensure workflow run: %w", err)
	}
	if !created {
		return Holder{}, ErrNotHolder
	}
	return Holder{Key: heldBy, Epoch: 1}, nil
}

func (r *RunRepo) InsertPause(ctx context.Context, p PausedWorkflow, h Holder) (Holder, error) {
	held, _, err := r.insertPause(ctx, p, h, false)
	return held, err
}

func (r *RunRepo) InsertSignalPause(ctx context.Context, p PausedWorkflow, h Holder) (Holder, *PausedWorkflow, error) {
	return r.insertPause(ctx, p, h, true)
}

func answeredBySameExecution(existing *PausedWorkflow, executionID string) bool {
	return existing != nil && existing.Answer != nil && existing.ExecutionID == executionID &&
		existing.Status != StatusPaused && existing.Status != StatusAnswered
}

func (r *RunRepo) insertPause(ctx context.Context, p PausedWorkflow, h Holder, replaceClosed bool) (Holder, *PausedWorkflow, error) {
	var alreadyAnswered *PausedWorkflow
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		run, err := r.lockRun(ctx, tx, p.ExecutionID)
		if err != nil {
			return err
		}
		switch {
		case run == nil:
			created, createErr := createRun(ctx, tx, p.ExecutionID, p.TaskID, p.WorkflowName, h)
			if createErr != nil {
				return createErr
			}
			if !created {
				return ErrNotHolder
			}
		case !h.holds(run):
			return ErrNotHolder
		default:
			if _, err = tx.ExecContext(ctx,
				`UPDATE workflow_checkpoint SET updated_at = ? WHERE execution_id = ?`, nowMillis(), p.ExecutionID); err != nil {
				return err
			}
		}
		h.Epoch = h.stored()
		if replaceClosed {
			existing, getErr := getPause(ctx, tx, p.RequestID)
			if getErr != nil {
				return getErr
			}
			if answeredBySameExecution(existing, p.ExecutionID) {
				alreadyAnswered = existing
				return nil
			}
			if _, err = tx.ExecContext(ctx,
				`DELETE FROM workflow_paused WHERE request_id = ? AND status NOT IN `+openStatuses, p.RequestID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO workflow_paused
			   (request_id, execution_id, task_id, workflow_name, node_id, requester_addr,
			    recipient_user_id, state_snapshot, exec_context, routing, status,
			    expires_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?, 'paused', ?, ?)`,
			p.RequestID, p.ExecutionID, p.TaskID, p.WorkflowName, p.NodeID, p.RequesterAddr,
			p.RecipientUserID, p.ExecContext, p.Routing, p.ExpiresAt, nowMillis())
		return err
	})
	if errors.Is(err, ErrNotHolder) {
		return Holder{}, nil, err
	}
	if err != nil {
		return Holder{}, nil, fmt.Errorf("insert workflow pause: %w", err)
	}
	return h, alreadyAnswered, nil
}

func (r *RunRepo) Park(ctx context.Context, executionID string, h Holder, st RunState) (answered []PausedWorkflow, err error) {
	open, err := st.openPausesJSON()
	if err != nil {
		return nil, err
	}
	err = r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		run, lockErr := r.lockRun(ctx, tx, executionID)
		if lockErr != nil {
			return lockErr
		}
		if run != nil && run.Status == RunParked && run.HolderEpoch == h.stored() &&
			run.StateSnapshot == st.StateSnapshot && run.OpenPauses == open {
			return nil
		}
		if run == nil {
			created, createErr := createRun(ctx, tx, executionID, st.TaskID, st.WorkflowName, h)
			if createErr != nil {
				return createErr
			}
			if !created {
				return ErrNotHolder
			}
		} else if !h.holds(run) {
			return ErrNotHolder
		}
		claimed, claimErr := anyPauseClosed(ctx, tx, st.OpenPauses)
		if claimErr != nil {
			return claimErr
		}
		if claimed {
			return ErrPauseClaimed
		}
		var readErr error
		answered, readErr = dbutil.QueryRows[PausedWorkflow](ctx, tx,
			`SELECT `+pausedColumns+` FROM workflow_paused
			 WHERE execution_id = ? AND status = 'answered' ORDER BY created_at ASC`, executionID)
		if readErr != nil || len(answered) > 0 {
			return readErr
		}
		_, writeErr := tx.ExecContext(ctx,
			`UPDATE workflow_checkpoint
			 SET definition_hash = ?, state_snapshot = ?, exec_context = ?, deadline_at = ?, wake_at = ?,
			     status = 'parked', held_by = '', open_pauses = ?, updated_at = ?
			 WHERE execution_id = ?`,
			st.DefinitionHash, st.StateSnapshot, st.ExecContext, st.DeadlineAt, st.WakeAt, open, nowMillis(), executionID)
		return writeErr
	})
	if errors.Is(err, ErrNotHolder) || errors.Is(err, ErrPauseClaimed) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("park workflow run: %w", err)
	}
	return answered, nil
}

func anyPauseClosed(ctx context.Context, tx *dbutil.Tx, ids []string) (bool, error) {
	for chunk := range slices.Chunk(ids, dbutil.InClauseCap) {
		placeholders, args, err := dbutil.InClause(chunk)
		if err != nil {
			return false, err
		}
		type closedRow struct {
			RequestID string `db:"request_id"`
		}
		closed, err := dbutil.QueryRow[closedRow](ctx, tx,
			`SELECT request_id FROM workflow_paused WHERE request_id IN (`+placeholders+`) AND status NOT IN `+openStatuses+` LIMIT 1`, args...)
		if err != nil {
			return false, err
		}
		if closed != nil {
			return true, nil
		}
	}
	return false, nil
}

func (r *RunRepo) WriteStep(ctx context.Context, executionID string, h Holder, st RunState) (Holder, error) {
	open, err := st.openPausesJSON()
	if err != nil {
		return Holder{}, err
	}
	h.Epoch = h.stored()
	now := nowMillis()
	res, err := r.db.ExecContext(ctx,
		`UPDATE workflow_checkpoint
		 SET definition_hash = ?, state_snapshot = ?, exec_context = ?, deadline_at = ?, wake_at = ?,
		     open_pauses = ?, updated_at = ?
		 WHERE execution_id = ? AND status = 'held' AND held_by = ? AND holder_epoch = ?`,
		st.DefinitionHash, st.StateSnapshot, st.ExecContext, st.DeadlineAt, st.WakeAt, open, now,
		executionID, h.Key, h.Epoch)
	if err != nil {
		return Holder{}, fmt.Errorf("write workflow run state: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil || n > 0 {
		return h, err
	}
	res, err = r.db.ExecContext(ctx,
		`INSERT INTO workflow_checkpoint
		   (execution_id, task_id, workflow_name, definition_hash, state_snapshot, exec_context,
		    deadline_at, wake_at, status, held_by, holder_epoch, open_pauses, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'held', ?, ?, ?, ?, ?)
		 ON CONFLICT (execution_id) DO NOTHING`,
		executionID, st.TaskID, st.WorkflowName, st.DefinitionHash, st.StateSnapshot, st.ExecContext,
		st.DeadlineAt, st.WakeAt, h.Key, h.Epoch, open, now, now)
	if err != nil {
		return Holder{}, fmt.Errorf("write workflow run state: %w", err)
	}
	if n, err = res.RowsAffected(); err != nil {
		return Holder{}, fmt.Errorf("write workflow run state rows affected: %w", err)
	}
	if n == 0 {
		return Holder{}, ErrNotHolder
	}
	return h, nil
}

func (r *RunRepo) Fence(ctx context.Context, executionID string, h Holder) (bool, error) {
	run, err := dbutil.QueryRow[WorkflowRun](ctx, r.db,
		`SELECT status, held_by, holder_epoch FROM workflow_checkpoint WHERE execution_id = ?`, executionID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("fence workflow run: %w", err)
	}
	return run == nil || h.holds(run), nil
}

func (r *RunRepo) LiveRunForTask(ctx context.Context, workflowName, taskID string) (*WorkflowRun, error) {
	run, err := dbutil.QueryRow[WorkflowRun](ctx, r.db,
		`SELECT `+runColumns+` FROM workflow_checkpoint
		 WHERE task_id = ? AND workflow_name = ? AND status != 'ended' ORDER BY updated_at DESC LIMIT 1`, taskID, workflowName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("live workflow run for task: %w", err)
	}
	return run, nil
}

func (r *RunRepo) TakeOverParked(ctx context.Context, workflowName, executionID, heldBy string) (*WorkflowRun, bool, error) {
	var run *WorkflowRun
	var took bool
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var err error
		if run, err = r.lockRun(ctx, tx, executionID); err != nil || run == nil {
			return err
		}
		if run.WorkflowName != workflowName {
			run = nil
			return nil
		}
		if run.Status != RunParked {
			return nil
		}
		took = true
		return holdRun(ctx, tx, run, heldBy)
	})
	if err != nil {
		return nil, false, fmt.Errorf("take over workflow run: %w", err)
	}
	return run, took, nil
}

func holdRun(ctx context.Context, tx *dbutil.Tx, run *WorkflowRun, heldBy string) error {
	run.Status, run.HeldBy = RunHeld, heldBy
	run.HolderEpoch++
	_, err := tx.ExecContext(ctx,
		`UPDATE workflow_checkpoint SET status = 'held', held_by = ?, holder_epoch = ?, updated_at = ? WHERE execution_id = ?`,
		heldBy, run.HolderEpoch, nowMillis(), run.ExecutionID)
	return err
}

type AnswerOutcome int

const (
	AnswerSettled AnswerOutcome = iota
	AnswerTookOver
	AnswerRecorded
	AnswerHoldsRun
	AnswerNoRun
	AnswerOtherWorkflow
)

type AnswerResult struct {
	Outcome AnswerOutcome
	Run     *WorkflowRun
	Pause   *PausedWorkflow
}

func (r *RunRepo) AcceptAnswer(ctx context.Context, workflowName, requestID, claimStatus, answerJSON string) (AnswerResult, error) {
	var res AnswerResult
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		pause, run, err := r.lockedPause(ctx, tx, requestID)
		if err != nil || pause == nil {
			return err
		}
		if pause.WorkflowName != workflowName {
			res.Outcome = AnswerOtherWorkflow
			return nil
		}
		res.Pause = pause
		switch {
		case run == nil && pause.StateSnapshot == "":
			_, err := resolvePause(ctx, tx, pause, "cancelled")
			return err
		case run == nil:
			res.Outcome = AnswerNoRun
			return nil
		}
		res.Run = run
		switch {
		case run.Status == RunHeld && run.HeldBy == requestID:
			res.Outcome = AnswerHoldsRun
		case pause.Status != StatusPaused:
		case run.Status == RunEnded:
			_, err := resolvePause(ctx, tx, pause, "cancelled")
			return err
		case run.Status == RunParked:
			resolved, err := resolvePause(ctx, tx, pause, claimStatus)
			if err != nil || !resolved {
				return err
			}
			if err := holdRun(ctx, tx, run, requestID); err != nil {
				return err
			}
			res.Outcome = AnswerTookOver
		case run.Status == RunHeld:
			written, err := tx.ExecContext(ctx,
				`UPDATE workflow_paused SET status = 'answered', answer = ?
				 WHERE request_id = ? AND status = 'paused'`, answerJSON, requestID)
			if err != nil {
				return err
			}
			if n, err := written.RowsAffected(); err != nil || n == 0 {
				return err
			}
			pause.Status, pause.Answer = StatusAnswered, &answerJSON
			res.Outcome = AnswerRecorded
		}
		return nil
	})
	if err != nil {
		return AnswerResult{}, fmt.Errorf("accept workflow answer: %w", err)
	}
	return res, nil
}

func resolvePause(ctx context.Context, tx *dbutil.Tx, pause *PausedWorkflow, status string) (bool, error) {
	now := nowMillis()
	res, err := tx.ExecContext(ctx,
		`UPDATE workflow_paused SET status = ?, resolved_at = ?
		 WHERE request_id = ? AND status IN `+openStatuses,
		status, now, pause.RequestID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	pause.Status, pause.ResolvedAt = status, &now
	return true, nil
}

func (r *RunRepo) ClaimHeld(ctx context.Context, requestID string, h Holder, claimStatus string) (pause *PausedWorkflow, claimed bool, err error) {
	err = r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var run *WorkflowRun
		var lockErr error
		if pause, run, lockErr = r.lockedPause(ctx, tx, requestID); lockErr != nil || pause == nil {
			return lockErr
		}
		if !h.holds(run) {
			return ErrNotHolder
		}
		var resolveErr error
		claimed, resolveErr = resolvePause(ctx, tx, pause, claimStatus)
		return resolveErr
	})
	if errors.Is(err, ErrNotHolder) {
		return nil, false, err
	}
	if err != nil {
		return nil, false, fmt.Errorf("claim held workflow pause: %w", err)
	}
	return pause, claimed, nil
}

const (
	reopenAnswered    = `status = 'answered', resolved_at = NULL`
	claimedWithAnswer = `status IN ('resumed', 'timed_out') AND answer IS NOT NULL`
)

func (r *RunRepo) ReleaseClaim(ctx context.Context, requestID string, h Holder) error {
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		pause, run, err := r.lockedPause(ctx, tx, requestID)
		if err != nil || pause == nil {
			return err
		}
		if !h.holds(run) {
			return ErrNotHolder
		}
		_, err = updatePauses(ctx, tx, []string{requestID}, reopenAnswered,
			claimedWithAnswer)
		return err
	})
	if err != nil {
		return fmt.Errorf("release workflow pause claim: %w", err)
	}
	return nil
}

func (r *RunRepo) Retake(ctx context.Context, executionID, heldBy, closedStatus string) (*WorkflowRun, int64, error) {
	var run *WorkflowRun
	var closed int64
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var err error
		if run, err = r.lockRun(ctx, tx, executionID); err != nil {
			return err
		}
		if run == nil || run.Status != RunHeld || run.HeldBy != heldBy {
			return ErrNotHolder
		}
		ids, err := run.OpenPauseIDs()
		if err != nil {
			return err
		}
		toClose, toReopen, err := retakePauses(ctx, tx, executionID, heldBy, ids)
		if err != nil {
			return err
		}
		now := nowMillis()
		closed, err = updatePauses(ctx, tx, toClose, `status = ?, resolved_at = ?`, `status IN `+openStatuses, closedStatus, now)
		if err != nil {
			return err
		}
		if _, err = updatePauses(ctx, tx, toReopen, reopenAnswered,
			claimedWithAnswer); err != nil {
			return err
		}
		return holdRun(ctx, tx, run, heldBy)
	})
	if errors.Is(err, ErrNotHolder) {
		return nil, 0, err
	}
	if err != nil {
		return nil, 0, fmt.Errorf("retake workflow run: %w", err)
	}
	return run, closed, nil
}

func retakePauses(ctx context.Context, tx *dbutil.Tx, executionID, heldBy string, openAtPark []string) (toClose, toReopen []string, err error) {
	type pauseRow struct {
		RequestID string  `db:"request_id"`
		Status    string  `db:"status"`
		Answer    *string `db:"answer"`
	}
	rows, err := dbutil.QueryRows[pauseRow](ctx, tx,
		`SELECT request_id, status, answer FROM workflow_paused WHERE execution_id = ?`, executionID)
	if err != nil {
		return nil, nil, err
	}
	for _, p := range rows {
		parked := slices.Contains(openAtPark, p.RequestID)
		switch {
		case !parked && (p.Status == StatusPaused || p.Status == StatusAnswered):
			toClose = append(toClose, p.RequestID)
		case parked && p.RequestID != heldBy && p.Answer != nil && (p.Status == "resumed" || p.Status == "timed_out"):
			toReopen = append(toReopen, p.RequestID)
		}
	}
	return toClose, toReopen, nil
}

func updatePauses(ctx context.Context, tx *dbutil.Tx, ids []string, set, where string, args ...any) (int64, error) {
	var n int64
	for chunk := range slices.Chunk(ids, dbutil.InClauseCap) {
		placeholders, idArgs, err := dbutil.InClause(chunk)
		if err != nil {
			return n, err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE workflow_paused SET `+set+` WHERE `+where+` AND request_id IN (`+placeholders+`)`,
			append(slices.Clone(args), idArgs...)...)
		if err != nil {
			return n, err
		}
		changed, err := res.RowsAffected()
		if err != nil {
			return n, err
		}
		n += changed
	}
	return n, nil
}

type RequestOutcome int

const (
	RequestNoRun RequestOutcome = iota
	RequestCarried
	RequestRestart
	RequestRestore
	RequestAnswered
)

type RequestResult struct {
	Outcome RequestOutcome
	Run     *WorkflowRun
	Closed  int64
}

func (r *RunRepo) SettleTaskRequest(ctx context.Context, taskID, closedStatus string, restore bool) (RequestResult, error) {
	var res RequestResult
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		q := `SELECT ` + runColumns + ` FROM workflow_checkpoint WHERE task_id = ? AND status != 'ended'`
		if r.db.Dialect() == "postgres" {
			q += " FOR UPDATE"
		}
		runs, err := dbutil.QueryRows[WorkflowRun](ctx, tx, q, taskID)
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			answered, err := dbutil.QueryRow[WorkflowRun](ctx, tx,
				`SELECT `+runColumns+` FROM workflow_checkpoint
				 WHERE task_id = ? AND answered_at IS NOT NULL ORDER BY answered_at DESC LIMIT 1`, taskID)
			if err != nil || answered == nil {
				return err
			}
			res.Outcome, res.Run = RequestAnswered, answered
			return nil
		}
		for i := range runs {
			if runs[i].Status == RunParked || runs[i].HeldBy != HeldByRequest {
				res.Outcome, res.Run = RequestCarried, &runs[i]
				return nil
			}
		}
		if restore && len(runs) == 1 && runs[0].StateSnapshot != "" {
			res.Outcome, res.Run = RequestRestore, &runs[0]
			return nil
		}
		res.Outcome = RequestRestart
		for i := range runs {
			n, err := restartRun(ctx, tx, runs[i].ExecutionID, closedStatus)
			if err != nil {
				return err
			}
			res.Closed += n
		}
		return nil
	})
	if err != nil {
		return RequestResult{}, fmt.Errorf("settle workflow task request: %w", err)
	}
	return res, nil
}

func restartRun(ctx context.Context, tx *dbutil.Tx, executionID, closedStatus string) (int64, error) {
	now := nowMillis()
	res, err := tx.ExecContext(ctx,
		`UPDATE workflow_paused SET status = ?, resolved_at = ?
		 WHERE execution_id = ? AND status IN `+openStatuses,
		closedStatus, now, executionID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, endRun(ctx, tx, executionID, now)
}

func (r *RunRepo) Restart(ctx context.Context, executionID, closedStatus string) (int64, error) {
	var closed int64
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		run, err := r.lockRun(ctx, tx, executionID)
		if err != nil || run == nil || run.Status != RunHeld || run.HeldBy != HeldByRequest {
			return err
		}
		closed, err = restartRun(ctx, tx, executionID, closedStatus)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("restart workflow run: %w", err)
	}
	return closed, nil
}

func endRun(ctx context.Context, q dbutil.Querier, executionID string, now int64) error {
	_, err := q.ExecContext(ctx,
		`UPDATE workflow_checkpoint SET status = 'ended', held_by = '', ended_at = ?, updated_at = ?
		 WHERE execution_id = ? AND status != 'ended'`,
		now, now, executionID)
	return err
}

func (r *RunRepo) End(ctx context.Context, executionID string, h Holder) error {
	now := nowMillis()
	if _, err := r.db.ExecContext(ctx,
		`UPDATE workflow_checkpoint SET status = 'ended', held_by = '', ended_at = ?, updated_at = ?
		 WHERE execution_id = ? AND status = 'held' AND held_by = ? AND holder_epoch = ?`,
		now, now, executionID, h.Key, h.stored()); err != nil {
		return fmt.Errorf("end workflow run: %w", err)
	}
	return nil
}

func (r *RunRepo) MarkAnswered(ctx context.Context, executionID string, h Holder) error {
	now := nowMillis()
	res, err := r.db.ExecContext(ctx,
		`UPDATE workflow_checkpoint
		 SET status = 'ended', held_by = '', answered_at = ?, ended_at = COALESCE(ended_at, ?), updated_at = ?
		 WHERE execution_id = ? AND status = 'held' AND held_by = ? AND holder_epoch = ?`,
		now, now, now, executionID, h.Key, h.stored())
	if err != nil {
		return fmt.Errorf("mark workflow run answered: %w", err)
	}
	if n, countErr := res.RowsAffected(); countErr != nil || n > 0 {
		return countErr
	}
	run, err := r.Get(ctx, executionID)
	switch {
	case err != nil:
		return err
	case run == nil, run.AnsweredAt != nil && run.HolderEpoch == h.stored():
		return nil
	}
	return ErrNotHolder
}

func rowsDeleted(res sql.Result) (int64, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

func (r *RunRepo) ParkedPastDeadline(ctx context.Context, workflowName string, cutoff int64, limit int) ([]string, error) {
	type idRow struct {
		ExecutionID string `db:"execution_id"`
	}
	rows, err := dbutil.QueryRows[idRow](ctx, r.db,
		`SELECT execution_id FROM workflow_checkpoint
		 WHERE workflow_name = ? AND status = 'parked' AND deadline_at IS NOT NULL AND deadline_at < ?
		 ORDER BY deadline_at ASC LIMIT ?`, workflowName, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("parked workflow runs past deadline: %w", err)
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ExecutionID
	}
	return ids, nil
}

const uuidBeforeEveryExecutionID = "00000000-0000-0000-0000-000000000000"

type WakeCursor struct {
	WakeAt      int64
	ExecutionID string
}

func (r *RunRepo) ParkedDueToWake(ctx context.Context, workflowName string, cutoff int64, after WakeCursor, limit int) ([]string, WakeCursor, error) {
	type dueRow struct {
		ExecutionID string `db:"execution_id"`
		WakeAt      int64  `db:"wake_at"`
	}
	afterID := after.ExecutionID
	if afterID == "" {
		afterID = uuidBeforeEveryExecutionID
	}
	rows, err := dbutil.QueryRows[dueRow](ctx, r.db,
		`SELECT execution_id, wake_at FROM workflow_checkpoint
		 WHERE status = 'parked' AND ended_at IS NULL AND workflow_name = ? AND wake_at IS NOT NULL AND wake_at <= ?
		   AND (wake_at > ? OR (wake_at = ? AND execution_id > ?))
		 ORDER BY wake_at ASC, execution_id ASC LIMIT ?`,
		workflowName, cutoff, after.WakeAt, after.WakeAt, afterID, limit)
	if err != nil {
		return nil, after, fmt.Errorf("parked workflow runs due to wake: %w", err)
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].ExecutionID
	}
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		after = WakeCursor{WakeAt: last.WakeAt, ExecutionID: last.ExecutionID}
	}
	return ids, after, nil
}

func (r *RunRepo) DeleteHeldPastDeadline(ctx context.Context, cutoff int64, limit int) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM workflow_checkpoint WHERE execution_id IN (
			SELECT execution_id FROM workflow_checkpoint
			WHERE status = 'held' AND deadline_at IS NOT NULL AND deadline_at < ? LIMIT ?)`,
		cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("delete held workflow runs past deadline: %w", err)
	}
	return rowsDeleted(res)
}

func (r *RunRepo) DeleteHeldIdleOlderThan(ctx context.Context, cutoff int64, limit int) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM workflow_checkpoint WHERE execution_id IN (
			SELECT execution_id FROM workflow_checkpoint
			WHERE status = 'held' AND deadline_at IS NULL AND updated_at < ?
			  AND NOT EXISTS (SELECT 1 FROM workflow_paused p
			                  WHERE p.execution_id = workflow_checkpoint.execution_id AND p.status IN ('paused', 'answered'))
			LIMIT ?)`,
		cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("delete idle held workflow runs: %w", err)
	}
	return rowsDeleted(res)
}

func (r *RunRepo) DeleteEndedOlderThan(ctx context.Context, cutoff int64, limit int) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM workflow_checkpoint WHERE execution_id IN (
			SELECT execution_id FROM workflow_checkpoint
			WHERE status = 'ended' AND ended_at < ? LIMIT ?)`,
		cutoff, limit)
	if err != nil {
		return 0, fmt.Errorf("delete ended workflow runs: %w", err)
	}
	return rowsDeleted(res)
}
