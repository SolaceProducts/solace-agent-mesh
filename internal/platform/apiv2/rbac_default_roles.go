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

type DefaultRoles struct {
	RoleIDs []string `json:"roleIds" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
	Origin  string   `json:"origin" example:"custom"`
}

type ReplaceDefaultRolesRequest struct {
	RoleIDs []string `json:"roleIds" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
}

type defaultRolesHandler struct {
	svc *entplatform.RBACService
}

func defaultRoleRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.RBACSvc == nil {
		return nil
	}
	h := &defaultRolesHandler{svc: deps.RBACSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/rbac/defaultRoles", Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getDefaultRoles", Summary: "Get the default roles",
				Text: "Returns the roles given to a user who holds no other role: the override set through the API, or else the defaults defined in the configuration.", Tag: rbacTag},
			Handler: Handle(h.get, "Unable to get default roles")},
		{Method: http.MethodPut, Path: "/rbac/defaultRoles", Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceDefaultRoles, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "replaceDefaultRoles", Summary: "Replace the default roles",
				Text: "Sets the default roles, overriding those defined in the configuration. An empty set clears the override.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.replace, "Unable to update default roles")},
	}
}

func (h *defaultRolesHandler) get(ctx context.Context, _ struct{}) (DefaultRoles, error) {
	resp, err := h.svc.GetDefaultRoles(ctx)
	if err != nil {
		return DefaultRoles{}, err
	}
	return h.toDefaultRoles(resp), nil
}

func (h *defaultRolesHandler) replace(ctx context.Context, req ReplaceDefaultRolesRequest) (DefaultRoles, error) {
	err := h.svc.SetDefaultRoles(ctx, req.RoleIDs, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	if err != nil {
		return DefaultRoles{}, rbacError(err)
	}
	return h.get(ctx, struct{}{})
}

func (h *defaultRolesHandler) toDefaultRoles(r *entplatform.DefaultRolesResponse) DefaultRoles {
	return DefaultRoles{
		RoleIDs: h.svc.V2RoleIDs(r.Origin, r.RoleIDs),
		Origin:  r.Origin,
	}
}
