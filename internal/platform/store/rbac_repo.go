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
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/google/uuid"
)

var ErrRBACNotFound = errors.New("rbac: not found")

var ErrRBACUniqueConflict = errors.New("rbac: unique constraint conflict")

func classifySQLError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate") {
		return fmt.Errorf("%w: %v", ErrRBACUniqueConflict, err)
	}
	return err
}

type RBACRepo struct {
	db *DB
}

func NewRBACRepo(db *DB) *RBACRepo {
	return &RBACRepo{db: db}
}

func (r *RBACRepo) DB() *DB { return r.db }

func (r *RBACRepo) WithTx(ctx context.Context, fn func(tx *dbutil.Tx) error) error {
	return r.db.WithTx(ctx, fn)
}

type RBACSnapshot struct {
	Roles             []*RBACRole
	RoleScopes        map[string][]string
	RoleInheritance   map[string][]string
	UserRoles         []*RBACUserRole
	ClaimMappings     []*RBACIdpClaimMapping
	ClaimMappingRoles map[string][]string
	DefaultRoleIDs    []string
}

func (r *RBACRepo) Snapshot(ctx context.Context) (*RBACSnapshot, error) {
	snap := &RBACSnapshot{
		RoleScopes:        map[string][]string{},
		RoleInheritance:   map[string][]string{},
		ClaimMappingRoles: map[string][]string{},
	}
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		roles, err := dbutil.QueryRowPtrs[RBACRole](ctx, tx,
			`SELECT id, name, description, created_by, updated_by, created_time, updated_time, builtin
			 FROM rbac_roles ORDER BY name`)
		if err != nil {
			return fmt.Errorf("snapshot roles: %w", err)
		}
		snap.Roles = roles

		type roleScopeRow struct {
			RoleID string `db:"role_id"`
			Scope  string `db:"scope"`
		}
		scopes, err := dbutil.QueryRowPtrs[roleScopeRow](ctx, tx,
			`SELECT role_id, scope FROM rbac_role_scopes`)
		if err != nil {
			return fmt.Errorf("snapshot role scopes: %w", err)
		}
		for _, s := range scopes {
			snap.RoleScopes[s.RoleID] = append(snap.RoleScopes[s.RoleID], s.Scope)
		}

		type roleParentRow struct {
			RoleID       string `db:"role_id"`
			ParentRoleID string `db:"parent_role_id"`
		}
		parents, err := dbutil.QueryRowPtrs[roleParentRow](ctx, tx,
			`SELECT role_id, parent_role_id FROM rbac_role_inheritance`)
		if err != nil {
			return fmt.Errorf("snapshot role inheritance: %w", err)
		}
		for _, p := range parents {
			snap.RoleInheritance[p.RoleID] = append(snap.RoleInheritance[p.RoleID], p.ParentRoleID)
		}

		userRoles, err := dbutil.QueryRowPtrs[RBACUserRole](ctx, tx,
			`SELECT id, user_id, role_id, subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time
			 FROM rbac_user_roles`)
		if err != nil {
			return fmt.Errorf("snapshot user roles: %w", err)
		}
		snap.UserRoles = userRoles

		claims, err := dbutil.QueryRowPtrs[RBACIdpClaimMapping](ctx, tx,
			`SELECT id, oidc_provider, claim_key, claim_value, name, created_by, updated_by, created_time, updated_time
			 FROM rbac_idp_claim_mappings`)
		if err != nil {
			return fmt.Errorf("snapshot claim mappings: %w", err)
		}
		snap.ClaimMappings = claims

		type claimRoleRow struct {
			MappingID string `db:"claim_mapping_id"`
			RoleID    string `db:"role_id"`
		}
		claimRoles, err := dbutil.QueryRowPtrs[claimRoleRow](ctx, tx,
			`SELECT claim_mapping_id, role_id FROM rbac_idp_claim_mapping_roles`)
		if err != nil {
			return fmt.Errorf("snapshot claim mapping roles: %w", err)
		}
		for _, cr := range claimRoles {
			snap.ClaimMappingRoles[cr.MappingID] = append(snap.ClaimMappingRoles[cr.MappingID], cr.RoleID)
		}

		defaults, err := dbutil.QueryVals[string](ctx, tx,
			`SELECT role_id FROM rbac_default_roles`)
		if err != nil {
			return fmt.Errorf("snapshot default roles: %w", err)
		}
		snap.DefaultRoleIDs = defaults
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("RBACRepo.Snapshot: %w", err)
	}
	return snap, nil
}

func (r *RBACRepo) CreateRole(ctx context.Context, role *RBACRole, scopes, parentRoleIDs []string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return r.CreateRoleInTx(ctx, tx, role, scopes, parentRoleIDs)
	})
}

func (r *RBACRepo) CreateRoleInTx(ctx context.Context, tx *dbutil.Tx, role *RBACRole, scopes, parentRoleIDs []string) error {
	if role.ID == "" {
		role.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if role.CreatedTime == 0 {
		role.CreatedTime = now
	}
	if role.UpdatedTime == 0 {
		role.UpdatedTime = now
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rbac_roles (id, name, description, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		role.ID, role.Name, role.Description, role.CreatedBy, role.UpdatedBy, role.CreatedTime, role.UpdatedTime,
	); err != nil {
		return fmt.Errorf("insert rbac_role: %w", classifySQLError(err))
	}
	if err := r.setRoleScopes(ctx, tx, role.ID, scopes); err != nil {
		return err
	}
	return r.setRoleParents(ctx, tx, role.ID, parentRoleIDs)
}

func (r *RBACRepo) UpdateRole(ctx context.Context, role *RBACRole, scopes, parentRoleIDs []string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return r.UpdateRoleInTx(ctx, tx, role, scopes, parentRoleIDs)
	})
}

func (r *RBACRepo) UpdateRoleInTx(ctx context.Context, tx *dbutil.Tx, role *RBACRole, scopes, parentRoleIDs []string) error {
	if _, err := uuid.Parse(role.ID); err != nil {
		return ErrRBACNotFound
	}
	role.UpdatedTime = time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx,
		`UPDATE rbac_roles SET name = ?, description = ?, updated_by = ?, updated_time = ?
		 WHERE id = ? AND builtin = 0`,
		role.Name, role.Description, role.UpdatedBy, role.UpdatedTime, role.ID,
	)
	if err != nil {
		return fmt.Errorf("update rbac_role: %w", classifySQLError(err))
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrRBACNotFound
	}
	if err := r.setRoleScopes(ctx, tx, role.ID, scopes); err != nil {
		return err
	}
	return r.setRoleParents(ctx, tx, role.ID, parentRoleIDs)
}

func (r *RBACRepo) DeleteRole(ctx context.Context, id string) error {
	return r.deleteRoleOn(ctx, r.db, id)
}

func (r *RBACRepo) DeleteRoleInTx(ctx context.Context, tx *dbutil.Tx, id string) error {
	return r.deleteRoleOn(ctx, tx, id)
}

func (r *RBACRepo) deleteRoleOn(ctx context.Context, q dbutil.Querier, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrRBACNotFound
	}
	result, err := q.ExecContext(ctx, `DELETE FROM rbac_roles WHERE id = ? AND builtin = 0`, id)
	if err != nil {
		return fmt.Errorf("RBACRepo.DeleteRole(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrRBACNotFound
	}
	return nil
}

func (r *RBACRepo) GetRoleByID(ctx context.Context, id string) (*RBACRole, []string, []string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil, nil, nil
	}
	role, err := dbutil.QueryRow[RBACRole](ctx, r.db,
		`SELECT id, name, description, created_by, updated_by, created_time, updated_time, builtin
		 FROM rbac_roles WHERE id = ?`, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("RBACRepo.GetRoleByID(%s): %w", id, err)
	}
	if role == nil {
		return nil, nil, nil, nil
	}
	scopes, err := r.getRoleScopes(ctx, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("RBACRepo.GetRoleByID(%s) scopes: %w", id, err)
	}
	parents, err := r.getRoleParents(ctx, id)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("RBACRepo.GetRoleByID(%s) parents: %w", id, err)
	}
	return role, scopes, parents, nil
}

func (r *RBACRepo) GetRoleByName(ctx context.Context, name string) (*RBACRole, error) {
	return dbutil.QueryRow[RBACRole](ctx, r.db,
		`SELECT id, name, description, created_by, updated_by, created_time, updated_time, builtin
		 FROM rbac_roles WHERE name = ?`, name)
}

func (r *RBACRepo) ListRoles(ctx context.Context) ([]*RBACRole, error) {
	return dbutil.QueryRowPtrs[RBACRole](ctx, r.db,
		`SELECT id, name, description, created_by, updated_by, created_time, updated_time, builtin
		 FROM rbac_roles ORDER BY name`)
}

func (r *RBACRepo) ListRolesForUser(ctx context.Context, userID string) ([]*RBACUserRole, error) {
	return dbutil.QueryRowPtrs[RBACUserRole](ctx, r.db,
		`SELECT id, user_id, role_id, subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time
		 FROM rbac_user_roles WHERE user_id = ? ORDER BY created_time`, userID)
}

func (r *RBACRepo) AddUserRole(ctx context.Context, userID, roleID, actor string) error {
	_, err := r.AddUserRoleWithSubject(ctx, userID, roleID, actor, RoleGrantSubject{})
	return err
}

func (r *RBACRepo) AddUserRoleWithSubject(ctx context.Context, userID, roleID, actor string, subject RoleGrantSubject) (string, error) {
	return r.addUserRoleWithSubjectOn(ctx, r.db, userID, roleID, actor, subject)
}

func (r *RBACRepo) AddUserRoleWithSubjectInTx(ctx context.Context, tx *dbutil.Tx, userID, roleID, actor string, subject RoleGrantSubject) (string, error) {
	return r.addUserRoleWithSubjectOn(ctx, tx, userID, roleID, actor, subject)
}

func (r *RBACRepo) AddNewUserRoleWithSubjectInTx(ctx context.Context, tx *dbutil.Tx, userID, roleID, actor string, subject RoleGrantSubject) (string, error) {
	id, inserted, _, err := insertUserRoleWithSubjectOn(ctx, tx, userID, roleID, actor, subject)
	if err != nil {
		return "", err
	}
	if !inserted {
		return "", fmt.Errorf("RBACRepo.AddNewUserRoleWithSubject: %w", ErrRBACUniqueConflict)
	}
	return id, nil
}

func insertUserRoleWithSubjectOn(ctx context.Context, q dbutil.Querier, userID, roleID, actor string, subject RoleGrantSubject) (id string, inserted bool, now int64, err error) {
	now = time.Now().UnixMilli()
	id = idgen.New()
	res, err := q.ExecContext(ctx,
		`INSERT INTO rbac_user_roles
		 (id, user_id, role_id, subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (user_id, role_id) DO NOTHING`,
		id, userID, roleID, subject.Type, subject.Value, subject.Issuer, actor, actor, now, now)
	if err != nil {
		return "", false, 0, fmt.Errorf("RBACRepo.AddUserRoleWithSubject: %w", classifySQLError(err))
	}
	n, affErr := res.RowsAffected()
	return id, affErr == nil && n == 1, now, nil
}

func (r *RBACRepo) addUserRoleWithSubjectOn(ctx context.Context, q dbutil.Querier, userID, roleID, actor string, subject RoleGrantSubject) (string, error) {
	const wrapErr = "RBACRepo.AddUserRoleWithSubject: %w"
	id, inserted, now, err := insertUserRoleWithSubjectOn(ctx, q, userID, roleID, actor, subject)
	if err != nil {
		return "", err
	}
	if inserted {
		return id, nil
	}
	if subject.Type != "" {
		if _, err := q.ExecContext(ctx,
			`UPDATE rbac_user_roles
			 SET subject_type = ?, subject_value = ?, issuer = ?, updated_by = ?, updated_time = ?
			 WHERE user_id = ? AND role_id = ? AND subject_type = ''`,
			subject.Type, subject.Value, subject.Issuer, actor, now, userID, roleID); err != nil {
			return "", fmt.Errorf(wrapErr, classifySQLError(err))
		}
	}
	var existing string
	if err := q.QueryRowContext(ctx,
		`SELECT id FROM rbac_user_roles WHERE user_id = ? AND role_id = ?`,
		userID, roleID).Scan(&existing); err != nil {
		return "", fmt.Errorf(wrapErr, classifySQLError(err))
	}
	return existing, nil
}

func (r *RBACRepo) DeleteUserRole(ctx context.Context, id string) error {
	return r.deleteUserRoleOn(ctx, r.db, id)
}

func (r *RBACRepo) DeleteUserRoleInTx(ctx context.Context, tx *dbutil.Tx, id string) error {
	return r.deleteUserRoleOn(ctx, tx, id)
}

func (r *RBACRepo) deleteUserRoleOn(ctx context.Context, q dbutil.Querier, id string) error {
	result, err := q.ExecContext(ctx, `DELETE FROM rbac_user_roles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("RBACRepo.DeleteUserRole(%s): %w", id, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrRBACNotFound
	}
	return nil
}

func (r *RBACRepo) DeletePendingUserRole(ctx context.Context, id string) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM rbac_pending_user_roles WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("RBACRepo.DeletePendingUserRole(%s): %w", id, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrRBACNotFound
	}
	return nil
}

func (r *RBACRepo) DeletePendingUserRolesForSubject(ctx context.Context, subjectType, subjectValue, issuer string) (int64, error) {
	return r.deletePendingUserRolesForSubjectOn(ctx, r.db, subjectType, subjectValue, issuer)
}

func (r *RBACRepo) DeletePendingUserRolesForSubjectInTx(ctx context.Context, tx *dbutil.Tx, subjectType, subjectValue, issuer string) (int64, error) {
	return r.deletePendingUserRolesForSubjectOn(ctx, tx, subjectType, subjectValue, issuer)
}

func (r *RBACRepo) deletePendingUserRolesForSubjectOn(ctx context.Context, q dbutil.Querier, subjectType, subjectValue, issuer string) (int64, error) {
	result, err := q.ExecContext(ctx,
		`DELETE FROM rbac_pending_user_roles WHERE subject_type = ? AND subject_value = ? AND issuer = ?`,
		subjectType, subjectValue, issuer)
	if err != nil {
		return 0, fmt.Errorf("RBACRepo.DeletePendingUserRolesForSubject: %w", err)
	}
	n, _ := result.RowsAffected()
	return n, nil
}

func (r *RBACRepo) ListUserRoles(ctx context.Context) ([]*RBACUserRole, error) {
	return dbutil.QueryRowPtrs[RBACUserRole](ctx, r.db,
		`SELECT id, user_id, role_id, subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time
		 FROM rbac_user_roles`)
}

func (r *RBACRepo) ListGrantHolders(ctx context.Context) ([]RBACGrantHolder, error) {
	return dbutil.QueryRows[RBACGrantHolder](ctx, r.db,
		`SELECT u.id, NULLIF(u.email, '') AS email, u.sub, u.issuer,
		 `+userEmailSharedSQL+` AS email_shared
		 FROM users u
		 WHERE u.id IN (SELECT user_id FROM rbac_user_roles)`)
}

func (r *RBACRepo) GetUserRole(ctx context.Context, id string) (*RBACUserRole, error) {
	row, err := dbutil.QueryRow[RBACUserRole](ctx, r.db,
		`SELECT id, user_id, role_id, subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time
		 FROM rbac_user_roles WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("RBACRepo.GetUserRole(%s): %w", id, err)
	}
	return row, nil
}

func (r *RBACRepo) SetUserRoleSubjectInTx(ctx context.Context, tx *dbutil.Tx, id, actor string, subject RoleGrantSubject) error {
	result, err := tx.ExecContext(ctx,
		`UPDATE rbac_user_roles
		 SET subject_type = ?, subject_value = ?, issuer = ?, updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		subject.Type, subject.Value, subject.Issuer, actor, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("RBACRepo.SetUserRoleSubject(%s): %w", id, classifySQLError(err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrRBACNotFound
	}
	return nil
}

func (r *RBACRepo) SetUserRoles(ctx context.Context, userID string, roleIDs []string, actor string) error {
	return r.SetUserRolesWithSubject(ctx, userID, roleIDs, actor, RoleGrantSubject{})
}

func (r *RBACRepo) SetUserRolesWithSubject(ctx context.Context, userID string, roleIDs []string, actor string, subject RoleGrantSubject) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return r.SetUserRolesWithSubjectInTx(ctx, tx, userID, roleIDs, actor, subject)
	})
}

func (r *RBACRepo) SetUserRolesWithSubjectInTx(ctx context.Context, tx *dbutil.Tx, userID string, roleIDs []string, actor string, subject RoleGrantSubject) error {
	now := time.Now().UnixMilli()
	desired := make(map[string]struct{}, len(roleIDs))
	for _, roleID := range roleIDs {
		desired[roleID] = struct{}{}
	}
	if tx.Dialect() == "postgres" {
		var locked string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = ? FOR UPDATE`, userID).Scan(&locked); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lock user for role grant update: %w", err)
		}
	}
	current, err := dbutil.QueryRowPtrs[RBACUserRole](ctx, tx,
		`SELECT id, user_id, role_id, subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time
		 FROM rbac_user_roles WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("list rbac_user_roles: %w", err)
	}
	existing := make(map[string]struct{}, len(current))
	var toRemove []string
	for _, row := range current {
		existing[row.RoleID] = struct{}{}
		if _, keep := desired[row.RoleID]; !keep {
			toRemove = append(toRemove, row.RoleID)
		}
	}
	if len(toRemove) > 0 {
		placeholders, args, inErr := dbutil.InClause(toRemove)
		if inErr != nil {
			return fmt.Errorf("build removed-role clause: %w", inErr)
		}
		args = append([]any{userID}, args...)
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM rbac_user_roles WHERE user_id = ? AND role_id IN (`+placeholders+`)`,
			args...); err != nil {
			return fmt.Errorf("remove rbac_user_roles: %w", err)
		}
	}
	for roleID := range desired {
		if _, already := existing[roleID]; already {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO rbac_user_roles
			 (id, user_id, role_id, subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			idgen.New(), userID, roleID, subject.Type, subject.Value, subject.Issuer, actor, actor, now, now); err != nil {
			return fmt.Errorf("insert rbac_user_role: %w", classifySQLError(err))
		}
	}
	return nil
}

func (r *RBACRepo) CreatePendingUserRole(ctx context.Context, p *RBACPendingUserRole) error {
	if p.ID == "" {
		p.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if p.CreatedTime == 0 {
		p.CreatedTime = now
	}
	if p.UpdatedTime == 0 {
		p.UpdatedTime = now
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO rbac_pending_user_roles
		 (id, subject_type, subject_value, issuer, role_id, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.SubjectType, p.SubjectValue, p.Issuer, p.RoleID,
		p.CreatedBy, p.UpdatedBy, p.CreatedTime, p.UpdatedTime,
	)
	if err != nil {
		return fmt.Errorf("RBACRepo.CreatePendingUserRole: %w", classifySQLError(err))
	}
	return nil
}

func (r *RBACRepo) ListPendingUserRoles(ctx context.Context) ([]*RBACPendingUserRole, error) {
	return dbutil.QueryRowPtrs[RBACPendingUserRole](ctx, r.db,
		`SELECT id, subject_type, subject_value, issuer, role_id, created_by, updated_by, created_time, updated_time
		 FROM rbac_pending_user_roles`)
}

func (r *RBACRepo) HasGrantMaterializedSince(ctx context.Context, userID, actor string, since time.Time) (bool, error) {
	n, err := dbutil.QueryVal[int](ctx, r.db,
		`SELECT 1 FROM rbac_user_roles WHERE user_id = ? AND updated_by = ? AND updated_time >= ? LIMIT 1`,
		userID, actor, since.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("RBACRepo.HasGrantMaterializedSince: %w", err)
	}
	return n == 1, nil
}

func (r *RBACRepo) MaterializePendingForUser(ctx context.Context, u *User, actor string) ([]string, error) {
	var granted []string
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		var txErr error
		granted, txErr = r.MaterializePendingForUserInTx(ctx, tx, u, actor)
		return txErr
	})
	if err != nil {
		return nil, err
	}
	return granted, nil
}

func (r *RBACRepo) MaterializePendingForUserInTx(ctx context.Context, tx *dbutil.Tx, u *User, actor string) ([]string, error) {
	email := ""
	if u.Email != nil {
		email = strings.ToLower(strings.TrimSpace(*u.Email))
	}
	resolvedIdentity := email
	if resolvedIdentity == "" {
		resolvedIdentity = u.Sub
	}

	var granted []string
	matched, err := dbutil.QueryRowPtrs[RBACPendingUserRole](ctx, tx,
		`SELECT id, subject_type, subject_value, issuer, role_id, created_by, updated_by, created_time, updated_time
		 FROM rbac_pending_user_roles
		 WHERE (subject_type = 'email'  AND subject_value = ?)
		    OR (subject_type = 'sub'    AND issuer = ? AND subject_value = ?)
		    OR (subject_type = 'legacy' AND subject_value = ?)`,
		email, u.Issuer, u.Sub, resolvedIdentity)
	if err != nil {
		return nil, fmt.Errorf("match pending grants: %w", err)
	}
	now := time.Now().UnixMilli()
	for _, p := range matched {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO rbac_user_roles
			 (id, user_id, role_id, subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (user_id, role_id) DO NOTHING`,
			idgen.New(), u.ID, p.RoleID, p.SubjectType, p.SubjectValue, p.Issuer, p.CreatedBy, actor, p.CreatedTime, now)
		if err != nil {
			return nil, fmt.Errorf("materialize grant: %w", classifySQLError(err))
		}
		inserted := false
		if n, affErr := res.RowsAffected(); affErr == nil && n == 1 {
			inserted = true
		}
		if !inserted {
			if _, err := tx.ExecContext(ctx,
				`UPDATE rbac_user_roles
				 SET subject_type = ?, subject_value = ?, issuer = ?, updated_by = ?, updated_time = ?
				 WHERE user_id = ? AND role_id = ? AND subject_type = ''`,
				p.SubjectType, p.SubjectValue, p.Issuer, actor, now, u.ID, p.RoleID); err != nil {
				return nil, fmt.Errorf("claim grant provenance: %w", classifySQLError(err))
			}
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM rbac_pending_user_roles WHERE id = ?`, p.ID); err != nil {
			return nil, fmt.Errorf("consume pending grant: %w", err)
		}
		if inserted {
			granted = append(granted, p.RoleID)
		}
	}
	return granted, nil
}

func (r *RBACRepo) CreateClaimMapping(ctx context.Context, m *RBACIdpClaimMapping, roleIDs []string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return r.CreateClaimMappingInTx(ctx, tx, m, roleIDs)
	})
}

func (r *RBACRepo) CreateClaimMappingInTx(ctx context.Context, tx *dbutil.Tx, m *RBACIdpClaimMapping, roleIDs []string) error {
	if m.ID == "" {
		m.ID = idgen.New()
	}
	now := time.Now().UnixMilli()
	if m.CreatedTime == 0 {
		m.CreatedTime = now
	}
	if m.UpdatedTime == 0 {
		m.UpdatedTime = now
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rbac_idp_claim_mappings
		 (id, oidc_provider, claim_key, claim_value, name, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.OIDCProvider, m.ClaimKey, m.ClaimValue, m.Name,
		m.CreatedBy, m.UpdatedBy, m.CreatedTime, m.UpdatedTime,
	); err != nil {
		return fmt.Errorf("insert rbac_idp_claim_mapping: %w", classifySQLError(err))
	}
	return r.setClaimMappingRoles(ctx, tx, m.ID, roleIDs)
}

func (r *RBACRepo) UpdateClaimMapping(ctx context.Context, m *RBACIdpClaimMapping, roleIDs []string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return r.UpdateClaimMappingInTx(ctx, tx, m, roleIDs)
	})
}

func (r *RBACRepo) UpdateClaimMappingInTx(ctx context.Context, tx *dbutil.Tx, m *RBACIdpClaimMapping, roleIDs []string) error {
	m.UpdatedTime = time.Now().UnixMilli()
	result, err := tx.ExecContext(ctx,
		`UPDATE rbac_idp_claim_mappings
		 SET oidc_provider = ?, claim_key = ?, claim_value = ?, name = ?, updated_by = ?, updated_time = ?
		 WHERE id = ?`,
		m.OIDCProvider, m.ClaimKey, m.ClaimValue, m.Name, m.UpdatedBy, m.UpdatedTime, m.ID,
	)
	if err != nil {
		return fmt.Errorf("update rbac_idp_claim_mapping: %w", classifySQLError(err))
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrRBACNotFound
	}
	return r.setClaimMappingRoles(ctx, tx, m.ID, roleIDs)
}

func (r *RBACRepo) DeleteClaimMapping(ctx context.Context, id string) error {
	return r.deleteClaimMappingOn(ctx, r.db, id)
}

func (r *RBACRepo) DeleteClaimMappingInTx(ctx context.Context, tx *dbutil.Tx, id string) error {
	return r.deleteClaimMappingOn(ctx, tx, id)
}

func (r *RBACRepo) deleteClaimMappingOn(ctx context.Context, q dbutil.Querier, id string) error {
	result, err := q.ExecContext(ctx,
		`DELETE FROM rbac_idp_claim_mappings WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("RBACRepo.DeleteClaimMapping(%s): %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrRBACNotFound
	}
	return nil
}

func (r *RBACRepo) GetClaimMappingByID(ctx context.Context, id string) (*RBACIdpClaimMapping, []string, error) {
	m, err := dbutil.QueryRow[RBACIdpClaimMapping](ctx, r.db,
		`SELECT id, oidc_provider, claim_key, claim_value, name, created_by, updated_by, created_time, updated_time
		 FROM rbac_idp_claim_mappings WHERE id = ?`, id)
	if err != nil {
		return nil, nil, fmt.Errorf("RBACRepo.GetClaimMappingByID(%s): %w", id, err)
	}
	if m == nil {
		return nil, nil, nil
	}
	roleIDs, err := dbutil.QueryVals[string](ctx, r.db, sqlSelectRBACClaimMappingRoles, id)
	if err != nil {
		return nil, nil, fmt.Errorf("RBACRepo.GetClaimMappingByID(%s) roles: %w", id, err)
	}
	return m, roleIDs, nil
}

func (r *RBACRepo) ListClaimMappings(ctx context.Context) ([]*RBACIdpClaimMapping, map[string][]string, error) {
	var mappings []*RBACIdpClaimMapping
	roles := map[string][]string{}
	err := r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		ms, err := dbutil.QueryRowPtrs[RBACIdpClaimMapping](ctx, tx,
			`SELECT id, oidc_provider, claim_key, claim_value, name, created_by, updated_by, created_time, updated_time
			 FROM rbac_idp_claim_mappings`)
		if err != nil {
			return err
		}
		mappings = ms
		type pairRow struct {
			MappingID string `db:"claim_mapping_id"`
			RoleID    string `db:"role_id"`
		}
		pairs, err := dbutil.QueryRowPtrs[pairRow](ctx, tx,
			`SELECT claim_mapping_id, role_id FROM rbac_idp_claim_mapping_roles`)
		if err != nil {
			return err
		}
		for _, p := range pairs {
			roles[p.MappingID] = append(roles[p.MappingID], p.RoleID)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("RBACRepo.ListClaimMappings: %w", err)
	}
	return mappings, roles, nil
}

func (r *RBACRepo) SetDefaultRoles(ctx context.Context, roleIDs []string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return r.SetDefaultRolesInTx(ctx, tx, roleIDs)
	})
}

func (r *RBACRepo) SetDefaultRolesInTx(ctx context.Context, tx *dbutil.Tx, roleIDs []string) error {
	if _, err := tx.ExecContext(ctx, sqlDeleteRBACDefaultRoles); err != nil {
		return fmt.Errorf("clear rbac_default_roles: %w", err)
	}
	for _, id := range roleIDs {
		if _, err := tx.ExecContext(ctx, sqlInsertRBACDefaultRole, id); err != nil {
			return fmt.Errorf("insert rbac_default_role: %w", err)
		}
	}
	return nil
}

func (r *RBACRepo) ListDefaultRoles(ctx context.Context) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db, sqlSelectRBACDefaultRoles)
}

const (
	sqlDeleteRBACRoleScopes      = `DELETE FROM rbac_role_scopes WHERE role_id = ?`
	sqlInsertRBACRoleScope       = `INSERT INTO rbac_role_scopes (role_id, scope) VALUES (?, ?)`
	sqlSelectRBACRoleScopes      = `SELECT scope FROM rbac_role_scopes WHERE role_id = ?`
	sqlDeleteRBACRoleInheritance = `DELETE FROM rbac_role_inheritance WHERE role_id = ?`
	sqlInsertRBACRoleInheritance = `INSERT INTO rbac_role_inheritance (role_id, parent_role_id) VALUES (?, ?)`
	sqlSelectRBACRoleInheritance = `SELECT parent_role_id FROM rbac_role_inheritance WHERE role_id = ?`
	sqlDeleteRBACDefaultRoles    = `DELETE FROM rbac_default_roles`
	sqlInsertRBACDefaultRole     = `INSERT INTO rbac_default_roles (role_id) VALUES (?)`
	sqlSelectRBACDefaultRoles    = `SELECT role_id FROM rbac_default_roles`

	sqlDeleteRBACClaimMappingRoles = `DELETE FROM rbac_idp_claim_mapping_roles WHERE claim_mapping_id = ?`
	sqlInsertRBACClaimMappingRole  = `INSERT INTO rbac_idp_claim_mapping_roles (claim_mapping_id, role_id) VALUES (?, ?)`
	sqlSelectRBACClaimMappingRoles = `SELECT role_id FROM rbac_idp_claim_mapping_roles WHERE claim_mapping_id = ?`
)

func (r *RBACRepo) setClaimMappingRoles(ctx context.Context, q dbutil.Querier, mappingID string, roleIDs []string) error {
	if _, err := q.ExecContext(ctx, sqlDeleteRBACClaimMappingRoles, mappingID); err != nil {
		return fmt.Errorf("clear rbac_idp_claim_mapping_roles: %w", err)
	}
	for _, id := range roleIDs {
		if _, err := q.ExecContext(ctx, sqlInsertRBACClaimMappingRole, mappingID, id); err != nil {
			return fmt.Errorf("insert rbac_idp_claim_mapping_roles: %w", classifySQLError(err))
		}
	}
	return nil
}

func (r *RBACRepo) setRoleScopes(ctx context.Context, q dbutil.Querier, roleID string, scopes []string) error {
	if _, err := q.ExecContext(ctx, sqlDeleteRBACRoleScopes, roleID); err != nil {
		return fmt.Errorf("clear rbac_role_scopes: %w", err)
	}
	for _, s := range scopes {
		if _, err := q.ExecContext(ctx, sqlInsertRBACRoleScope, roleID, s); err != nil {
			return fmt.Errorf("insert rbac_role_scopes: %w", err)
		}
	}
	return nil
}

func (r *RBACRepo) setRoleParents(ctx context.Context, q dbutil.Querier, roleID string, parentIDs []string) error {
	if _, err := q.ExecContext(ctx, sqlDeleteRBACRoleInheritance, roleID); err != nil {
		return fmt.Errorf("clear rbac_role_inheritance: %w", err)
	}
	for _, p := range parentIDs {
		if _, err := q.ExecContext(ctx, sqlInsertRBACRoleInheritance, roleID, p); err != nil {
			return fmt.Errorf("insert rbac_role_inheritance: %w", err)
		}
	}
	return nil
}

func (r *RBACRepo) getRoleScopes(ctx context.Context, roleID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db, sqlSelectRBACRoleScopes, roleID)
}

func (r *RBACRepo) getRoleParents(ctx context.Context, roleID string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db, sqlSelectRBACRoleInheritance, roleID)
}
