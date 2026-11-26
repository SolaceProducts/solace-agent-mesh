// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	WhatsAppOutboundQueued     = "queued"
	WhatsAppOutboundSending    = "sending"
	WhatsAppOutboundSent       = "sent"
	WhatsAppOutboundDelivered  = "delivered"
	WhatsAppOutboundRead       = "read"
	WhatsAppOutboundParked     = "parked"
	WhatsAppOutboundFailed     = "failed"
	WhatsAppOutboundSuperseded = "superseded"
)

var whatsAppReceiptRank = map[string]int{
	WhatsAppOutboundSent:      1,
	WhatsAppOutboundDelivered: 2,
	WhatsAppOutboundRead:      3,
}

type WhatsAppOutbound struct {
	ID          string `db:"id"`
	GatewayID   string `db:"gateway_id"`
	Recipient   string `db:"recipient"`
	TaskID      string `db:"task_id"`
	TraceID     string `db:"trace_id"`
	UserID      string `db:"user_id"`
	AgentName   string `db:"agent_name"`
	SessionID   string `db:"session_id"`
	Kind        string `db:"kind"`
	Payload     string `db:"payload"`
	ArtifactRef string `db:"artifact_ref"`
	State       string `db:"state"`
	Wamid       string `db:"wamid"`
	ErrorCode   int    `db:"error_code"`
	ErrorDetail string `db:"error_detail"`
	Attempts    int    `db:"attempts"`
	NotBefore   int64  `db:"not_before"`
	ParkedAt    int64  `db:"parked_at"`
	CreatedAt   int64  `db:"created_at"`
	UpdatedAt   int64  `db:"updated_at"`
}

type WhatsAppOutboundRepo struct {
	db *DB
}

func NewWhatsAppOutboundRepo(db *DB) *WhatsAppOutboundRepo {
	return &WhatsAppOutboundRepo{db: db}
}

const whatsAppOutboundColumns = `id, gateway_id, recipient, task_id, trace_id, user_id, agent_name,
		session_id, kind, payload, artifact_ref, state, wamid, error_code, error_detail,
		attempts, not_before, parked_at, created_at, updated_at`

func (r *WhatsAppOutboundRepo) Enqueue(ctx context.Context, q dbutil.Querier, row *WhatsAppOutbound) error {
	if q == nil {
		q = r.db
	}
	_, err := q.ExecContext(ctx, `INSERT INTO whatsapp_outbound (`+whatsAppOutboundColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.GatewayID, row.Recipient, row.TaskID, row.TraceID, row.UserID, row.AgentName,
		row.SessionID, row.Kind, row.Payload, row.ArtifactRef, row.State, row.Wamid, row.ErrorCode,
		row.ErrorDetail, row.Attempts, row.NotBefore, row.ParkedAt, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return fmt.Errorf("WhatsAppOutboundRepo.Enqueue %s: %w", row.ID, err)
	}
	return nil
}

func (r *WhatsAppOutboundRepo) ClaimNext(ctx context.Context, gatewayID, recipient string, nowMs int64) (row *WhatsAppOutbound, dueAtMs int64, err error) {
	for {
		candidate, err := dbutil.QueryRow[WhatsAppOutbound](ctx, r.db,
			`SELECT `+whatsAppOutboundColumns+` FROM whatsapp_outbound
			 WHERE gateway_id = ? AND state = ? AND recipient = ?
			 ORDER BY id LIMIT 1`,
			gatewayID, WhatsAppOutboundQueued, recipient)
		if err != nil {
			return nil, 0, fmt.Errorf("WhatsAppOutboundRepo.ClaimNext select: %w", err)
		}
		if candidate == nil {
			return nil, 0, nil
		}
		if candidate.NotBefore > nowMs {
			return nil, candidate.NotBefore, nil
		}
		affected, err := r.execCount(ctx,
			`UPDATE whatsapp_outbound SET state = ?, attempts = attempts + 1, updated_at = ?
			 WHERE id = ? AND state = ?`,
			WhatsAppOutboundSending, nowMs, candidate.ID, WhatsAppOutboundQueued)
		if err != nil {
			return nil, 0, fmt.Errorf("WhatsAppOutboundRepo.ClaimNext claim %s: %w", candidate.ID, err)
		}
		if affected == 0 {
			continue
		}
		candidate.State = WhatsAppOutboundSending
		candidate.Attempts++
		candidate.UpdatedAt = nowMs
		return candidate, 0, nil
	}
}

const recoveryScanLimit = 5000

const minUnsettledAge = 24 * time.Hour

var ErrOutboundRowMoved = errors.New("whatsapp outbound row moved out from under the claim")

func (r *WhatsAppOutboundRepo) MarkSent(ctx context.Context, q dbutil.Querier, id, wamid string, nowMs int64) error {
	return r.settle(ctx, q, "MarkSent", id,
		`UPDATE whatsapp_outbound SET state = ?, wamid = ?, error_code = 0, error_detail = '', updated_at = ?
		 WHERE id = ? AND state = ?`,
		WhatsAppOutboundSent, wamid, nowMs, id, WhatsAppOutboundSending)
}

func (r *WhatsAppOutboundRepo) Requeue(ctx context.Context, id string, notBeforeMs, nowMs int64, errorCode int, errorDetail string) error {
	return r.settle(ctx, nil, "Requeue", id,
		`UPDATE whatsapp_outbound SET state = ?, not_before = ?, error_code = ?, error_detail = ?, updated_at = ?
		 WHERE id = ? AND state = ?`,
		WhatsAppOutboundQueued, notBeforeMs, errorCode, errorDetail, nowMs, id, WhatsAppOutboundSending)
}

func (r *WhatsAppOutboundRepo) MarkFailed(ctx context.Context, q dbutil.Querier, id string, errorCode int, errorDetail string, nowMs int64) error {
	return r.settle(ctx, q, "MarkFailed", id,
		`UPDATE whatsapp_outbound SET state = ?, error_code = ?, error_detail = ?, updated_at = ?
		 WHERE id = ? AND state = ?`,
		WhatsAppOutboundFailed, errorCode, errorDetail, nowMs, id, WhatsAppOutboundSending)
}

func (r *WhatsAppOutboundRepo) Supersede(ctx context.Context, id string, nowMs int64) error {
	return r.settle(ctx, nil, "Supersede", id,
		`UPDATE whatsapp_outbound SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		WhatsAppOutboundSuperseded, nowMs, id, WhatsAppOutboundSending)
}

func (r *WhatsAppOutboundRepo) SupersedeQueuedOfKind(ctx context.Context, gatewayID, taskID, kind string, nowMs int64) (int, error) {
	if taskID == "" || kind == "" {
		return 0, nil
	}
	n, err := r.execCount(ctx,
		`UPDATE whatsapp_outbound SET state = ?, updated_at = ?
		 WHERE gateway_id = ? AND task_id = ? AND kind = ? AND state IN (?, ?)`,
		WhatsAppOutboundSuperseded, nowMs, gatewayID, taskID, kind,
		WhatsAppOutboundQueued, WhatsAppOutboundParked)
	if err != nil {
		return 0, fmt.Errorf("WhatsAppOutboundRepo.SupersedeQueuedOfKind %s: %w", taskID, err)
	}
	return n, nil
}

func (r *WhatsAppOutboundRepo) RenewClaims(ctx context.Context, gatewayID string, ids []string, nowMs int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	var total int
	for chunk := range slices.Chunk(ids, dbutil.InClauseCap) {
		placeholders, idArgs, err := dbutil.InClause(chunk)
		if err != nil {
			return total, fmt.Errorf("WhatsAppOutboundRepo.RenewClaims: %w", err)
		}
		args := append([]any{nowMs, gatewayID, WhatsAppOutboundSending}, idArgs...)
		n, err := r.execCount(ctx,
			`UPDATE whatsapp_outbound SET updated_at = ?
			 WHERE gateway_id = ? AND state = ? AND id IN (`+placeholders+`)`, args...)
		if err != nil {
			return total, fmt.Errorf("WhatsAppOutboundRepo.RenewClaims: %w", err)
		}
		total += n
	}
	return total, nil
}

func (r *WhatsAppOutboundRepo) RecoverInFlight(ctx context.Context, gatewayID string, maxAttempts int, lease, hardLease time.Duration, nowMs int64, skipRecipients []string) (requeued, exhausted int, err error) {
	type staleRow struct {
		ID        string `db:"id"`
		Recipient string `db:"recipient"`
		Attempts  int    `db:"attempts"`
		UpdatedAt int64  `db:"updated_at"`
	}
	staleBefore := nowMs - lease.Milliseconds()
	candidates, err := dbutil.QueryRows[staleRow](ctx,
		r.db,
		`SELECT id, recipient, attempts, updated_at FROM whatsapp_outbound
		 WHERE gateway_id = ? AND state = ? AND updated_at <= ?
		 ORDER BY updated_at LIMIT ?`,
		gatewayID, WhatsAppOutboundSending, staleBefore, recoveryScanLimit)
	if err != nil {
		return 0, 0, fmt.Errorf("WhatsAppOutboundRepo.RecoverInFlight scan: %w", err)
	}

	fenced := make(map[string]struct{}, len(skipRecipients))
	for _, recipient := range skipRecipients {
		fenced[recipient] = struct{}{}
	}
	fenceLapsedBefore := nowMs - max(hardLease, lease).Milliseconds()
	var toRequeue, toExhaust []string
	for _, row := range candidates {
		if _, live := fenced[row.Recipient]; live && row.UpdatedAt > fenceLapsedBefore {
			continue
		}
		if row.Attempts >= maxAttempts {
			toExhaust = append(toExhaust, row.ID)
			continue
		}
		toRequeue = append(toRequeue, row.ID)
	}

	requeued, err = r.settleRecovered(ctx, gatewayID, toRequeue, staleBefore,
		`UPDATE whatsapp_outbound SET state = ?, not_before = ?, updated_at = ?`,
		WhatsAppOutboundQueued, nowMs, nowMs)
	if err != nil {
		return requeued, 0, fmt.Errorf("WhatsAppOutboundRepo.RecoverInFlight requeue: %w", err)
	}
	exhausted, err = r.settleRecovered(ctx, gatewayID, toExhaust, staleBefore,
		`UPDATE whatsapp_outbound SET state = ?, updated_at = ?`,
		WhatsAppOutboundFailed, nowMs)
	if err != nil {
		return requeued, exhausted, fmt.Errorf("WhatsAppOutboundRepo.RecoverInFlight exhaust: %w", err)
	}
	return requeued, exhausted, nil
}

func (r *WhatsAppOutboundRepo) settleRecovered(ctx context.Context, gatewayID string, ids []string, staleBefore int64, setClause string, setArgs ...any) (int, error) {
	var total int
	for chunk := range slices.Chunk(ids, dbutil.InClauseCap) {
		placeholders, idArgs, err := dbutil.InClause(chunk)
		if err != nil {
			return total, err
		}
		args := append(append([]any{}, setArgs...), gatewayID, WhatsAppOutboundSending, staleBefore)
		args = append(args, idArgs...)
		n, err := r.execCount(ctx,
			setClause+` WHERE gateway_id = ? AND state = ? AND updated_at <= ? AND id IN (`+placeholders+`)`, args...)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (r *WhatsAppOutboundRepo) settle(ctx context.Context, q dbutil.Querier, op, id, query string, args ...any) error {
	affected, err := r.execCountWith(ctx, q, query, args...)
	if err != nil {
		return fmt.Errorf("WhatsAppOutboundRepo.%s %s: %w", op, id, err)
	}
	if affected == 0 {
		return fmt.Errorf("WhatsAppOutboundRepo.%s %s: %w", op, id, ErrOutboundRowMoved)
	}
	return nil
}

func (r *WhatsAppOutboundRepo) WithTx(ctx context.Context, fn func(q dbutil.Querier) error) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error { return fn(tx) })
}

type WhatsAppParkRequest struct {
	GatewayID      string
	ID             string
	Recipient      string
	MayYieldToTask string
	ErrorCode      int
	ErrorDetail    string
	NowMs          int64
}

func (r *WhatsAppOutboundRepo) Park(ctx context.Context, req WhatsAppParkRequest) (supersededID string, yielded bool, err error) {
	err = r.WithTx(ctx, func(q dbutil.Querier) error {
		prev, prevTaskID, readErr := r.parkedHead(ctx, q, req.GatewayID, req.Recipient)
		if readErr != nil {
			return fmt.Errorf("read the displaced park: %w", readErr)
		}
		if prev != "" && req.MayYieldToTask != "" && prevTaskID == req.MayYieldToTask {
			affected, yieldErr := r.execCountWith(ctx, q,
				`UPDATE whatsapp_outbound SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
				WhatsAppOutboundSuperseded, req.NowMs, req.ID, WhatsAppOutboundSending)
			if yieldErr != nil {
				return fmt.Errorf("retire the yielding row: %w", yieldErr)
			}
			if affected == 0 {
				return ErrOutboundRowMoved
			}
			yielded = true
			return nil
		}
		if prev != "" {
			if _, retireErr := q.ExecContext(ctx,
				`UPDATE whatsapp_outbound SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
				WhatsAppOutboundSuperseded, req.NowMs, prev, WhatsAppOutboundParked); retireErr != nil {
				return fmt.Errorf("supersede the displaced park: %w", retireErr)
			}
		}
		affected, parkErr := r.execCountWith(ctx, q,
			`UPDATE whatsapp_outbound
			 SET state = ?, parked_at = CASE WHEN parked_at = 0 THEN ? ELSE parked_at END,
				 attempts = 0, not_before = 0,
				 error_code = ?, error_detail = ?, updated_at = ?
			 WHERE id = ? AND state = ?`,
			WhatsAppOutboundParked, req.NowMs, req.ErrorCode, req.ErrorDetail, req.NowMs, req.ID, WhatsAppOutboundSending)
		if parkErr != nil {
			return fmt.Errorf("park the row: %w", parkErr)
		}
		if affected == 0 {
			return ErrOutboundRowMoved
		}
		supersededID = prev
		return nil
	})
	if err != nil {
		return "", false, fmt.Errorf("WhatsAppOutboundRepo.Park %s: %w", req.ID, err)
	}
	return supersededID, yielded, nil
}

func (r *WhatsAppOutboundRepo) parkedHead(ctx context.Context, q dbutil.Querier, gatewayID, recipient string) (id, taskID string, err error) {
	row, err := dbutil.QueryRow[struct {
		ID     string `db:"id"`
		TaskID string `db:"task_id"`
	}](ctx, q,
		`SELECT id, task_id FROM whatsapp_outbound
		 WHERE gateway_id = ? AND recipient = ? AND state = ?`,
		gatewayID, recipient, WhatsAppOutboundParked)
	if err != nil || row == nil {
		return "", "", err
	}
	return row.ID, row.TaskID, nil
}

func (r *WhatsAppOutboundRepo) GetParked(ctx context.Context, gatewayID, recipient string) (*WhatsAppOutbound, error) {
	row, err := dbutil.QueryRow[WhatsAppOutbound](ctx, r.db,
		`SELECT `+whatsAppOutboundColumns+` FROM whatsapp_outbound
		 WHERE gateway_id = ? AND recipient = ? AND state = ?`,
		gatewayID, recipient, WhatsAppOutboundParked)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppOutboundRepo.GetParked: %w", err)
	}
	return row, nil
}

func (r *WhatsAppOutboundRepo) Unpark(ctx context.Context, id string, nowMs int64) (bool, error) {
	n, err := r.execCount(ctx,
		`UPDATE whatsapp_outbound
		 SET state = ?, not_before = 0, updated_at = ?
		 WHERE id = ? AND state = ?`,
		WhatsAppOutboundQueued, nowMs, id, WhatsAppOutboundParked)
	if err != nil {
		return false, fmt.Errorf("WhatsAppOutboundRepo.Unpark %s: %w", id, err)
	}
	return n > 0, nil
}

func (r *WhatsAppOutboundRepo) ListStaleParks(ctx context.Context, gatewayID string, parkedBeforeMs int64, limit int) ([]WhatsAppOutbound, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := dbutil.QueryRows[WhatsAppOutbound](ctx, r.db,
		`SELECT `+whatsAppOutboundColumns+` FROM whatsapp_outbound
		 WHERE gateway_id = ? AND state = ? AND parked_at < ?
		 ORDER BY parked_at LIMIT ?`,
		gatewayID, WhatsAppOutboundParked, parkedBeforeMs, limit)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppOutboundRepo.ListStaleParks: %w", err)
	}
	return rows, nil
}

func (r *WhatsAppOutboundRepo) SupersedePark(ctx context.Context, id string, nowMs int64) (bool, error) {
	n, err := r.execCount(ctx,
		`UPDATE whatsapp_outbound SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
		WhatsAppOutboundSuperseded, nowMs, id, WhatsAppOutboundParked)
	if err != nil {
		return false, fmt.Errorf("WhatsAppOutboundRepo.SupersedePark %s: %w", id, err)
	}
	return n > 0, nil
}

func (r *WhatsAppOutboundRepo) TasksWithFinalRow(ctx context.Context, gatewayID, recipient string, taskIDs []string) ([]string, error) {
	if len(taskIDs) == 0 {
		return nil, nil
	}
	placeholders, args, err := dbutil.InClause(taskIDs)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppOutboundRepo.TasksWithFinalRow in clause: %w", err)
	}
	query := `SELECT DISTINCT task_id FROM whatsapp_outbound
		 WHERE gateway_id = ? AND recipient = ? AND payload LIKE '%"final":true%' AND task_id IN (` + placeholders + `)`
	rows, err := dbutil.QueryVals[string](ctx, r.db, query, append([]any{gatewayID, recipient}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppOutboundRepo.TasksWithFinalRow: %w", err)
	}
	return rows, nil
}

func (r *WhatsAppOutboundRepo) ActiveRecipients(ctx context.Context, gatewayID string) ([]string, error) {
	rows, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT recipient FROM whatsapp_outbound
		 WHERE gateway_id = ? AND state = ?
		 GROUP BY recipient ORDER BY MIN(id)`,
		gatewayID, WhatsAppOutboundQueued)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppOutboundRepo.ActiveRecipients: %w", err)
	}
	return rows, nil
}

func (r *WhatsAppOutboundRepo) PendingCount(ctx context.Context, gatewayID string) (int, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM whatsapp_outbound WHERE gateway_id = ? AND state IN (?, ?)`,
		gatewayID, WhatsAppOutboundQueued, WhatsAppOutboundSending)
	if err != nil {
		return 0, fmt.Errorf("WhatsAppOutboundRepo.PendingCount: %w", err)
	}
	return n, nil
}

func (r *WhatsAppOutboundRepo) GetByWamid(ctx context.Context, gatewayID, wamid string) (*WhatsAppOutbound, error) {
	if wamid == "" {
		return nil, nil
	}
	row, err := dbutil.QueryRow[WhatsAppOutbound](ctx, r.db,
		`SELECT `+whatsAppOutboundColumns+` FROM whatsapp_outbound
		 WHERE gateway_id = ? AND wamid = ?`,
		gatewayID, wamid)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppOutboundRepo.GetByWamid: %w", err)
	}
	return row, nil
}

func (r *WhatsAppOutboundRepo) Get(ctx context.Context, id string) (*WhatsAppOutbound, error) {
	row, err := dbutil.QueryRow[WhatsAppOutbound](ctx, r.db,
		`SELECT `+whatsAppOutboundColumns+` FROM whatsapp_outbound WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("WhatsAppOutboundRepo.Get %s: %w", id, err)
	}
	return row, nil
}

type WhatsAppReceipt struct {
	Wamid       string
	State       string
	ErrorCode   int
	ErrorDetail string
}

func (r *WhatsAppOutboundRepo) UpdateFromReceipt(ctx context.Context, gatewayID string, rec WhatsAppReceipt, nowMs int64) (moved bool, row *WhatsAppOutbound, err error) {
	row, err = r.GetByWamid(ctx, gatewayID, rec.Wamid)
	if err != nil || row == nil {
		return false, nil, err
	}
	if !whatsAppReceiptAdvances(row.State, rec.State) {
		return false, row, nil
	}

	affected, err := r.execCount(ctx,
		`UPDATE whatsapp_outbound SET state = ?, error_code = ?, error_detail = ?, updated_at = ?
		 WHERE id = ? AND state = ?`,
		rec.State, rec.ErrorCode, rec.ErrorDetail, nowMs, row.ID, row.State)
	if err != nil {
		return false, row, fmt.Errorf("WhatsAppOutboundRepo.UpdateFromReceipt %s: %w", row.ID, err)
	}
	if affected == 0 {
		return false, row, nil
	}
	row.State = rec.State
	row.ErrorCode = rec.ErrorCode
	row.ErrorDetail = rec.ErrorDetail
	row.UpdatedAt = nowMs
	return true, row, nil
}

func whatsAppReceiptAdvances(current, incoming string) bool {
	if whatsAppReceiptRank[current] == 0 {
		return false
	}
	if incoming == WhatsAppOutboundFailed {
		return current == WhatsAppOutboundSent
	}
	return whatsAppReceiptRank[incoming] > whatsAppReceiptRank[current]
}

func (r *WhatsAppOutboundRepo) PruneTerminal(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	beforeMs := before.UnixMilli()
	var total int64
	query := `DELETE FROM whatsapp_outbound WHERE id IN (
				SELECT id FROM whatsapp_outbound
				WHERE state IN (?, ?, ?, ?, ?) AND updated_at < ? LIMIT ?
			)`
	for {
		result, err := r.db.ExecContext(ctx, query,
			WhatsAppOutboundFailed, WhatsAppOutboundSuperseded, WhatsAppOutboundRead,
			WhatsAppOutboundDelivered, WhatsAppOutboundSent, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("WhatsAppOutboundRepo.PruneTerminal: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("WhatsAppOutboundRepo.PruneTerminal: rows affected: %w", err)
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
}

func (r *WhatsAppOutboundRepo) PruneStale(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	if floor := time.Now().Add(-minUnsettledAge); before.After(floor) {
		before = floor
	}
	beforeMs := before.UnixMilli()
	var total int64
	query := `DELETE FROM whatsapp_outbound WHERE id IN (
				SELECT id FROM whatsapp_outbound
				WHERE state IN (?, ?, ?) AND created_at < ? LIMIT ?
			)`
	for {
		result, err := r.db.ExecContext(ctx, query,
			WhatsAppOutboundQueued, WhatsAppOutboundSending, WhatsAppOutboundParked, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("WhatsAppOutboundRepo.PruneStale: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("WhatsAppOutboundRepo.PruneStale: rows affected: %w", err)
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
}

func (r *WhatsAppOutboundRepo) execCount(ctx context.Context, query string, args ...any) (int, error) {
	return r.execCountWith(ctx, r.db, query, args...)
}

func (r *WhatsAppOutboundRepo) execCountWith(ctx context.Context, q dbutil.Querier, query string, args ...any) (int, error) {
	if q == nil {
		q = r.db
	}
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}
