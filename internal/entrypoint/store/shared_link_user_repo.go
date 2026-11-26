// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type SharedLinkUserRepo struct {
	db *DB
}

func NewSharedLinkUserRepo(db *DB) *SharedLinkUserRepo {
	return &SharedLinkUserRepo{db: db}
}

func (r *SharedLinkUserRepo) Add(ctx context.Context, u *SharedLinkUser) (*SharedLinkUser, error) {
	if u.ID == "" {
		u.ID = idgen.New()
	}
	if u.AddedAt == 0 {
		u.AddedAt = time.Now().UnixMilli()
	}
	u.UserEmail = strings.ToLower(strings.TrimSpace(u.UserEmail))

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO shared_link_users
			(id, share_id, user_email, access_level, added_at, added_by_user_id, original_access_level, original_added_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.ShareID, u.UserEmail, u.AccessLevel, u.AddedAt, u.AddedByUserID,
		u.OriginalAccessLevel, u.OriginalAddedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("SharedLinkUserRepo.Add(%s/%s): %w", u.ShareID, u.UserEmail, err)
	}
	return u, nil
}

func (r *SharedLinkUserRepo) UpdateAccessLevel(ctx context.Context, shareID, userEmail, newAccessLevel string) (*SharedLinkUser, error) {
	email := strings.ToLower(strings.TrimSpace(userEmail))
	existing, err := r.GetByShareAndEmail(ctx, shareID, email)
	if err != nil {
		return nil, fmt.Errorf("SharedLinkUserRepo.UpdateAccessLevel(%s): %w", shareID, err)
	}
	if existing == nil {
		return nil, nil
	}

	now := time.Now().UnixMilli()
	if existing.OriginalAccessLevel == nil {
		lvl := existing.AccessLevel
		existing.OriginalAccessLevel = &lvl
	}
	if existing.OriginalAddedAt == nil {
		prev := existing.AddedAt
		existing.OriginalAddedAt = &prev
	}
	existing.AccessLevel = newAccessLevel
	existing.AddedAt = now
	existing.ViewedAt = nil

	_, err = r.db.ExecContext(ctx,
		`UPDATE shared_link_users
		    SET access_level = ?, added_at = ?, original_access_level = ?, original_added_at = ?, viewed_at = NULL
		  WHERE share_id = ? AND user_email = ?`,
		existing.AccessLevel, existing.AddedAt, existing.OriginalAccessLevel, existing.OriginalAddedAt,
		shareID, email,
	)
	if err != nil {
		return nil, fmt.Errorf("SharedLinkUserRepo.UpdateAccessLevel(%s): %w", shareID, err)
	}
	return existing, nil
}

func (r *SharedLinkUserRepo) UpdateSnapshotTime(ctx context.Context, shareID, userEmail string, newTime int64) (bool, error) {
	return r.updateSnapshot(ctx, "UpdateSnapshotTime", shareID, userEmail,
		`UPDATE shared_link_users SET added_at = ?, viewed_at = NULL WHERE share_id = ? AND user_email = ?`,
		newTime)
}

func (r *SharedLinkUserRepo) RefreshOwnSnapshot(ctx context.Context, shareID, userEmail string, newTime int64) (bool, error) {
	return r.updateSnapshot(ctx, "RefreshOwnSnapshot", shareID, userEmail,
		`UPDATE shared_link_users SET added_at = ?, viewed_at = ? WHERE share_id = ? AND user_email = ?`,
		newTime, newTime)
}

func (r *SharedLinkUserRepo) updateSnapshot(ctx context.Context, op, shareID, userEmail, query string, times ...any) (bool, error) {
	email := strings.ToLower(strings.TrimSpace(userEmail))
	args := append(append([]any{}, times...), shareID, email)
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("SharedLinkUserRepo.%s(%s): %w", op, shareID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("SharedLinkUserRepo.%s(%s): %w", op, shareID, err)
	}
	return n > 0, nil
}

func (r *SharedLinkUserRepo) MarkViewed(ctx context.Context, shareID, userEmail string, viewedAt int64) (bool, error) {
	email := strings.ToLower(strings.TrimSpace(userEmail))
	const errFmt = "SharedLinkUserRepo.MarkViewed(%s): %w"

	res, err := r.db.ExecContext(ctx,
		`UPDATE shared_link_users
		    SET viewed_at = CASE WHEN COALESCE(viewed_at, 0) > ? THEN COALESCE(viewed_at, 0) ELSE ? END
		  WHERE share_id = ? AND user_email = ?
		    AND EXISTS (SELECT 1 FROM shared_links sl WHERE sl.share_id = shared_link_users.share_id AND sl.deleted_at IS NULL)`,
		viewedAt, viewedAt, shareID, email,
	)
	if err != nil {
		return false, fmt.Errorf(errFmt, shareID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf(errFmt, shareID, err)
	}
	return n > 0, nil
}

func (r *SharedLinkUserRepo) BatchRemove(ctx context.Context, shareID string, emails []string) (int, error) {
	if len(emails) == 0 {
		return 0, nil
	}
	normalized := make([]string, 0, len(emails))
	for _, e := range emails {
		if v := strings.ToLower(strings.TrimSpace(e)); v != "" {
			normalized = append(normalized, v)
		}
	}
	if len(normalized) == 0 {
		return 0, nil
	}
	const errFmt = "SharedLinkUserRepo.BatchRemove(%s): %w"
	ph, emailArgs, err := dbutil.InClause(normalized)
	if err != nil {
		return 0, fmt.Errorf(errFmt, shareID, err)
	}
	args := append([]any{shareID}, emailArgs...)

	res, err := r.db.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM shared_link_users WHERE share_id = ? AND user_email IN (%s)`, ph),
		args...,
	)
	if err != nil {
		return 0, fmt.Errorf(errFmt, shareID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf(errFmt, shareID, err)
	}
	return int(n), nil
}

func (r *SharedLinkUserRepo) ListByShareID(ctx context.Context, shareID string) ([]SharedLinkUser, error) {
	return dbutil.QueryRows[SharedLinkUser](ctx, r.db,
		`SELECT id, share_id, user_email, access_level, added_at, added_by_user_id, original_access_level, original_added_at, viewed_at
		 FROM shared_link_users WHERE share_id = ? ORDER BY added_at ASC`, shareID)
}

func (r *SharedLinkUserRepo) HasAccess(ctx context.Context, shareID, userEmail string) (bool, error) {
	email := strings.ToLower(strings.TrimSpace(userEmail))
	count, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM shared_link_users WHERE share_id = ? AND user_email = ?`,
		shareID, email)
	if err != nil {
		return false, fmt.Errorf("SharedLinkUserRepo.HasAccess(%s): %w", shareID, err)
	}
	return count > 0, nil
}

func (r *SharedLinkUserRepo) HasAny(ctx context.Context, shareID string) (bool, error) {
	count, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM shared_link_users WHERE share_id = ?`, shareID)
	if err != nil {
		return false, fmt.Errorf("SharedLinkUserRepo.HasAny(%s): %w", shareID, err)
	}
	return count > 0, nil
}

func (r *SharedLinkUserRepo) GetByShareAndEmail(ctx context.Context, shareID, userEmail string) (*SharedLinkUser, error) {
	email := strings.ToLower(strings.TrimSpace(userEmail))
	return dbutil.QueryRow[SharedLinkUser](ctx, r.db,
		`SELECT id, share_id, user_email, access_level, added_at, added_by_user_id, original_access_level, original_added_at, viewed_at
		 FROM shared_link_users WHERE share_id = ? AND user_email = ?`,
		shareID, email)
}
