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

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var ErrDuplicate = errors.New("notify: duplicate pending request (dedup)")

type PendingRequest struct {
	RequestID       string  `db:"request_id"`
	RequesterAddr   string  `db:"requester_addr"`
	RecipientUserID string  `db:"recipient_user_id"`
	Dedup           *string `db:"dedup"`
	Status          string  `db:"status"`
	ExpiresAt       *int64  `db:"expires_at"`
	CreatedAt       int64   `db:"created_at"`
	ResolvedAt      *int64  `db:"resolved_at"`
	Approvers       *string `db:"approvers"`
	AnsweredBy      *string `db:"answered_by"`
	AnsweredVia     *string `db:"answered_via"`
	Decision        *string `db:"decision"`
	AnsweredByName  *string `db:"answered_by_name"`
}

const pendingRequestColumns = `request_id, requester_addr, recipient_user_id, dedup, status, expires_at, created_at, resolved_at,
		        approvers, answered_by, answered_via, decision, answered_by_name`

type PendingRequestRepo struct {
	db *DB
}

func NewPendingRequestRepo(db *DB) *PendingRequestRepo { return &PendingRequestRepo{db: db} }

func (r *PendingRequestRepo) Insert(ctx context.Context, pr PendingRequest) error {
	dedup := pr.Dedup
	if dedup != nil && *dedup == "" {
		dedup = nil
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO notify_pending_requests
		   (request_id, requester_addr, recipient_user_id, dedup, status, expires_at, created_at, approvers)
		 VALUES (?, ?, ?, ?, 'pending', ?, ?, ?)`,
		pr.RequestID, pr.RequesterAddr, pr.RecipientUserID, dedup, pr.ExpiresAt, nowMillis(), pr.Approvers,
	)
	if err != nil {
		if dbutil.IsUniqueViolation(err) {
			return ErrDuplicate
		}
		return fmt.Errorf("insert pending request: %w", err)
	}
	return nil
}

func (r *PendingRequestRepo) Claim(ctx context.Context, requestID, terminalStatus string) (addr string, claimed bool, err error) {
	return r.ClaimResolution(ctx, requestID, AnyRequest(), Resolution{Status: terminalStatus})
}

type ClaimGuard struct {
	where string
	args  []any
}

func AnyRequest() ClaimGuard { return ClaimGuard{} }

func SingleRecipientOnly() ClaimGuard { return ClaimGuard{where: " AND approvers IS NULL"} }

func ApproverSet(stored string) ClaimGuard {
	return ClaimGuard{where: " AND approvers = ? AND (expires_at IS NULL OR expires_at > ?)", args: []any{stored, nowMillis()}}
}

func RequesterAt(addr string) ClaimGuard {
	return ClaimGuard{where: " AND requester_addr = ?", args: []any{addr}}
}

type Resolution struct {
	Status         string
	Decision       string
	AnsweredBy     string
	AnsweredByName string
	AnsweredVia    string
}

func (r *PendingRequestRepo) ClaimResolution(ctx context.Context, requestID string, guard ClaimGuard, res Resolution) (addr string, claimed bool, err error) {
	args := []any{
		res.Status, nowMillis(), nullIfEmpty(res.AnsweredBy), nullIfEmpty(res.AnsweredVia),
		nullIfEmpty(res.Decision), nullIfEmpty(res.AnsweredByName), requestID,
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE notify_pending_requests
		 SET status = ?, resolved_at = ?, answered_by = ?, answered_via = ?, decision = ?, answered_by_name = ?
		 WHERE request_id = ? AND status = 'pending'`+guard.where,
		append(args, guard.args...)...,
	)
	if err != nil {
		return "", false, fmt.Errorf("claim pending request: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", false, fmt.Errorf("claim pending request rows affected: %w", err)
	}
	if affected == 0 {
		return "", false, nil
	}
	if err := r.db.QueryRowContext(ctx,
		`SELECT requester_addr FROM notify_pending_requests WHERE request_id = ?`,
		requestID).Scan(&addr); err != nil {
		return "", false, fmt.Errorf("load requester_addr after claim: %w", err)
	}
	return addr, true, nil
}

func (r *PendingRequestRepo) Get(ctx context.Context, requestID string) (*PendingRequest, error) {
	pr, err := dbutil.QueryRow[PendingRequest](ctx, r.db,
		`SELECT `+pendingRequestColumns+`
		 FROM notify_pending_requests WHERE request_id = ?`, requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get pending request: %w", err)
	}
	return pr, nil
}

func (r *PendingRequestRepo) Reopen(ctx context.Context, requestID, fromStatus string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE notify_pending_requests SET status = 'pending', resolved_at = NULL, answered_by = NULL, answered_via = NULL,
		     decision = NULL, answered_by_name = NULL
		 WHERE request_id = ? AND status = ?`,
		requestID, fromStatus,
	)
	if err != nil {
		return fmt.Errorf("reopen pending request: %w", err)
	}
	return nil
}

func (r *PendingRequestRepo) Expired(ctx context.Context, nowMs int64, limit int) ([]PendingRequest, error) {
	return dbutil.QueryRows[PendingRequest](ctx, r.db,
		`SELECT `+pendingRequestColumns+`
		 FROM notify_pending_requests
		 WHERE status = 'pending' AND expires_at IS NOT NULL AND expires_at <= ?
		 ORDER BY expires_at ASC LIMIT ?`,
		nowMs, limit,
	)
}

func (r *PendingRequestRepo) DeleteResolvedOlderThan(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM notify_pending_requests WHERE status != 'pending' AND resolved_at IS NOT NULL AND resolved_at < ?`,
		cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete resolved pending requests: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

func (r *PendingRequestRepo) DeleteStalePending(ctx context.Context, cutoff int64) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM notify_pending_requests WHERE status = 'pending' AND created_at < ?`,
		cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete stale pending requests: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
