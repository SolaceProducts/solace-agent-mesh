// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type IdentityResolver struct {
	authz        *AuthorizationService
	profileCache *auth.ProfileCache
}

func NewIdentityResolver(authz *AuthorizationService, profileCache *auth.ProfileCache) *IdentityResolver {
	return &IdentityResolver{authz: authz, profileCache: profileCache}
}

func (r *IdentityResolver) ResolveIdentity(ctx context.Context, userID string) (*auth.Subject, error) {
	identity, err := auth.ForScheduledTaskOwner(ctx, userID, lookupFor(r.authz))
	switch {
	case err == nil:
	case auth.IsScheduledOwnerRefusal(err):
		return nil, fmt.Errorf("%w: %w", entrypoint.ErrIdentityUnauthorized, err)
	default:
		return nil, fmt.Errorf("resolve scheduled task owner: %w", err)
	}
	if auth.ShouldCacheEnrichment(identity.EnrichmentStatus()) {
		if r.profileCache != nil {
			r.profileCache.Put(userID, auth.CachedProfile{
				Profile:          identity.Profile(),
				EnrichmentStatus: identity.EnrichmentStatus(),
				EnrichmentReason: identity.EnrichmentReason(),
			})
		}
	} else {
		r.authz.logger.Warn("profile enrichment degraded",
			"userID", userID,
			"enrichmentStatus", string(identity.EnrichmentStatus()),
			"reason", identity.EnrichmentReason(),
			"authMethod", auth.AuthMethodScheduledTask,
		)
	}
	return identity, nil
}

func (r *IdentityResolver) ResolveRunAs(ctx context.Context, principal string) (*auth.Subject, error) {
	identity, err := auth.ForScheduledRunAs(ctx, principal, lookupFor(r.authz))
	switch {
	case err == nil:
		return identity, nil
	case auth.IsScheduledRunAsRefusal(err):
		return nil, fmt.Errorf("%w: %w", entrypoint.ErrIdentityUnauthorized, err)
	default:
		return nil, fmt.Errorf("resolve run-as service account: %w", err)
	}
}

var (
	_ entrypoint.IdentityResolver = (*IdentityResolver)(nil)
	_ entrypoint.RunAsResolver    = (*IdentityResolver)(nil)
)
