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
)

const (
	inboundClaimProcessing = "processing"
	inboundClaimRetrying   = "retrying"
	inboundClaimDone       = "done"
)

type DedupClaim struct {
	Outcome       ClaimOutcome
	Token         int64
	Attempts      int
	FailureKind   string
	AttemptsTotal int
}

const StalledClaimFailure = "stalled"

type inboundClaimTable struct {
	db        *DB
	table     string
	keyColumn string
	repoName  string
}

func (t inboundClaimTable) Claim(ctx context.Context, gatewayID, key string, lease time.Duration) (DedupClaim, error) {
	fail := func(err error) (DedupClaim, error) {
		return DedupClaim{Outcome: ClaimInFlight}, err
	}
	insertSQL := fmt.Sprintf(`INSERT INTO %s (gateway_id, %s, state, claimed_at, processed_at, attempts)
		 VALUES (?, ?, ?, ?, NULL, 0)
		 ON CONFLICT (gateway_id, %s) DO NOTHING`, t.table, t.keyColumn, t.keyColumn)
	selectSQL := fmt.Sprintf(`SELECT state, claimed_at, attempts, failure_kind, attempts_total
		 FROM %s WHERE gateway_id = ? AND %s = ?`, t.table, t.keyColumn)
	takeoverSQL := fmt.Sprintf(`UPDATE %s
		 SET state = ?, claimed_at = ?, attempts = ?, failure_kind = ?, attempts_total = ?
		 WHERE gateway_id = ? AND %s = ? AND state = ? AND claimed_at = ?`, t.table, t.keyColumn)

	for range claimRaceRetries {
		now := time.Now().UnixMilli()
		res, err := t.db.ExecContext(ctx, insertSQL, gatewayID, key, inboundClaimProcessing, now)
		if err != nil {
			return fail(fmt.Errorf("%s.Claim(%s,%s) insert: %w", t.repoName, gatewayID, key, err))
		}
		inserted, err := res.RowsAffected()
		if err != nil {
			return fail(fmt.Errorf("%s.Claim(%s,%s) rows affected: %w", t.repoName, gatewayID, key, err))
		}
		if inserted > 0 {
			return DedupClaim{Outcome: ClaimAcquired, Token: now}, nil
		}

		var state, failureKind string
		var claimedAt int64
		var attempts, attemptsTotal int
		err = t.db.QueryRowContext(ctx, selectSQL, gatewayID, key).
			Scan(&state, &claimedAt, &attempts, &failureKind, &attemptsTotal)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fail(fmt.Errorf("%s.Claim(%s,%s) select: %w", t.repoName, gatewayID, key, err))
		}
		if state == inboundClaimDone {
			return DedupClaim{Outcome: ClaimDuplicate}, nil
		}
		if state == inboundClaimProcessing && now-claimedAt < lease.Milliseconds() {
			return DedupClaim{Outcome: ClaimInFlight}, nil
		}

		strictlyAdvancedClaimedAt := max(now, claimedAt+1)
		takenOverAttempts, takenOverKind, takenOverTotal := attempts, failureKind, attemptsTotal
		if state == inboundClaimProcessing {
			takenOverTotal++
			if failureKind == StalledClaimFailure {
				takenOverAttempts++
			} else {
				takenOverAttempts, takenOverKind = 1, StalledClaimFailure
			}
		}
		takeover, err := t.db.ExecContext(ctx, takeoverSQL,
			inboundClaimProcessing, strictlyAdvancedClaimedAt, takenOverAttempts, takenOverKind, takenOverTotal,
			gatewayID, key, state, claimedAt,
		)
		if err != nil {
			return fail(fmt.Errorf("%s.Claim(%s,%s) reclaim: %w", t.repoName, gatewayID, key, err))
		}
		won, err := takeover.RowsAffected()
		if err != nil {
			return fail(fmt.Errorf("%s.Claim(%s,%s) reclaim rows affected: %w", t.repoName, gatewayID, key, err))
		}
		if won > 0 {
			return DedupClaim{
				Outcome:       ClaimAcquired,
				Token:         strictlyAdvancedClaimedAt,
				Attempts:      takenOverAttempts,
				FailureKind:   takenOverKind,
				AttemptsTotal: takenOverTotal,
			}, nil
		}
	}
	return DedupClaim{Outcome: ClaimInFlight}, nil
}

func (t inboundClaimTable) MarkDone(ctx context.Context, gatewayID, key string, token int64) (bool, error) {
	res, err := t.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s SET state = ?, processed_at = ?
		 WHERE gateway_id = ? AND %s = ? AND state = ? AND claimed_at = ?`, t.table, t.keyColumn),
		inboundClaimDone, time.Now().UnixMilli(), gatewayID, key, inboundClaimProcessing, token,
	)
	return t.ownedAfter(res, err, "MarkDone", gatewayID, key)
}

func (t inboundClaimTable) ReleaseForRetry(ctx context.Context, gatewayID, key string, token int64, kind string) (bool, error) {
	res, err := t.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE %s
		 SET state = ?,
		     attempts = CASE WHEN failure_kind = ? THEN attempts + 1 ELSE 1 END,
		     failure_kind = ?,
		     attempts_total = attempts_total + 1
		 WHERE gateway_id = ? AND %s = ? AND state = ? AND claimed_at = ?`, t.table, t.keyColumn),
		inboundClaimRetrying, kind, kind, gatewayID, key, inboundClaimProcessing, token,
	)
	return t.ownedAfter(res, err, "ReleaseForRetry", gatewayID, key)
}

func (t inboundClaimTable) ownedAfter(res sql.Result, err error, op, gatewayID, key string) (bool, error) {
	if err != nil {
		return false, fmt.Errorf("%s.%s(%s,%s): %w", t.repoName, op, gatewayID, key, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("%s.%s(%s,%s): rows affected: %w", t.repoName, op, gatewayID, key, err)
	}
	return n > 0, nil
}

func (t inboundClaimTable) State(ctx context.Context, gatewayID, key string) (string, error) {
	var state string
	err := t.db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT state FROM %s WHERE gateway_id = ? AND %s = ?`, t.table, t.keyColumn),
		gatewayID, key,
	).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s.State(%s,%s): %w", t.repoName, gatewayID, key, err)
	}
	return state, nil
}

func (t inboundClaimTable) DeleteOlderThan(ctx context.Context, before time.Time, batchSize int) (int64, error) {
	query := fmt.Sprintf(`DELETE FROM %s
		  WHERE (gateway_id, %s) IN (
			SELECT gateway_id, %s FROM %s
			WHERE claimed_at < ? LIMIT ?
		  )`, t.table, t.keyColumn, t.keyColumn, t.table)
	beforeMs := before.UnixMilli()
	var total int64
	for {
		result, err := t.db.ExecContext(ctx, query, beforeMs, batchSize)
		if err != nil {
			return total, fmt.Errorf("%s.DeleteOlderThan: %w", t.repoName, err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("%s.DeleteOlderThan: rows affected: %w", t.repoName, err)
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
}
