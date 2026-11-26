// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

const evalGenerationTokenTTL = 10 * time.Minute

type userByIDResolver interface {
	ResolveByID(ctx context.Context, id string) (identityResolution, error)
}

func (h *AuthzLookupHandler) ResolveUserScopes(ctx context.Context, userID, platformUserID string) (rbac.Resolution, error) {
	if h == nil || h.composer == nil {
		return rbac.Resolution{}, errors.New("authz lookup: composer unavailable")
	}
	if platformUserID == "" {
		return rbac.Resolution{}, errors.New("authz lookup: the user has no platform user id")
	}
	byID, ok := h.userResolver.(userByIDResolver)
	if !ok {
		return rbac.Resolution{}, errors.New("authz lookup: no user store to resolve the user from")
	}
	resolved, err := byID.ResolveByID(ctx, platformUserID)
	if err != nil {
		return rbac.Resolution{}, fmt.Errorf("authz lookup: resolve user %s: %w", platformUserID, err)
	}
	if resolved.Outcome != identity.LookupMatched {
		return rbac.Resolution{}, fmt.Errorf("authz lookup: user %s not resolved: %s %s", platformUserID, resolved.Outcome, resolved.RefusalReason)
	}
	resolution, err := h.composer.Lookup(userID, "", resolved.Context)
	if err != nil {
		return rbac.Resolution{}, fmt.Errorf("authz lookup: resolve user scopes: %w", err)
	}
	return resolution, nil
}

type evalGenerationAuth struct {
	signer *trust.Signer
	lookup *AuthzLookupHandler
}

func NewEvalGenerationAuth(signer *trust.Signer, lookup *AuthzLookupHandler) eval.GenerationAuth {
	return &evalGenerationAuth{signer: signer, lookup: lookup}
}

func (a *evalGenerationAuth) AuthHeader(ctx context.Context, userID, platformUserID string) (string, error) {
	if a.signer == nil {
		return "", nil
	}
	claims := oidcauth.MintClaims{UserID: userID, PlatformUserID: platformUserID}
	if a.lookup != nil {
		resolution, err := a.lookup.ResolveUserScopes(ctx, userID, platformUserID)
		if err != nil {
			return "", err
		}
		if len(resolution.Scopes) == 0 {
			return "", fmt.Errorf("user %s holds no scopes", platformUserID)
		}
		claims.Roles, claims.Scopes = resolution.Roles, resolution.Scopes
	}
	token, err := oidcauth.MintSAMToken(a.signer, claims, evalGenerationTokenTTL)
	if err != nil {
		return "", err
	}
	return "Bearer " + token, nil
}
