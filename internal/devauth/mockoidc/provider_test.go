// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockoidc

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
)

func decodeJWTPayload(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT (want 3 segments): %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal JWT payload: %v", err)
	}
	return claims
}

func userinfoClaims(t *testing.T, p *MockOIDCProvider, accessToken string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest("GET", p.URL()+"/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET userinfo: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("userinfo status: %d", resp.StatusCode)
	}
	var info map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode userinfo: %v", err)
	}
	return info
}

func TestMockOIDCProvider_EntraFaithfulClaimSplit(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{
			Sub: "alice@example.com", Email: "alice@example.com", Name: "Alice",
			Claims: map[string]any{
				"groups":     []string{"engineering"},
				"roles":      []string{"admin"},
				"department": "platform",
			},
		}},
	})
	t.Cleanup(func() { _ = p.Close() })
	at, idt, _ := authorizeAndExchange(t, p, "alice@example.com")

	idClaims := decodeJWTPayload(t, idt)
	if idClaims["groups"] == nil || idClaims["roles"] == nil {
		t.Errorf("id_token must carry groups+roles (Entra delivers them here): %v", idClaims)
	}
	if idClaims["department"] == nil {
		t.Errorf("id_token must carry custom claims: %v", idClaims)
	}

	info := userinfoClaims(t, p, at)
	if _, ok := info["groups"]; ok {
		t.Errorf("/userinfo must NOT carry groups (Entra omits them): %v", info)
	}
	if _, ok := info["roles"]; ok {
		t.Errorf("/userinfo must NOT carry roles (Entra omits them): %v", info)
	}
	if info["department"] != "platform" {
		t.Errorf("/userinfo must keep non-authz custom claims: %v", info)
	}
	if info["email"] != "alice@example.com" {
		t.Errorf("/userinfo must keep profile claims: %v", info)
	}
}

func TestMockOIDCProvider_UserinfoClaimsOptOut(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		IDTokenOnlyClaims: []string{},
		Users: []FakeOIDCUser{{
			Sub: "bob", Email: "bob@example.com", Name: "Bob",
			Claims: map[string]any{"groups": []string{"eng"}},
		}},
	})
	t.Cleanup(func() { _ = p.Close() })
	at, _, _ := authorizeAndExchange(t, p, "bob")
	if _, ok := userinfoClaims(t, p, at)["groups"]; !ok {
		t.Errorf("empty IDTokenOnlyClaims must surface groups via /userinfo")
	}
}

func TestMockOIDCProvider_DiscoveryDoc(t *testing.T) {
	p, err := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
	})
	if err != nil {
		t.Fatalf("NewMockOIDCProvider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	resp, err := http.Get(p.URL() + "/.well-known/openid-configuration")
	if err != nil {
		t.Fatalf("GET discovery: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"issuer", "authorization_endpoint", "token_endpoint", "userinfo_endpoint", "jwks_uri"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("discovery missing key: %s", key)
		}
	}
}

func TestMockOIDCProvider_JWKSPublishesSigningKey(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c", ClientSecret: "s"})
	t.Cleanup(func() { _ = p.Close() })

	resp, err := http.Get(p.URL() + "/jwks.json")
	if err != nil {
		t.Fatalf("GET jwks: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	keys, _ := body["keys"].([]any)
	if len(keys) != 1 {
		t.Fatalf("expected 1 JWKS key, got %d (%v)", len(keys), body)
	}
	key, _ := keys[0].(map[string]any)
	if key["kty"] != "RSA" || key["alg"] != "RS256" {
		t.Errorf("expected RSA/RS256 key, got %v", key)
	}
	if key["kid"] == "" || key["n"] == "" || key["e"] == "" {
		t.Errorf("JWKS key missing kid/n/e: %v", key)
	}
}

func TestMockOIDCProvider_AuthorizeRedirect(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		Users: []FakeOIDCUser{
			{Sub: "alice@example.com", Email: "alice@example.com", Name: "Alice"},
		},
	})
	t.Cleanup(func() { _ = p.Close() })

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	redirectURI := "http://callback.example/cb"
	u := fmt.Sprintf("%s/authorize?client_id=test-client&response_type=code&redirect_uri=%s&state=xyz&fake_user_sub=alice@example.com",
		p.URL(), url.QueryEscape(redirectURI))
	resp, err := client.Get(u)
	if err != nil {
		t.Fatalf("GET authorize: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 302 {
		t.Fatalf("status: got %d, want 302", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, redirectURI) {
		t.Fatalf("Location does not start with redirect_uri: %s", loc)
	}
	parsed, _ := url.Parse(loc)
	if parsed.Query().Get("code") == "" {
		t.Errorf("missing code in redirect")
	}
	if parsed.Query().Get("state") != "xyz" {
		t.Errorf("state not echoed back")
	}
}

func TestMockOIDCProvider_AuthorizeRejectsBadClient(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "real-client", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "alice"}},
	})
	t.Cleanup(func() { _ = p.Close() })
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, _ := client.Get(p.URL() + "/authorize?client_id=wrong&response_type=code&redirect_uri=http://x&code_challenge=c&code_challenge_method=S256&fake_user_sub=alice")
	if resp.StatusCode == 302 {
		t.Errorf("authorize must reject unknown client_id; got 302")
	}
}

func TestMockOIDCProvider_AuthorizeRejectsUnknownUser(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "alice"}},
	})
	t.Cleanup(func() { _ = p.Close() })
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, _ := client.Get(p.URL() + "/authorize?client_id=c&response_type=code&redirect_uri=http://x&code_challenge=c&code_challenge_method=S256&fake_user_sub=eve")
	if resp.StatusCode == 302 {
		t.Errorf("authorize must reject fake_user_sub not in Users; got 302")
	}
}

func authorizeAndExchange(t *testing.T, p *MockOIDCProvider, sub string) (string, string, string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	redirectURI := "http://cb.example/cb"
	u := fmt.Sprintf("%s/authorize?client_id=%s&response_type=code&redirect_uri=%s&fake_user_sub=%s",
		p.URL(), url.QueryEscape(p.config.ClientID), url.QueryEscape(redirectURI), url.QueryEscape(sub))
	resp, err := client.Get(u)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_ = resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code returned")
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", p.config.ClientID)
	form.Set("client_secret", p.config.ClientSecret)
	tokResp, err := http.PostForm(p.URL()+"/token", form)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	defer func() { _ = tokResp.Body.Close() }()
	if tokResp.StatusCode != 200 {
		body, _ := io.ReadAll(tokResp.Body)
		t.Fatalf("token status %d: %s", tokResp.StatusCode, body)
	}
	var body struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	_ = json.NewDecoder(tokResp.Body).Decode(&body)
	if body.AccessToken == "" || body.IDToken == "" || body.TokenType != "Bearer" {
		t.Fatalf("malformed token response: %+v", body)
	}
	return body.AccessToken, body.IDToken, code
}

func TestMockOIDCProvider_TokenHappyPath(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "alice", Email: "alice@example.com", Name: "Alice"}},
	})
	t.Cleanup(func() { _ = p.Close() })
	at, idt, _ := authorizeAndExchange(t, p, "alice")
	if at == "" || idt == "" {
		t.Errorf("expected non-empty tokens")
	}
}

func TestMockOIDCProvider_TokenRejectsBadClientSecret(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "real-secret",
		Users: []FakeOIDCUser{{Sub: "alice"}},
	})
	t.Cleanup(func() { _ = p.Close() })

	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	redirectURI := "http://cb/cb"
	resp, _ := client.Get(fmt.Sprintf("%s/authorize?client_id=c&response_type=code&redirect_uri=%s&fake_user_sub=alice",
		p.URL(), url.QueryEscape(redirectURI)))
	_ = resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	code := loc.Query().Get("code")

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", "c")
	form.Set("client_secret", "WRONG")
	tokResp, err := http.PostForm(p.URL()+"/token", form)
	if err != nil {
		t.Fatalf("token post: %v", err)
	}
	defer func() { _ = tokResp.Body.Close() }()
	if tokResp.StatusCode == 200 {
		t.Errorf("token must reject wrong client_secret; got 200")
	}
}

func TestMockOIDCProvider_TokenCodeIsSingleUse(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "alice"}},
	})
	t.Cleanup(func() { _ = p.Close() })
	_, _, code := authorizeAndExchange(t, p, "alice")

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "http://cb.example/cb")
	form.Set("client_id", "c")
	form.Set("client_secret", "s")
	tokResp, err := http.PostForm(p.URL()+"/token", form)
	if err != nil {
		t.Fatalf("token post: %v", err)
	}
	defer func() { _ = tokResp.Body.Close() }()
	if tokResp.StatusCode == 200 {
		t.Errorf("second exchange of same code must fail; got 200")
	}
}

func TestMockOIDCProvider_UserinfoHappyPath(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "alice@example.com", Email: "alice@example.com", Name: "Alice"}},
	})
	t.Cleanup(func() { _ = p.Close() })
	at, _, _ := authorizeAndExchange(t, p, "alice@example.com")

	req, _ := http.NewRequest("GET", p.URL()+"/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+at)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET userinfo: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var info map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&info)
	if info["sub"] != "alice@example.com" || info["email"] != "alice@example.com" {
		t.Errorf("userinfo claims: %+v", info)
	}
	if p.UserinfoCount() != 1 {
		t.Errorf("UserinfoCount: got %d, want 1", p.UserinfoCount())
	}
}

func TestMockOIDCProvider_UserinfoRejectsUnknownToken(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "alice"}},
	})
	t.Cleanup(func() { _ = p.Close() })
	req, _ := http.NewRequest("GET", p.URL()+"/userinfo", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET userinfo: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 401 {
		t.Errorf("status: got %d, want 401", resp.StatusCode)
	}
}

func TestMockOIDCProvider_IssueAccessToken(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "eve", Email: "eve@x"}},
	})
	t.Cleanup(func() { _ = p.Close() })
	tok, err := p.IssueAccessToken("eve")
	if err != nil {
		t.Fatalf("IssueAccessToken: %v", err)
	}
	req, _ := http.NewRequest("GET", p.URL()+"/userinfo", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err2 := http.DefaultClient.Do(req)
	if err2 != nil {
		t.Fatalf("GET userinfo: %v", err2)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Errorf("IssueAccessToken should produce a valid /userinfo token; got status %d", resp.StatusCode)
	}
	if _, err := p.IssueAccessToken("nobody"); err == nil {
		t.Errorf("IssueAccessToken must error on unknown sub")
	}
}

func TestMockOIDCProvider_RealClientRoundTrip(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "real-client", ClientSecret: "real-secret",
		Users: []FakeOIDCUser{{Sub: "alice@example.com", Email: "alice@example.com", Name: "Alice"}},
	})
	t.Cleanup(func() { _ = p.Close() })

	cfg := oidcauth.OIDCConfig{
		Issuer:       p.URL(),
		ClientID:     "real-client",
		ClientSecret: "real-secret",
		Scopes:       []string{"openid", "email", "profile"},
	}
	nc, err := oidcauth.NewNativeOIDCClient(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}
	if err = nc.WarmDiscovery(t.Context()); err != nil {
		t.Fatalf("WarmDiscovery: %v", err)
	}

	redirectURI := "http://cb.example/cb"
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	u := fmt.Sprintf("%s/authorize?client_id=real-client&response_type=code&redirect_uri=%s&fake_user_sub=alice@example.com",
		p.URL(), url.QueryEscape(redirectURI))
	resp, err := client.Get(u)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	code := mustQueryParam(t, resp.Header.Get("Location"), "code")

	ctx := t.Context()
	tokenResp, err := nc.ExchangeCode(ctx, code, redirectURI, "")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tokenResp.IDToken == "" {
		t.Errorf("expected non-empty id_token (gateway relies on it for claim merge in Tasks 9-13)")
	}
	if tokenResp.TokenType != "Bearer" {
		t.Errorf("token_type: got %q, want Bearer", tokenResp.TokenType)
	}
	info, err := nc.GetUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	if info.Sub != "alice@example.com" {
		t.Errorf("sub: got %q, want alice@example.com", info.Sub)
	}
	if info.Email != "alice@example.com" {
		t.Errorf("email: got %q, want alice@example.com", info.Email)
	}
	if info.Name != "Alice" {
		t.Errorf("name: got %q, want Alice", info.Name)
	}
}

func mustQueryParam(t *testing.T, location, key string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	v := u.Query().Get(key)
	if v == "" {
		t.Fatalf("missing query param %q in Location %q", key, location)
	}
	return v
}

func TestMockOIDCProvider_RefreshTokenGrant(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "real-client", ClientSecret: "real-secret",
		Users: []FakeOIDCUser{{Sub: "alice@example.com", Email: "alice@example.com", Name: "Alice"}},
	})
	t.Cleanup(func() { _ = p.Close() })

	cfg := oidcauth.OIDCConfig{Issuer: p.URL(), ClientID: "real-client", ClientSecret: "real-secret", Scopes: []string{"openid", "email", "profile"}}
	nc, err := oidcauth.NewNativeOIDCClient(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}
	if err = nc.WarmDiscovery(t.Context()); err != nil {
		t.Fatalf("WarmDiscovery: %v", err)
	}

	redirectURI := "http://cb.example/cb"
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(fmt.Sprintf("%s/authorize?client_id=real-client&response_type=code&redirect_uri=%s&fake_user_sub=alice@example.com",
		p.URL(), url.QueryEscape(redirectURI)))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_ = resp.Body.Close()
	code := mustQueryParam(t, resp.Header.Get("Location"), "code")

	ctx := t.Context()
	tok, err := nc.ExchangeCode(ctx, code, redirectURI, "")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if tok.RefreshToken == "" {
		t.Fatalf("expected a refresh_token from the code exchange")
	}

	refreshed, err := nc.RefreshToken(ctx, tok.RefreshToken)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if refreshed.AccessToken == "" || refreshed.IDToken == "" {
		t.Errorf("refresh must return a new access+id token: %+v", refreshed)
	}
	if refreshed.AccessToken == tok.AccessToken {
		t.Errorf("refresh should mint a NEW access token")
	}
	if _, err := nc.GetUserInfo(ctx, refreshed.AccessToken); err != nil {
		t.Errorf("refreshed access token rejected by /userinfo: %v", err)
	}
}

func TestMockOIDCProvider_RefreshRejectsUnknownToken(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c", ClientSecret: "s", Users: []FakeOIDCUser{{Sub: "alice"}}})
	t.Cleanup(func() { _ = p.Close() })
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", "rt_does_not_exist")
	form.Set("client_id", "c")
	form.Set("client_secret", "s")
	resp, err := http.PostForm(p.URL()+"/token", form)
	if err != nil {
		t.Fatalf("token post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == 200 {
		t.Errorf("unknown refresh_token must be rejected; got 200")
	}
}

func TestMockOIDCProvider_TokenRejectsUnknownGrant(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c", ClientSecret: "s", Users: []FakeOIDCUser{{Sub: "alice"}}})
	t.Cleanup(func() { _ = p.Close() })
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", "c")
	form.Set("client_secret", "s")
	resp, err := http.PostForm(p.URL()+"/token", form)
	if err != nil {
		t.Fatalf("token post: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 400 {
		t.Errorf("unsupported grant_type: got %d, want 400", resp.StatusCode)
	}
}

func TestMockOIDCProvider_PKCE(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c", ClientSecret: "s", Users: []FakeOIDCUser{{Sub: "alice"}}})
	t.Cleanup(func() { _ = p.Close() })

	verifier := "this-is-a-long-enough-code-verifier-string-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	getCode := func() string {
		client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		u := fmt.Sprintf("%s/authorize?client_id=c&response_type=code&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&fake_user_sub=alice",
			p.URL(), url.QueryEscape("http://cb/cb"), url.QueryEscape(challenge))
		resp, err := client.Get(u)
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		_ = resp.Body.Close()
		return mustQueryParam(t, resp.Header.Get("Location"), "code")
	}
	exchange := func(code, verifier string) int {
		form := url.Values{}
		form.Set("grant_type", "authorization_code")
		form.Set("code", code)
		form.Set("redirect_uri", "http://cb/cb")
		form.Set("client_id", "c")
		form.Set("client_secret", "s")
		if verifier != "" {
			form.Set("code_verifier", verifier)
		}
		resp, err := http.PostForm(p.URL()+"/token", form)
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if got := exchange(getCode(), verifier); got != 200 {
		t.Errorf("correct PKCE verifier: got %d, want 200", got)
	}
	if got := exchange(getCode(), "wrong-verifier"); got == 200 {
		t.Errorf("wrong PKCE verifier must be rejected; got 200")
	}
	if got := exchange(getCode(), ""); got == 200 {
		t.Errorf("missing PKCE verifier (challenge present) must be rejected; got 200")
	}
}

func TestMockOIDCProvider_PublicClientNeedsPKCE(t *testing.T) {
	p, err := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c", PublicClient: true, Users: []FakeOIDCUser{{Sub: "alice"}}})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })

	verifier := "this-is-a-long-enough-code-verifier-string-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	getCode := func(challenge string) string {
		client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		u := fmt.Sprintf("%s/authorize?client_id=c&response_type=code&redirect_uri=%s&fake_user_sub=alice",
			p.URL(), url.QueryEscape("http://cb/cb"))
		if challenge != "" {
			u += "&code_challenge_method=S256&code_challenge=" + url.QueryEscape(challenge)
		}
		resp, err := client.Get(u)
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		_ = resp.Body.Close()
		return mustQueryParam(t, resp.Header.Get("Location"), "code")
	}
	exchange := func(code, verifier string) int {
		form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {"http://cb/cb"}, "client_id": {"c"}}
		if verifier != "" {
			form.Set("code_verifier", verifier)
		}
		resp, err := http.PostForm(p.URL()+"/token", form)
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if got := exchange(getCode(challenge), verifier); got != http.StatusOK {
		t.Errorf("public client with PKCE: got %d, want 200", got)
	}
	if got := exchange(getCode(""), ""); got == http.StatusOK {
		t.Errorf("public client without PKCE must be rejected; got 200")
	}
	if _, err := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c", ClientSecret: "s", PublicClient: true}); err == nil {
		t.Errorf("a public client with a secret must be refused")
	}
}

func TestMockOIDCProvider_PKCEThroughRealClient(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "real-client", ClientSecret: "real-secret",
		Users: []FakeOIDCUser{{Sub: "alice@example.com", Email: "alice@example.com"}},
	})
	t.Cleanup(func() { _ = p.Close() })

	cfg := oidcauth.OIDCConfig{Issuer: p.URL(), ClientID: "real-client", ClientSecret: "real-secret", Scopes: []string{"openid", "email", "profile"}}
	nc, err := oidcauth.NewNativeOIDCClient(cfg, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}
	if err = nc.WarmDiscovery(t.Context()); err != nil {
		t.Fatalf("WarmDiscovery: %v", err)
	}

	verifier := "this-is-a-long-enough-code-verifier-string-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirectURI := "http://cb.example/cb"

	getCode := func() string {
		client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
		u := fmt.Sprintf("%s/authorize?client_id=real-client&response_type=code&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&fake_user_sub=alice@example.com",
			p.URL(), url.QueryEscape(redirectURI), url.QueryEscape(challenge))
		resp, err := client.Get(u)
		if err != nil {
			t.Fatalf("authorize: %v", err)
		}
		_ = resp.Body.Close()
		return mustQueryParam(t, resp.Header.Get("Location"), "code")
	}

	ctx := t.Context()
	if _, err := nc.ExchangeCode(ctx, getCode(), redirectURI, verifier); err != nil {
		t.Errorf("ExchangeCode with correct verifier should succeed: %v", err)
	}
	if _, err := nc.ExchangeCode(ctx, getCode(), redirectURI, "wrong-verifier"); err == nil {
		t.Errorf("ExchangeCode with wrong verifier must fail")
	}
}

func TestMockOIDCProvider_PKCEFailureDoesNotBurnCode(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c", ClientSecret: "s", Users: []FakeOIDCUser{{Sub: "alice"}}})
	t.Cleanup(func() { _ = p.Close() })

	verifier := "this-is-a-long-enough-code-verifier-string-1234567890"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(fmt.Sprintf("%s/authorize?client_id=c&response_type=code&redirect_uri=%s&code_challenge=%s&code_challenge_method=S256&fake_user_sub=alice",
		p.URL(), url.QueryEscape("http://cb/cb"), url.QueryEscape(challenge)))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_ = resp.Body.Close()
	code := mustQueryParam(t, resp.Header.Get("Location"), "code")

	exchange := func(verifier string) int {
		form := url.Values{}
		form.Set("grant_type", "authorization_code")
		form.Set("code", code)
		form.Set("redirect_uri", "http://cb/cb")
		form.Set("client_id", "c")
		form.Set("client_secret", "s")
		form.Set("code_verifier", verifier)
		r, err := http.PostForm(p.URL()+"/token", form)
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		_ = r.Body.Close()
		return r.StatusCode
	}

	if got := exchange("wrong-verifier"); got == 200 {
		t.Fatalf("wrong verifier must be rejected; got 200")
	}
	if got := exchange(verifier); got != 200 {
		t.Errorf("code must remain usable after a failed PKCE attempt: got %d, want 200", got)
	}
}

func TestMockOIDCProvider_PKCEPlain(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c", ClientSecret: "s", Users: []FakeOIDCUser{{Sub: "alice"}}})
	t.Cleanup(func() { _ = p.Close() })

	secret := "plain-verifier-equals-challenge"
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(fmt.Sprintf("%s/authorize?client_id=c&response_type=code&redirect_uri=%s&code_challenge=%s&code_challenge_method=plain&fake_user_sub=alice",
		p.URL(), url.QueryEscape("http://cb/cb"), url.QueryEscape(secret)))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_ = resp.Body.Close()
	code := mustQueryParam(t, resp.Header.Get("Location"), "code")

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "http://cb/cb")
	form.Set("client_id", "c")
	form.Set("client_secret", "s")
	form.Set("code_verifier", secret)
	tr, err := http.PostForm(p.URL()+"/token", form)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	defer func() { _ = tr.Body.Close() }()
	if tr.StatusCode != 200 {
		t.Errorf("plain PKCE with matching verifier: got %d, want 200", tr.StatusCode)
	}
}

func TestMockOIDCProvider_NonceEchoedIntoIDToken(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c", ClientSecret: "s", Users: []FakeOIDCUser{{Sub: "alice", Email: "alice@x"}}})
	t.Cleanup(func() { _ = p.Close() })

	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(fmt.Sprintf("%s/authorize?client_id=c&response_type=code&redirect_uri=%s&nonce=n-12345&fake_user_sub=alice",
		p.URL(), url.QueryEscape("http://cb/cb")))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_ = resp.Body.Close()
	code := mustQueryParam(t, resp.Header.Get("Location"), "code")

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", "http://cb/cb")
	form.Set("client_id", "c")
	form.Set("client_secret", "s")
	tokResp, err := http.PostForm(p.URL()+"/token", form)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	defer func() { _ = tokResp.Body.Close() }()
	var body struct {
		IDToken string `json:"id_token"`
	}
	_ = json.NewDecoder(tokResp.Body).Decode(&body)
	claims := decodeJWTPayload(t, body.IDToken)
	if claims["nonce"] != "n-12345" {
		t.Errorf("id_token nonce: got %v, want n-12345", claims["nonce"])
	}
}

func TestMockOIDCProvider_RedirectURIWithQueryMerged(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{ClientID: "c", ClientSecret: "s", Users: []FakeOIDCUser{{Sub: "alice"}}})
	t.Cleanup(func() { _ = p.Close() })
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	redirectURI := "http://cb/cb?tenant=acme"
	resp, err := client.Get(fmt.Sprintf("%s/authorize?client_id=c&response_type=code&redirect_uri=%s&state=xyz&fake_user_sub=alice",
		p.URL(), url.QueryEscape(redirectURI)))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	loc := resp.Header.Get("Location")
	if strings.Count(loc, "?") != 1 {
		t.Errorf("redirect must contain exactly one '?': %s", loc)
	}
	parsed, _ := url.Parse(loc)
	q := parsed.Query()
	if q.Get("tenant") != "acme" {
		t.Errorf("existing redirect_uri query lost: %s", loc)
	}
	if q.Get("code") == "" || q.Get("state") != "xyz" {
		t.Errorf("code/state not merged into redirect_uri query: %s", loc)
	}
}

func TestMockOIDCProvider_EntraShapedClaims(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "u-1", Email: "u1@example.com", Name: "User One"}},
	})
	t.Cleanup(func() { _ = p.Close() })
	_, idt, _ := authorizeAndExchange(t, p, "u-1")
	claims := decodeJWTPayload(t, idt)
	for _, k := range []string{"tid", "oid", "preferred_username", "ver", "nbf"} {
		if claims[k] == nil {
			t.Errorf("id_token missing Entra claim %q: %v", k, claims)
		}
	}
	if claims["tid"] != defaultTenantID {
		t.Errorf("tid: got %v, want default tenant %q", claims["tid"], defaultTenantID)
	}
	if claims["oid"] != "u-1" {
		t.Errorf("oid should default to sub: got %v", claims["oid"])
	}
	if claims["preferred_username"] != "u1@example.com" {
		t.Errorf("preferred_username should default to email: got %v", claims["preferred_username"])
	}
	if claims["ver"] != "2.0" {
		t.Errorf("ver: got %v, want 2.0", claims["ver"])
	}
}

func TestMockOIDCProvider_TenantIDAndClaimOverride(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s", TenantID: "tenant-xyz",
		Users: []FakeOIDCUser{{Sub: "u-2", Email: "u2@x", Claims: map[string]any{"oid": "real-guid"}}},
	})
	t.Cleanup(func() { _ = p.Close() })
	_, idt, _ := authorizeAndExchange(t, p, "u-2")
	claims := decodeJWTPayload(t, idt)
	if claims["tid"] != "tenant-xyz" {
		t.Errorf("configured TenantID not used for tid: %v", claims["tid"])
	}
	if claims["oid"] != "real-guid" {
		t.Errorf("per-user Claims oid must override the default: %v", claims["oid"])
	}
}
