// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	userExpandBudget = "budget"
	msgNoPermission  = "Insufficient permissions"
)

type User struct {
	ID                    string         `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type                  string         `json:"type" example:"user"`
	DisplayName           *string        `json:"displayName" example:"Jane Doe"`
	UserName              *string        `json:"userName" example:"jane.doe"`
	Email                 *string        `json:"email" example:"jane.doe@example.com"`
	Issuer                *string        `json:"issuer" example:"https://idp.example.com"`
	Sub                   *string        `json:"sub" example:"00u1abcd2EFGH3ijk4l5"`
	OIDCProvider          *string        `json:"oidcProvider" example:"okta"`
	ProvisionedVia        string         `json:"provisionedVia" example:"jit"`
	Active                bool           `json:"active" example:"true"`
	Groups                []string       `json:"groups" example:"[\"engineering\",\"support\"]"`
	Roles                 []UserRole     `json:"roles"`
	LastLoginTime         Instant        `json:"lastLoginTime" example:"\"2026-01-03T10:15:30.000Z\""`
	CreatedBy             *string        `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy             *string        `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime           Instant        `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime           Instant        `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
	Budget                *AppliedBudget `json:"budget"`
	BudgetTopUp           *ActiveTopUp   `json:"budgetTopUp"`
	EffectiveBudgetAmount *string        `json:"effectiveBudgetAmount" example:"300.00"`
}

type ActiveTopUp struct {
	Amount      string  `json:"amount" example:"50.00"`
	ExpiresTime Instant `json:"expiresTime" example:"\"2026-02-01T00:00:00.000Z\""`
}

type UserRole struct {
	RoleID     string `json:"roleId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40"`
	Source     string `json:"source" example:"direct"`
	Manageable bool   `json:"manageable" example:"true"`
}

type AppliedBudget struct {
	ID           string  `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e50"`
	Name         string  `json:"name" example:"Engineering monthly"`
	Amount       *string `json:"amount" example:"250.00"`
	Source       string  `json:"source" example:"assigned"`
	AssignmentID *string `json:"assignmentId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e51"`
}

type UserRoleGrants struct {
	RoleIDs []string `json:"roleIds" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
}

type UserIdentityClaim struct {
	ClaimKey            string  `json:"claimKey" example:"phone_number"`
	ClaimValueCanonical string  `json:"claimValueCanonical" example:"+15551234567"`
	Verified            bool    `json:"verified" example:"true"`
	UpdatedTime         Instant `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type listUsersRequest struct {
	Page         Page      `json:"-"`
	Sort         SortOrder `sort:"displayName,email,lastLoginTime,createdTime,updatedTime" json:"-"`
	Search       string    `query:"search" json:"-" example:"jane"`
	RoleID       string    `query:"roleId" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40"`
	Group        string    `query:"group" json:"-" example:"engineering"`
	OIDCProvider string    `query:"oidcProvider" json:"-" example:"okta"`
	BudgetID     string    `query:"budgetId" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e50"`
	Expand       string    `query:"expand" json:"-" example:"budget"`
}

type getUserRequest struct {
	ID     string `path:"id" json:"-"`
	Expand string `query:"expand" json:"-" example:"budget"`
}

type ReplaceUserRoleGrantsRequest struct {
	ID      string   `path:"id" json:"-"`
	RoleIDs []string `json:"roleIds" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
}

type listUserIdentityClaimsRequest struct {
	ID   string `path:"id" json:"-"`
	Page Page   `json:"-"`
}

type userHandler struct {
	svc   *entplatform.UsersService
	roles *entplatform.RBACService
}

func userRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.UsersSvc == nil || deps.RBACSvc == nil {
		return nil
	}
	h := &userHandler{svc: deps.UsersSvc, roles: deps.RBACSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/users", Scope: rbac.ScopeRBACRead, Stability: StabilityDraft,
			Doc: Doc{ID: "listUsers", Summary: "Get a list of users",
				Text: "Returns one page of the users known to the platform, with the roles that apply to each.", Tag: usersTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.list, "Unable to list users")},
		{Method: http.MethodGet, Path: "/users/{id}", Scope: rbac.ScopeRBACRead, Stability: StabilityDraft,
			Doc: Doc{ID: "getUser", Summary: "Get a user",
				Text: "Returns one user with the roles that apply to them.", Tag: usersTag},
			Handler: Handle(h.get, "Unable to get user")},
		{Method: http.MethodGet, Path: "/users/{id}/roleGrants", Scope: rbac.ScopeRBACRead, Stability: StabilityDraft,
			Doc: Doc{ID: "getUserRoleGrants", Summary: "Get the roles granted to a user",
				Text: "Returns the roles granted directly to the user.", Tag: usersTag},
			Handler: Handle(h.getRoleGrants, "Unable to get user role grants")},
		{Method: http.MethodPut, Path: "/users/{id}/roleGrants", Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceGrant, Stability: StabilityDraft,
			Doc: Doc{ID: "replaceUserRoleGrants", Summary: "Replace the roles granted to a user",
				Text: "Replaces the roles granted directly to the user. A grant of a role that stays in the list is kept. Roles the user has through a claim mapping, the configuration, or the default roles are not affected.", Tag: usersTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.replaceRoleGrants, "Unable to update user role grants")},
	}
}

func identityClaimRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.IdentityClaimsSvc == nil {
		return nil
	}
	h := &identityClaimHandler{svc: deps.IdentityClaimsSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/users/{id}/identityClaims", Scope: rbac.ScopeRBACRead, Stability: StabilityDraft,
			Doc: Doc{ID: "listUserIdentityClaims", Summary: "Get a list of a user's identity claims",
				Text: "Returns the identity claims on file for the user, which are what an entrypoint's sender lookup matches against. Use it to see why a sender does not resolve to the user.", Tag: usersTag},
			Handler: Handle(h.list, "Unable to list user identity claims")},
		{Method: http.MethodDelete, Path: "/users/{id}/identityClaims", Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceIdentityClaims, Stability: StabilityDraft,
			Doc: Doc{ID: "revokeUserIdentityClaims", Summary: "Delete a user's identity claims",
				Text: "Deletes the user's identity claims and the entrypoint sender bindings made on their behalf. No entrypoint resolves a sender to the user until they next sign in.", Tag: usersTag},
			Handler: Handle(h.revoke, "Unable to revoke user identity claims")},
	}
}

var usersSortFields = map[string]string{
	"displayName":   "displayName",
	"email":         "email",
	"lastLoginTime": "lastLoginAt",
	"createdTime":   "createdAt",
	"updatedTime":   "updatedAt",
}

func (h *userHandler) list(ctx context.Context, req listUsersRequest) (List[User], error) {
	if err := CheckUUIDQuery("roleId", req.RoleID); err != nil {
		return List[User]{}, err
	}
	if err := CheckUUIDQuery("budgetId", req.BudgetID); err != nil {
		return List[User]{}, err
	}
	if req.BudgetID != "" {
		if err := h.requireBudgetAccess(ctx, "budgetId"); err != nil {
			return List[User]{}, err
		}
	}
	expand, err := h.parseExpand(ctx, req.Expand)
	if err != nil {
		return List[User]{}, err
	}
	sortDir := "asc"
	if req.Sort.Desc {
		sortDir = "desc"
	}
	resp, err := h.svc.List(ctx, entplatform.UserListQuery{
		PageNumber:   req.Page.Number,
		PageSize:     req.Page.Size,
		SortBy:       usersSortFields[req.Sort.Field],
		SortOrder:    sortDir,
		Search:       strings.TrimSpace(req.Search),
		Group:        req.Group,
		OIDCProvider: req.OIDCProvider,
		RoleID:       h.roles.V1RoleID(req.RoleID),
		BudgetID:     req.BudgetID,
		ExpandBudget: expand,
	})
	if err != nil {
		return List[User]{}, err
	}
	return ListOf(resp, h.toUser), nil
}

func (h *userHandler) get(ctx context.Context, req getUserRequest) (User, error) {
	expand, err := h.parseExpand(ctx, req.Expand)
	if err != nil {
		return User{}, err
	}
	resp, err := h.svc.GetByID(ctx, req.ID)
	if err != nil {
		return User{}, err
	}
	if expand {
		users := []entplatform.UserResponse{*resp}
		if err := h.svc.AttachBudgets(ctx, users); err != nil {
			return User{}, err
		}
		resp = &users[0]
	}
	return h.toUser(resp), nil
}

func (h *userHandler) getRoleGrants(ctx context.Context, req IDRequest) (UserRoleGrants, error) {
	resp, err := h.svc.GetUserRoleGrants(ctx, req.ID)
	if err != nil {
		return UserRoleGrants{}, err
	}
	return toUserRoleGrants(resp), nil
}

func (h *userHandler) replaceRoleGrants(ctx context.Context, req ReplaceUserRoleGrantsRequest) (UserRoleGrants, error) {
	resp, err := h.svc.SetUserRoleGrants(ctx, req.ID, req.RoleIDs, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	if err != nil {
		return UserRoleGrants{}, rbacError(err)
	}
	return toUserRoleGrants(resp), nil
}

func (h *userHandler) parseExpand(ctx context.Context, expand string) (bool, error) {
	asked, problem := apicontract.ExpandOnly(expand, userExpandBudget)
	if problem != "" {
		return false, QueryError(http.StatusUnprocessableEntity, "expand", problem)
	}
	if !asked {
		return false, nil
	}
	return true, h.requireBudgetAccess(ctx, "expand")
}

func (h *userHandler) requireBudgetAccess(ctx context.Context, param string) error {
	if !coreauth.HasScope(entplatform.ScopesFromContext(ctx), rbac.ScopeBudgetRead) {
		return &CallerError{Status: http.StatusForbidden, Message: msgNoPermission}
	}
	return nil
}

type identityClaimHandler struct {
	svc *entplatform.UsersService
}

func (h *identityClaimHandler) list(ctx context.Context, req listUserIdentityClaimsRequest) (List[UserIdentityClaim], error) {
	resp, err := h.svc.GetUserIdentityClaimsPage(ctx, req.ID, listQuery(req.Page, SortOrder{}))
	if err != nil {
		return List[UserIdentityClaim]{}, err
	}
	return ListOf(resp, toUserIdentityClaim), nil
}

func toUserIdentityClaim(c *entplatform.UserIdentityClaimResponse) UserIdentityClaim {
	return UserIdentityClaim{
		ClaimKey: c.ClaimKey, ClaimValueCanonical: c.ClaimValueCanonical,
		Verified: c.Verified, UpdatedTime: Instant(c.UpdatedAt),
	}
}

func (h *identityClaimHandler) revoke(ctx context.Context, req IDRequest) (struct{}, error) {
	_, err := h.svc.RevokeUserIdentityClaims(ctx, req.ID, entplatform.UserIDFromContext(ctx))
	return struct{}{}, err
}

func toUserRoleGrants(r *entplatform.UserRoleGrantsResponse) UserRoleGrants {
	return UserRoleGrants{RoleIDs: nonNilList(r.RoleIDs)}
}

func (h *userHandler) toUser(u *entplatform.UserResponse) User {
	roles := make([]UserRole, len(u.Roles))
	for i, r := range u.Roles {
		roles[i] = UserRole{RoleID: h.roles.V2ResolvedRoleID(r.RoleID), Source: r.Source, Manageable: r.Manageable}
	}
	return User{
		ID:                    u.ID,
		Type:                  "user",
		DisplayName:           NullIfZero(u.DisplayName),
		UserName:              NullIfZero(u.UserName),
		Email:                 NullIfZero(u.Email),
		Issuer:                NullIfZero(u.Issuer),
		Sub:                   NullIfZero(u.Sub),
		OIDCProvider:          NullIfZero(u.OIDCProvider),
		ProvisionedVia:        u.ProvisionedVia,
		Active:                u.Active,
		Groups:                nonNilList(u.Groups),
		Roles:                 roles,
		LastLoginTime:         Instant(u.LastLoginAt),
		CreatedBy:             NullIfZero(u.CreatedBy),
		UpdatedBy:             NullIfZero(u.UpdatedBy),
		CreatedTime:           Instant(u.CreatedAt),
		UpdatedTime:           Instant(u.UpdatedAt),
		Budget:                toAppliedBudget(u.Budget.Value()),
		BudgetTopUp:           toActiveTopUp(u.BudgetTopUp.Value()),
		EffectiveBudgetAmount: u.EffectiveBudgetAmount.Value(),
	}
}

func toActiveTopUp(t *entplatform.BudgetSubjectTopUp) *ActiveTopUp {
	if t == nil {
		return nil
	}
	return &ActiveTopUp{Amount: t.Amount, ExpiresTime: Instant(t.ExpiresTime)}
}

func toAppliedBudget(b *entplatform.BudgetSubjectBudget) *AppliedBudget {
	if b == nil {
		return nil
	}
	return &AppliedBudget{ID: b.ID, Name: b.Name, Amount: b.Amount, Source: b.Source, AssignmentID: b.AssignmentID}
}
