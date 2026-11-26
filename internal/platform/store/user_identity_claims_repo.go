// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type UserIdentityClaimsRepo struct {
	db *DB
}

func NewUserIdentityClaimsRepo(db *DB) *UserIdentityClaimsRepo {
	return &UserIdentityClaimsRepo{db: db}
}

func (r *UserIdentityClaimsRepo) UserIDsByClaim(ctx context.Context, claimKey, claimValueCanonical string) ([]string, error) {
	if claimKey == "" || claimValueCanonical == "" {
		return nil, nil
	}
	ids, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT user_id FROM user_identity_claims
		 WHERE claim_key = ? AND claim_value_canonical = ? AND verified = 1
		 ORDER BY user_id`, claimKey, claimValueCanonical)
	if err != nil {
		return nil, fmt.Errorf("UserIdentityClaimsRepo.UserIDsByClaim key=%q: %w", claimKey, err)
	}
	return ids, nil
}

func (r *UserIdentityClaimsRepo) ClaimsForUser(ctx context.Context, userID string) ([]UserIdentityClaim, error) {
	return r.claimsForUserOn(ctx, r.db, userID)
}

func (r *UserIdentityClaimsRepo) ClaimsForUserInTx(ctx context.Context, tx *dbutil.Tx, userID string) ([]UserIdentityClaim, error) {
	return r.claimsForUserOn(ctx, tx, userID)
}

func (r *UserIdentityClaimsRepo) claimsForUserOn(ctx context.Context, q dbutil.Querier, userID string) ([]UserIdentityClaim, error) {
	rows, err := dbutil.QueryRows[UserIdentityClaim](ctx, q,
		`SELECT user_id, claim_key, claim_value_canonical, verified, updated_time
		 FROM user_identity_claims WHERE user_id = ?
		 ORDER BY claim_key, claim_value_canonical`, userID)
	if err != nil {
		return nil, fmt.Errorf("UserIdentityClaimsRepo.ClaimsForUser %s: %w", userID, err)
	}
	return rows, nil
}

func (r *UserIdentityClaimsRepo) ClaimsForUserPage(ctx context.Context, userID string, offset, limit int) ([]UserIdentityClaim, int, error) {
	const wrapErr = "UserIdentityClaimsRepo.ClaimsForUserPage %s: %w"
	total, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM user_identity_claims WHERE user_id = ?`, userID)
	if err != nil {
		return nil, 0, fmt.Errorf(wrapErr, userID, err)
	}
	if limit <= 0 || offset >= total {
		return []UserIdentityClaim{}, total, nil
	}
	rows, err := dbutil.QueryRows[UserIdentityClaim](ctx, r.db,
		`SELECT user_id, claim_key, claim_value_canonical, verified, updated_time
		 FROM user_identity_claims WHERE user_id = ?
		 ORDER BY claim_key, claim_value_canonical
		 LIMIT ? OFFSET ?`, userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf(wrapErr, userID, err)
	}
	return rows, total, nil
}

func (r *UserIdentityClaimsRepo) DeleteForUser(ctx context.Context, userID string) (int64, error) {
	return r.deleteForUserOn(ctx, r.db, userID)
}

func (r *UserIdentityClaimsRepo) DeleteForUserInTx(ctx context.Context, tx *dbutil.Tx, userID string) (int64, error) {
	return r.deleteForUserOn(ctx, tx, userID)
}

func (r *UserIdentityClaimsRepo) deleteForUserOn(ctx context.Context, q dbutil.Querier, userID string) (int64, error) {
	if userID == "" {
		return 0, nil
	}
	res, err := q.ExecContext(ctx, `DELETE FROM user_identity_claims WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("UserIdentityClaimsRepo.DeleteForUser %s: %w", userID, err)
	}
	n, affErr := res.RowsAffected()
	if affErr != nil {
		return 0, nil
	}
	return n, nil
}

func replaceIdentityClaims(ctx context.Context, q dbutil.Querier, userID string, claims []UserIdentityClaim, now int64) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM user_identity_claims WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("clear identity claims: %w", err)
	}
	seen := make(map[string]struct{}, len(claims))
	for _, c := range claims {
		if c.ClaimKey == "" || c.ClaimValueCanonical == "" {
			continue
		}
		primaryKey := c.ClaimKey + "\x00" + c.ClaimValueCanonical
		if _, dup := seen[primaryKey]; dup {
			continue
		}
		seen[primaryKey] = struct{}{}
		if _, err := q.ExecContext(ctx,
			`INSERT INTO user_identity_claims (user_id, claim_key, claim_value_canonical, verified, updated_time)
			 VALUES (?, ?, ?, ?, ?)`, userID, c.ClaimKey, c.ClaimValueCanonical, dbutil.BoolToInt(c.Verified), now); err != nil {
			return fmt.Errorf("insert identity claim key=%q: %w", c.ClaimKey, err)
		}
	}
	return nil
}
