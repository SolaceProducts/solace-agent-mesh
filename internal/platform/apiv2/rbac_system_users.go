// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"net/http"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type ServiceAccount struct {
	ID          string   `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type        string   `json:"type" example:"serviceAccount"`
	Name        string   `json:"name" example:"support-webhook"`
	Sub         string   `json:"sub" example:"system:support-webhook"`
	DisplayName *string  `json:"displayName" example:"Support webhook"`
	RoleIDs     []string `json:"roleIds" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
	Origin      string   `json:"origin" example:"custom"`
	CreatedBy   *string  `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy   *string  `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime Instant  `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime Instant  `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type CreateServiceAccountRequest struct {
	Name        string   `json:"name" validate:"required,max=255" example:"support-webhook"`
	DisplayName string   `json:"displayName" example:"Support webhook"`
	RoleIDs     []string `json:"roleIds" validate:"required,min=1" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
}

type ReplaceServiceAccountRequest struct {
	ID          string   `path:"id" json:"-"`
	DisplayName string   `json:"displayName" example:"Support webhook"`
	RoleIDs     []string `json:"roleIds" validate:"required,min=1" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
}

type listServiceAccountsRequest struct {
	Page   Page      `json:"-"`
	Sort   SortOrder `sort:"name" json:"-"`
	Search string    `query:"search" json:"-" example:"webhook"`
}

type SystemUser struct {
	ID          string   `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type        string   `json:"type" example:"systemUser"`
	Name        string   `json:"name" example:"support-webhook"`
	Sub         string   `json:"sub" example:"system:support-webhook"`
	DisplayName *string  `json:"displayName" example:"Support webhook"`
	RoleIDs     []string `json:"roleIds" example:"[\"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40\"]"`
	Origin      string   `json:"origin" example:"custom"`
	CreatedBy   *string  `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy   *string  `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime Instant  `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime Instant  `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type CreateSystemUserRequest CreateServiceAccountRequest

type ReplaceSystemUserRequest ReplaceServiceAccountRequest

type listSystemUsersRequest listServiceAccountsRequest

const (
	serviceAccountType = "serviceAccount"
	systemUserType     = "systemUser"

	serviceAccountsPath = "/rbac/serviceAccounts"
	serviceAccountPath  = serviceAccountsPath + "/{id}"
	systemUsersPath     = "/rbac/systemUsers"
	systemUserPath      = systemUsersPath + "/{id}"

	systemUserRenameNoticeURL = rbac.ServiceAccountRenameNoticeURL
	systemUserRoutesRemoval   = rbac.ServiceAccountRenameRemovalDate
	systemUserRoutesReason    = "Deprecated: Reason: system users were renamed to service accounts; use " + serviceAccountsPath + " instead. Removal Date: " + systemUserRoutesRemoval + "."
)

var systemUserRoutesDeprecatedSince = time.Date(2026, time.November, 24, 0, 0, 0, 0, time.UTC)

var serviceAccountRenameDeprecation = &httputil.Deprecation{NoticeURL: systemUserRenameNoticeURL, Since: systemUserRoutesDeprecatedSince}

var isSystemUserFieldDeprecation = &httputil.Deprecation{NoticeURL: systemUserRenameNoticeURL, FieldsOnly: true}

func returnsIsSystemUser(rt Route) Route {
	rt.Deprecation = isSystemUserFieldDeprecation
	return rt
}

type serviceAccountHandler struct {
	svc *entplatform.RBACService
}

func systemUserRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.RBACSvc == nil {
		return nil
	}
	h := &serviceAccountHandler{svc: deps.RBACSvc}
	return slices.Concat(h.serviceAccountRoutes(), h.deprecatedSystemUserRoutes())
}

func (h *serviceAccountHandler) serviceAccountRoutes() []Route {
	return []Route{
		{Method: http.MethodGet, Path: serviceAccountsPath, Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listServiceAccounts", Summary: "Get a list of service accounts",
				Text: "Returns one page of the service accounts an entrypoint can run as, built-in ones included.", Tag: rbacTag},
			Handler: Handle(h.list, "Unable to list service accounts")},
		{Method: http.MethodPost, Path: serviceAccountsPath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceSystemUser, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createServiceAccount", Summary: "Create a service account",
				Text: "Creates a service account holding the roles given. If the name is already taken, the request fails with 409.", Tag: rbacTag},
			Handler: Handle(h.create, "Unable to create service account")},
		{Method: http.MethodGet, Path: serviceAccountPath, Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getServiceAccount", Summary: "Get a service account",
				Text: "Returns one service account, whatever its origin.", Tag: rbacTag},
			Handler: Handle(h.get, "Unable to get service account")},
		{Method: http.MethodPut, Path: serviceAccountPath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceSystemUser, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "replaceServiceAccount", Summary: "Replace a service account",
				Text: "Replaces the roles and display name of a custom service account. A display name left out is cleared. If the service account is built in, the request fails with 409.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.replace, "Unable to update service account")},
		{Method: http.MethodDelete, Path: serviceAccountPath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceSystemUser, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteServiceAccount", Summary: "Delete a service account",
				Text: "Deletes a custom service account together with its grants, API keys, and budget assignments. If the service account is built in, the request fails with 409.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.delete, "Unable to delete service account")},
	}
}

func (h *serviceAccountHandler) deprecatedSystemUserRoutes() []Route {
	deprecation := serviceAccountRenameDeprecation
	return []Route{
		{Method: http.MethodGet, Path: systemUsersPath, Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable, Deprecation: deprecation,
			Doc: Doc{ID: "listSystemUsers", Summary: "Get a list of system users",
				Text: "Returns one page of the service accounts an entrypoint can run as, built-in ones included, each with type systemUser.\n\n" + systemUserRoutesReason, Tag: rbacTag},
			Handler: Handle(h.listSystemUsers, "Unable to list service accounts")},
		{Method: http.MethodPost, Path: systemUsersPath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceSystemUser, AllowAPIKey: true, Published: true,
			Stability: StabilityStable, Deprecation: deprecation,
			Doc: Doc{ID: "createSystemUser", Summary: "Create a system user",
				Text: "Creates a service account holding the roles given, answering with type systemUser. If the name is already taken, the request fails with 409.\n\n" + systemUserRoutesReason, Tag: rbacTag},
			Handler: Handle(h.createSystemUser, "Unable to create service account")},
		{Method: http.MethodGet, Path: systemUserPath, Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable, Deprecation: deprecation,
			Doc: Doc{ID: "getSystemUser", Summary: "Get a system user",
				Text: "Returns one service account, whatever its origin, with type systemUser.\n\n" + systemUserRoutesReason, Tag: rbacTag},
			Handler: Handle(h.getSystemUser, "Unable to get service account")},
		{Method: http.MethodPut, Path: systemUserPath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceSystemUser, AllowAPIKey: true, Published: true,
			Stability: StabilityStable, Deprecation: deprecation,
			Doc: Doc{ID: "replaceSystemUser", Summary: "Replace a system user",
				Text: "Replaces the roles and display name of a custom service account, answering with type systemUser. A display name left out is cleared. If the service account is built in, the request fails with 409.\n\n" + systemUserRoutesReason, Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.replaceSystemUser, "Unable to update service account")},
		{Method: http.MethodDelete, Path: systemUserPath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceSystemUser, AllowAPIKey: true, Published: true,
			Stability: StabilityStable, Deprecation: deprecation,
			Doc: Doc{ID: "deleteSystemUser", Summary: "Delete a system user",
				Text: "Deletes a custom service account together with its grants, API keys, and budget assignments. If the service account is built in, the request fails with 409.\n\n" + systemUserRoutesReason, Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.delete, "Unable to delete service account")},
	}
}

func (h *serviceAccountHandler) list(ctx context.Context, req listServiceAccountsRequest) (List[ServiceAccount], error) {
	resp, err := h.svc.ListSystemUsersPage(ctx, entplatform.SystemUserListQuery{
		ListQuery: listQuery(req.Page, req.Sort), Search: req.Search,
	})
	if err != nil {
		return List[ServiceAccount]{}, err
	}
	return ListOf(resp, h.toServiceAccount), nil
}

func (h *serviceAccountHandler) create(ctx context.Context, req CreateServiceAccountRequest) (ServiceAccount, error) {
	rec, err := h.svc.CreateNewSystemUser(ctx, entplatform.CreateSystemUserRequest{
		Name: req.Name, DisplayName: req.DisplayName, RoleIDs: req.RoleIDs,
	}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return ServiceAccount{}, rbacError(onRoleIDs(err))
	}
	return h.toServiceAccount(rec), nil
}

func (h *serviceAccountHandler) get(ctx context.Context, req IDRequest) (ServiceAccount, error) {
	rec, err := h.svc.GetSystemUser(ctx, req.ID)
	if err != nil {
		return ServiceAccount{}, err
	}
	return h.toServiceAccount(rec), nil
}

func (h *serviceAccountHandler) replace(ctx context.Context, req ReplaceServiceAccountRequest) (ServiceAccount, error) {
	_, err := h.svc.UpdateSystemUser(ctx, req.ID, entplatform.UpdateSystemUserRequest{
		DisplayName: req.DisplayName, RoleIDs: req.RoleIDs,
	}, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	if err != nil {
		return ServiceAccount{}, rbacError(onRoleIDs(err))
	}
	rec, err := h.svc.GetSystemUser(ctx, req.ID)
	if err != nil {
		return ServiceAccount{}, err
	}
	return h.toServiceAccount(rec), nil
}

func (h *serviceAccountHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	err := h.svc.DeleteSystemUser(ctx, req.ID, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	return struct{}{}, rbacError(err)
}

func (h *serviceAccountHandler) listSystemUsers(ctx context.Context, req listSystemUsersRequest) (List[SystemUser], error) {
	resp, err := h.list(ctx, listServiceAccountsRequest(req))
	if err != nil {
		return List[SystemUser]{}, err
	}
	items := make([]SystemUser, len(resp.Items))
	for i, a := range resp.Items {
		items[i] = asSystemUser(a)
	}
	return List[SystemUser]{Items: items, Total: resp.Total}, nil
}

func (h *serviceAccountHandler) createSystemUser(ctx context.Context, req CreateSystemUserRequest) (SystemUser, error) {
	created, err := h.create(ctx, CreateServiceAccountRequest(req))
	return asSystemUser(created), err
}

func (h *serviceAccountHandler) getSystemUser(ctx context.Context, req IDRequest) (SystemUser, error) {
	found, err := h.get(ctx, req)
	return asSystemUser(found), err
}

func (h *serviceAccountHandler) replaceSystemUser(ctx context.Context, req ReplaceSystemUserRequest) (SystemUser, error) {
	replaced, err := h.replace(ctx, ReplaceServiceAccountRequest(req))
	return asSystemUser(replaced), err
}

func asSystemUser(a ServiceAccount) SystemUser {
	a.Type = systemUserType
	return SystemUser(a)
}

func (h *serviceAccountHandler) toServiceAccount(r *entplatform.SystemUserRecord) ServiceAccount {
	return ServiceAccount{
		ID:          r.ID,
		Type:        serviceAccountType,
		Name:        r.Name,
		Sub:         r.Sub,
		DisplayName: NullIfZero(r.DisplayName),
		RoleIDs:     h.roleIDs(r),
		Origin:      r.Origin,
		CreatedBy:   NullIfZero(r.CreatedBy),
		UpdatedBy:   NullIfZero(r.UpdatedBy),
		CreatedTime: Instant(r.CreatedTime),
		UpdatedTime: Instant(r.UpdatedTime),
	}
}

func (h *serviceAccountHandler) roleIDs(r *entplatform.SystemUserRecord) []string {
	configured, granted := r.RoleIDs[:r.ConfiguredRoleCount], r.RoleIDs[r.ConfiguredRoleCount:]
	return slices.Concat(h.svc.V2RoleIDs(entplatform.RBACOriginYAML, configured), h.svc.V2RoleIDs(entplatform.RBACOriginCustom, granted))
}
