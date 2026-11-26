// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"log/slog"
	"net/http"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type ProfileProvider struct {
	ToolsetID   *string `json:"toolsetId" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	LookupKey   string  `json:"lookupKey" example:"email"`
	CreatedBy   *string `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy   *string `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime Instant `json:"createdTime"`
	UpdatedTime Instant `json:"updatedTime"`
}

type ReplaceProfileProviderRequest struct {
	ToolsetID string `json:"toolsetId" validate:"required,uuid" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	LookupKey string `json:"lookupKey" validate:"required" example:"upn"`
}

type profileProviderHandler struct {
	svc *entplatform.IdentityConfigService
}

const profileProviderPath = "/profileProvider"

func profileProviderRoutes(deps *entplatform.RouteDeps, _ *slog.Logger) []Route {
	if deps.IdentityConfigSvc == nil {
		return nil
	}
	h := &profileProviderHandler{svc: deps.IdentityConfigSvc}
	return []Route{
		{Method: http.MethodGet, Path: profileProviderPath,
			AnyOfScopes: []string{rbac.ScopeProfileProviderRead, rbac.ScopeProfileProviderUpdate},
			AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "getProfileProvider", Summary: "Get the profile provider",
				Text: "Returns the toolset that enriches a signed-in user's profile and the attribute it looks the user up by. The toolset is null when none is wired, and the lookup key is email until one is set.", Tag: profileProviderTag},
			Handler: Handle(h.get, "Unable to get profile provider")},
		{Method: http.MethodPut, Path: profileProviderPath, Scope: rbac.ScopeProfileProviderUpdate,
			AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "replaceProfileProvider", Summary: "Replace the profile provider",
				Text: "Wires a toolset as the profile provider and sets the attribute it looks the user up by, replacing both. The toolset must be ready, declare the profileProvider capability, and expose the enrich_authenticated_user tool. If it does not, the request fails with 422 and lists each unmet condition under toolsetId.", Tag: profileProviderTag},
			Handler: Handle(h.replace, "Unable to replace profile provider")},
		{Method: http.MethodDelete, Path: profileProviderPath, Scope: rbac.ScopeProfileProviderUpdate,
			AllowAPIKey: true, Published: true, Stability: StabilityStable,
			Doc: Doc{ID: "deleteProfileProvider", Summary: "Delete the profile provider",
				Text: "Clears the wired toolset and keeps the lookup key. Returns 204 even when no toolset was wired.", Tag: profileProviderTag},
			Handler: Handle(h.delete, "Unable to delete profile provider")},
	}
}

func (h *profileProviderHandler) get(ctx context.Context, _ struct{}) (ProfileProvider, error) {
	resp, err := h.svc.Get(ctx)
	if err != nil {
		return ProfileProvider{}, err
	}
	return toProfileProvider(resp), nil
}

func (h *profileProviderHandler) replace(ctx context.Context, req ReplaceProfileProviderRequest) (ProfileProvider, error) {
	resp, err := h.svc.Set(ctx, entplatform.UpdateProfileProviderRequest{ToolsetID: req.ToolsetID, LookupKey: req.LookupKey},
		entplatform.UserIDFromContext(ctx))
	if err != nil {
		return ProfileProvider{}, err
	}
	return toProfileProvider(resp), nil
}

func (h *profileProviderHandler) delete(ctx context.Context, _ struct{}) (struct{}, error) {
	_, err := h.svc.Clear(ctx, entplatform.UserIDFromContext(ctx))
	return struct{}{}, err
}

func toProfileProvider(resp *entplatform.ProfileProviderResponse) ProfileProvider {
	return ProfileProvider{
		ToolsetID:   resp.ToolsetID,
		LookupKey:   resp.LookupKey,
		CreatedBy:   NullIfZero(resp.CreatedBy),
		UpdatedBy:   NullIfZero(resp.UpdatedBy),
		CreatedTime: Instant(resp.CreatedAt),
		UpdatedTime: Instant(resp.UpdatedAt),
	}
}
