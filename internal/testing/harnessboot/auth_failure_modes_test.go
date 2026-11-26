// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harnessboot_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harness"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/harnessboot"
)

func bootWithUsers(t *testing.T, users []harness.FakeOIDCUser) (*harnessboot.GatewayHandle, *harness.MockOIDCProvider) {
	t.Helper()
	idp, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s", Users: users,
	})
	if err != nil {
		t.Fatalf("NewMockOIDCProvider: %v", err)
	}
	t.Cleanup(func() { _ = idp.Close() })
	idps := map[string]*harness.MockOIDCProvider{"primary": idp}
	auth := &harness.GatewayAuth{
		FakeIDPs:             []harness.FakeIDPSpec{{Name: "primary", ClientID: "c", ClientSecret: "s"}},
		Catalog:              []harness.CatalogEntry{{Name: "primary", IDPRef: "primary"}},
		ExternalAuthProvider: "primary",
		SessionSecretKey:     "test-secret-key",
	}
	handle, err := harnessboot.BootGateway(t, &harness.Scenario{GatewayAuth: auth}, harness.SynthesizeCatalog(auth, idps))
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	return handle, idp
}

func TestAuthedClient_SpoofedXSAMUserIDIgnored(t *testing.T) {
	handle, idp := bootWithUsers(t, []harness.FakeOIDCUser{
		{Sub: "alice@example.com", Email: "alice@example.com", Name: "Alice"},
	})
	ctx := t.Context()
	client := harness.AuthedClient(ctx, t, handle.BaseURL, idp, "alice@example.com")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, handle.BaseURL+"/api/v1/user", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-SAM-User-ID", "eve@evil.example.com")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/user: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "alice@example.com") {
		t.Errorf("identity should remain alice under OIDC; body=%s", body)
	}
	if strings.Contains(string(body), "eve@evil.example.com") {
		t.Errorf("spoofed X-SAM-User-ID leaked into the resolved identity; body=%s", body)
	}
}

func TestAuthedClient_SpoofedXSAMScopesIgnored(t *testing.T) {
	rolesFile := filepath.Join(t.TempDir(), "roles.yaml")
	rolesYAML := `roles:
  rbac_reader:
    description: "Can read RBAC config, nothing else."
    scopes:
      - "rbac:_:read"
`
	if err := os.WriteFile(rolesFile, []byte(rolesYAML), 0o600); err != nil {
		t.Fatalf("write roles file: %v", err)
	}

	idp, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []harness.FakeOIDCUser{
			{Sub: "alice@example.com", Email: "alice@example.com", Claims: map[string]any{"groups": []string{"general"}}},
		},
	})
	if err != nil {
		t.Fatalf("NewMockOIDCProvider: %v", err)
	}
	t.Cleanup(func() { _ = idp.Close() })
	idps := map[string]*harness.MockOIDCProvider{"primary": idp}

	auth := &harness.GatewayAuth{
		FakeIDPs:             []harness.FakeIDPSpec{{Name: "primary", ClientID: "c", ClientSecret: "s"}},
		Catalog:              []harness.CatalogEntry{{Name: "primary", IDPRef: "primary"}},
		ExternalAuthProvider: "primary",
		SessionSecretKey:     "test-secret-key",
		AuthorizationService: map[string]any{
			"type":                           "default_rbac",
			"user_to_role_provider":          "idp_claims",
			"role_to_scope_definitions_path": rolesFile,
			"idp_claims_config": map[string]any{
				"oidc_provider": "primary",
				"claim_key":     "groups",
				"mappings": map[string]any{
					"general": []any{"rbac_reader"},
				},
			},
		},
	}
	handle, err := harnessboot.BootGateway(t, &harness.Scenario{GatewayAuth: auth}, harness.SynthesizeCatalog(auth, idps))
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	ctx := t.Context()
	client := harness.AuthedClient(ctx, t, handle.BaseURL, idp, "alice@example.com")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, handle.BaseURL+"/api/v1/user/capabilities?scopes=rbac:_:read,analytics:_:read", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-SAM-Scopes", "agent_builder:*:read")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/user/capabilities: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d, want 200; body=%s", resp.StatusCode, body)
	}
	var out struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if !out.Capabilities["rbac:_:read"] {
		t.Errorf("enterprise auth must never honour the dev-only X-SAM-Scopes header: alice's real rbac:_:read scope was narrowed by the spoofed header; caps=%v", out.Capabilities)
	}
	if out.Capabilities["analytics:_:read"] {
		t.Errorf("alice's baseline role grants only rbac:_:read; analytics:_:read must stay false (proves the assertion above is not the allow-all path); caps=%v", out.Capabilities)
	}
}

func TestAuthedClient_SpoofedXSAMScopesIgnored_Widening(t *testing.T) {
	rolesFile := filepath.Join(t.TempDir(), "roles.yaml")
	rolesYAML := `roles:
  base_user:
    description: "Baseline access, no analytics."
    scopes:
      - "chat:_:read"
`
	if err := os.WriteFile(rolesFile, []byte(rolesYAML), 0o600); err != nil {
		t.Fatalf("write roles file: %v", err)
	}

	idp, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []harness.FakeOIDCUser{
			{Sub: "bob@example.com", Email: "bob@example.com", Claims: map[string]any{"groups": []string{"general"}}},
		},
	})
	if err != nil {
		t.Fatalf("NewMockOIDCProvider: %v", err)
	}
	t.Cleanup(func() { _ = idp.Close() })
	idps := map[string]*harness.MockOIDCProvider{"primary": idp}

	auth := &harness.GatewayAuth{
		FakeIDPs:             []harness.FakeIDPSpec{{Name: "primary", ClientID: "c", ClientSecret: "s"}},
		Catalog:              []harness.CatalogEntry{{Name: "primary", IDPRef: "primary"}},
		ExternalAuthProvider: "primary",
		SessionSecretKey:     "test-secret-key",
		AuthorizationService: map[string]any{
			"type":                           "default_rbac",
			"user_to_role_provider":          "idp_claims",
			"role_to_scope_definitions_path": rolesFile,
			"idp_claims_config": map[string]any{
				"oidc_provider": "primary",
				"claim_key":     "groups",
				"mappings": map[string]any{
					"general": []any{"base_user"},
				},
			},
		},
	}
	handle, err := harnessboot.BootGateway(t, &harness.Scenario{GatewayAuth: auth}, harness.SynthesizeCatalog(auth, idps))
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	ctx := t.Context()
	client := harness.AuthedClient(ctx, t, handle.BaseURL, idp, "bob@example.com")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, handle.BaseURL+"/api/v1/user/capabilities?scopes=analytics:_:read,chat:_:read", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-SAM-Scopes", "*:*:*")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/user/capabilities: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d, want 200; body=%s", resp.StatusCode, body)
	}
	var out struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	if out.Capabilities["analytics:_:read"] {
		t.Errorf("spoofed X-SAM-Scopes: *:*:* widened bob's real scope set: analytics:_:read must stay false; caps=%v", out.Capabilities)
	}
	if !out.Capabilities["chat:_:read"] {
		t.Errorf("bob's baseline role should resolve chat:_:read (rules out the unauthenticated deny-all case); caps=%v", out.Capabilities)
	}
}

func TestExpiredIDToken_LoginRejected(t *testing.T) {
	handle, idp := bootWithUsers(t, []harness.FakeOIDCUser{
		{Sub: "alice@example.com", Email: "alice@example.com", Claims: map[string]any{"exp": 1}},
	})
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	step := harness.HTTPFlowStep{
		Step: "login", FakeIDP: "primary", AsUser: "alice@example.com",
		Expect: harness.HTTPFlowExpect{SessionCookieSet: true},
	}
	idps := map[string]*harness.MockOIDCProvider{"primary": idp}
	if err := harness.ExecuteLogin(t.Context(), t, client, step, handle.BaseURL, idps); err == nil {
		t.Fatal("expected login to fail when the IdP issues an expired id_token")
	}
	u, _ := url.Parse(handle.BaseURL)
	for _, c := range jar.Cookies(u) {
		if c.Name == "sam_access_token" {
			t.Fatal("sam_access_token cookie must not be set for an expired id_token")
		}
	}
}

func TestSigningKeyRotation_ExistingSessionSurvives(t *testing.T) {
	handle, idp := bootWithUsers(t, []harness.FakeOIDCUser{
		{Sub: "alice@example.com", Email: "alice@example.com"},
	})
	ctx := t.Context()
	client := harness.AuthedClient(ctx, t, handle.BaseURL, idp, "alice@example.com")

	if err := idp.RotateSigningKey(); err != nil {
		t.Fatalf("RotateSigningKey: %v", err)
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, handle.BaseURL+"/api/v1/user", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request after IdP key rotation: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("established session should survive IdP key rotation: got %d; body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "alice@example.com") {
		t.Errorf("expected alice's identity to persist post-rotation; body=%s", body)
	}
}

func TestIdPDown_LoginFailsThenRecovers(t *testing.T) {
	handle, idp := bootWithUsers(t, []harness.FakeOIDCUser{
		{Sub: "alice@example.com", Email: "alice@example.com"},
	})
	ctx := t.Context()
	idps := map[string]*harness.MockOIDCProvider{"primary": idp}

	if err := idp.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	jar, _ := cookiejar.New(nil)
	step := harness.HTTPFlowStep{
		Step: "login", FakeIDP: "primary", AsUser: "alice@example.com",
		Expect: harness.HTTPFlowExpect{SessionCookieSet: true},
	}
	if err := harness.ExecuteLogin(ctx, t, &http.Client{Jar: jar}, step, handle.BaseURL, idps); err == nil {
		t.Fatal("expected login to fail while the IdP is down")
	}

	if err := idp.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	_ = harness.AuthedClient(ctx, t, handle.BaseURL, idp, "alice@example.com")
}

func TestMultiProviderCatalog_PrimaryLoginWorks(t *testing.T) {
	primary, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
		ClientID: "pc", ClientSecret: "ps",
		Users: []harness.FakeOIDCUser{{Sub: "alice@example.com", Email: "alice@example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = primary.Close() })
	secondary, err := harness.NewMockOIDCProvider(harness.MockOIDCProviderConfig{
		ClientID: "sc", ClientSecret: "ss",
		Users: []harness.FakeOIDCUser{{Sub: "bob@example.com", Email: "bob@example.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondary.Close() })

	idps := map[string]*harness.MockOIDCProvider{"primary": primary, "secondary": secondary}
	auth := &harness.GatewayAuth{
		FakeIDPs: []harness.FakeIDPSpec{
			{Name: "primary", ClientID: "pc", ClientSecret: "ps"},
			{Name: "secondary", ClientID: "sc", ClientSecret: "ss"},
		},
		Catalog: []harness.CatalogEntry{
			{Name: "primary", IDPRef: "primary"},
			{Name: "secondary", IDPRef: "secondary"},
		},
		ExternalAuthProvider: "primary",
		SessionSecretKey:     "test-secret-key",
	}
	handle, err := harnessboot.BootGateway(t, &harness.Scenario{GatewayAuth: auth}, harness.SynthesizeCatalog(auth, idps))
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	ctx := t.Context()

	loginClient := harness.AuthedClient(ctx, t, handle.BaseURL, primary, "alice@example.com")
	loginReq := harness.HTTPFlowStep{
		Step: "request", Method: "GET", Path: "/api/v1/user",
		Expect: harness.HTTPFlowExpect{Status: 200, BodyContains: "alice@example.com"},
	}
	if err := harness.ExecuteRequest(ctx, t, loginClient, loginReq, handle.BaseURL); err != nil {
		t.Fatalf("primary login request: %v", err)
	}
}

func TestIDTokenOnlyClaims_WithheldFromUserinfo(t *testing.T) {
	idps := harnessboot.StartFakeIDPs(t, []harness.FakeIDPSpec{{
		Name: "entra", ClientID: "c", ClientSecret: "s",
		IDTokenOnlyClaims: []string{"groups"},
		Users: []harness.FakeOIDCUser{
			{Sub: "alice", Email: "alice@x", Claims: map[string]any{"groups": []string{"engineering"}}},
		},
	}})
	idp := idps["entra"]

	idToken, accessToken := authorizeAndExchangeRaw(t, idp, "alice")
	if !strings.Contains(decodeJWTClaims(t, idToken), "engineering") {
		t.Error("id_token should carry the groups claim")
	}
	if ui := getUserinfo(t, idp, accessToken); strings.Contains(ui, "engineering") || strings.Contains(ui, "groups") {
		t.Errorf("/userinfo must withhold id-token-only groups; got %s", ui)
	}
}

func authorizeAndExchangeRaw(t *testing.T, idp *harness.MockOIDCProvider, sub string) (idToken, accessToken string) {
	t.Helper()
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	authURL := fmt.Sprintf("%s/authorize?client_id=c&response_type=code&redirect_uri=%s&fake_user_sub=%s",
		idp.URL(), url.QueryEscape("http://cb/cb"), url.QueryEscape(sub))
	resp, err := noFollow.Get(authURL)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_ = resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in Location %q", resp.Header.Get("Location"))
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "http://cb/cb")
	form.Set("client_id", "c")
	form.Set("client_secret", "s")
	tokResp, err := http.PostForm(idp.URL()+"/token", form)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	defer func() { _ = tokResp.Body.Close() }()
	var tok struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokResp.Body).Decode(&tok); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tok.IDToken == "" || tok.AccessToken == "" {
		t.Fatalf("empty tokens in response: %+v", tok)
	}
	return tok.IDToken, tok.AccessToken
}

func getUserinfo(t *testing.T, idp *harness.MockOIDCProvider, accessToken string) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, idp.URL()+"/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("userinfo: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func decodeJWTClaims(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		t.Fatalf("malformed jwt: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode jwt payload: %v", err)
	}
	return string(raw)
}
