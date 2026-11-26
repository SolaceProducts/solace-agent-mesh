// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

const (
	syntTenantID   = "11111111-1111-1111-1111-111111111111"
	syntAudience   = "api://sam-synthetic-test"
	syntRole       = "Synthetics.Smoke"
	syntDatadogApp = "22222222-2222-2222-2222-222222222222"
	syntOtherApp   = "99999999-9999-9999-9999-999999999999"
)

var (
	syntIssuerV1 = "https://sts.windows.net/" + syntTenantID + "/"
	syntIssuerV2 = "https://login.microsoftonline.com/" + syntTenantID + "/v2.0"
)

func startSyntheticJWKSStub(t *testing.T) (*SyntheticAuthConfig, *rsa.PrivateKey, func()) {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	kid := "synt-test-kid"

	jwksHandler := func(w http.ResponseWriter, _ *http.Request) {
		eBytes := big.NewInt(int64(priv.E)).Bytes()
		key := map[string]any{
			"kty": "RSA",
			"alg": "RS256",
			"use": "sig",
			"kid": kid,
			"n":   base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(eBytes),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{key}})
	}
	srv := httptest.NewServer(http.HandlerFunc(jwksHandler))

	raw := map[string]any{
		"synthetic_auth": map[string]any{
			"enabled":         true,
			"tenant_id":       syntTenantID,
			"audience":        syntAudience,
			"role_name":       syntRole,
			"appid_allowlist": []any{syntDatadogApp},
			"roles":           []any{"SyntheticMonitor"},
			"endpoint_allowlist": []any{
				map[string]any{"method": "GET", "path": `^/api/v1/sessions$`},
				map[string]any{"method": "POST", "path": `^/api/v1/messages$`},
			},
			"jwks_uri": srv.URL,
		},
	}
	cfg, err := ParseSyntheticAuthConfig(raw)
	if err != nil {
		t.Fatalf("ParseSyntheticAuthConfig: %v", err)
	}
	if cfg == nil {
		t.Fatalf("ParseSyntheticAuthConfig returned nil, want config")
	}
	if err := cfg.InitVerifier(srv.Client(), slog.Default()); err != nil {
		t.Fatalf("InitVerifier: %v", err)
	}
	cfg.verifier.minRefresh = 0

	cleanup := func() { srv.Close() }
	t.Cleanup(cleanup)

	t.Cleanup(func() {})
	_ = kid

	syntTestKID = kid

	return cfg, priv, cleanup
}

var syntTestKID string

func mintSyntheticToken(t *testing.T, priv *rsa.PrivateKey, overrides map[string]any) string {
	t.Helper()
	now := time.Now().Unix()
	claims := jwt.MapClaims{
		"iss":      syntIssuerV2,
		"aud":      syntAudience,
		"tid":      syntTenantID,
		"appid":    syntDatadogApp,
		"azp":      syntDatadogApp,
		"appidacr": "1",
		"roles":    []any{syntRole},
		"iat":      now,
		"nbf":      now,
		"exp":      now + 300,
	}
	for k, v := range overrides {
		if v == nil {
			delete(claims, k)
			continue
		}
		claims[k] = v
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = syntTestKID
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	return signed
}

func wantNotApplicable(t *testing.T, _ map[string]any, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected ErrSyntheticTokenNotApplicable, got nil")
	}
	if !errors.Is(err, ErrSyntheticTokenNotApplicable) {
		t.Fatalf("expected ErrSyntheticTokenNotApplicable, got %v", err)
	}
}

func wantInvalid(t *testing.T, _ map[string]any, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected SyntheticTokenInvalidError, got nil")
	}
	var ie *SyntheticTokenInvalidError
	if !errors.As(err, &ie) {
		t.Fatalf("expected *SyntheticTokenInvalidError, got %v", err)
	}
}

func TestSyntheticTokenGarbageIsNotApplicable(t *testing.T) {
	cfg, _, _ := startSyntheticJWKSStub(t)
	_, err := cfg.ValidateSyntheticToken(context.Background(), "not-a-jwt")
	wantNotApplicable(t, nil, err)
}

func TestSyntheticTokenWithoutMarkersIsNotApplicable(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, map[string]any{
		"appid": syntOtherApp,
		"azp":   syntOtherApp,
		"roles": []any{"User.Read"},
	})
	_, err := cfg.ValidateSyntheticToken(context.Background(), token)
	wantNotApplicable(t, nil, err)
}

func TestSyntheticTokenBadSignatureIsInvalid(t *testing.T) {
	cfg, _, _ := startSyntheticJWKSStub(t)
	wrongKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	token := mintSyntheticToken(t, wrongKey, nil)
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenAlgNoneIsRejected(t *testing.T) {
	cfg, _, _ := startSyntheticJWKSStub(t)
	now := time.Now().Unix()
	claims := jwt.MapClaims{
		"iss": syntIssuerV2, "aud": syntAudience, "tid": syntTenantID,
		"appid": syntDatadogApp, "azp": syntDatadogApp, "appidacr": "1",
		"roles": []any{syntRole},
		"iat":   now, "nbf": now, "exp": now + 300,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	tok.Header["kid"] = syntTestKID
	signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("SignedString(none): %v", err)
	}
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), signed)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenWrongAudienceIsRejected(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, map[string]any{"aud": "api://wrong-app"})
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenWrongIssuerIsRejected(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, map[string]any{
		"iss": "https://login.microsoftonline.com/other/v2.0",
	})
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenWrongTenantIsRejected(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, map[string]any{
		"tid": "00000000-0000-0000-0000-000000000000",
	})
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenAppIDNotAllowlistedIsRejected(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, map[string]any{
		"appid": syntOtherApp,
		"azp":   syntOtherApp,
	})
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenExtraRolesIsRejected(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, map[string]any{
		"roles": []any{syntRole, "Admin"},
	})
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenMissingRoleIsRejected(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, map[string]any{"roles": []any{}})
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenUserClaimPresentIsRejected(t *testing.T) {
	for _, claim := range []string{"preferred_username", "upn", "unique_name"} {
		t.Run(claim, func(t *testing.T) {
			cfg, priv, _ := startSyntheticJWKSStub(t)
			token := mintSyntheticToken(t, priv, map[string]any{claim: "real.user@example.com"})
			_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
			wantInvalid(t, nil, gotErr)
		})
	}
}

func TestSyntheticTokenSubAndOidAreAccepted(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	spOID := "b6f8278f-0861-41c1-bc68-a933d5afdc00"
	token := mintSyntheticToken(t, priv, map[string]any{"sub": spOID, "oid": spOID})
	claims, err := cfg.ValidateSyntheticToken(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stringClaim(claims, "sub"); got != spOID {
		t.Errorf("sub = %q, want %q", got, spOID)
	}
	if got := stringClaim(claims, "oid"); got != spOID {
		t.Errorf("oid = %q, want %q", got, spOID)
	}
}

func TestSyntheticTokenV1IssuerIsAccepted(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, map[string]any{"iss": syntIssuerV1})
	claims, err := cfg.ValidateSyntheticToken(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stringClaim(claims, "iss"); got != syntIssuerV1 {
		t.Errorf("iss = %q, want %q", got, syntIssuerV1)
	}
}

func TestSyntheticTokenAppidacrZeroIsRejected(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, map[string]any{"appidacr": "0"})
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenAppidacrMissingIsRejected(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, map[string]any{"appidacr": nil})
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenExpiredIsRejected(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	now := time.Now().Unix()
	token := mintSyntheticToken(t, priv, map[string]any{
		"exp": now - 600, "iat": now - 1200, "nbf": now - 1200,
	})
	_, gotErr := cfg.ValidateSyntheticToken(context.Background(), token)
	wantInvalid(t, nil, gotErr)
}

func TestSyntheticTokenValidReturnsClaims(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, nil)
	claims, err := cfg.ValidateSyntheticToken(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := stringClaim(claims, "appid"); got != syntDatadogApp {
		t.Errorf("appid = %q, want %q", got, syntDatadogApp)
	}
	if got := stringClaim(claims, "tid"); got != syntTenantID {
		t.Errorf("tid = %q, want %q", got, syntTenantID)
	}
}

func fullSyntheticConfig() map[string]any {
	return map[string]any{
		"synthetic_auth": map[string]any{
			"enabled":         true,
			"tenant_id":       syntTenantID,
			"audience":        syntAudience,
			"role_name":       syntRole,
			"appid_allowlist": []any{syntDatadogApp},
			"roles":           []any{"SyntheticMonitor"},
			"endpoint_allowlist": []any{
				map[string]any{"method": "GET", "path": `^/api/v1/sessions$`},
				map[string]any{"method": "POST", "path": `^/api/v1/messages$`},
			},
		},
	}
}

func TestSyntheticConfigAbsentReturnsNil(t *testing.T) {
	cfg, err := ParseSyntheticAuthConfig(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("want nil cfg when section absent, got %#v", cfg)
	}
}

func TestSyntheticConfigDisabledByDefault(t *testing.T) {
	cfg, err := ParseSyntheticAuthConfig(map[string]any{
		"synthetic_auth": map[string]any{"enabled": false},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("want nil cfg when disabled, got %#v", cfg)
	}
}

func TestSyntheticConfigEnabledLoads(t *testing.T) {
	cfg, err := ParseSyntheticAuthConfig(fullSyntheticConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("want non-nil cfg")
	}
	if cfg.RoleName != syntRole {
		t.Errorf("RoleName = %q, want %q", cfg.RoleName, syntRole)
	}
	if _, ok := cfg.AppIDAllowlist[syntDatadogApp]; !ok {
		t.Errorf("AppIDAllowlist missing %q", syntDatadogApp)
	}
}

func TestSyntheticConfigMissingRequiredFieldErrors(t *testing.T) {
	cases := []string{"tenant_id", "audience", "role_name"}
	for _, missing := range cases {
		t.Run(missing, func(t *testing.T) {
			raw := fullSyntheticConfig()
			raw["synthetic_auth"].(map[string]any)[missing] = ""
			_, err := ParseSyntheticAuthConfig(raw)
			if err == nil {
				t.Fatalf("expected error when %s is empty", missing)
			}
		})
	}
}

func TestSyntheticConfigEmptyAppIDAllowlistErrors(t *testing.T) {
	raw := fullSyntheticConfig()
	raw["synthetic_auth"].(map[string]any)["appid_allowlist"] = []any{}
	_, err := ParseSyntheticAuthConfig(raw)
	if err == nil {
		t.Fatal("expected error when appid_allowlist is empty")
	}
}

func TestSyntheticConfigEmptyEndpointAllowlistErrors(t *testing.T) {
	raw := fullSyntheticConfig()
	raw["synthetic_auth"].(map[string]any)["endpoint_allowlist"] = []any{}
	_, err := ParseSyntheticAuthConfig(raw)
	if err == nil {
		t.Fatal("expected error when endpoint_allowlist is empty")
	}
}

func TestSyntheticConfigEmptyAppIDEntryErrors(t *testing.T) {
	raw := fullSyntheticConfig()
	raw["synthetic_auth"].(map[string]any)["appid_allowlist"] = []any{"", syntDatadogApp}
	_, err := ParseSyntheticAuthConfig(raw)
	if err == nil {
		t.Fatal("expected error when appid_allowlist contains empty string")
	}
}

func TestSyntheticConfigEmptyRoleEntryErrors(t *testing.T) {
	raw := fullSyntheticConfig()
	raw["synthetic_auth"].(map[string]any)["roles"] = []any{""}
	_, err := ParseSyntheticAuthConfig(raw)
	if err == nil {
		t.Fatal("expected error when roles contains empty string")
	}
}

func TestSyntheticConfigUnanchoredPathErrors(t *testing.T) {
	cases := []string{
		"/api/v1/sessions",
		"^/api/v1/sessions",
		"/api/v1/sessions$",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			raw := fullSyntheticConfig()
			raw["synthetic_auth"].(map[string]any)["endpoint_allowlist"] = []any{
				map[string]any{"method": "GET", "path": p},
			}
			_, err := ParseSyntheticAuthConfig(raw)
			if err == nil {
				t.Fatalf("expected error for unanchored path regex %q", p)
			}
		})
	}
}

func TestSyntheticConfigEmptyRolesErrors(t *testing.T) {
	raw := fullSyntheticConfig()
	raw["synthetic_auth"].(map[string]any)["roles"] = []any{}
	_, err := ParseSyntheticAuthConfig(raw)
	if err == nil {
		t.Fatal("expected error when roles is empty")
	}
}

func TestSyntheticConfigDefaultIssuersTargetEntraPublicCloud(t *testing.T) {
	cfg, err := ParseSyntheticAuthConfig(fullSyntheticConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantIssuers := []string{syntIssuerV1, syntIssuerV2}
	if len(cfg.Issuers) != 2 || cfg.Issuers[0] != wantIssuers[0] || cfg.Issuers[1] != wantIssuers[1] {
		t.Errorf("Issuers = %v, want %v", cfg.Issuers, wantIssuers)
	}
	wantJWKS := "https://login.microsoftonline.com/" + syntTenantID + "/discovery/v2.0/keys"
	if cfg.JWKSURI != wantJWKS {
		t.Errorf("JWKSURI = %q, want %q", cfg.JWKSURI, wantJWKS)
	}
}

func TestSyntheticConfigIssuerAndJWKSOverridesApply(t *testing.T) {
	raw := fullSyntheticConfig()
	raw["synthetic_auth"].(map[string]any)["issuers"] = []any{
		"https://login.microsoftonline.us/" + syntTenantID + "/v2.0",
	}
	raw["synthetic_auth"].(map[string]any)["jwks_uri"] = "https://login.microsoftonline.us/" + syntTenantID + "/discovery/v2.0/keys"
	cfg, err := ParseSyntheticAuthConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Issuers) != 1 || cfg.Issuers[0] != "https://login.microsoftonline.us/"+syntTenantID+"/v2.0" {
		t.Errorf("Issuers = %v, want sovereign override", cfg.Issuers)
	}
	if cfg.JWKSURI != "https://login.microsoftonline.us/"+syntTenantID+"/discovery/v2.0/keys" {
		t.Errorf("JWKSURI = %q, want sovereign override", cfg.JWKSURI)
	}
}

func TestSyntheticEndpointAllowlistAllowsMatchingMethodAndPath(t *testing.T) {
	cfg, _, _ := startSyntheticJWKSStub(t)
	if !cfg.IsEndpointAllowed("GET", "/api/v1/sessions") {
		t.Error("GET /api/v1/sessions should be allowed")
	}
	if !cfg.IsEndpointAllowed("POST", "/api/v1/messages") {
		t.Error("POST /api/v1/messages should be allowed")
	}
}

func TestSyntheticEndpointAllowlistRejectsMethodMismatch(t *testing.T) {
	cfg, _, _ := startSyntheticJWKSStub(t)
	if cfg.IsEndpointAllowed("POST", "/api/v1/sessions") {
		t.Error("POST to GET-only path should be denied")
	}
}

func TestSyntheticEndpointAllowlistRejectsUnmatchedPath(t *testing.T) {
	cfg, _, _ := startSyntheticJWKSStub(t)
	if cfg.IsEndpointAllowed("GET", "/api/v1/admin/users") {
		t.Error("unmatched path should be denied")
	}
}

func TestSyntheticEndpointAllowlistRejectsPathExtension(t *testing.T) {
	cfg, _, _ := startSyntheticJWKSStub(t)
	if cfg.IsEndpointAllowed("GET", "/api/v1/sessions/secret") {
		t.Error("regex anchors must prevent path-extension")
	}
}

func TestSyntheticEndpointAllowlistMethodIsCaseInsensitive(t *testing.T) {
	cfg, _, _ := startSyntheticJWKSStub(t)
	if !cfg.IsEndpointAllowed("get", "/api/v1/sessions") {
		t.Error("lowercase method should match")
	}
}

func TestSyntheticIdentityCarriesConfiguredRoles(t *testing.T) {
	cfg, priv, _ := startSyntheticJWKSStub(t)
	token := mintSyntheticToken(t, priv, nil)
	claims, err := cfg.ValidateSyntheticToken(context.Background(), token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	id := cfg.BuildSyntheticIdentity(claims, nil)
	if id.UserID() != auth.SyntheticUserID {
		t.Errorf("UserID = %q, want %q", id.UserID(), auth.SyntheticUserID)
	}
	if id.Email() != auth.SyntheticUserEmail {
		t.Errorf("Email = %q, want %q", id.Email(), auth.SyntheticUserEmail)
	}
	if id.AuthMethod() != auth.SyntheticAuthMethod {
		t.Errorf("AuthMethod = %q, want %q", id.AuthMethod(), auth.SyntheticAuthMethod)
	}
	if len(id.Roles()) != 1 || id.Roles()[0] != "SyntheticMonitor" {
		t.Errorf("Roles = %v, want [SyntheticMonitor]", id.Roles())
	}
	if id.Metadata()["is_synthetic"] != true {
		t.Errorf("Metadata[is_synthetic] = %v, want true", id.Metadata()["is_synthetic"])
	}
	if id.Metadata()["appid"] != syntDatadogApp {
		t.Errorf("Metadata[appid] = %v, want %q", id.Metadata()["appid"], syntDatadogApp)
	}
}

func TestSyntheticAuthEnabledAcceptsStringForms(t *testing.T) {
	for _, v := range []any{true, "true", "TRUE", "1", "yes", "on"} {
		if !SyntheticAuthEnabled(map[string]any{
			"synthetic_auth": map[string]any{"enabled": v},
		}) {
			t.Errorf("enabled=%v should be truthy", v)
		}
	}
	for _, v := range []any{false, "false", "no", "off", "", 0} {
		if SyntheticAuthEnabled(map[string]any{
			"synthetic_auth": map[string]any{"enabled": v},
		}) {
			t.Errorf("enabled=%v should be falsy", v)
		}
	}
}
