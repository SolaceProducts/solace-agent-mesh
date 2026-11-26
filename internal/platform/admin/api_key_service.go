// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const maxActiveKeysPerOwner = 10

const maxAPIKeyLabelLen = 255

const resourceAPIKey = "API key"

var apiKeySortColumns = map[string]string{
	"label":        "label",
	"prefix":       "prefix",
	"createdTime":  "created_time",
	"updatedTime":  "updated_time",
	"expiresTime":  "expires_time",
	"lastUsedTime": "last_used_time",
}

type APIKeyService struct {
	keys   *store.APIKeysRepo
	users  *store.UsersRepo
	owners ownerScopeResolver

	authorizationMode string

	maxActiveKeys int
	now           func() time.Time
	logger        *slog.Logger
}

func NewAPIKeyService(keys *store.APIKeysRepo, users *store.UsersRepo, owners ownerScopeResolver, authorizationMode string, logger *slog.Logger) *APIKeyService {
	return &APIKeyService{
		keys:              keys,
		users:             users,
		owners:            owners,
		authorizationMode: authorizationMode,
		maxActiveKeys:     maxActiveKeysPerOwner,
		now:               time.Now,
		logger:            withLoggerName(logger, "api-key-service"),
	}
}

const apiKeysNeedDefaultRBAC = "API keys require the default_rbac authorization mode; this deployment does not run it"

func APIKeyModeRefusal(err error) (*ForbiddenError, bool) {
	fe, ok := errors.AsType[*ForbiddenError](err)
	if !ok || fe.Message != apiKeysNeedDefaultRBAC {
		return nil, false
	}
	return fe, true
}

func (s *APIKeyService) Create(ctx context.Context, req CreateAPIKeyRequest, actor string, actorScopes []string) (*APIKeyResponse, error) {
	if s.authorizationMode != authorizationModeDefaultRBAC {
		return nil, &ForbiddenError{Message: apiKeysNeedDefaultRBAC}
	}

	label := strings.TrimSpace(req.Label)
	scopes := normalizeScopes(req.Scopes)

	details := map[string][]string{}
	if labelDetail := validateAPIKeyLabel(label); labelDetail != "" {
		details["label"] = []string{labelDetail}
	}
	if req.UserID == "" {
		details["userId"] = []string{"userId is required"}
	} else if err := validatePathUUID(req.UserID); err != nil {
		details["userId"] = []string{"userId must be a UUID"}
	}
	if len(scopes) == 0 {
		details["scopes"] = []string{"at least one scope is required"}
	} else if scopeDetails, ok := validateAPIKeyScopes(scopes); !ok {
		details["scopes"] = scopeDetails
	}
	now := s.now()
	expiresTime, expiryDetail := resolveRequestedExpiry(req, now)
	if expiryDetail != "" {
		details["expiresTime"] = []string{expiryDetail}
	}
	if len(details) > 0 {
		return nil, invalidAPIKeyRequest(details)
	}

	owner, ownerErr := s.loadEligibleOwner(ctx, req.UserID)
	if ownerErr != nil {
		return nil, ownerErr
	}

	if regrantErr := (regrantCheck{
		logger: withAuditCredential(ctx, s.logger), actor: actor, actorScopes: actorScopes,
		resource: auditResourceAPIKey,
	}).enforce(nil, scopes); regrantErr != nil {
		return nil, regrantErr
	}

	resolution, err := s.owners.ResolveOwnerScopes(owner.Sub, owner.ID)
	if err != nil {
		s.logger.Error("resolve owner scopes for api key creation", "error", err, "ownerId", owner.ID)
		return nil, &UnavailableError{Message: "Authorization state is unavailable on this instance"}
	}
	if uncovered := rbac.UnsatisfiedScopes(resolution.Scopes, scopes); len(uncovered) > 0 {
		return nil, &ValidationError{
			Message: "The owning service account does not hold every requested scope",
			Details: map[string][]string{"scopes": uncoveredScopeMessages(uncovered)},
		}
	}

	plaintext, err := apikey.Generate()
	if err != nil {
		return nil, fmt.Errorf("generate api key: %w", err)
	}

	created, err := s.keys.Create(ctx, store.APIKey{
		UserID:      owner.ID,
		Label:       label,
		KeyHash:     apikey.Hash(plaintext),
		Prefix:      apikey.DisplayPrefix(plaintext),
		ExpiresTime: expiresTime,
		CreatedBy:   actor,
		UpdatedBy:   actor,
	}, scopes, s.maxActiveKeys, now.UnixMilli())
	if err != nil {
		if errors.Is(err, store.ErrAPIKeyCapReached) {
			return nil, &ConflictError{Message: fmt.Sprintf(
				"This service account already holds the maximum of %d active API keys; delete one or let it expire before creating another",
				s.maxActiveKeys)}
		}
		return nil, fmt.Errorf("create api key: %w", err)
	}

	createdAttrs := []any{"ownerId", owner.ID, "prefix", created.Prefix, "scopes", scopes, "neverExpires", expiresTime == nil}
	if expiresTime != nil {
		createdAttrs = append(createdAttrs, "expiresTime", restTimestamp(*expiresTime).String())
	}
	logRBACAudit(withAuditCredential(ctx, s.logger), auditActionCreated, auditResourceAPIKey, created.ID, actor, createdAttrs...)

	resp := apiKeyToResponse(created, scopes, now.UnixMilli())
	resp.Secret = plaintext
	return resp, nil
}

func (s *APIKeyService) Get(ctx context.Context, id string) (*APIKeyResponse, error) {
	key, err := s.keys.GetByID(ctx, id)
	if err != nil {
		return nil, mapAPIKeyRepoError(err, id)
	}
	scopes, err := s.scopesForKeys(ctx, []string{key.ID})
	if err != nil {
		return nil, err
	}
	return apiKeyToResponse(key, scopes[key.ID], s.now().UnixMilli()), nil
}

type APIKeyListQuery struct {
	PageNumber int
	PageSize   int
	SortField  string
	SortOrder  string
	UserID     string
	Status     string
}

func (s *APIKeyService) List(ctx context.Context, query APIKeyListQuery) (*PaginatedResponse[APIKeyResponse], error) {
	sortColumn, sortOrder := "", ""
	if query.SortField != "" {
		column, ok := apiKeySortColumns[query.SortField]
		if !ok {
			return nil, &ValidationError{Message: fmt.Sprintf("sort field %q is not supported for API keys", query.SortField)}
		}
		sortColumn, sortOrder = column, query.SortOrder
	}
	switch query.Status {
	case "", store.APIKeyStatusActive, store.APIKeyStatusExpired:
	default:
		return nil, &ValidationError{Message: fmt.Sprintf("status must be %q or %q", store.APIKeyStatusActive, store.APIKeyStatusExpired)}
	}
	if query.UserID != "" {
		if err := validatePathUUID(query.UserID); err != nil {
			return nil, &ValidationError{Message: "userId must be a UUID"}
		}
	}

	now := s.now().UnixMilli()
	keys, total, err := s.keys.ListPaginated(ctx, store.APIKeyListOptions{
		PageNumber: query.PageNumber,
		PageSize:   query.PageSize,
		SortColumn: sortColumn,
		SortOrder:  sortOrder,
		UserID:     query.UserID,
		Status:     query.Status,
		Now:        now,
	})
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}

	ids := make([]string, 0, len(keys))
	for _, key := range keys {
		ids = append(ids, key.ID)
	}
	scopes, err := s.scopesForKeys(ctx, ids)
	if err != nil {
		return nil, err
	}

	data := make([]APIKeyResponse, 0, len(keys))
	for _, key := range keys {
		data = append(data, *apiKeyToResponse(key, scopes[key.ID], now))
	}
	return &PaginatedResponse[APIKeyResponse]{
		Data: data,
		Meta: NewPaginationMeta(query.PageNumber, query.PageSize, total),
	}, nil
}

func (s *APIKeyService) UpdateLabel(ctx context.Context, id string, req UpdateAPIKeyRequest, actor string) (*APIKeyResponse, error) {
	label := strings.TrimSpace(req.Label)
	if labelDetail := validateAPIKeyLabel(label); labelDetail != "" {
		return nil, invalidAPIKeyRequest(map[string][]string{"label": {labelDetail}})
	}

	updated, err := s.keys.UpdateLabel(ctx, id, label, actor)
	if err != nil {
		return nil, mapAPIKeyRepoError(err, id)
	}
	logRBACAudit(withAuditCredential(ctx, s.logger), auditActionUpdated, auditResourceAPIKey, updated.ID, actor,
		"ownerId", updated.UserID, "prefix", updated.Prefix)

	scopes, err := s.scopesForKeys(ctx, []string{updated.ID})
	if err != nil {
		return nil, err
	}
	return apiKeyToResponse(updated, scopes[updated.ID], s.now().UnixMilli()), nil
}

func (s *APIKeyService) Delete(ctx context.Context, id, actor string) error {
	key, err := s.keys.GetByID(ctx, id)
	if err != nil {
		return mapAPIKeyRepoError(err, id)
	}
	if err := s.keys.Delete(ctx, id); err != nil {
		return mapAPIKeyRepoError(err, id)
	}
	logRBACAudit(withAuditCredential(ctx, s.logger), auditActionDeleted, auditResourceAPIKey, id, actor,
		"ownerId", key.UserID, "prefix", key.Prefix)
	return nil
}

func (s *APIKeyService) loadEligibleOwner(ctx context.Context, userID string) (*store.User, error) {
	if userID == rbac.DefaultSystemUserID {
		return nil, ineligibleOwner(reasonReservedBuiltIn)
	}
	owner, _, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load key owner: %w", err)
	}
	if owner == nil {
		return nil, invalidAPIKeyRequest(map[string][]string{
			"userId": {"no user with this id exists"},
		})
	}
	if reason, ok := ownerIneligibleReason(owner); !ok {
		return nil, ineligibleOwner(reason)
	}
	return owner, nil
}

func ineligibleOwner(reason string) error {
	return invalidAPIKeyRequest(map[string][]string{
		"userId": {"API keys can only be issued to a custom service account: " + reason},
	})
}

func resolveRequestedExpiry(req CreateAPIKeyRequest, now time.Time) (*int64, string) {
	switch {
	case req.NeverExpires && req.ExpiresTime != nil:
		return nil, "supply either expiresTime or neverExpires, not both"
	case req.NeverExpires:
		return nil, ""
	case req.ExpiresTime == nil:
		return nil, "expiresTime is required; send neverExpires: true to issue a key that does not expire"
	}
	ms := int64(*req.ExpiresTime)
	if ms <= now.UnixMilli() {
		return nil, "expiresTime must be in the future"
	}
	return &ms, ""
}

func validateAPIKeyScopes(scopes []string) ([]string, bool) {
	var msgs []string
	if patternDetails, ok := rbac.ValidateScopes(scopes); !ok {
		msgs = append(msgs, patternDetails["scopes"]...)
	}
	if shapeDetails, ok := rbac.ValidateScopeShape(scopes); !ok {
		msgs = append(msgs, shapeDetails["scopes"]...)
	}
	return msgs, len(msgs) == 0
}

func normalizeScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		trimmed := rbac.CanonicalUniversalScope(strings.TrimSpace(scope))
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	slices.Sort(out)
	return out
}

func uncoveredScopeMessages(uncovered []string) []string {
	msgs := make([]string, 0, len(uncovered))
	for _, scope := range uncovered {
		msgs = append(msgs, fmt.Sprintf("the owning service account does not hold %q", scope))
	}
	sort.Strings(msgs)
	return msgs
}

func apiKeyStatus(key *store.APIKey, now int64) string {
	if key.ExpiresTime != nil && *key.ExpiresTime <= now {
		return store.APIKeyStatusExpired
	}
	return store.APIKeyStatusActive
}

func apiKeyToResponse(key *store.APIKey, scopes []string, now int64) *APIKeyResponse {
	return &APIKeyResponse{
		ID:           key.ID,
		Type:         apiKeyResourceType,
		UserID:       key.UserID,
		Label:        key.Label,
		Prefix:       key.Prefix,
		Scopes:       apicontract.NonNilList(scopes),
		Status:       apiKeyStatus(key, now),
		ExpiresTime:  nullableRESTTimestamp(key.ExpiresTime),
		LastUsedTime: nullableRESTTimestamp(key.LastUsedTime),
		CreatedBy:    key.CreatedBy,
		CreatedTime:  restTimestamp(key.CreatedTime),
		UpdatedBy:    key.UpdatedBy,
		UpdatedTime:  restTimestamp(key.UpdatedTime),
	}
}

func mapAPIKeyRepoError(err error, id string) error {
	if errors.Is(err, store.ErrAPIKeyNotFound) {
		return &NotFoundError{Resource: resourceAPIKey, ID: id}
	}
	return err
}

func invalidAPIKeyRequest(details map[string][]string) *ValidationError {
	return &ValidationError{Message: "invalid API key request", Details: details}
}

func validateAPIKeyLabel(label string) string {
	switch {
	case label == "":
		return "label is required"
	case utf8.RuneCountInString(label) > maxAPIKeyLabelLen:
		return fmt.Sprintf("label must be at most %d characters", maxAPIKeyLabelLen)
	case !namePattern.MatchString(label):
		return "label must not contain control characters"
	}
	return ""
}

func (s *APIKeyService) scopesForKeys(ctx context.Context, ids []string) (map[string][]string, error) {
	scopes, err := s.keys.ScopesForKeys(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load api key scopes: %w", err)
	}
	return scopes, nil
}
