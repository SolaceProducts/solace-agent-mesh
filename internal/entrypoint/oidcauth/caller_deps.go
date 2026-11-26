// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type samTokenVerifier struct{ signer *trust.Signer }

func (v samTokenVerifier) Verify(rawToken string) (auth.TokenClaims, error) {
	claims, err := ValidateSAMToken(rawToken, v.signer)
	if err != nil {
		return auth.TokenClaims{}, err
	}
	return auth.TokenClaims{
		UserID:         claims.Subject,
		Email:          claims.Email,
		Name:           claims.Name,
		Roles:          claims.Roles,
		Scopes:         claims.Scopes,
		PlatformUserID: claims.PlatformUserID,
	}, nil
}

type scopeExpander struct{ svc *AuthorizationService }

func (e scopeExpander) ResolveScopes(userID string, roles []string) []string {
	return e.svc.ResolveScopes(userID, roles, nil)
}

func NewScopeExpander(svc *AuthorizationService) auth.ScopeExpander {
	if svc == nil {
		return nil
	}
	return scopeExpander{svc}
}

type entitlementLookup struct{ svc *AuthorizationService }

func (l entitlementLookup) Lookup(ctx context.Context, userID string, gatewayContext map[string]any) (auth.Entitlement, error) {
	res, err := l.svc.Lookup(ctx, userID, gatewayContext)
	if err != nil {
		return auth.Entitlement{}, err
	}
	return auth.Entitlement{
		Roles:            res.Roles,
		Scopes:           res.Scopes,
		Profile:          res.Profile,
		EnrichmentStatus: res.EnrichmentStatus,
		EnrichmentReason: res.EnrichmentReason,
		PlatformUserID:   res.MatchedUserID(),
		Resolution:       res.Resolution,
		RefusalReason:    res.RefusalReason,
		RolesSource:      res.RolesSource,
		SystemUserID:     res.SystemUserID,
		SystemUserRow:    res.SystemUserRow,
	}, nil
}

func lookupFor(svc *AuthorizationService) auth.EntitlementLookup {
	if svc == nil {
		return nil
	}
	return entitlementLookup{svc}
}
