// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type Config struct {
	ExternalAuthServiceURL  string
	ExternalAuthProvider    string
	ExternalAuthClaimKey    string
	ExternalAuthCallbackURI string
	FrontendAuthLoginURL    string
	FrontendRedirectURL     string
	FrontendServerURL       string

	OIDC *OIDCConfig

	SessionSecretKey string

	AuthorizationService AuthorizationServiceConfig

	SAMAccessToken SAMAccessTokenConfig

	SyntheticAuth *SyntheticAuthConfig
}

type AuthorizationServiceConfig struct {
	Type string

	RoleToScopeDefinitionsPath string

	UserToRoleAssignmentsPath string

	UserToRoleProvider string

	IDPClaimsConfig *IDPClaimsConfig

	DefaultRoles []string

	MiniIDPMode bool
}

type IDPClaimsConfig struct {
	OIDCProvider string

	ClaimKey string

	Mappings map[string][]string
}

type SAMAccessTokenConfig struct {
	TTLSeconds         int
	IdleTimeoutSeconds int
}

const DefaultIdleTimeoutSeconds = 4 * 60 * 60

const injectedEnterpriseSentinel = "enterprise"

func (c *SAMAccessTokenConfig) TTL() time.Duration {
	ttl := time.Hour
	if c.TTLSeconds > 0 {
		ttl = time.Duration(c.TTLSeconds) * time.Second
	}
	if idle := c.IdleTimeout(); idle > 0 && idle < ttl {
		return idle
	}
	return ttl
}

func (c *SAMAccessTokenConfig) IdleTimeout() time.Duration {
	if c.IdleTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(c.IdleTimeoutSeconds) * time.Second
}

func ParseSAMAccessTokenConfig(appConfig map[string]any) (SAMAccessTokenConfig, error) {
	cfg := SAMAccessTokenConfig{TTLSeconds: 3600, IdleTimeoutSeconds: DefaultIdleTimeoutSeconds}
	satRaw, ok := appConfig["sam_access_token"].(map[string]any)
	if !ok {
		return cfg, nil
	}
	if v := getInt(satRaw, "ttl_seconds"); v > 0 {
		cfg.TTLSeconds = v
	}
	raw, set := satRaw["idle_timeout_seconds"]
	if !set || raw == nil {
		return cfg, nil
	}
	idle, ok := nonNegativeInt(raw)
	if !ok {
		return cfg, fmt.Errorf("sam_access_token.idle_timeout_seconds must be a non-negative integer, got %v", raw)
	}
	cfg.IdleTimeoutSeconds = idle
	return cfg, nil
}

func nonNegativeInt(v any) (int, bool) {
	var n int
	switch x := v.(type) {
	case int:
		n = x
	case int64:
		n = int(x)
	case float64:
		if x != float64(int(x)) {
			return 0, false
		}
		n = int(x)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return 0, false
		}
		n = parsed
	default:
		return 0, false
	}
	return n, n >= 0
}

func ParseConfig(m map[string]any, catalog map[string]*OIDCConfig) (*Config, error) {
	satCfg, err := ParseSAMAccessTokenConfig(m)
	if err != nil {
		return nil, fmt.Errorf("enterprise auth: %w", err)
	}
	cfg := &Config{
		SAMAccessToken:       satCfg,
		AuthorizationService: AuthorizationServiceConfig{Type: "deny_all"},
	}

	if v := getString(m, "external_auth_provider"); v != "" {
		cfg.ExternalAuthProvider = v
	}
	cfg.ExternalAuthClaimKey = getString(m, "external_auth_claim_key")
	cfg.ExternalAuthServiceURL = getString(m, "external_auth_service_url")
	cfg.ExternalAuthCallbackURI = getString(m, "external_auth_callback_uri")
	cfg.FrontendAuthLoginURL = getString(m, "frontend_auth_login_url")
	cfg.FrontendRedirectURL = getString(m, "frontend_redirect_url")
	cfg.FrontendServerURL = getString(m, "frontend_server_url")
	cfg.SessionSecretKey = getString(m, "session_secret_key")

	if cfg.SessionSecretKey == "" {
		cfg.SessionSecretKey = os.Getenv("SESSION_SECRET_KEY")
	}

	if len(catalog) == 1 && (cfg.ExternalAuthProvider == "" || cfg.ExternalAuthProvider == injectedEnterpriseSentinel) {
		for name := range catalog {
			cfg.ExternalAuthProvider = name
		}
	}

	if len(catalog) > 0 {
		if entry, ok := catalog[cfg.ExternalAuthProvider]; ok {
			oidcCfg := *entry
			if cfg.ExternalAuthCallbackURI != "" {
				oidcCfg.RedirectURI = cfg.ExternalAuthCallbackURI
			}
			cfg.OIDC = &oidcCfg
		}
	}

	if cfg.OIDC != nil && cfg.OIDC.RedirectURI != "" && cfg.ExternalAuthCallbackURI == "" {
		cfg.ExternalAuthCallbackURI = cfg.OIDC.RedirectURI
	}

	if cfg.OIDC != nil && cfg.OIDC.RedirectURI == "" && cfg.FrontendServerURL != "" {
		cfg.OIDC.RedirectURI = cfg.FrontendServerURL + "/api/v1/auth/callback"
		if cfg.ExternalAuthCallbackURI == "" {
			cfg.ExternalAuthCallbackURI = cfg.OIDC.RedirectURI
		}
	}

	if cfg.OIDC == nil {
		names := slices.Sorted(maps.Keys(catalog))
		if cfg.ExternalAuthProvider == "" || cfg.ExternalAuthProvider == injectedEnterpriseSentinel {
			return nil, fmt.Errorf(
				"enterprise auth: a 'providers:' catalog entry matching external_auth_provider is required (available providers: [%s])",
				strings.Join(names, ", "),
			)
		}
		return nil, fmt.Errorf(
			"enterprise auth: external_auth_provider=%q does not match any catalog entry (available: [%s])",
			cfg.ExternalAuthProvider, strings.Join(names, ", "),
		)
	}

	if cfg.SessionSecretKey == "" && cfg.OIDC.Issuer != "" {
		return nil, fmt.Errorf("enterprise auth: session_secret_key (or SESSION_SECRET_KEY env var) must be configured when OIDC is enabled")
	}

	if authzRaw, ok := m["authorization_service"].(map[string]any); ok {
		ParseAuthorizationServiceConfig(authzRaw, &cfg.AuthorizationService)

		if claims := cfg.AuthorizationService.IDPClaimsConfig; claims != nil && cfg.ExternalAuthProvider != "" {
			if rbac.ProviderUnsetOrSentinel(claims.OIDCProvider) {
				claims.OIDCProvider = cfg.ExternalAuthProvider
			}
		}

		if claims := cfg.AuthorizationService.IDPClaimsConfig; claims != nil && cfg.ExternalAuthClaimKey != "" && claims.ClaimKey == "" {
			claims.ClaimKey = cfg.ExternalAuthClaimKey
		}
	}

	syntheticCfg, err := ParseSyntheticAuthConfig(m)
	if err != nil {
		return nil, fmt.Errorf("synthetic auth config: %w", err)
	}
	cfg.SyntheticAuth = syntheticCfg

	return cfg, nil
}

func ParseAuthorizationServiceConfig(m map[string]any, cfg *AuthorizationServiceConfig) {
	if v := getString(m, "type"); v != "" {
		cfg.Type = v
	}
	cfg.RoleToScopeDefinitionsPath = getString(m, "role_to_scope_definitions_path")
	cfg.UserToRoleAssignmentsPath = getString(m, "user_to_role_assignments_path")
	cfg.UserToRoleProvider = getString(m, "user_to_role_provider")
	cfg.DefaultRoles = getStringSliceOrCSV(m, "default_roles")
	if v, ok := m["mini_idp_mode"].(bool); ok {
		cfg.MiniIDPMode = v
	}
	if idpRaw, ok := m["idp_claims_config"].(map[string]any); ok {
		idpCfg := &IDPClaimsConfig{
			OIDCProvider: getString(idpRaw, "oidc_provider"),
			ClaimKey:     getString(idpRaw, "claim_key"),
		}

		if mappingsRaw, ok := idpRaw["mappings"].(map[string]any); ok {
			idpCfg.Mappings = make(map[string][]string, len(mappingsRaw))
			for k, v := range mappingsRaw {
				idpCfg.Mappings[k] = toStringSlice(v)
			}
		}

		cfg.IDPClaimsConfig = idpCfg
	}
}

func SeedIDPClaimsConfigDefaults(cfg *AuthorizationServiceConfig, externalAuthProvider, externalAuthClaimKey string) {
	if externalAuthProvider == "" && externalAuthClaimKey == "" {
		return
	}
	if cfg.IDPClaimsConfig == nil {
		cfg.IDPClaimsConfig = &IDPClaimsConfig{}
	}
	if externalAuthProvider != "" && rbac.ProviderUnsetOrSentinel(cfg.IDPClaimsConfig.OIDCProvider) {
		cfg.IDPClaimsConfig.OIDCProvider = externalAuthProvider
	}
	if externalAuthClaimKey != "" && cfg.IDPClaimsConfig.ClaimKey == "" {
		cfg.IDPClaimsConfig.ClaimKey = externalAuthClaimKey
	}
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func getInt(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case int64:
		return int(v)
	}
	return 0
}

func getStringSlice(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func getStringSliceOrCSV(m map[string]any, key string) []string {
	switch v := m[key].(type) {
	case []any:
		return getStringSlice(m, key)
	case string:
		var result []string
		for field := range strings.SplitSeq(v, ",") {
			if field = strings.TrimSpace(field); field != "" {
				result = append(result, field)
			}
		}
		return result
	default:
		return nil
	}
}

func toStringSlice(v any) []string {
	switch val := v.(type) {
	case []any:
		result := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return val
	case string:
		return []string{val}
	}
	return nil
}
