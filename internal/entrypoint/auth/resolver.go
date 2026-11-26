// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"errors"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var (
	ErrInvalidExternalID       = errors.New("resolve caller: invalid external id")
	ErrReservedSystemPrincipal = errors.New("resolve caller: external identity claims a reserved system: principal")
	ErrNoTokenVerifier         = errors.New("resolve caller: no token verifier configured")
	ErrNoEntitlementLookup     = errors.New("resolve caller: no entitlement lookup configured")
	ErrNoEffectiveScopes       = errors.New("resolve caller: user has no effective scopes")
	ErrCredentialRejected      = errors.New("resolve caller: credential rejected")
)

type ScopeExpander interface {
	ResolveScopes(userID string, roles []string) []string
}

type Entitlement struct {
	Roles            []string
	Scopes           []string
	Profile          *identity.Profile
	EnrichmentStatus identity.EnrichmentStatus
	EnrichmentReason string
	PlatformUserID   string
	Resolution       identity.LookupResolution
	RefusalReason    string
	RolesSource      identity.RolesSource
	SystemUserID     string
	SystemUserRow    SystemUserRow
}

type EntitlementLookup interface {
	Lookup(ctx context.Context, userID string, gatewayContext map[string]any) (Entitlement, error)
}

type TokenClaims struct {
	UserID         string
	Email          string
	Name           string
	Roles          []string
	Scopes         []string
	PlatformUserID string
}

type TokenVerifier interface {
	Verify(rawToken string) (TokenClaims, error)
}

func validateExternal(ext ExternalID) error {
	if !ext.valid() {
		return ErrInvalidExternalID
	}
	if rbac.IsSystemPrincipal(ext.Value) {
		return ErrReservedSystemPrincipal
	}
	return nil
}
