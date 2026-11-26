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
)

const (
	InboundProcessing = "processing"
	InboundDone       = "done"
	InboundStored     = "stored"
)

const (
	InboundOutcomeAck    = "ack"
	InboundOutcomeReject = "reject"
)

type StoredInbound struct {
	MessageID string `db:"message_id"`
	TaskID    string `db:"task_id"`
	Payload   string `db:"payload"`
}

type inboundOutcomeRow struct {
	MessageID string `db:"message_id"`
	Outcome   string `db:"outcome"`
}

type InboundClaim struct {
	Outcome  ClaimOutcome
	Token    int64
	Attempts int
}

type LapsedInbound struct {
	MessageID  string
	Payload    string
	Token      int64
	Attempts   int
	ReceivedAt time.Time
}

type lapsedInboundRow struct {
	MessageID  string `db:"message_id"`
	Payload    string `db:"payload"`
	ClaimedAt  int64  `db:"claimed_at"`
	Attempts   int    `db:"attempts"`
	ReceivedAt int64  `db:"received_at"`
}

type EntrypointInboundRepo struct {
	db *DB
}

func NewEntrypointInboundRepo(db *DB) *EntrypointInboundRepo {
	return &EntrypointInboundRepo{db: db}
}

func (r *EntrypointInboundRepo) Claim(ctx context.Context, gatewayID, messageID, payload string, lease time.Duration) (InboundClaim, error) {
	fail := func(err error) (InboundClaim, error) {
		return InboundClaim{Outcome: ClaimInFlight}, err
	}
	for range claimRaceRetries {
		now := time.Now().UnixMilli()
		res, err := r.db.ExecContext(ctx,
			`INSERT INTO entrypoint_inbound_messages (gateway_id, message_id, state, payload, claimed_at, attempts, received_at)
			 VALUES (?, ?, ?, ?, ?, 0, ?)
			 ON CONFLICT (gateway_id, message_id) DO NOTHING`,
			gatewayID, messageID, InboundProcessing, payload, now, now)
		if err != nil {
			return fail(fmt.Errorf("EntrypointInboundRepo.Claim(%s, %s) insert: %w", gatewayID, messageID, err))
		}
		inserted, err := res.RowsAffected()
		if err != nil {
			return fail(fmt.Errorf("EntrypointInboundRepo.Claim(%s, %s) rows affected: %w", gatewayID, messageID, err))
		}
		if inserted > 0 {
			return InboundClaim{Outcome: ClaimAcquired, Token: now}, nil
		}

		var state string
		var claimedAt int64
		var attempts int
		err = r.db.QueryRowContext(ctx,
			`SELECT state, claimed_at, attempts FROM entrypoint_inbound_messages
			 WHERE gateway_id = ? AND message_id = ?`,
			gatewayID, messageID).Scan(&state, &claimedAt, &attempts)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fail(fmt.Errorf("EntrypointInboundRepo.Claim(%s, %s) select: %w", gatewayID, messageID, err))
		}
		if state == InboundDone {
			return InboundClaim{Outcome: ClaimDuplicate}, nil
		}
		if now-claimedAt < lease.Milliseconds() {
			return InboundClaim{Outcome: ClaimInFlight}, nil
		}
		token, won, err := r.takeOver(ctx, gatewayID, messageID, claimedAt, now)
		if err != nil {
			return fail(err)
		}
		if won {
			return InboundClaim{Outcome: ClaimAcquired, Token: token, Attempts: attempts + 1}, nil
		}
	}
	return InboundClaim{Outcome: ClaimInFlight}, nil
}

func (r *EntrypointInboundRepo) takeOver(ctx context.Context, gatewayID, messageID string, claimedAt, now int64) (int64, bool, error) {
	token := max(now, claimedAt+1)
	res, err := r.db.ExecContext(ctx,
		`UPDATE entrypoint_inbound_messages SET claimed_at = ?, attempts = attempts + 1
		 WHERE gateway_id = ? AND message_id = ? AND state = ? AND claimed_at = ?`,
		token, gatewayID, messageID, InboundProcessing, claimedAt)
	if err != nil {
		return 0, false, fmt.Errorf("EntrypointInboundRepo take over (%s, %s): %w", gatewayID, messageID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("EntrypointInboundRepo take over (%s, %s) rows affected: %w", gatewayID, messageID, err)
	}
	return token, n > 0, nil
}

func (r *EntrypointInboundRepo) Renew(ctx context.Context, gatewayID, messageID string, token int64) (int64, bool, error) {
	renewed := max(time.Now().UnixMilli(), token+1)
	res, err := r.db.ExecContext(ctx,
		`UPDATE entrypoint_inbound_messages SET claimed_at = ?
		 WHERE gateway_id = ? AND message_id = ? AND state = ? AND claimed_at = ?`,
		renewed, gatewayID, messageID, InboundProcessing, token)
	if err != nil {
		return 0, false, fmt.Errorf("EntrypointInboundRepo.Renew(%s, %s): %w", gatewayID, messageID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("EntrypointInboundRepo.Renew(%s, %s) rows affected: %w", gatewayID, messageID, err)
	}
	if n == 0 {
		return 0, false, nil
	}
	return renewed, true, nil
}

func (r *EntrypointInboundRepo) MarkDone(ctx context.Context, gatewayID, messageID string, token int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE entrypoint_inbound_messages SET state = ?, payload = ''
		 WHERE gateway_id = ? AND message_id = ? AND state = ? AND claimed_at = ?`,
		InboundDone, gatewayID, messageID, InboundProcessing, token)
	if err != nil {
		return false, fmt.Errorf("EntrypointInboundRepo.MarkDone(%s, %s): %w", gatewayID, messageID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("EntrypointInboundRepo.MarkDone(%s, %s) rows affected: %w", gatewayID, messageID, err)
	}
	return n > 0, nil
}

func (r *EntrypointInboundRepo) TakeLapsed(ctx context.Context, gatewayID string, lease time.Duration, limit int) ([]LapsedInbound, error) {
	now := time.Now().UnixMilli()
	lapsed, err := dbutil.QueryRows[lapsedInboundRow](ctx, r.db,
		`SELECT message_id, payload, claimed_at, attempts, received_at FROM entrypoint_inbound_messages
		 WHERE gateway_id = ? AND state = ? AND claimed_at <= ?
		 ORDER BY claimed_at LIMIT ?`,
		gatewayID, InboundProcessing, now-lease.Milliseconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("EntrypointInboundRepo.TakeLapsed(%s): %w", gatewayID, err)
	}

	var taken []LapsedInbound
	for _, l := range lapsed {
		token, won, err := r.takeOver(ctx, gatewayID, l.MessageID, l.ClaimedAt, now)
		if err != nil {
			return taken, err
		}
		if won {
			taken = append(taken, LapsedInbound{
				MessageID: l.MessageID, Payload: l.Payload, Token: token, Attempts: l.Attempts + 1,
				ReceivedAt: time.UnixMilli(l.ReceivedAt),
			})
		}
	}
	return taken, nil
}

func (r *EntrypointInboundRepo) Record(ctx context.Context, gatewayID, messageID string) (bool, error) {
	now := time.Now().UnixMilli()
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO entrypoint_inbound_messages (gateway_id, message_id, state, payload, claimed_at, attempts, received_at)
		 VALUES (?, ?, ?, '', ?, 0, ?)
		 ON CONFLICT (gateway_id, message_id) DO NOTHING`,
		gatewayID, messageID, InboundDone, now, now)
	if err != nil {
		return false, fmt.Errorf("EntrypointInboundRepo.Record(%s, %s): %w", gatewayID, messageID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("EntrypointInboundRepo.Record(%s, %s) rows affected: %w", gatewayID, messageID, err)
	}
	return n > 0, nil
}

func (r *EntrypointInboundRepo) Recorded(ctx context.Context, gatewayID, messageID string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entrypoint_inbound_messages WHERE gateway_id = ? AND message_id = ?`,
		gatewayID, messageID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("EntrypointInboundRepo.Recorded(%s, %s): %w", gatewayID, messageID, err)
	}
	return n > 0, nil
}

func (r *EntrypointInboundRepo) Forget(ctx context.Context, gatewayID, messageID string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM entrypoint_inbound_messages WHERE gateway_id = ? AND message_id = ?`,
		gatewayID, messageID); err != nil {
		return fmt.Errorf("EntrypointInboundRepo.Forget(%s, %s): %w", gatewayID, messageID, err)
	}
	return nil
}

func (r *EntrypointInboundRepo) DeleteOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	beforeMs := before.UnixMilli()
	var total int64
	for {
		res, err := r.db.ExecContext(ctx,
			`DELETE FROM entrypoint_inbound_messages
			 WHERE (gateway_id, message_id) IN (
				SELECT gateway_id, message_id FROM entrypoint_inbound_messages
				WHERE received_at < ? LIMIT ?
			 )`, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("EntrypointInboundRepo.DeleteOlderThan: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("EntrypointInboundRepo.DeleteOlderThan rows affected: %w", err)
		}
		total += n
		if n < int64(batchSize) {
			return total, nil
		}
	}
}

func (r *EntrypointInboundRepo) Store(ctx context.Context, gatewayID, messageID, taskID, payload string) (bool, error) {
	now := time.Now().UnixMilli()
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO entrypoint_inbound_messages (gateway_id, message_id, state, payload, claimed_at, attempts, received_at, task_id)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?)
		 ON CONFLICT (gateway_id, message_id) DO NOTHING`,
		gatewayID, messageID, InboundStored, payload, now, now, taskID)
	if err != nil {
		return false, fmt.Errorf("EntrypointInboundRepo.Store(%s, %s): %w", gatewayID, messageID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("EntrypointInboundRepo.Store(%s, %s) rows affected: %w", gatewayID, messageID, err)
	}
	return n > 0, nil
}

func (r *EntrypointInboundRepo) Stored(ctx context.Context, gatewayID string, limit int) ([]StoredInbound, error) {
	rows, err := dbutil.QueryRows[StoredInbound](ctx, r.db,
		`SELECT message_id, COALESCE(task_id, '') AS task_id, payload FROM entrypoint_inbound_messages
		 WHERE gateway_id = ? AND state = ?
		 ORDER BY received_at LIMIT ?`,
		gatewayID, InboundStored, limit)
	if err != nil {
		return nil, fmt.Errorf("EntrypointInboundRepo.Stored(%s): %w", gatewayID, err)
	}
	return rows, nil
}

func (r *EntrypointInboundRepo) MarkSubmitted(ctx context.Context, gatewayID, messageID, taskID, outcome string) error {
	if _, err := r.db.ExecContext(ctx,
		`UPDATE entrypoint_inbound_messages SET state = ?, payload = '', outcome = COALESCE(NULLIF(?, ''), outcome)
		 WHERE gateway_id = ? AND message_id = ? AND state = ? AND task_id = ?`,
		InboundDone, outcome, gatewayID, messageID, InboundStored, taskID); err != nil {
		return fmt.Errorf("EntrypointInboundRepo.MarkSubmitted(%s, %s): %w", gatewayID, messageID, err)
	}
	return nil
}

func (r *EntrypointInboundRepo) SetOutcome(ctx context.Context, gatewayID, messageID, taskID, outcome string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE entrypoint_inbound_messages SET outcome = ?
		 WHERE gateway_id = ? AND message_id = ? AND task_id = ? AND outcome IS NULL`,
		outcome, gatewayID, messageID, taskID)
	if err != nil {
		return false, fmt.Errorf("EntrypointInboundRepo.SetOutcome(%s, %s): %w", gatewayID, messageID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("EntrypointInboundRepo.SetOutcome(%s, %s) rows: %w", gatewayID, messageID, err)
	}
	return n > 0, nil
}

func (r *EntrypointInboundRepo) Outcomes(ctx context.Context, gatewayID string, messageIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(messageIDs))
	if len(messageIDs) == 0 {
		return out, nil
	}
	ph, idArgs, err := dbutil.InClause(messageIDs)
	if err != nil {
		return nil, fmt.Errorf("EntrypointInboundRepo.Outcomes(%s): %w", gatewayID, err)
	}
	rows, err := dbutil.QueryRows[inboundOutcomeRow](ctx, r.db,
		`SELECT message_id, outcome FROM entrypoint_inbound_messages
		 WHERE gateway_id = ? AND outcome IS NOT NULL AND message_id IN (`+ph+`)`,
		append([]any{gatewayID}, idArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("EntrypointInboundRepo.Outcomes(%s): %w", gatewayID, err)
	}
	for _, row := range rows {
		out[row.MessageID] = row.Outcome
	}
	return out, nil
}
