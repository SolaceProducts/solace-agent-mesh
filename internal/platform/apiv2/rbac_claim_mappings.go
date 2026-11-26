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

const msgYAMLClaimMappingImmutable = "Claim mappings defined in your configuration cannot be changed or deleted through the API"

type ClaimMapping struct {
	ID           string   `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type         string   `json:"type" example:"claimMapping"`
	Name         *string  `json:"name" example:"Platform engineers"`
	OIDCProvider *string  `json:"oidcProvider" example:"okta"`
	ClaimKey     *string  `json:"claimKey" example:"groups"`
	ClaimValue   string   `json:"claimValue" example:"platform-engineers"`
	RoleIDs      []string `json:"roleIds" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
	Origin       string   `json:"origin" example:"custom"`
	CreatedBy    *string  `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy    *string  `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime  Instant  `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime  Instant  `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type CreateClaimMappingRequest struct {
	Name         string   `json:"name" validate:"required,max=255" example:"Platform engineers"`
	OIDCProvider string   `json:"oidcProvider" example:"okta"`
	ClaimKey     string   `json:"claimKey" validate:"max=256" example:"groups"`
	ClaimValue   string   `json:"claimValue" validate:"required,max=256" example:"platform-engineers"`
	RoleIDs      []string `json:"roleIds" validate:"required,min=1" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
}

type ReplaceClaimMappingRequest struct {
	ID           string   `path:"id" json:"-"`
	Name         string   `json:"name" validate:"required,max=255" example:"Platform engineers"`
	OIDCProvider string   `json:"oidcProvider" example:"okta"`
	ClaimKey     string   `json:"claimKey" validate:"max=256" example:"groups"`
	ClaimValue   string   `json:"claimValue" validate:"required,max=256" example:"platform-engineers"`
	RoleIDs      []string `json:"roleIds" validate:"required,min=1" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
}

type listClaimMappingsRequest struct {
	Page         Page      `json:"-"`
	Sort         SortOrder `sort:"name,oidcProvider,claimValue,createdTime,updatedTime" json:"-"`
	Origin       string    `query:"origin" json:"-" example:"custom"`
	OIDCProvider string    `query:"oidcProvider" json:"-" example:"okta"`
	RoleID       string    `query:"roleId" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40"`
	Search       string    `query:"search" json:"-" example:"platform"`
}

type claimMappingHandler struct {
	svc *entplatform.RBACService
}

const claimMappingPath = "/rbac/claimMappings/{id}"

func claimMappingRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.RBACSvc == nil {
		return nil
	}
	h := &claimMappingHandler{svc: deps.RBACSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/rbac/claimMappings", Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listClaimMappings", Summary: "Get a list of claim mappings",
				Text: "Returns one page of claim mappings: those defined in the configuration first, then the custom ones.", Tag: rbacTag},
			Handler: Handle(h.list, "Unable to list claim mappings")},
		{Method: http.MethodPost, Path: "/rbac/claimMappings", Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceClaimMapping, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createClaimMapping", Summary: "Create a claim mapping",
				Text: "Creates a claim mapping that grants roles to identities whose token carries the claim value.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.create, "Unable to create claim mapping")},
		{Method: http.MethodGet, Path: claimMappingPath, Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getClaimMapping", Summary: "Get a claim mapping",
				Text: "Returns one claim mapping, whatever its origin.", Tag: rbacTag},
			Handler: Handle(h.get, "Unable to get claim mapping")},
		{Method: http.MethodPut, Path: claimMappingPath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceClaimMapping, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "replaceClaimMapping", Summary: "Replace a claim mapping",
				Text: "Replaces a custom claim mapping with the fields sent. If the mapping is defined in the configuration, the request fails with 409.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.replace, "Unable to update claim mapping")},
		{Method: http.MethodDelete, Path: claimMappingPath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceClaimMapping, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteClaimMapping", Summary: "Delete a claim mapping",
				Text: "Deletes a custom claim mapping. If the mapping is defined in the configuration, the request fails with 409.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.delete, "Unable to delete claim mapping")},
	}
}

func (h *claimMappingHandler) list(ctx context.Context, req listClaimMappingsRequest) (List[ClaimMapping], error) {
	if err := checkOneOf("origin", req.Origin, entplatform.RBACOriginCustom, entplatform.RBACOriginYAML); err != nil {
		return List[ClaimMapping]{}, err
	}
	if err := CheckUUIDQuery("roleId", req.RoleID); err != nil {
		return List[ClaimMapping]{}, err
	}
	resp, err := h.svc.ListClaimMappingsPage(ctx, entplatform.ClaimMappingListQuery{
		ListQuery: listQuery(req.Page, req.Sort), Origin: req.Origin,
		OIDCProvider: req.OIDCProvider, RoleID: req.RoleID, Search: req.Search,
	})
	if err != nil {
		return List[ClaimMapping]{}, err
	}
	return ListOf(resp, h.toClaimMapping), nil
}

func (h *claimMappingHandler) create(ctx context.Context, req CreateClaimMappingRequest) (ClaimMapping, error) {
	resp, err := h.svc.CreateClaimMapping(ctx, entplatform.CreateRBACClaimMappingRequest{
		Name: req.Name, OIDCProvider: req.OIDCProvider, ClaimKey: req.ClaimKey,
		ClaimValue: req.ClaimValue, RoleIDs: req.RoleIDs,
	}, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	if err != nil {
		return ClaimMapping{}, rbacError(onRoleIDs(err))
	}
	return h.stored(ctx, resp)
}

func (h *claimMappingHandler) get(ctx context.Context, req IDRequest) (ClaimMapping, error) {
	resp, err := h.svc.GetClaimMapping(ctx, req.ID)
	if err != nil {
		return ClaimMapping{}, err
	}
	return h.toClaimMapping(resp), nil
}

func (h *claimMappingHandler) replace(ctx context.Context, req ReplaceClaimMappingRequest) (ClaimMapping, error) {
	if err := h.refuseYAML(req.ID); err != nil {
		return ClaimMapping{}, err
	}
	resp, err := h.svc.ReplaceClaimMapping(ctx, req.ID, entplatform.UpdateRBACClaimMappingRequest{
		Name: req.Name, OIDCProvider: req.OIDCProvider, ClaimKey: req.ClaimKey,
		ClaimValue: req.ClaimValue, RoleIDs: req.RoleIDs,
	}, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	if err != nil {
		return ClaimMapping{}, rbacError(onRoleIDs(err))
	}
	return h.stored(ctx, resp)
}

func (h *claimMappingHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	if err := h.refuseYAML(req.ID); err != nil {
		return struct{}{}, err
	}
	err := h.svc.DeleteClaimMapping(ctx, req.ID, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	return struct{}{}, rbacError(err)
}

func (h *claimMappingHandler) refuseYAML(id string) error {
	if _, ok := h.svc.YAMLClaimMapping(id); ok {
		return &CallerError{Status: http.StatusConflict, Message: msgYAMLClaimMappingImmutable}
	}
	return nil
}

func (h *claimMappingHandler) stored(ctx context.Context, written *entplatform.RBACClaimMappingResponse) (ClaimMapping, error) {
	if written.CreatedAt != 0 && written.UpdatedAt != 0 {
		return h.toClaimMapping(written), nil
	}
	return h.get(ctx, IDRequest{ID: written.ID})
}

func (h *claimMappingHandler) toClaimMapping(m *entplatform.RBACClaimMappingResponse) ClaimMapping {
	return ClaimMapping{
		ID:           entplatform.V2RowID(entplatform.YAMLKindClaimMapping, m.Origin, m.ID),
		Type:         "claimMapping",
		Name:         NullIfZero(m.Name),
		OIDCProvider: NullIfZero(m.OIDCProvider),
		ClaimKey:     NullIfZero(m.ClaimKey),
		ClaimValue:   m.ClaimValue,
		RoleIDs:      h.svc.V2RoleIDs(m.Origin, m.RoleIDs),
		Origin:       m.Origin,
		CreatedBy:    NullIfZero(m.CreatedBy),
		UpdatedBy:    NullIfZero(m.UpdatedBy),
		CreatedTime:  Instant(m.CreatedAt),
		UpdatedTime:  Instant(m.UpdatedAt),
	}
}
