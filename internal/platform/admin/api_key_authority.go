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
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const authorizationModeDefaultRBAC = "default_rbac"

type ownerScopeResolver interface {
	ResolveOwnerScopes(sub, userID string) (rbac.Resolution, error)
}

type APIKeyAuthority struct {
	keys   *store.APIKeysRepo
	users  *store.UsersRepo
	owners ownerScopeResolver

	authorizationMode string

	lastUsed *lastUsedThrottle
	stamps   lastUsedToucher

	now    func() time.Time
	logger *slog.Logger
}

func NewAPIKeyAuthority(
	keys *store.APIKeysRepo,
	users *store.UsersRepo,
	owners ownerScopeResolver,
	authorizationMode string,
	logger *slog.Logger,
) *APIKeyAuthority {
	return &APIKeyAuthority{
		keys:              keys,
		users:             users,
		owners:            owners,
		authorizationMode: authorizationMode,
		lastUsed:          newLastUsedThrottle(lastUsedWriteThrottle, lastUsedStaleAge),
		stamps:            keys,
		now:               time.Now,
		logger:            withLoggerName(logger, "api-key-authority"),
	}
}

var _ apikey.Authority = (*APIKeyAuthority)(nil)

func (a *APIKeyAuthority) Validate(ctx context.Context, keyHash string) (apikey.Resolution, error) {
	if a.authorizationMode != authorizationModeDefaultRBAC {
		return apikey.Resolution{}, apikey.ErrAuthorizationModeDisabled
	}
	if keyHash == "" {
		return apikey.Resolution{}, apikey.ErrInvalidKey
	}

	key, err := a.keys.GetByKeyHash(ctx, keyHash)
	if err != nil {
		if errors.Is(err, store.ErrAPIKeyNotFound) {
			return a.reject("unknown credential", "")
		}
		return apikey.Resolution{}, fmt.Errorf("%w: look up key: %w", apikey.ErrAuthorityUnavailable, err)
	}

	if key.ExpiresTime != nil && *key.ExpiresTime <= a.now().UnixMilli() {
		return a.reject("expired", key.ID)
	}

	owner, _, err := a.users.GetByID(ctx, key.UserID)
	if err != nil {
		return apikey.Resolution{}, fmt.Errorf("%w: load key owner: %w", apikey.ErrAuthorityUnavailable, err)
	}
	if owner == nil {
		return a.reject("owner row absent", key.ID)
	}
	if reason, ok := ownerIneligibleReason(owner); !ok {
		return a.reject(reason, key.ID)
	}

	selected, err := a.keys.ScopesForKeys(ctx, []string{key.ID})
	if err != nil {
		return apikey.Resolution{}, fmt.Errorf("%w: load key scopes: %w", apikey.ErrAuthorityUnavailable, err)
	}
	ownerResolution, err := a.owners.ResolveOwnerScopes(owner.Sub, owner.ID)
	if err != nil {
		return apikey.Resolution{}, fmt.Errorf("%w: resolve owner scopes: %w", apikey.ErrAuthorityUnavailable, err)
	}

	surviving := survivingScopes(ownerResolution.Scopes, selected[key.ID])
	if len(surviving) == 0 {
		return a.reject("no selected scope is still covered by the owner", key.ID)
	}
	if dropped := len(selected[key.ID]) - len(surviving); dropped > 0 {
		a.logger.Debug("api key scopes narrowed by owner roles",
			"credentialID", key.ID, "selected", len(selected[key.ID]), "surviving", len(surviving))
	}

	a.stampLastUsed(ctx, key.ID)

	return apikey.Resolution{
		KeyID:    key.ID,
		OwnerID:  owner.ID,
		OwnerSub: owner.Sub,
		Scopes:   surviving,
	}, nil
}

const reasonReservedBuiltIn = "owner is a reserved built-in service account"

func ownerIneligibleReason(owner *store.User) (string, bool) {
	switch {
	case !owner.Active:
		return "owner is inactive", false
	case owner.ProvisionedVia != rbac.ProvisionedViaSystem:
		return "owner is not a service account", false
	case owner.Issuer != rbac.SystemUserIssuer:
		return "owner issuer is not the system issuer", false
	case !rbac.IsSystemPrincipal(owner.Sub):
		return "owner subject lacks the system prefix", false
	case rbac.IsReservedSystemUserSub(owner.Sub):
		return reasonReservedBuiltIn, false
	}
	return "", true
}

func survivingScopes(ownerScopes, selected []string) []string {
	if len(selected) == 0 {
		return nil
	}
	uncovered := rbac.UnsatisfiedScopes(ownerScopes, selected)
	if len(uncovered) == 0 {
		return selected
	}
	dropped := make(map[string]struct{}, len(uncovered))
	for _, scope := range uncovered {
		dropped[scope] = struct{}{}
	}
	surviving := make([]string, 0, len(selected))
	for _, scope := range selected {
		if _, gone := dropped[scope]; !gone {
			surviving = append(surviving, scope)
		}
	}
	return surviving
}

func (a *APIKeyAuthority) reject(reason, keyID string) (apikey.Resolution, error) {
	if keyID == "" {
		a.logger.Warn("api key rejected", "reason", reason)
	} else {
		a.logger.Warn("api key rejected", "reason", reason, "credentialID", keyID)
	}
	return apikey.Resolution{}, apikey.ErrInvalidKey
}
