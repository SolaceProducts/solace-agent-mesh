// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Role struct {
	ID           string   `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type         string   `json:"type" example:"role"`
	Name         string   `json:"name" example:"Support agent operator"`
	Description  *string  `json:"description" example:"Runs and monitors the support agents."`
	Scopes       []string `json:"scopes" example:"[\"agent:*:invoke\",\"agent_builder:*:read\"]"`
	Inherits     []string `json:"inherits" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
	Origin       string   `json:"origin" example:"custom"`
	NameConflict bool     `json:"nameConflict" example:"false"`
	CreatedBy    *string  `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy    *string  `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime  Instant  `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime  Instant  `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type CreateRoleRequest struct {
	Name        string   `json:"name" validate:"required,safename" example:"Support agent operator"`
	Description string   `json:"description" example:"Runs and monitors the support agents."`
	Scopes      []string `json:"scopes" example:"[\"agent:*:invoke\",\"agent_builder:*:read\"]"`
	Inherits    []string `json:"inherits" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
}

type ReplaceRoleRequest struct {
	ID          string   `path:"id" json:"-"`
	Name        string   `json:"name" validate:"required,safename" example:"Support agent operator"`
	Description string   `json:"description" example:"Runs and monitors the support agents."`
	Scopes      []string `json:"scopes" example:"[\"agent:*:invoke\",\"agent_builder:*:read\"]"`
	Inherits    []string `json:"inherits" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
}

type listRolesRequest struct {
	Page   Page      `json:"-"`
	Sort   SortOrder `sort:"name,createdTime,updatedTime" json:"-"`
	Origin string    `query:"origin" json:"-" example:"custom"`
	Search string    `query:"search" json:"-" example:"support"`
}

type roleHandler struct {
	svc *entplatform.RBACService
}

const rolePath = "/rbac/roles/{id}"

func roleRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.RBACSvc == nil {
		return nil
	}
	h := &roleHandler{svc: deps.RBACSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/rbac/roles", Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listRoles", Summary: "Get a list of roles",
				Text: "Returns one page of roles: those defined in the configuration first, then the custom and built-in roles.", Tag: rbacTag},
			Handler: Handle(h.list, "Unable to list roles")},
		{Method: http.MethodPost, Path: "/rbac/roles", Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceRole, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createRole", Summary: "Create a role",
				Text: "Creates a custom role.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.create, "Unable to create role")},
		{Method: http.MethodGet, Path: rolePath, Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getRole", Summary: "Get a role",
				Text: "Returns one role, whatever its origin.", Tag: rbacTag},
			Handler: Handle(h.get, "Unable to get role")},
		{Method: http.MethodPut, Path: rolePath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceRole, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "replaceRole", Summary: "Replace a role",
				Text: "Replaces a custom role with the fields sent. A field left out is cleared. If the role is defined in the configuration or shipped by Solace, the request fails with 409.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.replace, "Unable to update role")},
		{Method: http.MethodDelete, Path: rolePath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceRole, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteRole", Summary: "Delete a role",
				Text: "Deletes a custom role and every grant of it, and removes it from claim mappings and inheriting roles. If the role is defined in the configuration, shipped by Solace, or a default role, the request fails with 409.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.delete, "Unable to delete role")},
	}
}

func (h *roleHandler) list(ctx context.Context, req listRolesRequest) (List[Role], error) {
	if err := checkOneOf("origin", req.Origin, entplatform.RBACOriginCustom, entplatform.RBACOriginYAML, entplatform.RBACOriginBuiltin); err != nil {
		return List[Role]{}, err
	}
	resp, err := h.svc.ListRolesPage(ctx, entplatform.RoleListQuery{
		ListQuery: listQuery(req.Page, req.Sort), Origin: req.Origin, Search: req.Search,
	})
	if err != nil {
		return List[Role]{}, err
	}
	return ListOf(resp, h.toRole), nil
}

func (h *roleHandler) create(ctx context.Context, req CreateRoleRequest) (Role, error) {
	resp, err := h.svc.CreateRole(ctx, entplatform.CreateRBACRoleRequest{
		Name: req.Name, Description: req.Description, Scopes: req.Scopes, Inherits: req.Inherits,
	}, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	if err != nil {
		return Role{}, rbacError(err)
	}
	return h.toRole(resp), nil
}

func (h *roleHandler) get(ctx context.Context, req IDRequest) (Role, error) {
	resp, err := h.svc.GetRole(ctx, h.svc.V1RoleID(req.ID))
	if err != nil {
		return Role{}, err
	}
	return h.toRole(resp), nil
}

func (h *roleHandler) replace(ctx context.Context, req ReplaceRoleRequest) (Role, error) {
	resp, err := h.svc.UpdateRole(ctx, h.svc.V1RoleID(req.ID), entplatform.UpdateRBACRoleRequest{
		Name: req.Name, Description: req.Description, Scopes: req.Scopes, Inherits: req.Inherits,
	}, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	if err != nil {
		return Role{}, rbacError(err)
	}
	return h.toRole(resp), nil
}

func (h *roleHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	err := h.svc.DeleteRole(ctx, h.svc.V1RoleID(req.ID), entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	return struct{}{}, rbacError(err)
}

func (h *roleHandler) toRole(r *entplatform.RBACRoleResponse) Role {
	return Role{
		ID:           h.svc.V2RoleID(r.Origin, r.ID),
		Type:         "role",
		Name:         r.Name,
		Description:  NullIfZero(r.Description),
		Scopes:       nonNilList(r.Scopes),
		Inherits:     h.svc.V2RoleIDs(r.Origin, r.Inherits),
		Origin:       r.Origin,
		NameConflict: r.NameConflict,
		CreatedBy:    NullIfZero(r.CreatedBy),
		UpdatedBy:    NullIfZero(r.UpdatedBy),
		CreatedTime:  Instant(r.CreatedAt),
		UpdatedTime:  Instant(r.UpdatedAt),
	}
}
