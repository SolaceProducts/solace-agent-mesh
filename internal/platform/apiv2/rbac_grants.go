// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	grantSubjectField = "subject"
	grantPath         = "/rbac/grants/{id}"
	grantExpandUser   = "user"
)

const msgYAMLGrantImmutable = "Grants defined in your configuration cannot be deleted through the API"

type Grant struct {
	ID          string        `json:"id" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e3f"`
	Type        string        `json:"type" example:"grant"`
	Subject     *GrantSubject `json:"subject"`
	UserID      *string       `json:"userId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e41"`
	RoleID      string        `json:"roleId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40"`
	State       *string       `json:"state" example:"pending"`
	Origin      string        `json:"origin" example:"custom"`
	CreatedBy   *string       `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy   *string       `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime Instant       `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime Instant       `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
	User        *GrantUser    `json:"user"`
}

type GrantUser struct {
	Email       *string `json:"email" example:"jane.doe@example.com"`
	EmailShared bool    `json:"emailShared" example:"false"`
	Sub         string  `json:"sub" example:"00u1abcd2EFGH3ijk4l5"`
	Issuer      string  `json:"issuer" example:"https://idp.example.com"`
}

type GrantSubject struct {
	Type   string  `json:"type" example:"email"`
	Value  string  `json:"value" example:"jane.doe@example.com"`
	Issuer *string `json:"issuer" example:"https://idp.example.com"`
}

type CreateGrantSubject struct {
	Type   string `json:"type" validate:"required,oneof=email sub legacy" example:"email"`
	Value  string `json:"value" validate:"required" example:"jane.doe@example.com"`
	Issuer string `json:"issuer" example:"https://idp.example.com"`
}

type CreateGrantRequest struct {
	Subject CreateGrantSubject `json:"subject" validate:"required"`
	RoleID  string             `json:"roleId" validate:"required" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40"`
}

type UpdateGrantRequest struct {
	ID      string                    `path:"id" json:"-"`
	Subject Patch[CreateGrantSubject] `json:"subject" validate:"-"`
	RoleID  Patch[string]             `json:"roleId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40"`
	UserID  Patch[string]             `json:"userId" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e41"`
}

func (r UpdateGrantRequest) CrossFieldProblems() map[string][]string {
	details := map[string][]string{}
	if r.Subject.Set {
		if r.Subject.Null {
			details[grantSubjectField] = []string{"cannot be empty"}
		} else if problems := grantSubjectProblems(r.Subject.Value); len(problems) > 0 {
			details[grantSubjectField] = problems
		}
	}
	if r.RoleID.Set {
		details["roleId"] = []string{"cannot be changed"}
	}
	if r.UserID.Set {
		details["userId"] = []string{"cannot be changed"}
	}
	return details
}

type listGrantsRequest struct {
	Page   Page      `json:"-"`
	Sort   SortOrder `sort:"roleId,state,createdTime,updatedTime" json:"-"`
	Origin string    `query:"origin" json:"-" example:"custom"`
	State  string    `query:"state" json:"-" example:"pending"`
	RoleID string    `query:"roleId" json:"-" example:"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40"`
	Expand string    `query:"expand" json:"-" example:"user"`
}

type grantHandler struct {
	svc *entplatform.RBACService
}

func grantRoutes(deps *entplatform.RouteDeps) []Route {
	if deps.RBACSvc == nil {
		return nil
	}
	h := &grantHandler{svc: deps.RBACSvc}
	return []Route{
		{Method: http.MethodGet, Path: "/rbac/grants", Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listGrants", Summary: "Get a list of grants",
				Text: "Returns one page of grants: those defined in the configuration first, then the custom ones. With expand=user, each concrete grant also carries how its user signs in now, so a client can find a grant recorded under a subject that no longer names its user.", Tag: rbacTag},
			Handler: Handle(h.list, "Unable to list grants")},
		{Method: http.MethodPost, Path: "/rbac/grants", Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceGrant, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createGrant", Summary: "Create a grant",
				Text: "Gives a role to an identity. If the grant already exists, is defined in the configuration, or is for a built-in service account, the request fails with 409 and the existing grant is left unchanged.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.create, "Unable to create grant")},
		{Method: http.MethodGet, Path: grantPath, Scope: rbac.ScopeRBACRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getGrant", Summary: "Get a grant",
				Text: "Returns one grant, whatever its origin.", Tag: rbacTag},
			Handler: Handle(h.get, "Unable to get grant")},
		{Method: http.MethodDelete, Path: grantPath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceGrant, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteGrant", Summary: "Delete a grant",
				Text: "Deletes a custom grant. If the grant is defined in the configuration, the request fails with 409.", Tag: rbacTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.delete, "Unable to delete grant")},
		{Method: http.MethodPatch, Path: grantPath, Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceGrant, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "updateGrant", Summary: "Update a grant",
				Text: "Records a concrete grant under a different subject, such as an email in place of a legacy subject, keeping its id, user and role. The subject must name the grant's user, or the answer is 422 on subject; a null subject is also 422, and a subject left out changes nothing. A roleId or userId in the body is 422, because neither can change. A grant defined in the configuration, a pending grant, the grant of a service account, a subject that is a built-in service account, and a subject that the configuration already gives the role answer 409.", Tag: rbacTag},
			Handler: Handle(h.update, "Unable to update grant")},
	}
}

func (h *grantHandler) list(ctx context.Context, req listGrantsRequest) (List[Grant], error) {
	if err := checkOneOf("origin", req.Origin, entplatform.RBACOriginCustom, entplatform.RBACOriginYAML); err != nil {
		return List[Grant]{}, err
	}
	if err := checkOneOf("state", req.State, entplatform.GrantStateConcrete, entplatform.GrantStatePending); err != nil {
		return List[Grant]{}, err
	}
	if err := CheckUUIDQuery("roleId", req.RoleID); err != nil {
		return List[Grant]{}, err
	}
	expandUser, err := parseExpandOf(req.Expand, grantExpandUser)
	if err != nil {
		return List[Grant]{}, err
	}
	resp, err := h.svc.ListGrantsPage(ctx, entplatform.GrantListQuery{
		ListQuery: listQuery(req.Page, req.Sort), Origin: req.Origin, State: req.State, RoleID: req.RoleID,
		ExpandUser: expandUser,
	})
	if err != nil {
		return List[Grant]{}, err
	}
	return ListOf(resp, h.toGrant), nil
}

func (h *grantHandler) create(ctx context.Context, req CreateGrantRequest) (Grant, error) {
	resp, err := h.svc.CreateNewGrant(ctx, entplatform.CreateGrantRequest{
		Subject: entplatform.GrantSubject{Type: req.Subject.Type, Value: req.Subject.Value, Issuer: req.Subject.Issuer},
		RoleID:  req.RoleID,
	}, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	if err != nil {
		return Grant{}, rbacError(err)
	}
	return h.toGrant(resp), nil
}

func (h *grantHandler) get(ctx context.Context, req IDRequest) (Grant, error) {
	resp, err := h.svc.GetGrant(ctx, req.ID)
	if err != nil {
		return Grant{}, err
	}
	return h.toGrant(resp), nil
}

func (h *grantHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	if _, ok := h.svc.YAMLGrant(req.ID); ok {
		return struct{}{}, &CallerError{Status: http.StatusConflict, Message: msgYAMLGrantImmutable}
	}
	err := h.svc.DeleteGrant(ctx, req.ID, entplatform.UserIDFromContext(ctx), entplatform.ScopesFromContext(ctx))
	return struct{}{}, rbacError(err)
}

func (h *grantHandler) update(ctx context.Context, req UpdateGrantRequest) (Grant, error) {
	var subject *entplatform.GrantSubject
	if req.Subject.Set {
		subject = &entplatform.GrantSubject{Type: req.Subject.Value.Type, Value: req.Subject.Value.Value, Issuer: req.Subject.Value.Issuer}
	}
	resp, err := h.svc.UpdateGrant(ctx, req.ID, subject, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return Grant{}, rbacError(err)
	}
	return h.toGrant(resp), nil
}

func grantSubjectProblems(s CreateGrantSubject) []string {
	var problems []string
	if err := entplatform.ValidateRequest(&s); err != nil {
		if invalid, ok := errors.AsType[*apierr.ValidationError](err); ok {
			for _, field := range slices.Sorted(maps.Keys(invalid.Details)) {
				problems = append(problems, invalid.Details[field]...)
			}
		}
	}
	if s.Value != "" && strings.TrimSpace(s.Value) == "" {
		problems = append(problems, "value cannot be empty")
	}
	if s.Type == store.PendingSubjectSub && s.Issuer == "" {
		problems = append(problems, "issuer is required when type is sub")
	}
	return problems
}

func (h *grantHandler) toGrant(g *entplatform.GrantRecord) Grant {
	return Grant{
		ID:          entplatform.V2RowID(entplatform.YAMLKindGrant, g.Origin, g.ID),
		Type:        "grant",
		Subject:     toGrantSubject(g.Subject),
		UserID:      NullIfZero(g.UserID),
		RoleID:      h.svc.V2RoleID(g.Origin, g.RoleID),
		State:       NullIfZero(g.State),
		Origin:      g.Origin,
		CreatedBy:   NullIfZero(g.CreatedBy),
		UpdatedBy:   NullIfZero(g.UpdatedBy),
		CreatedTime: Instant(g.CreatedTime),
		UpdatedTime: Instant(g.UpdatedTime),
		User:        toGrantUser(g.User),
	}
}

func toGrantUser(u *entplatform.GrantUser) *GrantUser {
	if u == nil {
		return nil
	}
	return &GrantUser{Email: u.Email, EmailShared: u.EmailShared, Sub: u.Sub, Issuer: u.Issuer}
}

func toGrantSubject(s *entplatform.GrantSubject) *GrantSubject {
	if s == nil {
		return nil
	}
	return &GrantSubject{Type: s.Type, Value: s.Value, Issuer: NullIfZero(s.Issuer)}
}
