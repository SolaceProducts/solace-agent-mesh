// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"testing"
)

func TestIdpClaimsProvider_BasicMapping(t *testing.T) {
	p := NewIdpClaimsProvider(IDPClaimsConfig{
		OIDCProvider: "azure",
		ClaimKey:     "groups",
		Mappings: map[string][]string{
			"group-uuid-1": {"admin"},
			"group-uuid-2": {"editor", "viewer"},
		},
	})

	ctx := map[string]any{
		"oidc_provider": "azure",
		"idp_claims": map[string]any{
			"groups": []any{"group-uuid-1", "group-uuid-2"},
		},
	}

	roles, err := p.GetUserRoles("alice", ctx)
	if err != nil {
		t.Fatalf("GetUserRoles: %v", err)
	}

	found := map[string]bool{}
	for _, r := range roles {
		found[r] = true
	}
	if !found["admin"] || !found["editor"] || !found["viewer"] {
		t.Errorf("expected [admin, editor, viewer], got %v", roles)
	}
}

func TestIdpClaimsProvider_ProviderMismatch(t *testing.T) {
	p := NewIdpClaimsProvider(IDPClaimsConfig{
		OIDCProvider: "azure",
		ClaimKey:     "groups",
		Mappings:     map[string][]string{"g1": {"admin"}},
	})

	ctx := map[string]any{
		"oidc_provider": "okta",
		"idp_claims": map[string]any{
			"groups": []any{"g1"},
		},
	}

	roles, err := p.GetUserRoles("alice", ctx)
	if err != nil {
		t.Fatalf("GetUserRoles: %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("provider mismatch should return no roles, got %v", roles)
	}
}

func TestIdpClaimsProvider_NilContext(t *testing.T) {
	p := NewIdpClaimsProvider(IDPClaimsConfig{
		OIDCProvider: "azure",
		ClaimKey:     "groups",
	})

	roles, err := p.GetUserRoles("alice", nil)
	if err != nil {
		t.Fatalf("GetUserRoles: %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("nil context should return no roles, got %v", roles)
	}
}

func TestIdpClaimsProvider_MissingClaims(t *testing.T) {
	p := NewIdpClaimsProvider(IDPClaimsConfig{
		OIDCProvider: "azure",
		ClaimKey:     "groups",
		Mappings:     map[string][]string{"g1": {"admin"}},
	})

	ctx := map[string]any{
		"oidc_provider": "azure",
		"idp_claims":    map[string]any{},
	}

	roles, err := p.GetUserRoles("alice", ctx)
	if err != nil {
		t.Fatalf("GetUserRoles: %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("missing claim should return no roles, got %v", roles)
	}
}

func TestIdpClaimsProvider_StringClaim(t *testing.T) {
	p := NewIdpClaimsProvider(IDPClaimsConfig{
		OIDCProvider: "azure",
		ClaimKey:     "role",
		Mappings: map[string][]string{
			"dev-team": {"developer"},
		},
	})

	ctx := map[string]any{
		"oidc_provider": "azure",
		"idp_claims": map[string]any{
			"role": "dev-team",
		},
	}

	roles, err := p.GetUserRoles("alice", ctx)
	if err != nil {
		t.Fatalf("GetUserRoles: %v", err)
	}
	if len(roles) != 1 || roles[0] != "developer" {
		t.Errorf("expected [developer], got %v", roles)
	}
}

func TestIdpClaimsProvider_UnmappedClaims(t *testing.T) {
	p := NewIdpClaimsProvider(IDPClaimsConfig{
		OIDCProvider: "azure",
		ClaimKey:     "groups",
		Mappings: map[string][]string{
			"known-group": {"admin"},
		},
	})

	ctx := map[string]any{
		"oidc_provider": "azure",
		"idp_claims": map[string]any{
			"groups": []any{"unknown-group"},
		},
	}

	roles, err := p.GetUserRoles("alice", ctx)
	if err != nil {
		t.Fatalf("GetUserRoles: %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("unmapped claim should return no roles, got %v", roles)
	}
}

func TestIdpClaimsProvider_DeduplicatesRoles(t *testing.T) {
	p := NewIdpClaimsProvider(IDPClaimsConfig{
		OIDCProvider: "azure",
		ClaimKey:     "groups",
		Mappings: map[string][]string{
			"g1": {"admin", "viewer"},
			"g2": {"admin", "editor"},
		},
	})

	ctx := map[string]any{
		"oidc_provider": "azure",
		"idp_claims": map[string]any{
			"groups": []any{"g1", "g2"},
		},
	}

	roles, err := p.GetUserRoles("alice", ctx)
	if err != nil {
		t.Fatalf("GetUserRoles: %v", err)
	}

	if len(roles) != 3 {
		t.Errorf("expected 3 deduplicated roles, got %v", roles)
	}
}
