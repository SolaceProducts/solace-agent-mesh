// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

var syntheticForbiddenUserClaims = []string{"preferred_username", "upn", "unique_name"}

var syntheticAllowedAppAuthContext = map[string]struct{}{"1": {}, "2": {}}

var ErrSyntheticTokenNotApplicable = errors.New("synthetic: token not applicable")

type SyntheticTokenInvalidError struct {
	Reason string
	Err    error
}

func (e *SyntheticTokenInvalidError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("synthetic: %s", e.Reason)
	}
	return fmt.Sprintf("synthetic: %s: %v", e.Reason, e.Err)
}

func (e *SyntheticTokenInvalidError) Unwrap() error { return e.Err }

type endpointRule struct {
	Method  string
	Pattern *regexp.Regexp
}

type SyntheticAuthConfig struct {
	TenantID       string
	Audience       string
	RoleName       string
	AppIDAllowlist map[string]struct{}
	EndpointRules  []endpointRule
	PrincipalRoles []string
	Issuers        []string
	JWKSURI        string
	verifier       *JWKSIDTokenVerifier
}

func SyntheticAuthEnabled(m map[string]any) bool {
	syntheticRaw, ok := m["synthetic_auth"].(map[string]any)
	if !ok {
		return false
	}
	switch v := syntheticRaw["enabled"].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "on":
			return true
		}
	}
	return false
}

func ParseSyntheticAuthConfig(m map[string]any) (*SyntheticAuthConfig, error) {
	syntheticRaw, ok := m["synthetic_auth"].(map[string]any)
	if !ok {
		return nil, nil
	}
	if !SyntheticAuthEnabled(m) {
		return nil, nil
	}

	tenantID := getString(syntheticRaw, "tenant_id")
	audience := getString(syntheticRaw, "audience")
	roleName := getString(syntheticRaw, "role_name")

	missing := []string{}
	if tenantID == "" {
		missing = append(missing, "tenant_id")
	}
	if audience == "" {
		missing = append(missing, "audience")
	}
	if roleName == "" {
		missing = append(missing, "role_name")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("synthetic_auth: missing required fields: %s", strings.Join(missing, ", "))
	}

	appidAllowlist := getStringSlice(syntheticRaw, "appid_allowlist")
	if len(appidAllowlist) == 0 {
		return nil, fmt.Errorf("synthetic_auth: appid_allowlist must not be empty (would accept any tenant app with the role)")
	}
	for i, id := range appidAllowlist {
		if id == "" {
			return nil, fmt.Errorf("synthetic_auth: appid_allowlist[%d] must not be empty", i)
		}
	}

	principalRoles := getStringSlice(syntheticRaw, "roles")
	if len(principalRoles) == 0 {
		return nil, fmt.Errorf("synthetic_auth: roles must not be empty (without roles the principal falls back to the role-provider lookup which 404s on the synthetic identity)")
	}
	for i, role := range principalRoles {
		if role == "" {
			return nil, fmt.Errorf("synthetic_auth: roles[%d] must not be empty", i)
		}
	}

	endpointEntries, ok := syntheticRaw["endpoint_allowlist"].([]any)
	if !ok || len(endpointEntries) == 0 {
		return nil, fmt.Errorf("synthetic_auth: endpoint_allowlist must not be empty (would deny every request)")
	}
	rules := make([]endpointRule, 0, len(endpointEntries))
	for i, raw := range endpointEntries {
		entry, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("synthetic_auth: endpoint_allowlist[%d]: expected map, got %T", i, raw)
		}
		method := strings.ToUpper(strings.TrimSpace(getString(entry, "method")))
		pathPattern := getString(entry, "path")
		if method == "" || pathPattern == "" {
			return nil, fmt.Errorf("synthetic_auth: endpoint_allowlist[%d]: both method and path are required", i)
		}
		if !strings.HasPrefix(pathPattern, "^") || !strings.HasSuffix(pathPattern, "$") {
			return nil, fmt.Errorf("synthetic_auth: endpoint_allowlist[%d]: path regex %q must be anchored with ^ and $", i, pathPattern)
		}
		compiled, err := regexp.Compile(pathPattern)
		if err != nil {
			return nil, fmt.Errorf("synthetic_auth: endpoint_allowlist[%d]: invalid path regex %q: %w", i, pathPattern, err)
		}
		rules = append(rules, endpointRule{Method: method, Pattern: compiled})
	}

	issuers := getStringSlice(syntheticRaw, "issuers")
	if len(issuers) == 0 {
		issuers = []string{
			fmt.Sprintf("https://sts.windows.net/%s/", tenantID),
			fmt.Sprintf("https://login.microsoftonline.com/%s/v2.0", tenantID),
		}
	}
	jwksURI := getString(syntheticRaw, "jwks_uri")
	if jwksURI == "" {
		jwksURI = fmt.Sprintf("https://login.microsoftonline.com/%s/discovery/v2.0/keys", tenantID)
	}

	allowlistSet := make(map[string]struct{}, len(appidAllowlist))
	for _, id := range appidAllowlist {
		allowlistSet[id] = struct{}{}
	}

	return &SyntheticAuthConfig{
		TenantID:       tenantID,
		Audience:       audience,
		RoleName:       roleName,
		AppIDAllowlist: allowlistSet,
		EndpointRules:  rules,
		PrincipalRoles: principalRoles,
		Issuers:        issuers,
		JWKSURI:        jwksURI,
	}, nil
}

func (c *SyntheticAuthConfig) InitVerifier(httpClient *http.Client, logger *slog.Logger) error {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if logger == nil {
		logger = slog.Default()
	}
	jwksURI := c.JWKSURI
	v, err := NewJWKSIDTokenVerifier(
		"",
		c.Audience,
		func(_ context.Context) (string, error) { return jwksURI, nil },
		httpClient,
		logger.With("logger_name", "synthetic-auth"),
	)
	if err != nil {
		return fmt.Errorf("synthetic_auth: build JWKS verifier: %w", err)
	}
	c.verifier = v
	return nil
}

func (c *SyntheticAuthConfig) ValidateSyntheticToken(ctx context.Context, token string) (map[string]any, error) {
	if c.verifier == nil {
		return nil, &SyntheticTokenInvalidError{Reason: "JWKS verifier not initialised (InitVerifier was not called)"}
	}
	unverifiedTok, _, err := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSyntheticTokenNotApplicable, err)
	}
	unverified, _ := unverifiedTok.Claims.(jwt.MapClaims)

	unverifiedAppID := stringClaim(unverified, "appid")
	if unverifiedAppID == "" {
		unverifiedAppID = stringClaim(unverified, "azp")
	}
	_, appidLooksSynthetic := c.AppIDAllowlist[unverifiedAppID]
	roleLooksSynthetic := false
	if rolesAny, ok := unverified["roles"].([]any); ok {
		for _, r := range rolesAny {
			if s, ok := r.(string); ok && s == c.RoleName {
				roleLooksSynthetic = true
				break
			}
		}
	}
	if !appidLooksSynthetic && !roleLooksSynthetic {
		return nil, fmt.Errorf("%w: no synthetic appid or role", ErrSyntheticTokenNotApplicable)
	}

	claims, verifyErr := c.verifier.VerifyIDToken(ctx, token)
	if verifyErr != nil {
		return nil, &SyntheticTokenInvalidError{Reason: "jwt verification failed", Err: verifyErr}
	}

	iss := stringClaim(claims, "iss")
	if !slices.Contains(c.Issuers, iss) {
		return nil, &SyntheticTokenInvalidError{Reason: fmt.Sprintf("unexpected issuer: %q", iss)}
	}

	tid := stringClaim(claims, "tid")
	if tid != c.TenantID {
		return nil, &SyntheticTokenInvalidError{Reason: fmt.Sprintf("tenant mismatch: got %q, expected %q", tid, c.TenantID)}
	}

	appid := stringClaim(claims, "appid")
	if appid == "" {
		appid = stringClaim(claims, "azp")
	}
	if _, ok := c.AppIDAllowlist[appid]; !ok {
		return nil, &SyntheticTokenInvalidError{Reason: fmt.Sprintf("appid %q not in allowlist", appid)}
	}

	appidacr := stringClaim(claims, "appidacr")
	if _, ok := syntheticAllowedAppAuthContext[appidacr]; !ok {
		return nil, &SyntheticTokenInvalidError{Reason: fmt.Sprintf("app authentication context too weak: appidacr=%q", appidacr)}
	}

	rolesAny, ok := claims["roles"].([]any)
	if !ok || len(rolesAny) != 1 {
		return nil, &SyntheticTokenInvalidError{Reason: fmt.Sprintf("roles must equal [%q], got %v", c.RoleName, claims["roles"])}
	}
	role, _ := rolesAny[0].(string)
	if role != c.RoleName {
		return nil, &SyntheticTokenInvalidError{Reason: fmt.Sprintf("roles must equal [%q], got [%q]", c.RoleName, role)}
	}

	for _, claim := range syntheticForbiddenUserClaims {
		if _, present := claims[claim]; present {
			return nil, &SyntheticTokenInvalidError{Reason: fmt.Sprintf("synthetic token must not carry user claim %q", claim)}
		}
	}

	return claims, nil
}

func (c *SyntheticAuthConfig) IsEndpointAllowed(method, path string) bool {
	upMethod := strings.ToUpper(method)
	for _, rule := range c.EndpointRules {
		if rule.Method == upMethod && rule.Pattern.MatchString(path) {
			return true
		}
	}
	return false
}

func (c *SyntheticAuthConfig) BuildSyntheticIdentity(claims map[string]any, authzSvc *AuthorizationService) *auth.Subject {
	appid := stringClaim(claims, "appid")
	if appid == "" {
		appid = stringClaim(claims, "azp")
	}
	return auth.ForSyntheticMonitor(c.PrincipalRoles,
		map[string]any{"appid": appid, "is_synthetic": true},
		NewScopeExpander(authzSvc))
}

func stringClaim(claims map[string]any, key string) string {
	v, _ := claims[key].(string)
	return v
}
