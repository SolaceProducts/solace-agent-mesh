// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type ListQuery struct {
	PageNumber int
	PageSize   int
	Sort       string
	Desc       bool
}

func (q ListQuery) offset() int { return (q.PageNumber - 1) * q.PageSize }

func pageAfterYAML[T any](ctx context.Context, yaml []T, q ListQuery, rest func(context.Context, store.PageQuery) ([]T, int, error)) (*PaginatedResponse[T], error) {
	start := min(q.offset(), len(yaml))
	end := min(q.offset()+q.PageSize, len(yaml))
	items := append(make([]T, 0, q.PageSize), yaml[start:end]...)
	tail, dbTotal, err := rest(ctx, store.PageQuery{
		Sort:   q.Sort,
		Desc:   q.Desc,
		Offset: max(0, q.offset()-len(yaml)),
		Limit:  q.PageSize - len(items),
	})
	if err != nil {
		return nil, err
	}
	items = append(items, tail...)
	return &PaginatedResponse[T]{
		Data: items,
		Meta: NewPaginationMeta(q.PageNumber, q.PageSize, len(yaml)+dbTotal),
	}, nil
}

type RoleListQuery struct {
	ListQuery
	Origin string
	Search string
}

func (s *RBACService) ListRolesPage(ctx context.Context, q RoleListQuery) (*PaginatedResponse[RBACRoleResponse], error) {
	var yaml []RBACRoleResponse
	if q.Origin == "" || q.Origin == RBACOriginYAML {
		var err error
		if yaml, err = s.yamlRolesMatching(ctx, q); err != nil {
			return nil, err
		}
	}
	return pageAfterYAML(ctx, yaml, q.ListQuery, func(ctx context.Context, p store.PageQuery) ([]RBACRoleResponse, int, error) {
		if q.Origin == RBACOriginYAML {
			return nil, 0, nil
		}
		opts := store.RoleListOptions{Search: q.Search, PageQuery: p}
		if q.Origin != "" {
			builtin := q.Origin == RBACOriginBuiltin
			opts.Builtin = &builtin
		}
		roles, total, err := s.repo.ListRolesPage(ctx, opts)
		if err != nil {
			return nil, 0, mapRepoError(err, auditResourceRole, "")
		}
		ids := make([]string, len(roles))
		for i, r := range roles {
			ids[i] = r.ID
		}
		scopes, parents, err := s.repo.RoleEdges(ctx, ids)
		if err != nil {
			return nil, 0, mapRepoError(err, auditResourceRole, "")
		}
		state := s.yamlState()
		out := make([]RBACRoleResponse, len(roles))
		for i, r := range roles {
			out[i] = *roleToResponse(r, scopes[r.ID], parents[r.ID])
			out[i].NameConflict = yamlHasRoleName(state, r.Name)
		}
		return out, total, nil
	})
}

func (s *RBACService) yamlRolesMatching(ctx context.Context, q RoleListQuery) ([]RBACRoleResponse, error) {
	search := strings.TrimSpace(q.Search)
	rows := slices.DeleteFunc(yamlRoleResponses(s.yamlState()), func(r RBACRoleResponse) bool {
		return search != "" && !containsFold(r.Name, search) && !containsFold(r.Description, search)
	})
	slices.SortFunc(rows, func(a, b RBACRoleResponse) int {
		c := cmp.Or(compareFold(a.Name, b.Name), cmp.Compare(a.Name, b.Name))
		if q.Desc && q.Sort == "name" {
			return -c
		}
		return c
	})
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	taken, err := s.repo.RoleNamesTaken(ctx, names)
	if err != nil {
		return nil, mapRepoError(err, auditResourceRole, "")
	}
	for i := range rows {
		rows[i].NameConflict = taken[rows[i].Name]
	}
	return rows, nil
}

func (s *RBACService) matchesYAMLRole(ref, roleID string) bool {
	return s.V2RoleID(RBACOriginYAML, ref) == roleID
}

type GrantRecord struct {
	GrantResponse
	CreatedBy   string
	UpdatedBy   string
	CreatedTime int64
	UpdatedTime int64
}

type GrantListQuery struct {
	ListQuery
	Origin     string
	State      string
	RoleID     string
	ExpandUser bool
}

func (s *RBACService) ListGrantsPage(ctx context.Context, q GrantListQuery) (*PaginatedResponse[GrantRecord], error) {
	var yaml []GrantRecord
	if (q.Origin == "" || q.Origin == RBACOriginYAML) && q.State == "" {
		for _, g := range yamlGrantResponses(s.yamlState()) {
			if q.RoleID == "" || s.matchesYAMLRole(g.RoleID, q.RoleID) {
				yaml = append(yaml, GrantRecord{GrantResponse: g})
			}
		}
		slices.SortFunc(yaml, func(a, b GrantRecord) int { return cmp.Compare(a.ID, b.ID) })
	}
	return pageAfterYAML(ctx, yaml, q.ListQuery, func(ctx context.Context, p store.PageQuery) ([]GrantRecord, int, error) {
		if q.Origin == RBACOriginYAML {
			return nil, 0, nil
		}
		grants, total, err := s.repo.ListGrantsPage(ctx, store.GrantListOptions{State: q.State, RoleID: q.RoleID, PageQuery: p})
		if err != nil {
			return nil, 0, mapRepoError(err, auditResourceGrant, "")
		}
		out := make([]GrantRecord, len(grants))
		for i, g := range grants {
			out[i] = s.grantRecord(ctx, g)
		}
		if q.ExpandUser && len(out) > 0 {
			users, err := s.grantUsersByID(ctx)
			if err != nil {
				return nil, 0, err
			}
			for i := range out {
				out[i].setUser(users)
			}
		}
		return out, total, nil
	})
}

func (s *RBACService) grantRecord(ctx context.Context, g *store.RBACGrant) GrantRecord {
	resp := GrantResponse{ID: g.ID, RoleID: g.RoleID, State: g.State, Origin: RBACOriginCustom}
	if g.State == GrantStateConcrete {
		resp.UserID = g.UserID
		resp.Subject = s.concreteGrantSubject(ctx, &store.RBACUserRole{
			ID: g.ID, UserID: g.UserID, RoleID: g.RoleID,
			SubjectType: g.SubjectType, SubjectValue: g.SubjectValue, Issuer: g.Issuer,
		})
	} else {
		resp.Subject = &GrantSubject{Type: g.SubjectType, Value: g.SubjectValue, Issuer: g.Issuer}
	}
	return GrantRecord{
		GrantResponse: resp,
		CreatedBy:     g.CreatedBy, UpdatedBy: g.UpdatedBy,
		CreatedTime: g.CreatedTime, UpdatedTime: g.UpdatedTime,
	}
}

func (s *RBACService) YAMLGrant(id string) (*GrantRecord, bool) {
	g, ok := findYAMLRow(yamlGrantResponses(s.yamlState()), YAMLKindGrant, id, func(g GrantResponse) string { return g.ID })
	if !ok {
		return nil, false
	}
	return &GrantRecord{GrantResponse: g}, true
}

func (s *RBACService) GetGrant(ctx context.Context, id string) (*GrantRecord, error) {
	if g, ok := s.YAMLGrant(id); ok {
		return g, nil
	}
	g, err := s.repo.GetGrant(ctx, id)
	if err != nil {
		return nil, mapRepoError(err, auditResourceGrant, id)
	}
	if g == nil {
		return nil, &NotFoundError{Resource: auditResourceGrant, ID: id}
	}
	record := s.grantRecord(ctx, g)
	return &record, nil
}

func (s *RBACService) UpdateGrant(ctx context.Context, id string, subject *GrantSubject, actor string) (*GrantRecord, error) {
	if _, ok := s.YAMLGrant(id); ok {
		return nil, sayableConflict{&ConflictError{
			Message:    "Grants defined in your configuration cannot be changed through the API",
			LogMessage: "grant is defined in the configuration",
		}}
	}
	current, err := s.GetGrant(ctx, id)
	if err != nil {
		return nil, err
	}
	if current.State == GrantStatePending {
		return nil, sayableConflict{&ConflictError{
			Message:    "A pending grant has no user yet, so its subject cannot be changed. Delete it and create the grant again",
			LogMessage: "grant is pending",
		}}
	}
	if subject == nil {
		return current, nil
	}
	if _, err := s.UpdateGrantSubject(ctx, id, UpdateGrantRequest{Subject: *subject}, actor); err != nil {
		switch {
		case errors.Is(err, errGrantSubjectNamesAnotherUser):
			return nil, &ValidationError{
				Message: "Invalid subject",
				Details: map[string][]string{"subject": {errGrantSubjectNamesAnotherUser.Message}},
			}
		case errors.Is(err, errSystemUserGrantSubject):
			return nil, sayableConflict{errSystemUserGrantSubject}
		}
		return nil, err
	}
	return s.GetGrant(ctx, id)
}

func (s *RBACService) CreateNewGrant(ctx context.Context, req CreateGrantRequest, actor string, actorScopes []string) (*GrantRecord, error) {
	created, err := s.createGrant(ctx, req, actor, actorScopes, true)
	if err != nil {
		return nil, err
	}
	g, err := s.repo.GetGrant(ctx, created.ID)
	if err != nil || g == nil {
		return nil, mapRepoError(cmp.Or(err, store.ErrRBACNotFound), auditResourceGrant, created.ID)
	}
	record := s.grantRecord(ctx, g)
	return &record, nil
}

type SystemUserRecord struct {
	SystemUserResponse
	ConfiguredRoleCount int
	CreatedBy           string
	UpdatedBy           string
	CreatedTime         int64
	UpdatedTime         int64
}

type SystemUserListQuery struct {
	ListQuery
	Search string
}

func (s *RBACService) ListSystemUsersPage(ctx context.Context, q SystemUserListQuery) (*PaginatedResponse[SystemUserRecord], error) {
	if s.usersRepo == nil {
		return nil, errUsersRepoNotConfigured
	}
	var hidden []string
	for _, u := range rbac.ReservedSystemUsers() {
		if u.HiddenFromRunAsPicker {
			hidden = append(hidden, u.Sub)
		}
	}
	return pageAfterYAML(ctx, nil, q.ListQuery, func(ctx context.Context, p store.PageQuery) ([]SystemUserRecord, int, error) {
		users, total, err := s.usersRepo.ListSystemUsersPage(ctx, store.SystemUserListOptions{Search: q.Search, ExcludeSubs: hidden, PageQuery: p})
		if err != nil {
			return nil, 0, mapRepoError(err, resourceSystemUser, "")
		}
		builtinRoles := s.builtinSystemUserRoles()
		out := make([]SystemUserRecord, len(users))
		for i, u := range users {
			record, err := s.systemUserRecord(ctx, u, builtinRoles)
			if err != nil {
				return nil, 0, err
			}
			out[i] = *record
		}
		return out, total, nil
	})
}

func (s *RBACService) systemUserRecord(ctx context.Context, u *store.User, builtinRoles func(string) []string) (*SystemUserRecord, error) {
	resp, err := s.systemUserResponse(ctx, u, builtinRoles)
	if err != nil {
		return nil, err
	}
	record := &SystemUserRecord{
		SystemUserResponse: *resp,
		CreatedBy:          u.CreatedBy, UpdatedBy: u.UpdatedBy,
		CreatedTime: u.CreatedTime, UpdatedTime: u.UpdatedTime,
	}
	if resp.Origin == RBACOriginBuiltin {
		record.ConfiguredRoleCount = len(builtinRoles(u.Sub))
	}
	return record, nil
}

func (s *RBACService) GetSystemUser(ctx context.Context, id string) (*SystemUserRecord, error) {
	if s.usersRepo == nil {
		return nil, errUsersRepoNotConfigured
	}
	u, err := s.systemUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.systemUserRecord(ctx, u, s.builtinSystemUserRoles())
}

func (s *RBACService) CreateNewSystemUser(ctx context.Context, req CreateSystemUserRequest, actor string) (*SystemUserRecord, error) {
	created, err := s.createSystemUser(ctx, req, actor, true)
	if err != nil {
		return nil, err
	}
	return s.GetSystemUser(ctx, created.ID)
}

type ClaimMappingListQuery struct {
	ListQuery
	Origin       string
	OIDCProvider string
	RoleID       string
	Search       string
}

func (s *RBACService) ListClaimMappingsPage(ctx context.Context, q ClaimMappingListQuery) (*PaginatedResponse[RBACClaimMappingResponse], error) {
	var yaml []RBACClaimMappingResponse
	if q.Origin == "" || q.Origin == RBACOriginYAML {
		search := strings.TrimSpace(q.Search)
		for _, m := range yamlClaimMappingResponses(s.yamlState()) {
			if q.OIDCProvider != "" && m.OIDCProvider != q.OIDCProvider {
				continue
			}
			if q.RoleID != "" && !slices.ContainsFunc(m.RoleIDs, func(ref string) bool { return s.matchesYAMLRole(ref, q.RoleID) }) {
				continue
			}
			if search != "" && !containsFold(m.ClaimValue, search) {
				continue
			}
			yaml = append(yaml, m)
		}
		slices.SortFunc(yaml, func(a, b RBACClaimMappingResponse) int {
			return cmp.Or(compareFold(a.ClaimValue, b.ClaimValue), cmp.Compare(a.ID, b.ID))
		})
	}
	return pageAfterYAML(ctx, yaml, q.ListQuery, func(ctx context.Context, p store.PageQuery) ([]RBACClaimMappingResponse, int, error) {
		if q.Origin == RBACOriginYAML {
			return nil, 0, nil
		}
		mappings, roles, total, err := s.repo.ListClaimMappingsPage(ctx, store.ClaimMappingListOptions{
			OIDCProvider: q.OIDCProvider, RoleID: q.RoleID, Search: q.Search, PageQuery: p,
		})
		if err != nil {
			return nil, 0, mapRepoError(err, resourceClaimMapping, "")
		}
		out := make([]RBACClaimMappingResponse, len(mappings))
		for i, m := range mappings {
			out[i] = *claimMappingToResponse(m, roles[m.ID])
		}
		return out, total, nil
	})
}

func (s *RBACService) YAMLClaimMapping(id string) (*RBACClaimMappingResponse, bool) {
	m, ok := findYAMLRow(yamlClaimMappingResponses(s.yamlState()), YAMLKindClaimMapping, id, func(m RBACClaimMappingResponse) string { return m.ID })
	if !ok {
		return nil, false
	}
	return &m, true
}

func (s *RBACService) GetClaimMapping(ctx context.Context, id string) (*RBACClaimMappingResponse, error) {
	if m, ok := s.YAMLClaimMapping(id); ok {
		return m, nil
	}
	m, roleIDs, err := s.repo.GetClaimMappingByID(ctx, id)
	if err != nil {
		return nil, mapRepoError(err, resourceClaimMapping, id)
	}
	if m == nil {
		return nil, &NotFoundError{Resource: resourceClaimMapping, ID: id}
	}
	return claimMappingToResponse(m, roleIDs), nil
}
