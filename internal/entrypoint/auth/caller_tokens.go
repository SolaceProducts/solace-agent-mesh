// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"fmt"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

const (
	AuthMethodSAMAccessToken = "sam_access_token"
	AuthMethodOIDC           = "oidc"
)

func ForSAMAccessToken(rawToken string, verifier TokenVerifier, scopes ScopeExpander) (*Subject, error) {
	if verifier == nil {
		return nil, ErrNoTokenVerifier
	}
	claims, err := verifier.Verify(rawToken)
	if err != nil {
		return nil, fmt.Errorf("%w: sam access token: %w", ErrCredentialRejected, err)
	}
	return identityFromVerifiedClaims(claims, scopes), nil
}

func identityFromVerifiedClaims(claims TokenClaims, scopes ScopeExpander) *Subject {
	id := &Subject{
		fullIdentity:   true,
		userID:         claims.UserID,
		email:          claims.Email,
		name:           claims.Name,
		authenticated:  true,
		authMethod:     AuthMethodSAMAccessToken,
		roles:          slices.Clone(claims.Roles),
		scopes:         slices.Clone(claims.Scopes),
		platformUserID: claims.PlatformUserID,
	}
	if len(id.scopes) == 0 && scopes != nil {
		id.scopes = slices.Clone(scopes.ResolveScopes(claims.UserID, claims.Roles))
	}
	return id
}

func ForIDPAccessToken(ctx context.Context, ext ExternalID, email, name string, gatewayContext map[string]any, lookups EntitlementLookup) (*Subject, error) {
	if err := validateExternal(ext); err != nil {
		return nil, err
	}
	if lookups == nil {
		return nil, ErrNoEntitlementLookup
	}
	ent, err := lookups.Lookup(ctx, ext.Value, gatewayContext)
	if err != nil {
		return nil, fmt.Errorf("resolve caller: idp authz lookup: %w", err)
	}
	return &Subject{
		fullIdentity:     true,
		userID:           ext.Value,
		email:            email,
		name:             name,
		authenticated:    true,
		authMethod:       AuthMethodOIDC,
		roles:            slices.Clone(ent.Roles),
		scopes:           slices.Clone(ent.Scopes),
		profile:          ent.Profile,
		enrichmentStatus: ent.EnrichmentStatus,
		enrichmentReason: ent.EnrichmentReason,
		platformUserID:   ent.PlatformUserID,
	}, nil
}

func ForAPIKey(resolution apikey.Resolution) *Subject {
	return &Subject{
		fullIdentity:     true,
		userID:           resolution.OwnerSub,
		name:             resolution.OwnerSub,
		authenticated:    true,
		authMethod:       APIKeyAuthMethod,
		credentialID:     resolution.KeyID,
		platformUserID:   resolution.OwnerID,
		scopes:           slices.Clone(resolution.Scopes),
		scopesResolved:   true,
		enrichmentStatus: identity.EnrichmentNotConfigured,
		enrichmentReason: "machine credential has no profile",
	}
}
