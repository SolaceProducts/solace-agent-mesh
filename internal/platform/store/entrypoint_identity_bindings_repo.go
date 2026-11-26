// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type EntrypointIdentityBinding struct {
	GatewayID            string  `db:"gateway_id"`
	LookupKey            string  `db:"lookup_key"`
	LookupValueCanonical string  `db:"lookup_value_canonical"`
	UserID               *string `db:"user_id"`
	ContinuityToken      string  `db:"continuity_token"`
	CorroboratedAt       *int64  `db:"corroborated_at"`
	RotatedAt            *int64  `db:"rotated_at"`
	LastSeenAt           int64   `db:"last_seen_at"`
}

type EntrypointIdentityBindingsRepo struct {
	db *DB
}

func NewEntrypointIdentityBindingsRepo(db *DB) *EntrypointIdentityBindingsRepo {
	return &EntrypointIdentityBindingsRepo{db: db}
}

const entrypointBindingColumns = `gateway_id, lookup_key, lookup_value_canonical,
	user_id, continuity_token, corroborated_at, rotated_at, last_seen_at`

type ContinuityVerdict int

const (
	ContinuityFirstSeen ContinuityVerdict = iota
	ContinuityStable
	ContinuityRotated
	ContinuityAwaitingCorroboration
	ContinuityForeignHolder
	ContinuityReassigned
)

func (v ContinuityVerdict) Withholds() bool {
	return v == ContinuityRotated || v == ContinuityAwaitingCorroboration || v == ContinuityForeignHolder
}

func (v ContinuityVerdict) Reason() string {
	switch v {
	case ContinuityRotated:
		return "sender_changed"
	case ContinuityAwaitingCorroboration:
		return "awaiting_corroboration"
	case ContinuityForeignHolder:
		return "value_held_by_another_user"
	case ContinuityReassigned:
		return "reassigned"
	case ContinuityStable:
		return "stable"
	default:
		return "first_seen"
	}
}

func (r *EntrypointIdentityBindingsRepo) ObserveContinuity(ctx context.Context, gatewayID, lookupKey, canonicalValue, senderToken, matchedUserID string, nowMs int64) (ContinuityVerdict, error) {
	if gatewayID == "" || lookupKey == "" || canonicalValue == "" {
		return ContinuityFirstSeen, nil
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO entrypoint_identity_bindings (`+entrypointBindingColumns+`)
		 VALUES (?, ?, ?, NULL, ?, NULL, NULL, ?)
		 ON CONFLICT (gateway_id, lookup_key, lookup_value_canonical) DO NOTHING`,
		gatewayID, lookupKey, canonicalValue, senderToken, nowMs)
	if err != nil {
		return ContinuityFirstSeen, fmt.Errorf("EntrypointIdentityBindingsRepo.ObserveContinuity insert key=%q: %w", lookupKey, err)
	}
	if inserted, affErr := res.RowsAffected(); affErr == nil && inserted > 0 {
		return ContinuityFirstSeen, nil
	}

	if matchedUserID != "" {
		reseatRes, reseatErr := r.db.ExecContext(ctx,
			`UPDATE entrypoint_identity_bindings
			 SET continuity_token = ?, user_id = ?, corroborated_at = ?, rotated_at = NULL, last_seen_at = ?
			 WHERE gateway_id = ? AND lookup_key = ? AND lookup_value_canonical = ?
			   AND rotated_at IS NOT NULL AND user_id IS NOT NULL AND user_id <> ?`,
			senderToken, matchedUserID, nowMs, nowMs, gatewayID, lookupKey, canonicalValue, matchedUserID)
		if reseatErr != nil {
			return ContinuityFirstSeen, fmt.Errorf("EntrypointIdentityBindingsRepo.ObserveContinuity reseat key=%q: %w", lookupKey, reseatErr)
		}
		if reseated, affErr := reseatRes.RowsAffected(); affErr == nil && reseated > 0 {
			return ContinuityReassigned, nil
		}
	}

	if senderToken != "" {
		if _, adoptErr := r.db.ExecContext(ctx,
			`UPDATE entrypoint_identity_bindings SET continuity_token = ?
			 WHERE gateway_id = ? AND lookup_key = ? AND lookup_value_canonical = ? AND continuity_token = ''`,
			senderToken, gatewayID, lookupKey, canonicalValue); adoptErr != nil {
			return ContinuityFirstSeen, fmt.Errorf("EntrypointIdentityBindingsRepo.ObserveContinuity adopt token key=%q: %w", lookupKey, adoptErr)
		}

		rotateRes, rotateErr := r.db.ExecContext(ctx,
			`UPDATE entrypoint_identity_bindings
			 SET continuity_token = ?, user_id = COALESCE(user_id, ?), rotated_at = ?, last_seen_at = ?
			 WHERE gateway_id = ? AND lookup_key = ? AND lookup_value_canonical = ?
			   AND continuity_token <> ? AND rotated_at IS NULL`,
			senderToken, nullableID(matchedUserID), nowMs, nowMs, gatewayID, lookupKey, canonicalValue, senderToken)
		if rotateErr != nil {
			return ContinuityFirstSeen, fmt.Errorf("EntrypointIdentityBindingsRepo.ObserveContinuity rotate key=%q: %w", lookupKey, rotateErr)
		}
		if rotated, affErr := rotateRes.RowsAffected(); affErr == nil && rotated > 0 {
			return ContinuityRotated, nil
		}
	}

	row, err := r.Get(ctx, gatewayID, lookupKey, canonicalValue)
	if err != nil {
		return ContinuityFirstSeen, err
	}
	if row == nil {
		return ContinuityFirstSeen, nil
	}

	if _, err := r.db.ExecContext(ctx,
		`UPDATE entrypoint_identity_bindings SET last_seen_at = ?
		 WHERE gateway_id = ? AND lookup_key = ? AND lookup_value_canonical = ?`,
		nowMs, gatewayID, lookupKey, canonicalValue); err != nil {
		return ContinuityStable, fmt.Errorf("EntrypointIdentityBindingsRepo.ObserveContinuity touch key=%q: %w", lookupKey, err)
	}

	switch {
	case row.RotatedAt != nil:
		return ContinuityAwaitingCorroboration, nil
	case row.UserID != nil && matchedUserID != "" && *row.UserID != matchedUserID:
		return ContinuityForeignHolder, nil
	default:
		return ContinuityStable, nil
	}
}

func (r *EntrypointIdentityBindingsRepo) Corroborate(ctx context.Context, q dbutil.Querier, lookupKey, canonicalValue, userID string, nowMs int64) error {
	if q == nil {
		q = r.db
	}
	return corroborateBinding(ctx, q, lookupKey, canonicalValue, userID, nowMs)
}

func corroborateBinding(ctx context.Context, q dbutil.Querier, lookupKey, canonicalValue, userID string, nowMs int64) error {
	return corroborateBindingValues(ctx, q, lookupKey, []string{canonicalValue}, userID, nowMs)
}

func corroborateBindingValues(ctx context.Context, q dbutil.Querier, lookupKey string, canonicalValues []string, userID string, nowMs int64) error {
	if lookupKey == "" || len(canonicalValues) == 0 {
		return nil
	}
	for chunk := range slices.Chunk(canonicalValues, dbutil.InClauseCap) {
		placeholders, values, err := dbutil.InClause(chunk)
		if err != nil {
			return fmt.Errorf("corroborate binding key=%q: %w", lookupKey, err)
		}
		args := append([]any{userID, nowMs, lookupKey}, values...)
		if _, err := q.ExecContext(ctx,
			`UPDATE entrypoint_identity_bindings SET user_id = ?, corroborated_at = ?
			 WHERE lookup_key = ? AND lookup_value_canonical IN (`+placeholders+`) AND rotated_at IS NULL`,
			args...); err != nil {
			return fmt.Errorf("corroborate binding key=%q: %w", lookupKey, err)
		}
	}
	return nil
}

func (r *EntrypointIdentityBindingsRepo) DeleteForValues(ctx context.Context, lookupKey string, canonicalValues []string) (int64, error) {
	return r.deleteForValuesOn(ctx, r.db, lookupKey, canonicalValues)
}

func (r *EntrypointIdentityBindingsRepo) DeleteForValuesInTx(ctx context.Context, tx *dbutil.Tx, lookupKey string, canonicalValues []string) (int64, error) {
	return r.deleteForValuesOn(ctx, tx, lookupKey, canonicalValues)
}

func (r *EntrypointIdentityBindingsRepo) deleteForValuesOn(ctx context.Context, q dbutil.Querier, lookupKey string, canonicalValues []string) (int64, error) {
	if lookupKey == "" || len(canonicalValues) == 0 {
		return 0, nil
	}
	var total int64
	for chunk := range slices.Chunk(canonicalValues, dbutil.InClauseCap) {
		placeholders, values, err := dbutil.InClause(chunk)
		if err != nil {
			return total, fmt.Errorf("EntrypointIdentityBindingsRepo.DeleteForValues key=%q: %w", lookupKey, err)
		}
		res, err := q.ExecContext(ctx,
			`DELETE FROM entrypoint_identity_bindings WHERE lookup_key = ? AND lookup_value_canonical IN (`+placeholders+`)`,
			append([]any{lookupKey}, values...)...)
		if err != nil {
			return total, fmt.Errorf("EntrypointIdentityBindingsRepo.DeleteForValues key=%q: %w", lookupKey, err)
		}
		if n, affErr := res.RowsAffected(); affErr == nil {
			total += n
		}
	}
	return total, nil
}

func (r *EntrypointIdentityBindingsRepo) DeleteForUser(ctx context.Context, userID string) (int64, error) {
	return r.deleteForUserOn(ctx, r.db, userID)
}

func (r *EntrypointIdentityBindingsRepo) DeleteForUserInTx(ctx context.Context, tx *dbutil.Tx, userID string) (int64, error) {
	return r.deleteForUserOn(ctx, tx, userID)
}

func (r *EntrypointIdentityBindingsRepo) deleteForUserOn(ctx context.Context, q dbutil.Querier, userID string) (int64, error) {
	if userID == "" {
		return 0, nil
	}
	res, err := q.ExecContext(ctx, `DELETE FROM entrypoint_identity_bindings WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("EntrypointIdentityBindingsRepo.DeleteForUser %s: %w", userID, err)
	}
	n, affErr := res.RowsAffected()
	if affErr != nil {
		return 0, nil
	}
	return n, nil
}

func (r *EntrypointIdentityBindingsRepo) Get(ctx context.Context, gatewayID, lookupKey, canonicalValue string) (*EntrypointIdentityBinding, error) {
	row, err := dbutil.QueryRow[EntrypointIdentityBinding](ctx, r.db,
		`SELECT `+entrypointBindingColumns+` FROM entrypoint_identity_bindings
		 WHERE gateway_id = ? AND lookup_key = ? AND lookup_value_canonical = ?`,
		gatewayID, lookupKey, canonicalValue)
	if err != nil {
		return nil, fmt.Errorf("EntrypointIdentityBindingsRepo.Get key=%q: %w", lookupKey, err)
	}
	return row, nil
}

type TokenRotation struct {
	Predecessor string
	Successor   string
}

func (r *EntrypointIdentityBindingsRepo) Rebind(ctx context.Context, gatewayID, lookupKey, canonicalValue string, rotation TokenRotation, nowMs int64) (bool, error) {
	return r.rebindOn(ctx, r.db, gatewayID, lookupKey, canonicalValue, rotation, nowMs)
}

func (r *EntrypointIdentityBindingsRepo) RebindInTx(ctx context.Context, tx *dbutil.Tx, gatewayID, lookupKey, canonicalValue string, rotation TokenRotation, nowMs int64) (bool, error) {
	return r.rebindOn(ctx, tx, gatewayID, lookupKey, canonicalValue, rotation, nowMs)
}

func (r *EntrypointIdentityBindingsRepo) rebindOn(ctx context.Context, q dbutil.Querier, gatewayID, lookupKey, canonicalValue string, rotation TokenRotation, nowMs int64) (bool, error) {
	predecessor, senderToken := rotation.Predecessor, rotation.Successor
	if predecessor == "" || senderToken == "" || predecessor == senderToken {
		return false, nil
	}
	res, err := q.ExecContext(ctx,
		`UPDATE entrypoint_identity_bindings SET continuity_token = ?, rotated_at = NULL, last_seen_at = ?
		 WHERE gateway_id = ? AND lookup_key = ? AND lookup_value_canonical = ? AND continuity_token = ?`,
		senderToken, nowMs, gatewayID, lookupKey, canonicalValue, predecessor)
	if err != nil {
		return false, fmt.Errorf("EntrypointIdentityBindingsRepo.Rebind key=%q: %w", lookupKey, err)
	}
	n, err := res.RowsAffected()
	return err == nil && n > 0, nil
}

func (r *EntrypointIdentityBindingsRepo) Discard(ctx context.Context, gatewayID, lookupKey, canonicalValue, matchedUserID string, nowMs int64) (bool, error) {
	return r.discardOn(ctx, r.db, gatewayID, lookupKey, canonicalValue, matchedUserID, nowMs)
}

func (r *EntrypointIdentityBindingsRepo) DiscardInTx(ctx context.Context, tx *dbutil.Tx, gatewayID, lookupKey, canonicalValue, matchedUserID string, nowMs int64) (bool, error) {
	return r.discardOn(ctx, tx, gatewayID, lookupKey, canonicalValue, matchedUserID, nowMs)
}

func (r *EntrypointIdentityBindingsRepo) discardOn(ctx context.Context, q dbutil.Querier, gatewayID, lookupKey, canonicalValue, matchedUserID string, nowMs int64) (bool, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE entrypoint_identity_bindings
		 SET continuity_token = '', user_id = COALESCE(user_id, ?), rotated_at = ?, last_seen_at = ?
		 WHERE gateway_id = ? AND lookup_key = ? AND lookup_value_canonical = ?`,
		nullableID(matchedUserID), nowMs, nowMs, gatewayID, lookupKey, canonicalValue)
	if err != nil {
		return false, fmt.Errorf("EntrypointIdentityBindingsRepo.Discard key=%q: %w", lookupKey, err)
	}
	n, affErr := res.RowsAffected()
	return affErr == nil && n > 0, nil
}

func nullableID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func (r *EntrypointIdentityBindingsRepo) DeleteForGateway(ctx context.Context, gatewayID string) (int64, error) {
	if gatewayID == "" {
		return 0, nil
	}
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM entrypoint_identity_bindings WHERE gateway_id = ?`, gatewayID)
	if err != nil {
		return 0, fmt.Errorf("EntrypointIdentityBindingsRepo.DeleteForGateway %q: %w", gatewayID, err)
	}
	n, affErr := res.RowsAffected()
	if affErr != nil {
		return 0, nil
	}
	return n, nil
}

func (r *EntrypointIdentityBindingsRepo) DeleteStale(ctx context.Context, cutoffMs int64) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM entrypoint_identity_bindings WHERE last_seen_at < ?`, cutoffMs)
	if err != nil {
		return 0, fmt.Errorf("EntrypointIdentityBindingsRepo.DeleteStale: %w", err)
	}
	n, affErr := res.RowsAffected()
	if affErr != nil {
		return 0, nil
	}
	return n, nil
}
