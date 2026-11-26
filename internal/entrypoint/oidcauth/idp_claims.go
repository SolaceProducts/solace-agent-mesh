// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

type IdpClaimsProvider struct {
	oidcProvider string
	claimKey     string
	mappings     map[string][]string
}

func NewIdpClaimsProvider(cfg IDPClaimsConfig) *IdpClaimsProvider {
	return &IdpClaimsProvider{
		oidcProvider: cfg.OIDCProvider,
		claimKey:     cfg.ClaimKey,
		mappings:     cfg.Mappings,
	}
}

func (p *IdpClaimsProvider) GetUserRoles(_ string, ctx map[string]any) ([]string, error) {
	if ctx == nil {
		return nil, nil
	}

	provider, _ := ctx["oidc_provider"].(string)
	if provider != p.oidcProvider {
		return nil, nil
	}

	idpClaims, ok := ctx["idp_claims"].(map[string]any)
	if !ok {
		return nil, nil
	}

	claimValues := extractClaimValues(idpClaims, p.claimKey)
	if len(claimValues) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool)
	var roles []string
	for _, val := range claimValues {
		if mapped, ok := p.mappings[val]; ok {
			for _, role := range mapped {
				if !seen[role] {
					seen[role] = true
					roles = append(roles, role)
				}
			}
		}
	}

	return roles, nil
}

func extractClaimValues(claims map[string]any, key string) []string {
	val := claims[key]
	if val == nil {
		return nil
	}

	switch v := val.(type) {
	case string:
		return []string{v}
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return v
	}

	return nil
}

var _ RoleProvider = (*IdpClaimsProvider)(nil)
