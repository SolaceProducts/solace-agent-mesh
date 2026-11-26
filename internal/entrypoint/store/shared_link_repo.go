// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type SharedLinkRepo struct {
	db *DB
}

func NewSharedLinkRepo(db *DB) *SharedLinkRepo {
	return &SharedLinkRepo{db: db}
}

func (r *SharedLinkRepo) Create(ctx context.Context, s *SharedLink) error {
	now := time.Now().UnixMilli()
	s.CreatedTime = now
	s.UpdatedTime = now
	s.SnapshotTime = now

	_, err := r.db.ExecContext(ctx,
		`INSERT INTO shared_links (share_id, session_id, user_id, title, is_public, require_authentication, allowed_domains, created_time, updated_time, snapshot_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ShareID, s.SessionID, s.UserID, s.Title, dbutil.BoolToInt(s.IsPublic), dbutil.BoolToInt(s.RequireAuthentication), s.AllowedDomains, s.CreatedTime, s.UpdatedTime, s.SnapshotTime,
	)
	if err != nil {
		return fmt.Errorf("SharedLinkRepo.Create(%s): %w", s.ShareID, err)
	}
	return nil
}

func (r *SharedLinkRepo) GetByID(ctx context.Context, shareID string) (*SharedLink, error) {
	return dbutil.QueryRow[SharedLink](ctx, r.db,
		`SELECT share_id, session_id, user_id, title, is_public, require_authentication, allowed_domains, created_time, updated_time, snapshot_time, deleted_at
		 FROM shared_links WHERE share_id = ? AND deleted_at IS NULL`, shareID)
}

func (r *SharedLinkRepo) GetBySessionAndUser(ctx context.Context, sessionID, userID string) (*SharedLink, error) {
	return dbutil.QueryRow[SharedLink](ctx, r.db,
		`SELECT share_id, session_id, user_id, title, is_public, require_authentication, allowed_domains, created_time, updated_time, snapshot_time, deleted_at
		 FROM shared_links WHERE session_id = ? AND user_id = ? AND deleted_at IS NULL`, sessionID, userID)
}

func (r *SharedLinkRepo) ListByUser(ctx context.Context, userID string, limit, offset int) ([]*SharedLink, error) {
	return dbutil.QueryRowPtrs[SharedLink](ctx, r.db,
		`SELECT share_id, session_id, user_id, title, is_public, require_authentication, allowed_domains, created_time, updated_time, snapshot_time, deleted_at
		 FROM shared_links WHERE user_id = ? AND deleted_at IS NULL ORDER BY created_time DESC, share_id DESC LIMIT ? OFFSET ?`,
		userID, limit, offset)
}

func (r *SharedLinkRepo) ListSharedWithUser(ctx context.Context, userEmail string, limit, offset int) ([]*SharedWithUserRow, error) {
	return dbutil.QueryRowPtrs[SharedWithUserRow](ctx, r.db,
		`SELECT sl.share_id, sl.session_id, sl.user_id AS owner_user_id, sl.title,
		        slu.access_level, slu.added_at, slu.viewed_at
		 FROM shared_link_users slu
		 INNER JOIN shared_links sl ON sl.share_id = slu.share_id
		 WHERE slu.user_email = ? AND sl.deleted_at IS NULL
		 ORDER BY slu.added_at DESC, sl.share_id DESC LIMIT ? OFFSET ?`,
		userEmail, limit, offset)
}

func (r *SharedLinkRepo) CountSharedWithUser(ctx context.Context, userEmail string) (int, error) {
	count, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM shared_link_users slu
		 INNER JOIN shared_links sl ON sl.share_id = slu.share_id
		 WHERE slu.user_email = ? AND sl.deleted_at IS NULL`, userEmail)
	if err != nil {
		return 0, fmt.Errorf("SharedLinkRepo.CountSharedWithUser: %w", err)
	}
	return count, nil
}

func (r *SharedLinkRepo) Update(ctx context.Context, s *SharedLink) error {
	s.UpdatedTime = time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE shared_links SET title = ?, is_public = ?, require_authentication = ?, allowed_domains = ?, updated_time = ?
		 WHERE share_id = ? AND deleted_at IS NULL`,
		s.Title, dbutil.BoolToInt(s.IsPublic), dbutil.BoolToInt(s.RequireAuthentication), s.AllowedDomains, s.UpdatedTime, s.ShareID,
	)
	if err != nil {
		return fmt.Errorf("SharedLinkRepo.Update(%s): %w", s.ShareID, err)
	}
	return nil
}

func (r *SharedLinkRepo) UpdateLinkSnapshotTime(ctx context.Context, shareID string, newTime int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE shared_links SET snapshot_time = ?, updated_time = ? WHERE share_id = ? AND deleted_at IS NULL`,
		newTime, newTime, shareID,
	)
	if err != nil {
		return false, fmt.Errorf("SharedLinkRepo.UpdateLinkSnapshotTime(%s): %w", shareID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("SharedLinkRepo.UpdateLinkSnapshotTime(%s): %w", shareID, err)
	}
	return n > 0, nil
}

func (r *SharedLinkRepo) SoftDelete(ctx context.Context, shareID string) error {
	now := time.Now().UnixMilli()
	_, err := r.db.ExecContext(ctx,
		`UPDATE shared_links SET deleted_at = ?, updated_time = ? WHERE share_id = ? AND deleted_at IS NULL`,
		now, now, shareID,
	)
	if err != nil {
		return fmt.Errorf("SharedLinkRepo.SoftDelete(%s): %w", shareID, err)
	}
	return nil
}
