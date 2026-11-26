// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type PageQuery struct {
	Sort   string
	Desc   bool
	Offset int
	Limit  int
}

type pageSource struct {
	columns     string
	from        string
	where       string
	args        []any
	sortColumns map[string]string
	defaultSort string
}

func pageRows[T any](ctx context.Context, q dbutil.Querier, src pageSource, p PageQuery) ([]*T, int, error) {
	from, where, args := src.from, src.where, src.args
	if where != "" {
		where = " WHERE " + where
	}
	total, err := dbutil.QueryVal[int](ctx, q, `SELECT COUNT(*) FROM `+from+where, args...)
	if err != nil {
		return nil, 0, err
	}
	if p.Limit <= 0 || p.Offset >= total {
		return []*T{}, total, nil
	}
	sortKey := p.Sort
	if sortKey == "" {
		sortKey = src.defaultSort
	}
	expr, ok := src.sortColumns[sortKey]
	if !ok {
		return nil, 0, fmt.Errorf("unsupported sort column %q", p.Sort)
	}
	order := "ASC"
	if p.Desc {
		order = "DESC"
	}
	query := `SELECT ` + src.columns + ` FROM ` + from + where + ` ORDER BY ` + expr + ` ` + order + `, id ` + order + ` LIMIT ? OFFSET ?`
	rows, err := dbutil.QueryRowPtrs[T](ctx, q, query, append(append([]any{}, args...), p.Limit, p.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

type RoleListOptions struct {
	Search  string
	Builtin *bool
	PageQuery
}

var roleSortColumns = map[string]string{
	"name":        "LOWER(name)",
	"createdTime": "created_time",
	"updatedTime": "updated_time",
}

func (r *RBACRepo) ListRolesPage(ctx context.Context, opts RoleListOptions) ([]*RBACRole, int, error) {
	var conds []string
	var args []any
	if s := strings.TrimSpace(opts.Search); s != "" {
		namePred, like := dbutil.LikeContainsFold("name", s)
		descPred, _ := dbutil.LikeContainsFold("description", s)
		conds = append(conds, "("+namePred+" OR "+descPred+")")
		args = append(args, like, like)
	}
	if opts.Builtin != nil {
		conds = append(conds, "builtin = ?")
		args = append(args, dbutil.BoolToInt(*opts.Builtin))
	}
	roles, total, err := pageRows[RBACRole](ctx, r.db, pageSource{
		columns:     `id, name, description, created_by, updated_by, created_time, updated_time, builtin`,
		from:        "rbac_roles",
		where:       strings.Join(conds, " AND "),
		args:        args,
		sortColumns: roleSortColumns,
		defaultSort: "name",
	}, opts.PageQuery)
	if err != nil {
		return nil, 0, fmt.Errorf("RBACRepo.ListRolesPage: %w", err)
	}
	return roles, total, nil
}

func (r *RBACRepo) RoleNamesTaken(ctx context.Context, names []string) (map[string]bool, error) {
	taken := map[string]bool{}
	if len(names) == 0 {
		return taken, nil
	}
	in, args, err := dbutil.InClause(names)
	if err != nil {
		return nil, fmt.Errorf("RBACRepo.RoleNamesTaken: %w", err)
	}
	held, err := dbutil.QueryVals[string](ctx, r.db, `SELECT name FROM rbac_roles WHERE name IN (`+in+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("RBACRepo.RoleNamesTaken: %w", err)
	}
	for _, name := range held {
		taken[name] = true
	}
	return taken, nil
}

func (r *RBACRepo) RoleEdges(ctx context.Context, roleIDs []string) (scopes, parents map[string][]string, err error) {
	scopes, parents = map[string][]string{}, map[string][]string{}
	if len(roleIDs) == 0 {
		return scopes, parents, nil
	}
	type edge struct {
		RoleID string `db:"role_id"`
		Value  string `db:"value"`
	}
	in, args, err := dbutil.InClause(roleIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("RBACRepo.RoleEdges: %w", err)
	}
	for _, read := range []struct {
		query string
		into  map[string][]string
	}{
		{`SELECT role_id, scope AS value FROM rbac_role_scopes WHERE role_id IN (` + in + `) ORDER BY scope`, scopes},
		{`SELECT role_id, parent_role_id AS value FROM rbac_role_inheritance WHERE role_id IN (` + in + `) ORDER BY parent_role_id`, parents},
	} {
		rows, err := dbutil.QueryRowPtrs[edge](ctx, r.db, read.query, args...)
		if err != nil {
			return nil, nil, fmt.Errorf("RBACRepo.RoleEdges: %w", err)
		}
		for _, e := range rows {
			read.into[e.RoleID] = append(read.into[e.RoleID], e.Value)
		}
	}
	return scopes, parents, nil
}

type RBACGrant struct {
	ID           string `db:"id"`
	UserID       string `db:"user_id"`
	RoleID       string `db:"role_id"`
	SubjectType  string `db:"subject_type"`
	SubjectValue string `db:"subject_value"`
	Issuer       string `db:"issuer"`
	CreatedBy    string `db:"created_by"`
	UpdatedBy    string `db:"updated_by"`
	CreatedTime  int64  `db:"created_time"`
	UpdatedTime  int64  `db:"updated_time"`
	State        string `db:"state"`
}

const (
	GrantStateConcrete = "concrete"
	GrantStatePending  = "pending"
)

const (
	selectConcreteGrants = `SELECT CAST(id AS TEXT) AS id, CAST(user_id AS TEXT) AS user_id, CAST(role_id AS TEXT) AS role_id,
		subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time, '` + GrantStateConcrete + `' AS state
	FROM rbac_user_roles`
	selectPendingGrants = `SELECT CAST(id AS TEXT) AS id, '' AS user_id, CAST(role_id AS TEXT) AS role_id,
		subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time, '` + GrantStatePending + `' AS state
	FROM rbac_pending_user_roles`
)

func grantsRelation(opts GrantListOptions) (string, []any) {
	where, args := "", []any{}
	if opts.RoleID != "" {
		where, args = " WHERE role_id = ?", []any{opts.RoleID}
	}
	var branches []string
	var branchArgs []any
	for _, b := range []struct{ state, query string }{
		{GrantStateConcrete, selectConcreteGrants},
		{GrantStatePending, selectPendingGrants},
	} {
		if opts.State == "" || opts.State == b.state {
			branches = append(branches, b.query+where)
			branchArgs = append(branchArgs, args...)
		}
	}
	return "(" + strings.Join(branches, " UNION ALL ") + ") AS grants", branchArgs
}

const grantColumns = `id, user_id, role_id, subject_type, subject_value, issuer, created_by, updated_by, created_time, updated_time, state`

type GrantListOptions struct {
	State  string
	RoleID string
	PageQuery
}

var grantSortColumns = map[string]string{
	"roleId":      "role_id",
	"state":       "state",
	"createdTime": "created_time",
	"updatedTime": "updated_time",
}

func (r *RBACRepo) ListGrantsPage(ctx context.Context, opts GrantListOptions) ([]*RBACGrant, int, error) {
	relation, args := grantsRelation(opts)
	grants, total, err := pageRows[RBACGrant](ctx, r.db, pageSource{
		columns:     grantColumns,
		from:        relation,
		args:        args,
		sortColumns: grantSortColumns,
		defaultSort: "roleId",
	}, opts.PageQuery)
	if err != nil {
		return nil, 0, fmt.Errorf("RBACRepo.ListGrantsPage: %w", err)
	}
	return grants, total, nil
}

func (r *RBACRepo) GetGrant(ctx context.Context, id string) (*RBACGrant, error) {
	for _, query := range []string{selectConcreteGrants, selectPendingGrants} {
		g, err := dbutil.QueryRow[RBACGrant](ctx, r.db, query+` WHERE id = ?`, id)
		if err != nil {
			return nil, fmt.Errorf("RBACRepo.GetGrant(%s): %w", id, err)
		}
		if g != nil {
			return g, nil
		}
	}
	return nil, nil
}

type ClaimMappingListOptions struct {
	OIDCProvider string
	RoleID       string
	Search       string
	PageQuery
}

var claimMappingSortColumns = map[string]string{
	"name":         "LOWER(name)",
	"oidcProvider": "oidc_provider",
	"claimValue":   "LOWER(claim_value)",
	"createdTime":  "created_time",
	"updatedTime":  "updated_time",
}

func (r *RBACRepo) ListClaimMappingsPage(ctx context.Context, opts ClaimMappingListOptions) ([]*RBACIdpClaimMapping, map[string][]string, int, error) {
	var conds []string
	var args []any
	if opts.OIDCProvider != "" {
		conds = append(conds, "oidc_provider = ?")
		args = append(args, opts.OIDCProvider)
	}
	if opts.RoleID != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM rbac_idp_claim_mapping_roles r WHERE r.claim_mapping_id = rbac_idp_claim_mappings.id AND CAST(r.role_id AS TEXT) = ?)")
		args = append(args, opts.RoleID)
	}
	if s := strings.TrimSpace(opts.Search); s != "" {
		namePred, like := dbutil.LikeContainsFold("name", s)
		valuePred, _ := dbutil.LikeContainsFold("claim_value", s)
		conds = append(conds, "("+namePred+" OR "+valuePred+")")
		args = append(args, like, like)
	}
	mappings, total, err := pageRows[RBACIdpClaimMapping](ctx, r.db, pageSource{
		columns:     `id, oidc_provider, claim_key, claim_value, name, created_by, updated_by, created_time, updated_time`,
		from:        "rbac_idp_claim_mappings",
		where:       strings.Join(conds, " AND "),
		args:        args,
		sortColumns: claimMappingSortColumns,
		defaultSort: "name",
	}, opts.PageQuery)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("RBACRepo.ListClaimMappingsPage: %w", err)
	}
	roles := map[string][]string{}
	if len(mappings) == 0 {
		return mappings, roles, total, nil
	}
	ids := make([]string, len(mappings))
	for i, m := range mappings {
		ids[i] = m.ID
	}
	in, inArgs, err := dbutil.InClause(ids)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("RBACRepo.ListClaimMappingsPage: %w", err)
	}
	type pair struct {
		MappingID string `db:"claim_mapping_id"`
		RoleID    string `db:"role_id"`
	}
	pairs, err := dbutil.QueryRowPtrs[pair](ctx, r.db,
		`SELECT claim_mapping_id, role_id FROM rbac_idp_claim_mapping_roles WHERE claim_mapping_id IN (`+in+`) ORDER BY role_id`, inArgs...)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("RBACRepo.ListClaimMappingsPage roles: %w", err)
	}
	for _, p := range pairs {
		roles[p.MappingID] = append(roles[p.MappingID], p.RoleID)
	}
	return mappings, roles, total, nil
}
