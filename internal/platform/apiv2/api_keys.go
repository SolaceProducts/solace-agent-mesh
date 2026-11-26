// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	apiKeyResourceType = "apiKey"
	apiKeyPath         = "/apiKeys/{id}"
)

type APIKey struct {
	ID           string   `json:"id" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	Type         string   `json:"type" example:"apiKey"`
	UserID       string   `json:"userId" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Label        string   `json:"label" example:"deploy-pipeline"`
	Prefix       string   `json:"prefix" example:"Ab3dE-ex"`
	Scopes       []string `json:"scopes" example:"[\"agent_builder:*:read\"]"`
	Status       string   `json:"status" example:"active"`
	ExpiresTime  Instant  `json:"expiresTime" example:"\"2027-01-02T03:04:05.678Z\""`
	LastUsedTime Instant  `json:"lastUsedTime" example:"\"2026-01-03T10:15:30.000Z\""`
	CreatedBy    string   `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy    string   `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime  Instant  `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime  Instant  `json:"updatedTime" example:"\"2026-01-03T10:15:30.000Z\""`
}

type CreatedAPIKey struct {
	ID           string   `json:"id" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	Type         string   `json:"type" example:"apiKey"`
	UserID       string   `json:"userId" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Label        string   `json:"label" example:"deploy-pipeline"`
	Prefix       string   `json:"prefix" example:"Ab3dE-ex"`
	Secret       string   `json:"secret" example:"samk_Ab3dE-example-secret-not-real"`
	Scopes       []string `json:"scopes" example:"[\"agent_builder:*:read\"]"`
	Status       string   `json:"status" example:"active"`
	ExpiresTime  Instant  `json:"expiresTime" example:"\"2027-01-02T03:04:05.678Z\""`
	LastUsedTime Instant  `json:"lastUsedTime"`
	CreatedBy    string   `json:"createdBy" example:"jane.doe@example.com"`
	UpdatedBy    string   `json:"updatedBy" example:"jane.doe@example.com"`
	CreatedTime  Instant  `json:"createdTime" example:"\"2026-01-02T03:04:05.678Z\""`
	UpdatedTime  Instant  `json:"updatedTime" example:"\"2026-01-02T03:04:05.678Z\""`
}

func (CreatedAPIKey) ResponseHeaders() map[string]string {
	return map[string]string{"Cache-Control": "no-store"}
}

type CreateAPIKeyRequest struct {
	UserID       string   `json:"userId" required:"true" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Label        string   `json:"label" required:"true" example:"deploy-pipeline"`
	Scopes       []string `json:"scopes" required:"true" example:"[\"agent_builder:*:read\"]"`
	ExpiresTime  *Instant `json:"expiresTime" example:"\"2027-01-02T03:04:05.678Z\""`
	NeverExpires bool     `json:"neverExpires" example:"false"`
}

type UpdateAPIKeyRequest struct {
	ID    string `path:"id" json:"-" example:"0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"`
	Label string `json:"label" required:"true" example:"deploy-pipeline"`
}

type listAPIKeysRequest struct {
	Page   Page      `json:"-"`
	Sort   SortOrder `sort:"label,prefix,createdTime,updatedTime,expiresTime,lastUsedTime" json:"-"`
	UserID string    `query:"userId,nonempty" json:"-" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Status string    `query:"status,nonempty" json:"-" example:"active"`
}

type listUserAPIKeysRequest struct {
	UserID string    `path:"userId" json:"-" example:"0190f5e4-8b2c-7c3e-9a1d-2f4b6c8d0e1f"`
	Page   Page      `json:"-"`
	Sort   SortOrder `sort:"label,prefix,createdTime,updatedTime,expiresTime,lastUsedTime" json:"-"`
	Status string    `query:"status,nonempty" json:"-" example:"active"`
}

type apiKeyHandler struct {
	svc *entplatform.APIKeyService
}

func apiKeyRoutes(deps *entplatform.RouteDeps, _ *slog.Logger) []Route {
	if deps.APIKeySvc == nil {
		return nil
	}
	h := &apiKeyHandler{svc: deps.APIKeySvc}
	return []Route{
		{Method: http.MethodGet, Path: "/apiKeys", Gate: GateAPIKeyRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listAPIKeys", Summary: "Get a list of API keys",
				Text: "Returns one page of API keys, optionally only those of one service account or one status.", Tag: apiKeysTag},
			Handler: Handle(h.list, "Unable to list API keys")},
		{Method: http.MethodPost, Path: "/apiKeys", Gate: GateAPIKeyWrite, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "createAPIKey", Summary: "Create an API key",
				Text: "Issues a key for a custom service account. Only this response shows the key's secret. If the owner already holds 10 active keys, the request fails with 409. On a deployment whose authorization mode is not default_rbac, it fails with 403 and a message naming that mode. If authorization state is unavailable, it fails with 503.", Tag: apiKeysTag,
				Errors: []int{http.StatusServiceUnavailable}},
			Handler: Handle(h.create, "Unable to create API key")},
		{Method: http.MethodGet, Path: apiKeyPath, Gate: GateAPIKeyRead, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "getAPIKey", Summary: "Get an API key",
				Text: "Returns one API key without its secret.", Tag: apiKeysTag},
			Handler: Handle(h.get, "Unable to get API key")},
		{Method: http.MethodPut, Path: apiKeyPath, Gate: GateAPIKeyWrite, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "replaceAPIKey", Summary: "Replace an API key",
				Text: "Renames a key. The label is the only field that can be replaced. The scopes and expiry of a key are fixed when it is issued.", Tag: apiKeysTag},
			Handler: Handle(h.update, "Unable to update API key")},
		{Method: http.MethodDelete, Path: apiKeyPath, Gate: GateAPIKeyWrite, AllowAPIKey: true, Published: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "deleteAPIKey", Summary: "Delete an API key",
				Text: "Revokes a key immediately. A task the key already submitted keeps running.", Tag: apiKeysTag},
			Handler: Handle(h.delete, "Unable to delete API key")},
		{Method: http.MethodGet, Path: "/users/{userId}/apiKeys", Gate: GateAPIKeyRead, AllowAPIKey: true,
			Stability: StabilityStable,
			Doc: Doc{ID: "listUserAPIKeys", Summary: "Get a list of a user's API keys",
				Text: "Returns one page of the API keys one service account owns: the same list as GET /apiKeys with userId set.", Tag: usersTag},
			Handler: Handle(h.listForUser, "Unable to list API keys")},
	}
}

func (h *apiKeyHandler) list(ctx context.Context, req listAPIKeysRequest) (List[APIKey], error) {
	if err := CheckUUIDQuery("userId", req.UserID); err != nil {
		return List[APIKey]{}, err
	}
	return h.page(ctx, req.UserID, req.Status, req.Page, req.Sort)
}

func (h *apiKeyHandler) listForUser(ctx context.Context, req listUserAPIKeysRequest) (List[APIKey], error) {
	return h.page(ctx, req.UserID, req.Status, req.Page, req.Sort)
}

func (h *apiKeyHandler) page(ctx context.Context, userID, status string, p Page, sort SortOrder) (List[APIKey], error) {
	switch status {
	case "", store.APIKeyStatusActive, store.APIKeyStatusExpired:
	default:
		return List[APIKey]{}, &CallerError{Status: http.StatusUnprocessableEntity, Message: msgInvalidQueryParam,
			Details: map[string][]string{"status": {"must be " + store.APIKeyStatusActive + " or " + store.APIKeyStatusExpired}}}
	}
	query := entplatform.APIKeyListQuery{
		PageNumber: p.Number, PageSize: p.Size, SortField: sort.Field, UserID: userID, Status: status,
	}
	if sort.Field != "" {
		query.SortOrder = "asc"
		if sort.Desc {
			query.SortOrder = "desc"
		}
	}
	resp, err := h.svc.List(ctx, query)
	if err != nil {
		return List[APIKey]{}, err
	}
	return ListOf(resp, toAPIKey), nil
}

func (h *apiKeyHandler) create(ctx context.Context, req CreateAPIKeyRequest) (CreatedAPIKey, error) {
	var expires *entplatform.RESTTimestamp
	if req.ExpiresTime != nil {
		expires = new(entplatform.RESTTimestamp(*req.ExpiresTime))
	}
	var actorScopes []string
	if identity, ok := auth.SubjectFromContext(ctx); ok && identity != nil {
		actorScopes = identity.Scopes()
	}
	resp, err := h.svc.Create(ctx, entplatform.CreateAPIKeyRequest{
		UserID: req.UserID, Label: req.Label, Scopes: req.Scopes,
		ExpiresTime: expires, NeverExpires: req.NeverExpires,
	}, entplatform.UserIDFromContext(ctx), actorScopes)
	if err != nil {
		return CreatedAPIKey{}, keepModeRefusal(err)
	}
	return toCreatedAPIKey(resp), nil
}

func keepModeRefusal(err error) error {
	fe, ok := entplatform.APIKeyModeRefusal(err)
	if !ok {
		return err
	}
	return &CallerError{Status: http.StatusForbidden, Message: fe.Message, Cause: err}
}

func (h *apiKeyHandler) get(ctx context.Context, req IDRequest) (APIKey, error) {
	resp, err := h.svc.Get(ctx, req.ID)
	if err != nil {
		return APIKey{}, err
	}
	return toAPIKey(resp), nil
}

func (h *apiKeyHandler) update(ctx context.Context, req UpdateAPIKeyRequest) (APIKey, error) {
	resp, err := h.svc.UpdateLabel(ctx, req.ID, entplatform.UpdateAPIKeyRequest{Label: req.Label}, entplatform.UserIDFromContext(ctx))
	if err != nil {
		return APIKey{}, err
	}
	return toAPIKey(resp), nil
}

func (h *apiKeyHandler) delete(ctx context.Context, req IDRequest) (struct{}, error) {
	if err := h.svc.Delete(ctx, req.ID, entplatform.UserIDFromContext(ctx)); err != nil {
		return struct{}{}, err
	}
	return struct{}{}, nil
}

func toAPIKey(resp *entplatform.APIKeyResponse) APIKey {
	return APIKey{
		ID:           resp.ID,
		Type:         apiKeyResourceType,
		UserID:       resp.UserID,
		Label:        resp.Label,
		Prefix:       resp.Prefix,
		Scopes:       nonNilList(resp.Scopes),
		Status:       resp.Status,
		ExpiresTime:  InstantOf(resp.ExpiresTime),
		LastUsedTime: InstantOf(resp.LastUsedTime),
		CreatedBy:    resp.CreatedBy,
		UpdatedBy:    resp.UpdatedBy,
		CreatedTime:  Instant(resp.CreatedTime),
		UpdatedTime:  Instant(resp.UpdatedTime),
	}
}

func toCreatedAPIKey(resp *entplatform.APIKeyResponse) CreatedAPIKey {
	key := toAPIKey(resp)
	return CreatedAPIKey{
		ID: key.ID, Type: key.Type, UserID: key.UserID, Label: key.Label, Prefix: key.Prefix,
		Secret: resp.Secret, Scopes: key.Scopes, Status: key.Status,
		ExpiresTime: key.ExpiresTime, LastUsedTime: key.LastUsedTime,
		CreatedBy: key.CreatedBy, UpdatedBy: key.UpdatedBy,
		CreatedTime: key.CreatedTime, UpdatedTime: key.UpdatedTime,
	}
}
