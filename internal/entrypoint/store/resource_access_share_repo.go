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

const (
	ResourceTypeProject     = "project"
	ResourceTypePromptGroup = "prompt_group"
)

type ResourceAccessShareRepo struct {
	db *DB
}

func NewResourceAccessShareRepo(db *DB) *ResourceAccessShareRepo {
	return &ResourceAccessShareRepo{db: db}
}

type ShareInput struct {
	UserEmail   string
	AccessLevel string
}

func (r *ResourceAccessShareRepo) GetCollaborators(ctx context.Context, resourceID, resourceType string) ([]ResourceAccessShare, error) {
	return getCollaboratorsImpl(ctx, r.db, resourceID, resourceType)
}

func (r *ResourceAccessShareRepo) GetCollaboratorsTx(ctx context.Context, tx *dbutil.Tx, resourceID, resourceType string) ([]ResourceAccessShare, error) {
	return getCollaboratorsImpl(ctx, tx, resourceID, resourceType)
}

func (r *ResourceAccessShareRepo) LockResourceTx(ctx context.Context, tx *dbutil.Tx, resourceID, resourceType string) error {
	if tx.Dialect() != "postgres" {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtext(?), hashtext(?))`,
		resourceID, resourceType); err != nil {
		return fmt.Errorf("LockResourceTx(%s/%s): %w", resourceType, resourceID, err)
	}
	return nil
}

func (r *ResourceAccessShareRepo) BatchShare(ctx context.Context, resourceID, resourceType string, shares []ShareInput, sharedBy string) (created, updated []ResourceAccessShare, err error) {
	return batchShareImpl(ctx, r.db, r.db.Dialect(), resourceID, resourceType, shares, sharedBy)
}

func (r *ResourceAccessShareRepo) BatchShareTx(ctx context.Context, tx *dbutil.Tx, resourceID, resourceType string, shares []ShareInput, sharedBy string) (created, updated []ResourceAccessShare, err error) {
	return batchShareImpl(ctx, tx, tx.Dialect(), resourceID, resourceType, shares, sharedBy)
}

func (r *ResourceAccessShareRepo) BatchUnshare(ctx context.Context, resourceID, resourceType string, emails []string) (int, []string, error) {
	return batchUnshareImpl(ctx, r.db, resourceID, resourceType, emails)
}

func (r *ResourceAccessShareRepo) BatchUnshareTx(ctx context.Context, tx *dbutil.Tx, resourceID, resourceType string, emails []string) (int, []string, error) {
	return batchUnshareImpl(ctx, tx, resourceID, resourceType, emails)
}

func (r *ResourceAccessShareRepo) GetSharedResourceIDs(ctx context.Context, userEmail, resourceType string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT resource_id FROM resource_access_shares
		 WHERE shared_with_user_email = ? AND resource_type = ?`,
		strings.ToLower(userEmail), resourceType)
}

func (r *ResourceAccessShareRepo) GetUserAccessLevels(ctx context.Context, userEmail, resourceType string) (map[string]string, error) {
	type row struct {
		ResourceID  string `db:"resource_id"`
		AccessLevel string `db:"access_level"`
	}
	rows, err := dbutil.QueryRows[row](ctx, r.db,
		`SELECT resource_id, access_level FROM resource_access_shares
		 WHERE shared_with_user_email = ? AND resource_type = ?`,
		strings.ToLower(userEmail), resourceType)
	if err != nil {
		return nil, fmt.Errorf("ResourceAccessShareRepo.GetUserAccessLevels: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, share := range rows {
		out[share.ResourceID] = share.AccessLevel
	}
	return out, nil
}

func (r *ResourceAccessShareRepo) HasAccess(ctx context.Context, resourceID, resourceType, userEmail string) (bool, error) {
	count, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT COUNT(*) FROM resource_access_shares
		 WHERE resource_id = ? AND resource_type = ? AND shared_with_user_email = ?`,
		resourceID, resourceType, strings.ToLower(userEmail))
	if err != nil {
		return false, fmt.Errorf("ResourceAccessShareRepo.HasAccess(%s): %w", resourceID, err)
	}
	return count > 0, nil
}

func (r *ResourceAccessShareRepo) UpdateAccessLevelTx(ctx context.Context, tx *dbutil.Tx, resourceID, resourceType, userEmail, accessLevel string) (*ResourceAccessShare, error) {
	email := strings.ToLower(userEmail)
	now := time.Now().UnixMilli()
	res, err := tx.ExecContext(ctx,
		`UPDATE resource_access_shares
		 SET access_level = ?, updated_at = ?
		 WHERE resource_id = ? AND resource_type = ? AND shared_with_user_email = ?`,
		accessLevel, now, resourceID, resourceType, email,
	)
	if err != nil {
		return nil, fmt.Errorf("ResourceAccessShareRepo.UpdateAccessLevelTx(%s/%s): %w", resourceID, email, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("ResourceAccessShareRepo.UpdateAccessLevelTx RowsAffected(%s/%s): %w", resourceID, email, err)
	}
	if rows == 0 {
		return nil, nil
	}
	row, err := dbutil.QueryRow[ResourceAccessShare](ctx, tx,
		`SELECT id, resource_id, resource_type, shared_with_user_email,
			access_level, shared_by_user_email, shared_at, updated_at
		 FROM resource_access_shares
		 WHERE resource_id = ? AND resource_type = ? AND shared_with_user_email = ?`,
		resourceID, resourceType, email,
	)
	if err != nil {
		return nil, fmt.Errorf("ResourceAccessShareRepo.UpdateAccessLevelTx readback(%s/%s): %w", resourceID, email, err)
	}
	return row, nil
}

func (r *ResourceAccessShareRepo) DeleteAllForResource(ctx context.Context, resourceID, resourceType string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM resource_access_shares WHERE resource_id = ? AND resource_type = ?`,
		resourceID, resourceType,
	)
	if err != nil {
		return fmt.Errorf("ResourceAccessShareRepo.DeleteAllForResource(%s): %w", resourceID, err)
	}
	return nil
}

func (r *ResourceAccessShareRepo) DeleteAllForResourceTx(ctx context.Context, tx *dbutil.Tx, resourceID, resourceType string) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM resource_access_shares WHERE resource_id = ? AND resource_type = ?`,
		resourceID, resourceType,
	)
	if err != nil {
		return fmt.Errorf("ResourceAccessShareRepo.DeleteAllForResourceTx(%s): %w", resourceID, err)
	}
	return nil
}

func getCollaboratorsImpl(ctx context.Context, q dbutil.Querier, resourceID, resourceType string) ([]ResourceAccessShare, error) {
	return dbutil.QueryRows[ResourceAccessShare](ctx, q,
		`SELECT id, resource_id, resource_type, shared_with_user_email,
			access_level, shared_by_user_email, shared_at, updated_at
		 FROM resource_access_shares
		 WHERE resource_id = ? AND resource_type = ?
		 ORDER BY shared_at DESC`,
		resourceID, resourceType)
}

func batchShareImpl(ctx context.Context, q dbutil.Querier, dialect, resourceID, resourceType string, shares []ShareInput, sharedBy string) (created, updated []ResourceAccessShare, err error) {
	now := time.Now().UnixMilli()

	type existingRow struct {
		ID            string `db:"id"`
		Email         string `db:"shared_with_user_email"`
		SharedByEmail string `db:"shared_by_user_email"`
		SharedAt      int64  `db:"shared_at"`
	}

	emails := make([]string, len(shares))
	for i, s := range shares {
		emails[i] = strings.ToLower(s.UserEmail)
	}
	placeholders, lookupArgs, inErr := dbutil.InClause(emails)
	if inErr != nil {
		return nil, nil, fmt.Errorf("batchShare: %w", inErr)
	}
	existingByEmail := make(map[string]existingRow)
	if placeholders != "" {
		args := make([]any, 0, 2+len(lookupArgs))
		args = append(args, resourceType, resourceID)
		args = append(args, lookupArgs...)
		rows, qErr := dbutil.QueryRows[existingRow](ctx, q, fmt.Sprintf(
			`SELECT id, shared_with_user_email, shared_by_user_email, shared_at FROM resource_access_shares
			 WHERE resource_type = ? AND resource_id = ? AND shared_with_user_email IN (%s)`,
			placeholders), args...)
		if qErr != nil {
			return nil, nil, qErr
		}
		for _, row := range rows {
			existingByEmail[row.Email] = row
		}
	}

	insertSQL := `INSERT INTO resource_access_shares
		(id, resource_id, resource_type, shared_with_user_email, access_level, shared_by_user_email, shared_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(dialect, insertSQL,
		"resource_type, resource_id, shared_with_user_email",
		[]string{"access_level", "updated_at"})

	for _, s := range shares {
		email := strings.ToLower(s.UserEmail)
		id := idgen.New()
		_, err = q.ExecContext(ctx, upsertSQL,
			id, resourceID, resourceType, email, s.AccessLevel, sharedBy, now, now)
		if err != nil {
			return nil, nil, fmt.Errorf("upsert share for %s: %w", email, err)
		}
		if existing, ok := existingByEmail[email]; ok {
			updatedAt := now
			updated = append(updated, ResourceAccessShare{
				ID: existing.ID, ResourceID: resourceID, ResourceType: resourceType,
				SharedWithUserEmail: email, AccessLevel: s.AccessLevel,
				SharedByUserEmail: existing.SharedByEmail, SharedAt: existing.SharedAt,
				UpdatedAt: &updatedAt,
			})
		} else {
			created = append(created, ResourceAccessShare{
				ID: id, ResourceID: resourceID, ResourceType: resourceType,
				SharedWithUserEmail: email, AccessLevel: s.AccessLevel,
				SharedByUserEmail: sharedBy, SharedAt: now,
			})
		}
	}
	return created, updated, nil
}

func batchUnshareImpl(ctx context.Context, q dbutil.Querier, resourceID, resourceType string, emails []string) (int, []string, error) {
	if len(emails) == 0 {
		return 0, nil, nil
	}

	placeholders := make([]string, len(emails))
	args := make([]any, 0, 2+len(emails))
	args = append(args, resourceType, resourceID)
	for i, email := range emails {
		placeholders[i] = "?"
		args = append(args, strings.ToLower(email))
	}

	query := fmt.Sprintf(
		`SELECT shared_with_user_email FROM resource_access_shares
		 WHERE resource_type = ? AND resource_id = ? AND shared_with_user_email IN (%s)`,
		strings.Join(placeholders, ", "),
	)
	deletedEmails, err := dbutil.QueryVals[string](ctx, q, query, args...)
	if err != nil {
		return 0, nil, fmt.Errorf("batchUnshare lookup: %w", err)
	}

	if len(deletedEmails) == 0 {
		return 0, nil, nil
	}

	deleteQuery := fmt.Sprintf(
		`DELETE FROM resource_access_shares
		 WHERE resource_type = ? AND resource_id = ? AND shared_with_user_email IN (%s)`,
		strings.Join(placeholders, ", "),
	)
	result, err := q.ExecContext(ctx, deleteQuery, args...)
	if err != nil {
		return 0, nil, fmt.Errorf("batchUnshare delete: %w", err)
	}
	count, _ := result.RowsAffected()
	return int(count), deletedEmails, nil
}
