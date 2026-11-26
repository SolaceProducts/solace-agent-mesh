// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcpauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust/trusttest"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	return New(Config{
		EnableAuth:           true,
		ExternalAuthProvider: "azure",
		GatewayID:            "test-gw",
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       &fakeMinter{},
		AuthzResolver:        fakeResolver{},
		RequirePKCE:          true,
		OAuthStateTTLSeconds: 300,
		OAuthCodeTTLSeconds:  300,
		Host:                 "localhost",
		Port:                 8090,
	})
}

func TestComputeS256Challenge(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	expected := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

	got := ComputeS256Challenge(verifier)
	if got != expected {
		t.Errorf("ComputeS256Challenge(%q) = %q, want %q", verifier, got, expected)
	}
}

func TestVerifyPKCE_RequiredAndValid(t *testing.T) {
	h := newTestHandler(t)
	verifier := "test-verifier-value-12345"
	challenge := ComputeS256Challenge(verifier)

	if err := h.verifyPKCE(challenge, verifier); err != nil {
		t.Errorf("verifyPKCE should succeed: %v", err)
	}
}

func TestVerifyPKCE_RequiredMissingChallenge(t *testing.T) {
	h := newTestHandler(t)
	if err := h.verifyPKCE("", "some-verifier"); err == nil {
		t.Error("verifyPKCE should fail when challenge is missing and PKCE required")
	}
}

func TestVerifyPKCE_RequiredMissingVerifier(t *testing.T) {
	h := newTestHandler(t)
	if err := h.verifyPKCE("some-challenge", ""); err == nil {
		t.Error("verifyPKCE should fail when verifier is missing and PKCE required")
	}
}

func TestVerifyPKCE_RequiredWrongVerifier(t *testing.T) {
	h := newTestHandler(t)
	challenge := ComputeS256Challenge("correct-verifier")
	if err := h.verifyPKCE(challenge, "wrong-verifier"); err == nil {
		t.Error("verifyPKCE should fail with wrong verifier")
	}
}

func TestVerifyPKCE_OptionalNoChallengeSkips(t *testing.T) {
	h := New(Config{
		EnableAuth:  true,
		RequirePKCE: false,
		Host:        "localhost",
		Port:        8090,
	})
	if err := h.verifyPKCE("", ""); err != nil {
		t.Errorf("verifyPKCE should skip when PKCE not required and no challenge: %v", err)
	}
}

func TestVerifyPKCE_OptionalWithChallengeValidates(t *testing.T) {
	h := New(Config{
		EnableAuth:  true,
		RequirePKCE: false,
		Host:        "localhost",
		Port:        8090,
	})
	verifier := "optional-verifier"
	challenge := ComputeS256Challenge(verifier)

	if err := h.verifyPKCE(challenge, verifier); err != nil {
		t.Errorf("verifyPKCE should succeed for optional PKCE: %v", err)
	}
	if err := h.verifyPKCE(challenge, "wrong"); err == nil {
		t.Error("verifyPKCE should fail for optional PKCE with wrong verifier")
	}
}

func TestStateExpiry(t *testing.T) {
	h := New(Config{
		EnableAuth:           true,
		RequirePKCE:          true,
		OAuthStateTTLSeconds: 1,
		Host:                 "localhost",
		Port:                 8090,
	})

	mem(h).mu.Lock()
	mem(h).states["expired"] = &oauthState{
		CreatedAt: time.Now().Add(-2 * time.Second),
		TTL:       1 * time.Second,
	}
	mem(h).states["valid"] = &oauthState{
		CreatedAt: time.Now(),
		TTL:       300 * time.Second,
	}
	mem(h).cleanupExpiredStatesLocked(time.Now())
	mem(h).mu.Unlock()

	if mem(h).statesLen() != 1 {
		t.Errorf("expected 1 state after cleanup, got %d", mem(h).statesLen())
	}
}

func TestCodeExpiry(t *testing.T) {
	h := newTestHandler(t)

	mem(h).mu.Lock()
	mem(h).codes["expired"] = &oauthCode{
		CreatedAt: time.Now().Add(-10 * time.Minute),
		TTL:       5 * time.Minute,
	}
	mem(h).codes["valid"] = &oauthCode{
		CreatedAt: time.Now(),
		TTL:       5 * time.Minute,
	}
	mem(h).cleanupExpiredCodesLocked(time.Now())
	mem(h).mu.Unlock()

	if mem(h).codesLen() != 1 {
		t.Errorf("expected 1 code after cleanup, got %d", mem(h).codesLen())
	}
}

func TestAuthorizationCodeSingleUse(t *testing.T) {
	h := newTestHandler(t)

	verifier := "test-code-verifier-for-single-use"
	challenge := ComputeS256Challenge(verifier)

	mem(h).mu.Lock()
	mem(h).codes["test-code"] = &oauthCode{
		AccessToken:     "at_123",
		IDPRefreshToken: "rt_456",
		CodeChallenge:   challenge,
		RedirectURI:     "http://localhost:9999/callback",
		CreatedAt:       time.Now(),
		TTL:             300 * time.Second,
	}
	mem(h).mu.Unlock()

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", "test-code")
	form.Set("redirect_uri", "http://localhost:9999/callback")
	form.Set("code_verifier", verifier)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleToken(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("first exchange: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var tokenResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if tokenResp["access_token"] != "at_123" {
		t.Errorf("expected access_token 'at_123', got %v", tokenResp["access_token"])
	}

	req2 := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w2 := httptest.NewRecorder()
	h.HandleToken(w2, req2)

	if w2.Code != http.StatusBadRequest {
		t.Errorf("second exchange: expected 400, got %d", w2.Code)
	}
}

func TestMetadataResponse(t *testing.T) {
	h := newTestHandler(t)

	t.Setenv("MCP_EXTERNAL_BASE_URL", "https://mcp.example.com")

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	w := httptest.NewRecorder()
	h.HandleMetadata(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var meta map[string]any
	if err := json.NewDecoder(w.Body).Decode(&meta); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}

	checks := map[string]string{
		"issuer":                 "https://mcp.example.com",
		"authorization_endpoint": "https://mcp.example.com/oauth/authorize",
		"token_endpoint":         "https://mcp.example.com/oauth/token",
		"registration_endpoint":  "https://mcp.example.com/oauth/register",
	}
	for key, want := range checks {
		if got, _ := meta[key].(string); got != want {
			t.Errorf("metadata[%q] = %q, want %q", key, got, want)
		}
	}

	if meta["require_pkce"] != true {
		t.Error("expected require_pkce=true in metadata")
	}
}

func TestRegisterEndpoint(t *testing.T) {
	h := newTestHandler(t)

	body := `{"redirect_uris": ["http://localhost:9999/callback"]}`
	req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.HandleRegister(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if resp["client_id"] == nil || resp["client_id"] == "" {
		t.Error("expected non-empty client_id")
	}
	if resp["client_secret"] == nil || resp["client_secret"] == "" {
		t.Error("expected non-empty client_secret")
	}
	if resp["require_pkce"] != true {
		t.Error("expected require_pkce=true")
	}
	if resp["redirect_uris"] == nil {
		t.Error("expected redirect_uris to be echoed back")
	}
	assertNotCacheable(t, w)
}

func assertNotCacheable(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := w.Header().Get("Pragma"); got != "no-cache" {
		t.Errorf("Pragma = %q, want no-cache", got)
	}
}

func TestAuthorize_MissingRedirectURI(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize", nil)
	w := httptest.NewRecorder()
	h.HandleAuthorize(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestAuthorize_MissingPKCE(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?redirect_uri=http://localhost/cb&state=xyz", nil)
	w := httptest.NewRecorder()
	h.HandleAuthorize(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestAuthorize_UnsupportedChallengeMethod(t *testing.T) {
	h := newTestHandler(t)

	q := url.Values{}
	q.Set("redirect_uri", "http://localhost/cb")
	q.Set("code_challenge", "abc")
	q.Set("code_challenge_method", "plain")
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	h.HandleAuthorize(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestAuthorize_ValidRedirect(t *testing.T) {
	h := newTestHandler(t)

	q := url.Values{}
	q.Set("redirect_uri", "http://localhost:9999/callback")
	q.Set("state", "client-state")
	q.Set("code_challenge", "test-challenge")
	q.Set("code_challenge_method", "S256")
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil)
	w := httptest.NewRecorder()
	h.HandleAuthorize(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d: %s", w.Code, w.Body.String())
	}

	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://idp.example.com/") {
		t.Errorf("expected redirect to IdP, got %s", loc)
	}
	if mem(h).statesLen() != 1 {
		t.Errorf("expected 1 state, got %d", mem(h).statesLen())
	}
}

func TestHandleAuthorize_RedirectsToIDP(t *testing.T) {
	h := New(Config{
		EnableAuth:           true,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		ExternalAuthProvider: "azure",
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       &fakeMinter{},
		AuthzResolver:        fakeResolver{},
		OAuthStateTTLSeconds: 300,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		RequirePKCE:          true,
	})
	req := httptest.NewRequest("GET",
		"/oauth/authorize?response_type=code&client_id=c1"+
			"&redirect_uri=http://client/cb&state=client-state"+
			"&code_challenge=abcdef&code_challenge_method=S256", nil)
	rr := httptest.NewRecorder()
	h.HandleAuthorize(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://idp.example.com/") {
		t.Fatalf("Location = %q (expected IdP URL from fakeIDPClient)", loc)
	}
	if mem(h).statesLen() != 1 {
		t.Fatalf("expected 1 state stored, got %d", mem(h).statesLen())
	}
	mem(h).mu.Lock()
	var hadVerifier bool
	for _, s := range mem(h).states {
		if s.CodeVerifier != "" {
			hadVerifier = true
		}
	}
	mem(h).mu.Unlock()
	if !hadVerifier {
		t.Error("oauthState.CodeVerifier was empty — HandleAuthorize must generate gateway-side verifier")
	}
}

func TestToken_UnsupportedGrantType(t *testing.T) {
	h := newTestHandler(t)

	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleToken(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "unsupported_grant_type" {
		t.Errorf("expected error 'unsupported_grant_type', got %q", resp["error"])
	}
}

func TestToken_RedirectURIMismatch(t *testing.T) {
	h := newTestHandler(t)

	verifier := "verifier123"
	challenge := ComputeS256Challenge(verifier)

	mem(h).mu.Lock()
	mem(h).codes["code1"] = &oauthCode{
		AccessToken:   "at",
		CodeChallenge: challenge,
		RedirectURI:   "http://localhost/correct",
		CreatedAt:     time.Now(),
		TTL:           300 * time.Second,
	}
	mem(h).mu.Unlock()

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", "code1")
	form.Set("redirect_uri", "http://localhost/wrong")
	form.Set("code_verifier", verifier)
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleToken(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleCallback_DirectIdPExchange(t *testing.T) {
	minter := &fakeMinter{}
	h := New(Config{
		EnableAuth:           true,
		Host:                 "127.0.0.1",
		Port:                 8090,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       minter,
		AuthzResolver:        fakeResolver{},
		OAuthStateTTLSeconds: 300,
		OAuthCodeTTLSeconds:  300,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	state := "state-internal-abc"
	mem(h).mu.Lock()
	mem(h).states[state] = &oauthState{
		ClientRedirectURI:   "http://client.example/cb",
		ClientState:         "client-state-xyz",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		CodeVerifier:        "verifier",
		CreatedAt:           time.Now(),
		TTL:                 5 * time.Minute,
	}
	mem(h).mu.Unlock()

	req := httptest.NewRequest("GET",
		"/oauth/callback?code=idp-authcode&state="+state, nil)
	rr := httptest.NewRecorder()
	h.HandleCallback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://client.example/cb?") {
		t.Fatalf("Location = %q", loc)
	}
	if !strings.Contains(loc, "state=client-state-xyz") {
		t.Errorf("missing client state: %q", loc)
	}
	if !strings.Contains(loc, "code=") {
		t.Errorf("missing code: %q", loc)
	}

	if minter.LastUserID != "u@example.com" {
		t.Errorf("LastUserID = %q (want u@example.com — email-first canonical user_id)", minter.LastUserID)
	}
	if minter.LastEmail != "u@example.com" {
		t.Errorf("LastEmail = %q (want u@example.com)", minter.LastEmail)
	}
	if len(minter.LastRoles) != 1 || minter.LastRoles[0] != "viewer" {
		t.Errorf("LastRoles = %v (want [viewer])", minter.LastRoles)
	}
}

type recordingResolver struct {
	gotUserID  string
	gotContext map[string]any
	roles      []string
	scopes     []string
	platformID string
	err        error
}

func (r *recordingResolver) Lookup(_ context.Context, userID string, ctx map[string]any) (roles, scopes []string, platformUserID string, err error) {
	r.gotUserID = userID
	r.gotContext = ctx
	return r.roles, r.scopes, r.platformID, r.err
}

type noEmailIDPClient struct{ fakeIDPClient }

func (noEmailIDPClient) GetUserInfo(_ context.Context, _ string) (*UserInfo, error) {
	return &UserInfo{Sub: "u-sub-only", Email: "unknown", Name: "U"}, nil
}

func TestHandleCallback_ResolvesRolesWithCanonicalUserID(t *testing.T) {
	cases := []struct {
		name       string
		idpClient  IDPClient
		wantUserID string
	}{
		{"email-first", fakeIDPClient{}, "u@example.com"},
		{"sub-fallback", noEmailIDPClient{}, "u-sub-only"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &recordingResolver{roles: []string{"editor"}}
			minter := &fakeMinter{}
			h := New(Config{
				EnableAuth:           true,
				Host:                 "127.0.0.1",
				Port:                 8090,
				Issuer:               "http://127.0.0.1:8090",
				GatewayID:            "test-gw",
				IDPClient:            tc.idpClient,
				SAMTokenMinter:       minter,
				AuthzResolver:        resolver,
				OAuthStateTTLSeconds: 300,
				OAuthCodeTTLSeconds:  300,
				Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
			})

			state := "state-canonical-" + tc.name
			mem(h).mu.Lock()
			mem(h).states[state] = &oauthState{
				ClientRedirectURI:   "http://client.example/cb",
				ClientState:         "client-state",
				CodeChallenge:       "challenge",
				CodeChallengeMethod: "S256",
				CodeVerifier:        "verifier",
				CreatedAt:           time.Now(),
				TTL:                 5 * time.Minute,
			}
			mem(h).mu.Unlock()

			req := httptest.NewRequest("GET", "/oauth/callback?code=idp-authcode&state="+state, nil)
			rr := httptest.NewRecorder()
			h.HandleCallback(rr, req)

			if rr.Code != http.StatusFound {
				t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
			}
			if resolver.gotUserID != tc.wantUserID {
				t.Errorf("ResolveRoles user_id = %q, want %q", resolver.gotUserID, tc.wantUserID)
			}
			if minter.LastUserID != tc.wantUserID {
				t.Errorf("MintForUser user_id = %q, want %q", minter.LastUserID, tc.wantUserID)
			}
			if len(minter.LastRoles) != 1 || minter.LastRoles[0] != "editor" {
				t.Errorf("LastRoles = %v, want [editor]", minter.LastRoles)
			}
		})
	}
}

type errIDPClient struct{ fakeIDPClient }

func (errIDPClient) ExchangeCode(ctx context.Context, code, redirectURI, codeVerifier string) (*IDPTokens, error) {
	return nil, errors.New("idp says no")
}

func TestHandleCallback_ErrorRedirectsToClient(t *testing.T) {
	h := New(Config{
		EnableAuth:           true,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		IDPClient:            errIDPClient{},
		SAMTokenMinter:       &fakeMinter{},
		AuthzResolver:        fakeResolver{},
		OAuthStateTTLSeconds: 300,
		OAuthCodeTTLSeconds:  300,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	state := "state-internal-err"
	mem(h).mu.Lock()
	mem(h).states[state] = &oauthState{
		ClientRedirectURI: "http://client.example/cb",
		ClientState:       "client-state-zzz",
		CodeChallenge:     "challenge",
		CodeVerifier:      "verifier",
		CreatedAt:         time.Now(),
		TTL:               5 * time.Minute,
	}
	mem(h).mu.Unlock()

	req := httptest.NewRequest("GET", "/oauth/callback?code=anything&state="+state, nil)
	rr := httptest.NewRecorder()
	h.HandleCallback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d (want 302), body = %s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "http://client.example/cb?") {
		t.Fatalf("Location = %q (want client redirect)", loc)
	}
	if !strings.Contains(loc, "error=server_error") {
		t.Errorf("missing error code: %q", loc)
	}
	if !strings.Contains(loc, "state=client-state-zzz") {
		t.Errorf("missing client state: %q", loc)
	}
}

func TestCallback_MissingCode(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?state=abc", nil)
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCallback_InvalidState(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=xyz&state=nonexistent", nil)
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCallback_ExpiredState(t *testing.T) {
	h := newTestHandler(t)

	mem(h).mu.Lock()
	mem(h).states["expired-state"] = &oauthState{
		ClientRedirectURI: "http://localhost/cb",
		CreatedAt:         time.Now().Add(-10 * time.Minute),
		TTL:               1 * time.Second,
	}
	mem(h).mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=xyz&state=expired-state", nil)
	w := httptest.NewRecorder()
	h.HandleCallback(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	if mem(h).statesLen() != 0 {
		t.Error("expired state should be deleted")
	}
}

func TestParseConfig(t *testing.T) {
	m := map[string]any{
		"enable_auth":             true,
		"external_auth_provider":  "google",
		"require_pkce":            false,
		"oauth_state_ttl_seconds": 600,
		"host":                    "0.0.0.0",
		"port":                    9090,
	}

	cfg := ParseConfig(m)

	if !cfg.EnableAuth {
		t.Error("expected EnableAuth=true")
	}
	if cfg.ExternalAuthProvider != "google" {
		t.Errorf("expected provider 'google', got %s", cfg.ExternalAuthProvider)
	}
	if cfg.RequirePKCE {
		t.Error("expected RequirePKCE=false")
	}
	if cfg.OAuthStateTTLSeconds != 600 {
		t.Errorf("expected TTL 600, got %d", cfg.OAuthStateTTLSeconds)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("expected host '0.0.0.0', got %s", cfg.Host)
	}
	if cfg.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Port)
	}
}

func TestParseConfig_Defaults(t *testing.T) {
	cfg := ParseConfig(map[string]any{})

	if cfg.EnableAuth {
		t.Error("expected EnableAuth=false by default")
	}
	if !cfg.RequirePKCE {
		t.Error("expected RequirePKCE=true by default")
	}
	if cfg.OAuthStateTTLSeconds != 300 {
		t.Errorf("expected default TTL 300, got %d", cfg.OAuthStateTTLSeconds)
	}
}

func TestFullFlow_AuthorizeCallbackToken(t *testing.T) {
	minter := &fakeMinter{}
	h := New(Config{
		EnableAuth:           true,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		ExternalAuthProvider: "azure",
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       minter,
		AuthzResolver:        fakeResolver{},
		OAuthStateTTLSeconds: 300,
		OAuthCodeTTLSeconds:  300,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		RequirePKCE:          true,
	})

	clientVerifier := "client-verifier-abc-1234567890"
	clientChallenge := ComputeS256Challenge(clientVerifier)

	authorizeReq := httptest.NewRequest("GET",
		"/oauth/authorize?response_type=code&client_id=c1"+
			"&redirect_uri=http://client/cb&state=client-state"+
			"&code_challenge="+clientChallenge+"&code_challenge_method=S256", nil)
	authorizeRR := httptest.NewRecorder()
	h.HandleAuthorize(authorizeRR, authorizeReq)
	if authorizeRR.Code != http.StatusFound {
		t.Fatalf("authorize: status = %d, body = %s", authorizeRR.Code, authorizeRR.Body.String())
	}

	mem(h).mu.Lock()
	var internalState string
	for s := range mem(h).states {
		internalState = s
	}
	mem(h).mu.Unlock()
	if internalState == "" {
		t.Fatal("no internal state stored after authorize")
	}

	callbackReq := httptest.NewRequest("GET",
		"/oauth/callback?code=idp-authcode&state="+internalState, nil)
	callbackRR := httptest.NewRecorder()
	h.HandleCallback(callbackRR, callbackReq)
	if callbackRR.Code != http.StatusFound {
		t.Fatalf("callback: status = %d, body = %s", callbackRR.Code, callbackRR.Body.String())
	}

	redirect, err := url.Parse(callbackRR.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	mcpCode := redirect.Query().Get("code")
	if mcpCode == "" {
		t.Fatalf("missing MCP code in callback redirect: %s", redirect)
	}

	body := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {mcpCode},
		"redirect_uri":  {"http://client/cb"},
		"code_verifier": {clientVerifier},
	}
	tokenReq := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(body.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRR := httptest.NewRecorder()
	h.HandleToken(tokenRR, tokenReq)
	if tokenRR.Code != http.StatusOK {
		t.Fatalf("token: status = %d, body = %s", tokenRR.Code, tokenRR.Body.String())
	}
	assertNotCacheable(t, tokenRR)

	var tokenResp map[string]any
	if err := json.Unmarshal(tokenRR.Body.Bytes(), &tokenResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if tokenResp["access_token"] != "sam-token" {
		t.Errorf("access_token = %v, want sam-token", tokenResp["access_token"])
	}
	mcpRT, _ := tokenResp["refresh_token"].(string)
	if mcpRT == "" {
		t.Errorf("refresh_token missing or empty")
	}
	if mcpRT == "idp-rt" {
		t.Errorf("refresh_token MUST be opaque, not the IdP's RT — got %q", mcpRT)
	}
	if tokenResp["token_type"] != "Bearer" {
		t.Errorf("token_type = %v, want Bearer", tokenResp["token_type"])
	}

	if minter.LastUserID != "u@example.com" {
		t.Errorf("minter.LastUserID = %q (want u@example.com)", minter.LastUserID)
	}
	if len(minter.LastRoles) != 1 || minter.LastRoles[0] != "viewer" {
		t.Errorf("minter.LastRoles = %v (want [viewer])", minter.LastRoles)
	}
}

func TestRefreshToken_Missing(t *testing.T) {
	h := newTestHandler(t)

	form := url.Values{}
	form.Set("grant_type", "refresh_token")

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleToken(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleRefresh_RemintsSAMToken(t *testing.T) {
	minter := &fakeMinter{}
	h := New(Config{
		EnableAuth:           true,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		ExternalAuthProvider: "azure",
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       minter,
		AuthzResolver:        fakeResolver{},
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	const mcpRT = "mcp-opaque-rt"
	const idpRT = "idp-rt-original"
	mem(h).mu.Lock()
	mem(h).refreshTokens[mcpRT] = &mcpRefreshTokenEntry{
		IDPRefreshToken: idpRT,
		CreatedAt:       time.Now(),
		TTL:             time.Hour,
	}
	mem(h).mu.Unlock()

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {mcpRT},
	}
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.HandleToken(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	assertNotCacheable(t, rr)
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["access_token"] != "sam-token" {
		t.Errorf("access_token = %v, want sam-token", resp["access_token"])
	}
	newRT, _ := resp["refresh_token"].(string)
	if newRT == "" {
		t.Errorf("refresh_token missing or empty")
	}
	if newRT == "idp-rt-2" {
		t.Errorf("refresh_token leaked the IdP's RT — got %q", newRT)
	}
	if newRT == mcpRT {
		t.Errorf("refresh_token must rotate on refresh; got the same value back: %q", newRT)
	}
	mem(h).mu.Lock()
	if _, stillThere := mem(h).refreshTokens[mcpRT]; stillThere {
		t.Errorf("old opaque RT must be deleted on rotation")
	}
	if entry, ok := mem(h).refreshTokens[newRT]; !ok {
		t.Errorf("new opaque RT not stored")
	} else if entry.IDPRefreshToken != "idp-rt-2" {
		t.Errorf("new entry IDPRefreshToken = %q, want idp-rt-2", entry.IDPRefreshToken)
	}
	mem(h).mu.Unlock()
	if resp["token_type"] != "Bearer" {
		t.Errorf("token_type = %v", resp["token_type"])
	}
	if minter.LastUserID != "u@example.com" {
		t.Errorf("LastUserID = %q (want u@example.com)", minter.LastUserID)
	}
	if len(minter.LastRoles) != 1 || minter.LastRoles[0] != "viewer" {
		t.Errorf("LastRoles = %v (want [viewer])", minter.LastRoles)
	}
}

func TestHandleRefresh_RejectsUnknownRefreshToken(t *testing.T) {
	minter := &fakeMinter{}
	h := New(Config{
		EnableAuth:           true,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		ExternalAuthProvider: "azure",
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       minter,
		AuthzResolver:        fakeResolver{},
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {"some-IdP-RT-from-elsewhere"},
	}
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.HandleToken(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (want 400), body = %s", rr.Code, rr.Body.String())
	}
	if minter.LastUserID != "" {
		t.Errorf("minter must not be called when refresh_token is unknown")
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "invalid_grant" {
		t.Errorf("error = %v (want invalid_grant)", resp["error"])
	}
}

func TestHandleRefresh_RejectsExpiredRefreshToken(t *testing.T) {
	h := New(Config{
		EnableAuth:           true,
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       &fakeMinter{},
		AuthzResolver:        fakeResolver{},
		ExternalAuthProvider: "azure",
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	const mcpRT = "stale-opaque-rt"
	mem(h).mu.Lock()
	mem(h).refreshTokens[mcpRT] = &mcpRefreshTokenEntry{
		IDPRefreshToken: "idp-rt-original",
		CreatedAt:       time.Now().Add(-2 * time.Hour),
		TTL:             time.Hour,
	}
	mem(h).mu.Unlock()

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {mcpRT},
	}
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.HandleToken(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (want 400)", rr.Code)
	}
	mem(h).mu.Lock()
	if _, stillThere := mem(h).refreshTokens[mcpRT]; stillThere {
		t.Errorf("expired entry must be deleted")
	}
	mem(h).mu.Unlock()
}

type sentinelSubIDPClient struct{ fakeIDPClient }

func (sentinelSubIDPClient) GetUserInfo(_ context.Context, _ string) (*UserInfo, error) {
	return &UserInfo{Sub: "Unknown", Email: "u@example.com", Name: "U"}, nil
}

type sentinelProfileIDPClient struct{ fakeIDPClient }

func (sentinelProfileIDPClient) GetUserInfo(_ context.Context, _ string) (*UserInfo, error) {
	return &UserInfo{Sub: "real-user-7", Email: "unknown", Name: "Unknown"}, nil
}

func TestHandleRefresh_RejectsSentinelSub(t *testing.T) {
	minter := &fakeMinter{}
	h := New(Config{
		EnableAuth:           true,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		ExternalAuthProvider: "azure",
		IDPClient:            sentinelSubIDPClient{},
		SAMTokenMinter:       minter,
		AuthzResolver:        fakeResolver{},
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	const mcpRT = "mcp-opaque-rt-sentinel"
	mem(h).mu.Lock()
	mem(h).refreshTokens[mcpRT] = &mcpRefreshTokenEntry{
		IDPRefreshToken: "idp-rt-original",
		CreatedAt:       time.Now(),
		TTL:             time.Hour,
	}
	mem(h).mu.Unlock()

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {mcpRT},
	}
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.HandleToken(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d (want 502 BadGateway), body = %s", rr.Code, rr.Body.String())
	}
	if minter.LastUserID != "" {
		t.Errorf("minter must not be called when sub is sentinel; got LastUserID=%q", minter.LastUserID)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "invalid_grant" {
		t.Errorf("error = %v (want invalid_grant)", resp["error"])
	}
}

func TestHandleRefresh_ScrubsSentinelEmailAndName(t *testing.T) {
	minter := &fakeMinter{}
	h := New(Config{
		EnableAuth:           true,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		ExternalAuthProvider: "azure",
		IDPClient:            sentinelProfileIDPClient{},
		SAMTokenMinter:       minter,
		AuthzResolver:        fakeResolver{},
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	const mcpRT = "mcp-opaque-rt-profile-scrub"
	mem(h).mu.Lock()
	mem(h).refreshTokens[mcpRT] = &mcpRefreshTokenEntry{
		IDPRefreshToken: "idp-rt-original",
		CreatedAt:       time.Now(),
		TTL:             time.Hour,
	}
	mem(h).mu.Unlock()

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {mcpRT},
	}
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.HandleToken(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (want 200), body = %s", rr.Code, rr.Body.String())
	}
	if minter.LastUserID != "real-user-7" {
		t.Errorf("LastUserID = %q (want real-user-7)", minter.LastUserID)
	}
	if minter.LastEmail != "" {
		t.Errorf("LastEmail = %q (want empty after sentinel scrub)", minter.LastEmail)
	}
	if minter.LastName != "real-user-7" {
		t.Errorf("LastName = %q (want sub fallback after sentinel scrub)", minter.LastName)
	}
}

func TestHandleCallback_IdPErrorPropagated(t *testing.T) {
	h := newTestHandler(t)

	const internalState = "internal-state-xyz"
	const clientState = "client-state-123"
	const clientRedirect = "https://mcp-client.example.com/cb"

	mem(h).mu.Lock()
	mem(h).states[internalState] = &oauthState{
		ClientRedirectURI: clientRedirect,
		ClientState:       clientState,
		CodeChallenge:     "challenge",
		CreatedAt:         time.Now(),
		TTL:               5 * time.Minute,
	}
	mem(h).mu.Unlock()

	req := httptest.NewRequest("GET", "/oauth/callback?error=access_denied&error_description=user+declined&state="+internalState, nil)
	rr := httptest.NewRecorder()
	h.HandleCallback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d (want 302); body=%q", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if loc == "" {
		t.Fatal("expected Location header on redirect")
	}
	parsed, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse Location %q: %v", loc, err)
	}
	if parsed.Scheme+"://"+parsed.Host+parsed.Path != clientRedirect {
		t.Errorf("redirect target = %q (want %q)", parsed.Scheme+"://"+parsed.Host+parsed.Path, clientRedirect)
	}
	if got := parsed.Query().Get("error"); got != "access_denied" {
		t.Errorf("error param = %q (want access_denied)", got)
	}
	if got := parsed.Query().Get("error_description"); got != "user declined" {
		t.Errorf("error_description = %q (want %q)", got, "user declined")
	}
	if got := parsed.Query().Get("state"); got != clientState {
		t.Errorf("state = %q (want %q)", got, clientState)
	}

	mem(h).mu.Lock()
	_, stillThere := mem(h).states[internalState]
	mem(h).mu.Unlock()
	if stillThere {
		t.Error("state should be deleted after callback (one-time use)")
	}
}

func TestHandleAuthorize_BuildAuthURLFailureReturns503(t *testing.T) {
	h := New(Config{
		EnableAuth:           true,
		Issuer:               "http://localhost:8090",
		GatewayID:            "test-gw",
		IDPClient:            failingIDPClient{},
		SAMTokenMinter:       &fakeMinter{},
		AuthzResolver:        fakeResolver{},
		RequirePKCE:          false,
		OAuthStateTTLSeconds: 300,
		OAuthCodeTTLSeconds:  300,
		Host:                 "localhost",
		Port:                 8090,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	before := mem(h).statesLen()

	req := httptest.NewRequest("GET", "/oauth/authorize?response_type=code&client_id=c&redirect_uri=https%3A%2F%2Fclient.example.com%2Fcb&state=cs", nil)
	rr := httptest.NewRecorder()
	h.HandleAuthorize(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d (want 503); body=%q", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "" {
		t.Errorf("expected no redirect on discovery failure, got Location=%q", loc)
	}
	if got := mem(h).statesLen(); got != before {
		t.Errorf("state map should be rolled back: before=%d after=%d", before, got)
	}
}

type failingIDPClient struct{ fakeIDPClient }

func (failingIDPClient) BuildAuthURL(_ context.Context, _, _, _, _ string) (string, error) {
	return "", errors.New("discovery never succeeded")
}

func TestHandleProtectedResourceMetadata(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest("GET", PathProtectedResource, nil)
	rr := httptest.NewRecorder()
	h.HandleProtectedResourceMetadata(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d (want 200): %s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}

	resource, _ := got["resource"].(string)
	if resource == "" || strings.HasSuffix(resource, "/") {
		t.Errorf("resource = %q; want non-empty bare base with no trailing slash", resource)
	}
	servers, _ := got["authorization_servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("authorization_servers = %v; want 1 entry", servers)
	}
	if gotServer, _ := servers[0].(string); gotServer != resource {
		t.Errorf("authorization_servers[0] = %q; want %q", gotServer, resource)
	}
	methods, _ := got["bearer_methods_supported"].([]any)
	if len(methods) == 0 {
		t.Errorf("bearer_methods_supported missing")
	}
}

func TestHandleAuthorize_RejectsUnlistedRedirectURI(t *testing.T) {
	h := New(Config{
		EnableAuth:     true,
		IDPClient:      fakeIDPClient{},
		SAMTokenMinter: &fakeMinter{},
		AuthzResolver:  fakeResolver{},
		Issuer:         "http://127.0.0.1:8090",
		GatewayID:      "test-gw",
		Host:           "localhost",
		Port:           8090,
		AllowedRedirectURIs: []string{
			"https://app.example.com/cb",
			"http://127.0.0.1/callback",
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	cases := []struct {
		name        string
		redirectURI string
		wantStatus  int
		wantOnList  bool
	}{
		{"attacker domain", "https://evil.example.com/cb", http.StatusBadRequest, false},
		{"port confusion", "https://app.example.com:8443/cb", http.StatusBadRequest, false},
		{"path traversal", "https://app.example.com/cb/../evil", http.StatusBadRequest, false},
		{"exact match", "https://app.example.com/cb", http.StatusFound, true},
		{"loopback any port (Claude Code style)", "http://127.0.0.1:54321/callback", http.StatusFound, true},
		{"loopback different path is rejected", "http://127.0.0.1:54321/different", http.StatusBadRequest, false},
		{"non-loopback host can't borrow port wildcard", "http://127.0.0.1.evil.com/callback", http.StatusBadRequest, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{}
			q.Set("redirect_uri", tc.redirectURI)
			q.Set("state", "s1")
			q.Set("code_challenge", "challenge")
			q.Set("code_challenge_method", "S256")

			req := httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil)
			rr := httptest.NewRecorder()
			h.HandleAuthorize(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if !tc.wantOnList {
				var resp map[string]any
				if err := json.Unmarshal(rr.Body.Bytes(), &resp); err == nil {
					if resp["error"] != "invalid_request" {
						t.Errorf("error = %v (want invalid_request)", resp["error"])
					}
				}
			}
		})
	}
}

func TestHandleAuthorize_EmptyAllowlistPreservesBackwardCompat(t *testing.T) {
	h := New(Config{
		EnableAuth:     true,
		IDPClient:      fakeIDPClient{},
		SAMTokenMinter: &fakeMinter{},
		AuthzResolver:  fakeResolver{},
		Issuer:         "http://127.0.0.1:8090",
		GatewayID:      "test-gw",
		Host:           "localhost",
		Port:           8090,
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	q := url.Values{}
	q.Set("redirect_uri", "https://anywhere.example.com/cb")
	q.Set("state", "s1")
	q.Set("code_challenge", "challenge")
	q.Set("code_challenge_method", "S256")
	req := httptest.NewRequest("GET", "/oauth/authorize?"+q.Encode(), nil)
	rr := httptest.NewRecorder()
	h.HandleAuthorize(rr, req)
	if rr.Code != http.StatusFound {
		t.Errorf("status = %d (want 302); empty allowlist should accept any URI", rr.Code)
	}
}

func TestScrubSentinelProfile(t *testing.T) {
	cases := []struct {
		name      string
		in        UserInfo
		wantEmail string
		wantName  string
	}{
		{name: "all_real", in: UserInfo{Sub: "u1", Email: "u@x", Name: "U"}, wantEmail: "u@x", wantName: "U"},
		{name: "sentinel_email_lower", in: UserInfo{Sub: "u1", Email: "unknown", Name: "U"}, wantEmail: "", wantName: "U"},
		{name: "sentinel_email_caps", in: UserInfo{Sub: "u1", Email: "Unknown", Name: "U"}, wantEmail: "", wantName: "U"},
		{name: "sentinel_name", in: UserInfo{Sub: "u1", Email: "u@x", Name: "Unknown"}, wantEmail: "u@x", wantName: "u1"},
		{name: "both_sentinel", in: UserInfo{Sub: "u1", Email: "unknown", Name: "Unknown"}, wantEmail: "", wantName: "u1"},
		{name: "null_email", in: UserInfo{Sub: "u1", Email: "null", Name: "U"}, wantEmail: "", wantName: "U"},
		{name: "empty_email", in: UserInfo{Sub: "u1", Email: "", Name: "U"}, wantEmail: "", wantName: "U"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in
			scrubSentinelProfile(&got)
			if got.Email != tc.wantEmail {
				t.Errorf("Email = %q (want %q)", got.Email, tc.wantEmail)
			}
			if got.Name != tc.wantName {
				t.Errorf("Name = %q (want %q)", got.Name, tc.wantName)
			}
			if got.Sub != tc.in.Sub {
				t.Errorf("Sub mutated: got %q, want %q", got.Sub, tc.in.Sub)
			}
		})
	}
}

func TestHandleCallback_IdPErrorNoState(t *testing.T) {
	h := newTestHandler(t)

	req := httptest.NewRequest("GET", "/oauth/callback?error=login_required&error_description=session+expired&state=unknown", nil)
	rr := httptest.NewRecorder()
	h.HandleCallback(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d (want 400)", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "login_required") {
		t.Errorf("response should contain IdP error code; got %q", body)
	}
	if !strings.Contains(body, "session expired") {
		t.Errorf("response should contain IdP error description; got %q", body)
	}
}

func TestCanonicalUserID_Email_SubFallback(t *testing.T) {
	u := &UserInfo{Sub: "subj-1", Email: "u@example.com"}
	if got := canonicalUserID(u, "email"); got != "u@example.com" {
		t.Errorf("email-present: got %q, want %q", got, "u@example.com")
	}
	u = &UserInfo{Sub: "subj-1"}
	if got := canonicalUserID(u, "email"); got != "subj-1" {
		t.Errorf("email-missing: got %q, want %q", got, "subj-1")
	}
}

func TestCanonicalUserID_Sub_AlwaysSub(t *testing.T) {
	u := &UserInfo{Sub: "subj-1", Email: "u@example.com"}
	if got := canonicalUserID(u, "sub"); got != "subj-1" {
		t.Errorf("sub-claim: got %q, want %q", got, "subj-1")
	}
}

func TestCanonicalUserID_PreferredUsername_FallsBackToSub(t *testing.T) {
	u := &UserInfo{Sub: "subj-1", Raw: map[string]any{"preferred_username": "alice"}}
	if got := canonicalUserID(u, "preferred_username"); got != "alice" {
		t.Errorf("preferred_username-present: got %q, want %q", got, "alice")
	}
	u = &UserInfo{Sub: "subj-1", Raw: map[string]any{}}
	if got := canonicalUserID(u, "preferred_username"); got != "subj-1" {
		t.Errorf("preferred_username-missing: got %q, want %q (sub fallback)", got, "subj-1")
	}
}

func TestCanonicalUserID_UPN_FallsBackToSub(t *testing.T) {
	u := &UserInfo{Sub: "subj-1", Raw: map[string]any{"upn": "alice@corp"}}
	if got := canonicalUserID(u, "upn"); got != "alice@corp" {
		t.Errorf("upn-present: got %q, want %q", got, "alice@corp")
	}
	u = &UserInfo{Sub: "subj-1", Raw: map[string]any{}}
	if got := canonicalUserID(u, "upn"); got != "subj-1" {
		t.Errorf("upn-missing: got %q, want %q (sub fallback)", got, "subj-1")
	}
}

func TestCanonicalUserID_AllSentinel_ReturnsEmpty(t *testing.T) {
	u := &UserInfo{Sub: "unknown", Email: "unknown"}
	if got := canonicalUserID(u, "email"); got != "" {
		t.Errorf("all sentinel: got %q, want \"\"", got)
	}
}

func TestCanonicalUserID_EmptyClaim_DefaultsToEmail(t *testing.T) {
	u := &UserInfo{Sub: "subj-1", Email: "u@example.com"}
	if got := canonicalUserID(u, ""); got != "u@example.com" {
		t.Errorf("empty claim defaults to email: got %q", got)
	}
}

func TestHandleCallback_GatewayContextIncludesSource(t *testing.T) {
	resolver := &recordingResolver{roles: []string{"editor"}}
	minter := &fakeMinter{}
	h := New(Config{
		EnableAuth:           true,
		Host:                 "127.0.0.1",
		Port:                 8090,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       minter,
		AuthzResolver:        resolver,
		OAuthStateTTLSeconds: 300,
		OAuthCodeTTLSeconds:  300,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	state := "state-source-callback"
	mem(h).mu.Lock()
	mem(h).states[state] = &oauthState{
		ClientRedirectURI:   "http://client.example/cb",
		ClientState:         "client-state",
		CodeChallenge:       "challenge",
		CodeChallengeMethod: "S256",
		CodeVerifier:        "verifier",
		CreatedAt:           time.Now(),
		TTL:                 5 * time.Minute,
	}
	mem(h).mu.Unlock()

	req := httptest.NewRequest("GET", "/oauth/callback?code=idp-authcode&state="+state, nil)
	rr := httptest.NewRecorder()
	h.HandleCallback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if resolver.gotContext == nil {
		t.Fatal("ResolveRoles was not invoked")
	}
	if got := resolver.gotContext["source"]; got != "mcp_oauth_login" {
		t.Errorf("gatewayContext[source]: got %v, want %q", got, "mcp_oauth_login")
	}
	if got := resolver.gotContext["gateway_id"]; got != "test-gw" {
		t.Errorf("gatewayContext[gateway_id]: got %v, want %q", got, "test-gw")
	}
}

func TestHandleCallback_MintsTheTokenForTheUserTheLookupMatched(t *testing.T) {
	for _, tc := range []struct {
		name       string
		platformID string
	}{
		{"a matched user", "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40"},
		{"no matched user", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			minter := &fakeMinter{}
			h := New(Config{
				EnableAuth:           true,
				Host:                 "127.0.0.1",
				Port:                 8090,
				Issuer:               "http://127.0.0.1:8090",
				GatewayID:            "test-gw",
				IDPClient:            fakeIDPClient{},
				SAMTokenMinter:       minter,
				AuthzResolver:        &recordingResolver{platformID: tc.platformID},
				OAuthStateTTLSeconds: 300,
				OAuthCodeTTLSeconds:  300,
				Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			const state = "state-platform-user"
			mem(h).mu.Lock()
			mem(h).states[state] = &oauthState{
				ClientRedirectURI:   "http://client.example/cb",
				ClientState:         "client-state",
				CodeChallenge:       "challenge",
				CodeChallengeMethod: "S256",
				CodeVerifier:        "verifier",
				CreatedAt:           time.Now(),
				TTL:                 5 * time.Minute,
			}
			mem(h).mu.Unlock()

			rr := httptest.NewRecorder()
			h.HandleCallback(rr, httptest.NewRequest("GET", "/oauth/callback?code=idp-authcode&state="+state, nil))

			if rr.Code != http.StatusFound {
				t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
			}
			if minter.LastPlatformUserID != tc.platformID {
				t.Errorf("minted for platform user %q, want %q", minter.LastPlatformUserID, tc.platformID)
			}
		})
	}
}

func TestHandler_RegisterRoutes_RegistersFinish(t *testing.T) {
	h := New(Config{
		EnableAuth:              true,
		StateRedirectionEnabled: true,
		StateSigner:             &trust.StateSigner{},
		StateVerifier:           &trust.StateVerifier{},
		OAuthDispatcherURL:      "https://example.com/api/v1/auth/callback",
		AllowedRedirectURIs:     []string{"http://localhost/cb"},
		GatewayID:               "mcp-a",
	})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, PathFinish, nil)
	_, pattern := mux.Handler(req)
	if pattern == "" {
		t.Fatal("no handler registered for /oauth/finish")
	}
}

func TestHandleAuthorize_LegacyPathUnchangedWhenFlagOff(t *testing.T) {
	h := New(Config{
		EnableAuth:           true,
		GatewayID:            "mcp-a",
		AllowedRedirectURIs:  []string{"http://localhost:8081/cb"},
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       &fakeMinter{},
		OAuthStateTTLSeconds: 300,
	})
	req := httptest.NewRequest(http.MethodGet,
		"/oauth/authorize?response_type=code&client_id=cid&redirect_uri=http://localhost:8081/cb&code_challenge=x&code_challenge_method=S256&state=client-nonce",
		nil)
	rec := httptest.NewRecorder()
	h.HandleAuthorize(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("legacy status: want 302, got %d", rec.Code)
	}
	idpURL, _ := url.Parse(rec.Header().Get("Location"))
	state := idpURL.Query().Get("state")
	if strings.HasPrefix(state, "eyJ") && strings.Count(state, ".") == 2 {
		t.Fatalf("legacy state should NOT be a JWS, got %q", state)
	}
}

func TestHandleAuthorize_StateRedirectionMintsJWS(t *testing.T) {
	km, err := trust.NewKeyManager("mcp-a")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := trust.NewSigner(km, "mcp-a", time.Hour, 2*time.Hour)

	capture := &captureIDP{}
	h := New(Config{
		EnableAuth:              true,
		GatewayID:               "mcp-a",
		StateRedirectionEnabled: true,
		OAuthDispatcherURL:      "https://webui.example.com/api/v1/auth/callback",
		AllowedRedirectURIs:     []string{"http://localhost:8081/cb"},
		IDPClient:               capture,
		SAMTokenMinter:          &fakeMinter{},
		StateSigner:             trust.NewStateSigner(signer),
		StateVerifier:           trusttest.NewStateVerifier(km, "mcp-a", map[string]bool{"webui.example.com": true}),
		OAuthStateTTLSeconds:    300,
	})

	req := httptest.NewRequest(http.MethodGet,
		"/oauth/authorize?response_type=code&client_id=cid&redirect_uri=http://localhost:8081/cb&code_challenge=x&code_challenge_method=S256&state=client-nonce",
		nil)
	rec := httptest.NewRecorder()
	h.HandleAuthorize(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status: want 302, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(capture.gotState, "eyJ") || strings.Count(capture.gotState, ".") != 2 {
		t.Fatalf("state should be a JWS, got %q", capture.gotState)
	}
	if capture.gotRedirectURI != "https://webui.example.com/api/v1/auth/callback" {
		t.Fatalf("redirect_uri: want dispatcher URL, got %s", capture.gotRedirectURI)
	}
}

type captureIDP struct {
	fakeIDPClient
	gotState       string
	gotRedirectURI string
}

func (c *captureIDP) BuildAuthURL(_ context.Context, state, redirectURI, codeChallenge, codeChallengeMethod string) (string, error) {
	c.gotState = state
	c.gotRedirectURI = redirectURI
	return "https://idp.example.com/authorize?state=" + url.QueryEscape(state) + "&redirect_uri=" + url.QueryEscape(redirectURI), nil
}

func TestHandleRefresh_GatewayContextIncludesSource(t *testing.T) {
	resolver := &recordingResolver{roles: []string{"viewer"}}
	minter := &fakeMinter{}
	h := New(Config{
		EnableAuth:           true,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		ExternalAuthProvider: "azure",
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       minter,
		AuthzResolver:        resolver,
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	const mcpRT = "mcp-opaque-rt-source"
	const idpRT = "idp-rt-original"
	mem(h).mu.Lock()
	mem(h).refreshTokens[mcpRT] = &mcpRefreshTokenEntry{
		IDPRefreshToken: idpRT,
		CreatedAt:       time.Now(),
		TTL:             time.Hour,
	}
	mem(h).mu.Unlock()

	body := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {mcpRT},
	}
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	h.HandleToken(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if resolver.gotContext == nil {
		t.Fatal("ResolveRoles was not invoked")
	}
	if got := resolver.gotContext["source"]; got != "mcp_token_refresh" {
		t.Errorf("gatewayContext[source]: got %v, want %q", got, "mcp_token_refresh")
	}
}

func TestHandleRefresh_RemintsForThePlatformUserTheLookupMatches(t *testing.T) {
	const platformID = "0192f0c4-6a4e-7b1e-9d3c-5f2a8b7e1c40"
	minter := &fakeMinter{}
	h := New(Config{
		EnableAuth:           true,
		Issuer:               "http://127.0.0.1:8090",
		GatewayID:            "test-gw",
		ExternalAuthProvider: "azure",
		IDPClient:            fakeIDPClient{},
		SAMTokenMinter:       minter,
		AuthzResolver:        &recordingResolver{roles: []string{"viewer"}, platformID: platformID},
		Logger:               slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	const mcpRT = "mcp-opaque-rt"
	mem(h).mu.Lock()
	mem(h).refreshTokens[mcpRT] = &mcpRefreshTokenEntry{IDPRefreshToken: "idp-rt-original", CreatedAt: time.Now(), TTL: time.Hour}
	mem(h).mu.Unlock()
	body := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {mcpRT}}
	req := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()

	h.HandleToken(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if minter.LastPlatformUserID != platformID {
		t.Errorf("refresh minted for platform user %q, want %q", minter.LastPlatformUserID, platformID)
	}
}
