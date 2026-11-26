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
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

const provisionedViaSystem = "system"

const userEmailSharedSQL = `CASE WHEN u.email <> '' AND (SELECT COUNT(*) FROM users s WHERE s.email = u.email) > 1 THEN 1 ELSE 0 END`

var ErrUserPhoneConflict = errors.New("user: phone conflict")

func classifyUserSQLError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	isUniqueViolation := strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate")
	if isUniqueViolation && strings.Contains(msg, "phone") {
		return fmt.Errorf("%w: %v", ErrUserPhoneConflict, err)
	}
	return err
}

type UsersRepo struct {
	db *DB
}

func NewUsersRepo(db *DB) *UsersRepo {
	return &UsersRepo{db: db}
}

func (r *UsersRepo) UpsertUser(ctx context.Context, u *User, groups []string) error {
	return r.UpsertUserWithClaims(ctx, u, groups, IdentityClaimWrite{})
}

func (r *UsersRepo) WithTx(ctx context.Context, fn func(tx *dbutil.Tx) error) error {
	return r.db.WithTx(ctx, fn)
}

func (r *UsersRepo) UpsertUserInTx(ctx context.Context, tx *dbutil.Tx, u *User, groups []string) error {
	return r.UpsertUserWithClaimsInTx(ctx, tx, u, groups, IdentityClaimWrite{})
}

func (r *UsersRepo) UpsertUserWithClaims(ctx context.Context, u *User, groups []string, claims IdentityClaimWrite) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		return r.UpsertUserWithClaimsInTx(ctx, tx, u, groups, claims)
	})
}

func (r *UsersRepo) UpsertUserWithClaimsInTx(ctx context.Context, tx *dbutil.Tx, u *User, groups []string, claims IdentityClaimWrite) error {
	now := time.Now().UnixMilli()
	if u.ID == "" {
		u.ID = idgen.New()
	}
	if u.Email != nil {
		normalized := strings.ToLower(strings.TrimSpace(*u.Email))
		u.Email = &normalized
	}
	if u.CreatedTime == 0 {
		u.CreatedTime = now
	}
	u.UpdatedTime = now
	u.LastLoginAt = &now
	if u.Source == "" {
		u.Source = "{}"
	}
	if u.ProvisionedVia == "" {
		u.ProvisionedVia = "jit"
	}

	insertSQL := `INSERT INTO users
		(id, issuer, sub, oidc_provider, external_id, display_name, user_name, email, phone, source, last_login_at, provisioned_via, created_by, updated_by, created_time, updated_time)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	upsertSQL := dbutil.UpsertSQL(r.db.Dialect(), insertSQL, "issuer, sub",
		[]string{"oidc_provider", "display_name", "user_name", "email", "phone", "source", "last_login_at", "updated_by", "updated_time"})

	if u.ProvisionedVia != "scim" {
		existing, err := dbutil.QueryRow[User](ctx, tx,
			`SELECT id, provisioned_via FROM users WHERE issuer = ? AND sub = ?`, u.Issuer, u.Sub)
		if err != nil {
			return fmt.Errorf("UsersRepo.UpsertUser: check ownership: %w", err)
		}
		if existing != nil && existing.ProvisionedVia == "scim" {
			u.ID = existing.ID
			if _, err := tx.ExecContext(ctx,
				`UPDATE users SET last_login_at = ? WHERE id = ?`, now, u.ID); err != nil {
				return fmt.Errorf("UsersRepo.UpsertUser: record login: %w", err)
			}
			return nil
		}
	}

	if _, err := tx.ExecContext(ctx, upsertSQL,
		u.ID, u.Issuer, u.Sub, u.OIDCProvider, u.ExternalID, u.DisplayName, u.UserName, u.Email, u.Phone, u.Source, u.LastLoginAt, u.ProvisionedVia,
		u.CreatedBy, u.UpdatedBy, u.CreatedTime, u.UpdatedTime,
	); err != nil {
		return fmt.Errorf("UsersRepo.UpsertUser: %w", classifyUserSQLError(err))
	}

	id, err := dbutil.QueryVal[string](ctx, tx,
		`SELECT id FROM users WHERE issuer = ? AND sub = ?`, u.Issuer, u.Sub)
	if err != nil {
		return fmt.Errorf("UsersRepo.UpsertUser: resolve id: %w", err)
	}
	if id == "" {
		return fmt.Errorf("UsersRepo.UpsertUser: row missing after upsert (issuer=%q, sub=%q)", u.Issuer, u.Sub)
	}
	u.ID = id

	if _, err := tx.ExecContext(ctx, `DELETE FROM user_groups WHERE user_id = ?`, id); err != nil {
		return fmt.Errorf("UsersRepo.UpsertUser: clear groups: %w", err)
	}
	seen := make(map[string]struct{}, len(groups))
	for _, g := range groups {
		if g == "" {
			continue
		}
		if _, dup := seen[g]; dup {
			continue
		}
		seen[g] = struct{}{}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_groups (user_id, group_value) VALUES (?, ?)`, id, g); err != nil {
			return fmt.Errorf("UsersRepo.UpsertUser: insert group: %w", err)
		}
	}

	if claims.Replace {
		if err := replaceIdentityClaims(ctx, tx, id, claims.Claims, now); err != nil {
			return fmt.Errorf("UsersRepo.UpsertUser: identity claims: %w", err)
		}
	}
	return nil
}

func (r *UsersRepo) CorroborateBindings(ctx context.Context, userID string, claims []UserIdentityClaim) error {
	if userID == "" || len(claims) == 0 {
		return nil
	}
	return corroborateBindings(ctx, r.db, userID, claims, time.Now().UnixMilli())
}

func corroborateBindings(ctx context.Context, q dbutil.Querier, userID string, claims []UserIdentityClaim, now int64) error {
	byKey := make(map[string][]string, len(claims))
	seen := make(map[string]struct{}, len(claims))
	for _, c := range claims {
		if !c.Verified || c.ClaimKey == "" || c.ClaimValueCanonical == "" {
			continue
		}
		dedupe := c.ClaimKey + "\x00" + c.ClaimValueCanonical
		if _, dup := seen[dedupe]; dup {
			continue
		}
		seen[dedupe] = struct{}{}
		byKey[c.ClaimKey] = append(byKey[c.ClaimKey], c.ClaimValueCanonical)
	}
	for _, key := range bindingKeysInLockOrder(byKey) {
		if err := corroborateBindingValues(ctx, q, key, byKey[key], userID, now); err != nil {
			return err
		}
	}
	return nil
}

func bindingKeysInLockOrder(byKey map[string][]string) []string {
	return slices.Sorted(maps.Keys(byKey))
}

func (r *UsersRepo) GetByIssuerSub(ctx context.Context, issuer, sub string) (*User, []string, error) {
	user, err := dbutil.QueryRow[User](ctx, r.db,
		`SELECT id, issuer, sub, oidc_provider, external_id, display_name, user_name, email, phone, source, last_login_at, provisioned_via, active, created_by, updated_by, created_time, updated_time
		 FROM users WHERE issuer = ? AND sub = ?`, issuer, sub)
	if err != nil {
		return nil, nil, fmt.Errorf("UsersRepo.GetByIssuerSub: %w", err)
	}
	return r.withGroups(ctx, user)
}

func (r *UsersRepo) GetByIssuerSubInTx(ctx context.Context, tx *dbutil.Tx, issuer, sub string) (*User, error) {
	user, err := dbutil.QueryRow[User](ctx, tx,
		`SELECT id, issuer, sub, oidc_provider, external_id, display_name, user_name, email, phone, source, last_login_at, provisioned_via, active, created_by, updated_by, created_time, updated_time
		 FROM users WHERE issuer = ? AND sub = ?`, issuer, sub)
	if err != nil {
		return nil, fmt.Errorf("UsersRepo.GetByIssuerSubInTx: %w", err)
	}
	return user, nil
}

func (r *UsersRepo) GetByEmail(ctx context.Context, email string) (*User, []string, error) {
	user, err := dbutil.QueryRow[User](ctx, r.db,
		`SELECT id, issuer, sub, oidc_provider, external_id, display_name, user_name, email, phone, source, last_login_at, provisioned_via, active, created_by, updated_by, created_time, updated_time
		 FROM users WHERE email = ? ORDER BY updated_time DESC, id DESC LIMIT 1`, email)
	if err != nil {
		return nil, nil, fmt.Errorf("UsersRepo.GetByEmail: %w", err)
	}
	return r.withGroups(ctx, user)
}

func (r *UsersRepo) UserIDsByEmail(ctx context.Context, email string) ([]string, error) {
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT id FROM users WHERE email = ?`, email)
}

func (r *UsersRepo) SystemUserID(ctx context.Context, issuer, sub string) (string, error) {
	ids, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT id FROM users WHERE issuer = ? AND sub = ? AND provisioned_via = ?`, issuer, sub, provisionedViaSystem)
	if err != nil {
		return "", fmt.Errorf("UsersRepo.SystemUserID: %w", err)
	}
	if len(ids) != 1 {
		return "", nil
	}
	return ids[0], nil
}

func (r *UsersRepo) GetByID(ctx context.Context, id string) (*User, []string, error) {
	user, err := dbutil.QueryRow[User](ctx, r.db,
		`SELECT id, issuer, sub, oidc_provider, external_id, display_name, user_name, email, phone, source, last_login_at, provisioned_via, active, created_by, updated_by, created_time, updated_time
		 FROM users WHERE id = ?`, id)
	if err != nil {
		return nil, nil, fmt.Errorf("UsersRepo.GetByID: %w", err)
	}
	return r.withGroups(ctx, user)
}

type UserListOptions struct {
	PageNumber   int
	PageSize     int
	SortColumn   string
	SortOrder    string
	Search       string
	Group        string
	OIDCProvider string
	ExcludeSubs  []string
	Budget       *UserBudgetMatch
}

type UserBudgetMatch struct {
	BudgetID  string
	IsDefault bool
}

func budgetAppliesCondition(m *UserBudgetMatch, userRef string) (string, []any) {
	assignment := `SELECT 1 FROM budget_assignments ba WHERE ba.subject_type = '` + BudgetSubjectUser + `' AND ba.subject_ref = ` + userRef
	assigned := `EXISTS (` + assignment + ` AND ba.budget_id = ?)`
	if !m.IsDefault {
		return assigned, []any{m.BudgetID}
	}
	return `(` + assigned + ` OR NOT EXISTS (` + assignment + `))`, []any{m.BudgetID}
}

const nilLastLoginSortKey = "9223372036854775807"

var usersSortExpressions = map[string]string{
	"display_name":  "LOWER(COALESCE(display_name, email, ''))",
	"email":         "LOWER(COALESCE(email, ''))",
	"last_login_at": "COALESCE(last_login_at, " + nilLastLoginSortKey + ")",
	"created_time":  "created_time",
	"updated_time":  "updated_time",
}

const usersSelectColumns = `id, issuer, sub, oidc_provider, external_id, display_name, user_name, email, phone, source, last_login_at, provisioned_via, active, created_by, updated_by, created_time, updated_time`

func (r *UsersRepo) ListUsers(ctx context.Context, opts UserListOptions) ([]User, int, error) {
	var conds []string
	var args []any
	if s := strings.TrimSpace(opts.Search); s != "" {
		namePred, like := dbutil.LikeContainsFold("users.display_name", s)
		emailPred, _ := dbutil.LikeContainsFold("users.email", s)
		conds = append(conds, "("+namePred+" OR "+emailPred+")")
		args = append(args, like, like)
	}
	if opts.OIDCProvider != "" {
		conds = append(conds, "users.oidc_provider = ?")
		args = append(args, opts.OIDCProvider)
	}
	if opts.Group != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM user_groups g WHERE g.user_id = users.id AND g.group_value = ?)")
		args = append(args, opts.Group)
	}
	if opts.Budget != nil {
		cond, condArgs := budgetAppliesCondition(opts.Budget, "CAST(users.id AS TEXT)")
		conds = append(conds, cond)
		args = append(args, condArgs...)
	}
	if len(opts.ExcludeSubs) > 0 {
		conds = append(conds, "users.sub NOT IN (?"+strings.Repeat(", ?", len(opts.ExcludeSubs)-1)+")")
		for _, sub := range opts.ExcludeSubs {
			args = append(args, sub)
		}
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	total, err := dbutil.QueryVal[int](ctx, r.db, `SELECT COUNT(*) FROM users`+where, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("UsersRepo.ListUsers count: %w", err)
	}

	sortColumn := opts.SortColumn
	if sortColumn == "" {
		sortColumn = "updated_time"
	}
	sortExpr, ok := usersSortExpressions[sortColumn]
	if !ok {
		return nil, 0, fmt.Errorf("UsersRepo.ListUsers: unsupported sort column %q", opts.SortColumn)
	}
	order := "DESC"
	switch strings.ToUpper(opts.SortOrder) {
	case "", "DESC":
	case "ASC":
		order = "ASC"
	default:
		return nil, 0, fmt.Errorf("UsersRepo.ListUsers: unsupported sort order %q", opts.SortOrder)
	}

	if opts.PageSize > 0 {
		if lastPage := (total + opts.PageSize - 1) / opts.PageSize; opts.PageNumber > lastPage {
			return []User{}, total, nil
		}
	}
	offset := (opts.PageNumber - 1) * opts.PageSize

	query := `SELECT ` + usersSelectColumns + ` FROM users` + where +
		` ORDER BY ` + sortExpr + ` ` + order + `, id DESC LIMIT ? OFFSET ?`
	pageArgs := append(append([]any{}, args...), opts.PageSize, offset)
	users, err := dbutil.QueryRows[User](ctx, r.db, query, pageArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("UsersRepo.ListUsers query: %w", err)
	}
	return users, total, nil
}

func (r *UsersRepo) ListAllUsers(ctx context.Context) ([]User, error) {
	return dbutil.QueryRows[User](ctx, r.db,
		`SELECT id, issuer, sub, oidc_provider, external_id, display_name, user_name, email, phone, source, last_login_at, provisioned_via, active, created_by, updated_by, created_time, updated_time
		 FROM users ORDER BY updated_time DESC, id DESC`)
}

var ErrSystemUserNotFound = errors.New("system user: not found")

func (r *UsersRepo) UpdateSystemUser(ctx context.Context, id string, displayName *string, actor string) error {
	return r.updateSystemUserOn(ctx, r.db, id, displayName, actor)
}

func (r *UsersRepo) UpdateSystemUserInTx(ctx context.Context, tx *dbutil.Tx, id string, displayName *string, actor string) error {
	return r.updateSystemUserOn(ctx, tx, id, displayName, actor)
}

func (r *UsersRepo) updateSystemUserOn(ctx context.Context, q dbutil.Querier, id string, displayName *string, actor string) error {
	result, err := q.ExecContext(ctx,
		`UPDATE users SET display_name = ?, updated_by = ?, updated_time = ?
		 WHERE id = ? AND provisioned_via = ?`,
		displayName, actor, time.Now().UnixMilli(), id, provisionedViaSystem)
	if err != nil {
		return fmt.Errorf("UsersRepo.UpdateSystemUser: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrSystemUserNotFound
	}
	return nil
}

func (r *UsersRepo) DeleteSystemUser(ctx context.Context, id string) error {
	return r.db.WithTx(ctx, func(tx *dbutil.Tx) error {
		_, err := r.DeleteSystemUserInTx(ctx, tx, id)
		return err
	})
}

type SystemUserBudgetRowsDeleted struct {
	Assignments int
	TopUps      int
}

func (r *UsersRepo) DeleteSystemUserInTx(ctx context.Context, tx *dbutil.Tx, id string) (SystemUserBudgetRowsDeleted, error) {
	var deleted SystemUserBudgetRowsDeleted
	lock := `SELECT id FROM users WHERE id = ? AND provisioned_via = ?`
	if tx.Dialect() == "postgres" {
		lock += ` FOR UPDATE`
	}
	var locked string
	if err := tx.QueryRowContext(ctx, lock, id, provisionedViaSystem).Scan(&locked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return deleted, ErrSystemUserNotFound
		}
		return deleted, fmt.Errorf("UsersRepo.DeleteSystemUser: lock the user: %w", err)
	}

	if ref, ok := canonicalBudgetID(id); ok {
		var err error
		if deleted.Assignments, err = deleteUserBudgetRows(ctx, tx, "budget_assignments", ref); err != nil {
			return SystemUserBudgetRowsDeleted{}, err
		}
		if deleted.TopUps, err = deleteUserBudgetRows(ctx, tx, "budget_top_ups", ref); err != nil {
			return SystemUserBudgetRowsDeleted{}, err
		}
	}

	result, err := tx.ExecContext(ctx,
		`DELETE FROM users WHERE id = ? AND provisioned_via = ?`, id, provisionedViaSystem)
	if err != nil {
		return SystemUserBudgetRowsDeleted{}, fmt.Errorf("UsersRepo.DeleteSystemUser: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return SystemUserBudgetRowsDeleted{}, ErrSystemUserNotFound
	}
	return deleted, nil
}

func deleteUserBudgetRows(ctx context.Context, tx *dbutil.Tx, table, ref string) (int, error) {
	result, err := tx.ExecContext(ctx,
		`DELETE FROM `+table+` WHERE subject_type = ? AND subject_ref = ?`, BudgetSubjectUser, ref)
	if err != nil {
		return 0, fmt.Errorf("UsersRepo.DeleteSystemUser: delete %s: %w", table, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("UsersRepo.DeleteSystemUser: count %s: %w", table, err)
	}
	return int(n), nil
}

func (r *UsersRepo) ListByProvisionedVia(ctx context.Context, provisionedVia string) ([]User, error) {
	return dbutil.QueryRows[User](ctx, r.db,
		`SELECT id, issuer, sub, oidc_provider, external_id, display_name, user_name, email, phone, source, last_login_at, provisioned_via, active, created_by, updated_by, created_time, updated_time
		 FROM users WHERE provisioned_via = ? ORDER BY updated_time DESC, id DESC`, provisionedVia)
}

type userGroupRow struct {
	UserID     string `db:"user_id"`
	GroupValue string `db:"group_value"`
}

func (r *UsersRepo) GroupsForUsers(ctx context.Context, ids []string) (map[string][]string, error) {
	result := make(map[string][]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	ph, args, err := dbutil.InClause(ids)
	if err != nil {
		return nil, fmt.Errorf("UsersRepo.GroupsForUsers: id filter: %w", err)
	}
	rows, err := dbutil.QueryRows[userGroupRow](ctx, r.db,
		`SELECT user_id, group_value FROM user_groups WHERE user_id IN (`+ph+`) ORDER BY group_value`, args...)
	if err != nil {
		return nil, fmt.Errorf("UsersRepo.GroupsForUsers: %w", err)
	}
	for _, row := range rows {
		result[row.UserID] = append(result[row.UserID], row.GroupValue)
	}
	return result, nil
}

func (r *UsersRepo) UserIDsByIdentities(ctx context.Context, identities []string) ([]string, error) {
	if len(identities) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(identities)
	if err != nil {
		return nil, fmt.Errorf("UsersRepo.UserIDsByIdentities: %w", err)
	}
	dup := append(append([]any{}, args...), args...)
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT id FROM users WHERE email IN (`+ph+`) OR sub IN (`+ph+`)`, dup...)
}

func (r *UsersRepo) UserIDsByGroups(ctx context.Context, groups []string) ([]string, error) {
	if len(groups) == 0 {
		return nil, nil
	}
	ph, args, err := dbutil.InClause(groups)
	if err != nil {
		return nil, fmt.Errorf("UsersRepo.UserIDsByGroups: %w", err)
	}
	return dbutil.QueryVals[string](ctx, r.db,
		`SELECT DISTINCT user_id FROM user_groups WHERE group_value IN (`+ph+`)`, args...)
}

func (r *UsersRepo) withGroups(ctx context.Context, user *User) (*User, []string, error) {
	if user == nil {
		return nil, nil, nil
	}
	groups, err := dbutil.QueryVals[string](ctx, r.db,
		`SELECT group_value FROM user_groups WHERE user_id = ? ORDER BY group_value`, user.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("UsersRepo.withGroups: %w", err)
	}
	return user, groups, nil
}

type SystemUserListOptions struct {
	Search      string
	ExcludeSubs []string
	PageQuery
}

var systemUserNameColumn = fmt.Sprintf("SUBSTR(sub, %d)", len(systemSubPrefix)+1)

const systemSubPrefix = "system:"

var systemUserSortColumns = map[string]string{"name": "LOWER(sub)"}

func (r *UsersRepo) ListSystemUsersPage(ctx context.Context, opts SystemUserListOptions) ([]*User, int, error) {
	conds := []string{"provisioned_via = ?"}
	args := []any{provisionedViaSystem}
	if s := strings.TrimSpace(opts.Search); s != "" {
		subPred, like := dbutil.LikeContainsFold(systemUserNameColumn, s)
		namePred, _ := dbutil.LikeContainsFold("display_name", s)
		conds = append(conds, "("+subPred+" OR "+namePred+")")
		args = append(args, like, like)
	}
	if len(opts.ExcludeSubs) > 0 {
		in, inArgs, err := dbutil.InClause(opts.ExcludeSubs)
		if err != nil {
			return nil, 0, fmt.Errorf("UsersRepo.ListSystemUsersPage: %w", err)
		}
		conds = append(conds, "sub NOT IN ("+in+")")
		args = append(args, inArgs...)
	}
	users, total, err := pageRows[User](ctx, r.db, pageSource{
		columns:     usersSelectColumns,
		from:        "users",
		where:       strings.Join(conds, " AND "),
		args:        args,
		sortColumns: systemUserSortColumns,
		defaultSort: "name",
	}, opts.PageQuery)
	if err != nil {
		return nil, 0, fmt.Errorf("UsersRepo.ListSystemUsersPage: %w", err)
	}
	return users, total, nil
}
