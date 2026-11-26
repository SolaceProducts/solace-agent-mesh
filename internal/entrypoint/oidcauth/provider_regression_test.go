// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type fakeOIDCClient struct {
	expectedToken string
	userInfo      *UserInfo
	userInfoErr   error
	tokenResponse *TokenResponse
	validateState bool
	calls         int
}

func (f *fakeOIDCClient) BuildAuthURL(_ context.Context, _, _, _, _ string) (string, error) {
	return "", nil
}

func (f *fakeOIDCClient) RequiresStateValidation() bool { return f.validateState }

func (f *fakeOIDCClient) ExchangeCode(_ context.Context, _, _, _ string) (*TokenResponse, error) {
	if f.tokenResponse != nil {
		return f.tokenResponse, nil
	}
	return nil, errNotUsed
}
func (f *fakeOIDCClient) RefreshToken(_ context.Context, _ string) (*TokenResponse, error) {
	if f.tokenResponse != nil {
		return f.tokenResponse, nil
	}
	return nil, errNotUsed
}
func (f *fakeOIDCClient) ValidateToken(_ context.Context, token string) (bool, error) {
	return token == f.expectedToken, nil
}
func (f *fakeOIDCClient) GetUserInfo(_ context.Context, token string) (*UserInfo, error) {
	f.calls++
	if f.userInfoErr != nil {
		return nil, f.userInfoErr
	}
	if token != f.expectedToken {
		return nil, errBadToken
	}
	return f.userInfo, nil
}

var errBadToken = &fakeErr{"bad token"}
var errNotUsed = &fakeErr{"not used"}

type fakeErr struct{ s string }

func (e *fakeErr) Error() string { return e.s }

func newProviderWithFakeOIDC(t *testing.T, fake *fakeOIDCClient) *Provider {
	t.Helper()
	km, err := trust.NewKeyManager("test-gateway")
	if err != nil {
		t.Fatalf("NewKeyManager: %v", err)
	}
	signer := trust.NewSigner(km, "test-gateway", time.Hour, 2*time.Hour)

	cfg := &Config{
		ExternalAuthProvider: "azure",
		FrontendServerURL:    "http://localhost:3000",
		SessionSecretKey:     "test-session-key",
		SAMAccessToken:       SAMAccessTokenConfig{TTLSeconds: 3600},
		AuthorizationService: AuthorizationServiceConfig{Type: "none"},
	}

	authzSvc, err := NewAuthorizationService(cfg.AuthorizationService, "gw-1", slog.Default())
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}

	return &Provider{
		oidcClient:  fake,
		signer:      signer,
		authzSvc:    authzSvc,
		auditLogger: coreauth.NoopAuditLogger{},
		config:      cfg,
		gatewayID:   "gw-1",
		logger:      slog.Default().With("logger_name", "enterprise-auth"),
	}
}

func TestMiddleware_IdPFallback(t *testing.T) {
	for _, tc := range []struct {
		name       string
		userInfo   *UserInfo
		wantUserID string
		wantEmail  string
		wantName   string
	}{
		{
			name: "email wins over sub (converged resolveUserID)",
			userInfo: &UserInfo{
				Sub:   "user-123",
				Email: "alice@example.com",
				Name:  "Alice Smith",
				RawResponse: map[string]any{
					"sub":   "user-123",
					"email": "alice@example.com",
					"name":  "Alice Smith",
				},
			},
			wantUserID: "alice@example.com",
			wantEmail:  "alice@example.com",
			wantName:   "Alice Smith",
		},
		{
			name: "no email — sub is used as UserID",
			userInfo: &UserInfo{
				Sub:  "bob-sub-uuid",
				Name: "Bob",
				RawResponse: map[string]any{
					"sub":  "bob-sub-uuid",
					"name": "Bob",
				},
			},
			wantUserID: "bob-sub-uuid",
			wantEmail:  "bob-sub-uuid",
			wantName:   "Bob",
		},
		{
			name: "sub only — sub used for all fields when email and name absent",
			userInfo: &UserInfo{
				Sub: "u1",
				RawResponse: map[string]any{
					"sub": "u1",
				},
			},
			wantUserID: "u1",
			wantEmail:  "u1",
			wantName:   "u1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeOIDCClient{
				expectedToken: "idp-access-token",
				userInfo:      tc.userInfo,
			}
			p := newProviderWithFakeOIDC(t, fake)

			var gotIdentity *auth.Subject
			handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if id, ok := auth.SubjectFromContext(r.Context()); ok {
					gotIdentity = id
				}
				w.WriteHeader(200)
			}))

			req := httptest.NewRequest("GET", "/api/v1/something", nil)
			req.Header.Set("Authorization", "Bearer idp-access-token")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
			}
			if gotIdentity == nil {
				t.Fatal("expected identity in context after IdP fallback")
			}
			if gotIdentity.UserID() != tc.wantUserID {
				t.Errorf("UserID = %q, want %q", gotIdentity.UserID(), tc.wantUserID)
			}
			if gotIdentity.Email() != tc.wantEmail {
				t.Errorf("Email = %q, want %q", gotIdentity.Email(), tc.wantEmail)
			}
			if gotIdentity.Name() != tc.wantName {
				t.Errorf("Name = %q, want %q", gotIdentity.Name(), tc.wantName)
			}
			if gotIdentity.AuthMethod() != "oidc" {
				t.Errorf("AuthMethod = %q, want oidc", gotIdentity.AuthMethod())
			}
			if fake.calls != 1 {
				t.Errorf("GetUserInfo call count = %d, want 1", fake.calls)
			}
		})
	}
}

func TestMiddleware_IdPFallback_InvalidToken(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "good-token",
		userInfo: &UserInfo{
			RawResponse: map[string]any{"sub": "user"},
		},
	}
	p := newProviderWithFakeOIDC(t, fake)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertErrorShape(t, rec, auth.ErrorInvalidToken)
}

func TestMiddleware_CookieToken_IdPFallback(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "cookie-bearer-token",
		userInfo: &UserInfo{
			Sub:         "from-cookie",
			RawResponse: map[string]any{"sub": "from-cookie"},
		},
	}
	p := newProviderWithFakeOIDC(t, fake)

	var gotIdentity *auth.Subject
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := auth.SubjectFromContext(r.Context()); ok {
			gotIdentity = id
		}
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.AddCookie(&http.Cookie{
		Name:  accessTokenCookieName,
		Value: signValue("cookie-bearer-token", p.config.SessionSecretKey),
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
	if gotIdentity == nil || gotIdentity.UserID() != "from-cookie" {
		t.Fatalf("expected cookie-authed identity, got %+v", gotIdentity)
	}
}

func TestMiddleware_AuthHeaderWinsOverCookie(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "from-header",
		userInfo: &UserInfo{
			Sub:         "header-user",
			RawResponse: map[string]any{"sub": "header-user"},
		},
	}
	p := newProviderWithFakeOIDC(t, fake)

	var gotUserID string
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := auth.SubjectFromContext(r.Context()); ok {
			gotUserID = id.UserID()
		}
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.Header.Set("Authorization", "Bearer from-header")
	req.AddCookie(&http.Cookie{
		Name:  accessTokenCookieName,
		Value: signValue("ignored-cookie-token", p.config.SessionSecretKey),
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotUserID != "header-user" {
		t.Errorf("UserID = %q, want header-user (header should win)", gotUserID)
	}
}

func TestMiddleware_TamperedCookieRejected(t *testing.T) {
	fake := &fakeOIDCClient{expectedToken: "never-checked"}
	p := newProviderWithFakeOIDC(t, fake)

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.AddCookie(&http.Cookie{
		Name:  accessTokenCookieName,
		Value: "forged.value",
	})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered cookie must be rejected; status = %d", rec.Code)
	}
	assertErrorShape(t, rec, auth.ErrorAuthenticationRequired)
}

func TestHandleLogout_ClearsAccessCookie(t *testing.T) {
	fake := &fakeOIDCClient{}
	p := newProviderWithFakeOIDC(t, fake)

	rec := httptest.NewRecorder()
	p.handleLogout(rec, httptest.NewRequest("POST", "/api/v1/auth/logout", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	var cleared *http.Cookie
	for _, c := range cookies {
		if c.Name == accessTokenCookieName {
			cleared = c
		}
	}
	if cleared == nil {
		t.Fatal("logout must issue a Set-Cookie for the access cookie")
	}
	if cleared.MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1 (delete)", cleared.MaxAge)
	}
}

func TestMiddleware_MissingCredentials_ErrorShape(t *testing.T) {
	p := newProviderWithFakeOIDC(t, &fakeOIDCClient{})

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertErrorShape(t, rec, auth.ErrorAuthenticationRequired)
}

func TestMiddleware_MalformedAuthorization_ErrorShape(t *testing.T) {
	p := newProviderWithFakeOIDC(t, &fakeOIDCClient{})

	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/v1/something", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	assertErrorShape(t, rec, auth.ErrorAuthenticationRequired)
}

func TestMiddleware_LegacyIdPCookieStillWorks(t *testing.T) {
	const legacyIDPToken = "legacy-idp-rs256-token"
	fake := &fakeOIDCClient{
		expectedToken: legacyIDPToken,
		userInfo: &UserInfo{
			Sub:   "6f1ba32d-f48e-4327-9471-edf52a61db1b",
			Email: "grace.miller@example.org",
			Name:  "Grace Miller",
			RawResponse: map[string]any{
				"sub":   "6f1ba32d-f48e-4327-9471-edf52a61db1b",
				"email": "grace.miller@example.org",
				"name":  "Grace Miller",
			},
		},
	}
	p := newProviderWithFakeOIDC(t, fake)

	var gotIdentity *auth.Subject
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := auth.SubjectFromContext(r.Context()); ok {
			gotIdentity = id
		}
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	signed := signValue(legacyIDPToken, p.config.SessionSecretKey)
	req.AddCookie(&http.Cookie{Name: accessTokenCookieName, Value: signed})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if gotIdentity == nil {
		t.Fatal("identity not set on request context")
	}
	if gotIdentity.Email() != "grace.miller@example.org" {
		t.Errorf("Email = %q, want grace.miller@example.org", gotIdentity.Email())
	}
	if fake.calls != 1 {
		t.Errorf("userinfo calls = %d, want 1", fake.calls)
	}
}

func newCookieTestFake(accessToken, refreshToken string) *fakeOIDCClient {
	return &fakeOIDCClient{
		expectedToken: accessToken,
		userInfo: &UserInfo{
			Sub:   "sub-uuid",
			Email: "user@example.com",
			Name:  "User",
		},
		tokenResponse: &TokenResponse{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresIn:    7200,
		},
	}
}

func assertSAMTokenCookie(t *testing.T, rec *httptest.ResponseRecorder, p *Provider, rawIDPToken string) int {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name != accessTokenCookieName {
			continue
		}
		token, err := verifySignedValue(c.Value, p.config.SessionSecretKey)
		if err != nil {
			t.Fatalf("unsign cookie: %v", err)
		}
		if _, err := ValidateSAMToken(token, p.signer); err != nil {
			t.Errorf("cookie contents are not a valid SAM token: %v", err)
		}
		if rawIDPToken != "" && token == rawIDPToken {
			t.Error("cookie still contains the raw IdP access token")
		}
		return c.MaxAge
	}
	t.Fatal("sam_access_token cookie not set")
	return 0
}

func TestHandleCallback_CookieContainsSAMToken(t *testing.T) {
	p := newProviderWithFakeOIDC(t, newCookieTestFake("idp-access-token", "idp-refresh-token"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=abc", nil)
	p.handleCallback(rec, req)

	if maxAge := assertSAMTokenCookie(t, rec, p, "idp-access-token"); maxAge != 3600 {
		t.Errorf("cookie MaxAge = %d, want 3600 (SAM TTL)", maxAge)
	}
}

func TestHandleRefresh_CookieContainsSAMToken(t *testing.T) {
	p := newProviderWithFakeOIDC(t, newCookieTestFake("new-idp-access-token", "new-idp-refresh-token"))

	body, _ := json.Marshal(map[string]string{"refresh_token": "old-refresh"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRefresh(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertSAMTokenCookie(t, rec, p, "new-idp-access-token")
}

func TestCookieMaxAge_UsesSAMTokenTTL(t *testing.T) {
	p := newProviderWithFakeOIDC(t, newCookieTestFake("idp-access-token", ""))
	p.config.SAMAccessToken.TTLSeconds = 1800

	body, _ := json.Marshal(map[string]string{"refresh_token": "r"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRefresh(rec, req)

	if maxAge := assertSAMTokenCookie(t, rec, p, ""); maxAge != 1800 {
		t.Errorf("MaxAge=%d, want 1800 (SAM TTL)", maxAge)
	}
}

func TestIdentity_BothPaths_SameUserID(t *testing.T) {
	const (
		userEmail = "grace.miller@example.org"
		userUUID  = "6f1ba32d-f48e-4327-9471-edf52a61db1b"
	)
	fake := &fakeOIDCClient{
		expectedToken: "idp-access-token",
		userInfo: &UserInfo{
			Sub:   userUUID,
			Email: userEmail,
			Name:  "Grace Miller",
			RawResponse: map[string]any{
				"sub":   userUUID,
				"email": userEmail,
				"name":  "Grace Miller",
			},
		},
	}
	p := newProviderWithFakeOIDC(t, fake)

	userID := p.resolveUserID(fake.userInfo)
	samToken, err := MintSAMToken(p.signer, MintClaims{
		UserID: userID,
		Email:  fake.userInfo.Email,
		Name:   fake.userInfo.Name,
	}, time.Hour)
	if err != nil {
		t.Fatalf("MintSAMToken: %v", err)
	}
	samClaims, err := ValidateSAMToken(samToken, p.signer)
	if err != nil {
		t.Fatalf("ValidateSAMToken: %v", err)
	}
	pathAUserID := samClaims.Subject

	identity, err := p.IdentityFromIDPToken(context.Background(), "idp-access-token")
	if err != nil {
		t.Fatalf("IdentityFromIDPToken: %v", err)
	}
	pathBUserID := identity.UserID()

	if pathAUserID != pathBUserID {
		t.Errorf("UserID divergence: SAM path=%q, IdP path=%q", pathAUserID, pathBUserID)
	}
	if pathAUserID != userEmail {
		t.Errorf("expected both paths to resolve email, got %q", pathAUserID)
	}
}

func TestIdentity_SentinelEmailFallsThroughToSub(t *testing.T) {
	cases := []struct {
		name     string
		email    string
		sub      string
		wantUser string
	}{
		{"lowercase unknown sentinel", "unknown", "real-sub-uuid", "real-sub-uuid"},
		{"capitalized Unknown sentinel", "Unknown", "real-sub-uuid", "real-sub-uuid"},
		{"null sentinel", "null", "real-sub-uuid", "real-sub-uuid"},
		{"none sentinel", "none", "real-sub-uuid", "real-sub-uuid"},
		{"real email wins", "user@example.com", "sub-uuid", "user@example.com"},
		{"empty email falls through to sub", "", "sub-uuid", "sub-uuid"},
		{"both sentinels resolve to empty", "unknown", "Unknown", ""},
		{"both empty resolves to empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newProviderWithFakeOIDC(t, &fakeOIDCClient{})
			got := p.resolveUserID(&UserInfo{Email: tc.email, Sub: tc.sub})
			if got != tc.wantUser {
				t.Errorf("resolveUserID = %q, want %q", got, tc.wantUser)
			}
		})
	}
}

func TestSentinels_PartialFallsThrough(t *testing.T) {
	sentinelFake := func() *fakeOIDCClient {
		return &fakeOIDCClient{
			expectedToken: "idp-access-token",
			userInfo: &UserInfo{
				Sub:   "real-sub-uuid",
				Email: "unknown",
				Name:  "Unknown",
			},
			tokenResponse: &TokenResponse{
				AccessToken:  "idp-access-token",
				RefreshToken: "refresh",
				ExpiresIn:    7200,
			},
		}
	}

	t.Run("IdentityFromIDPToken falls back to sub", func(t *testing.T) {
		p := newProviderWithFakeOIDC(t, sentinelFake())
		identity, err := p.IdentityFromIDPToken(context.Background(), "idp-access-token")
		if err != nil {
			t.Fatalf("IdentityFromIDPToken: %v", err)
		}
		if identity.UserID() != "real-sub-uuid" {
			t.Errorf("UserID = %q, want %q", identity.UserID(), "real-sub-uuid")
		}
		if identity.Email() != "real-sub-uuid" {
			t.Errorf("Email = %q, want %q (sentinel should fall back to userID)", identity.Email(), "real-sub-uuid")
		}
		if identity.Name() != "real-sub-uuid" {
			t.Errorf("Name = %q, want %q", identity.Name(), "real-sub-uuid")
		}
	})

	t.Run("handleCallback succeeds", func(t *testing.T) {
		p := newProviderWithFakeOIDC(t, sentinelFake())
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=abc", nil)
		p.handleCallback(rec, req)
		if rec.Code != http.StatusFound {
			t.Errorf("callback status=%d, want %d", rec.Code, http.StatusFound)
		}
	})

	t.Run("handleRefresh succeeds", func(t *testing.T) {
		p := newProviderWithFakeOIDC(t, sentinelFake())
		body, _ := json.Marshal(map[string]string{"refresh_token": "r"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh",
			bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		p.handleRefresh(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("refresh status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestSentinels_BothMissing_FailsClosed(t *testing.T) {
	bothSentinelFake := func() *fakeOIDCClient {
		return &fakeOIDCClient{
			expectedToken: "idp-access-token",
			userInfo: &UserInfo{
				Sub:   "Unknown",
				Email: "unknown",
				Name:  "Unknown",
			},
			tokenResponse: &TokenResponse{
				AccessToken:  "idp-access-token",
				RefreshToken: "refresh",
				ExpiresIn:    7200,
			},
		}
	}

	t.Run("IdentityFromIDPToken returns error", func(t *testing.T) {
		p := newProviderWithFakeOIDC(t, bothSentinelFake())
		_, err := p.IdentityFromIDPToken(context.Background(), "idp-access-token")
		if err == nil {
			t.Fatal("expected error when both email and sub are sentinels")
		}
	})

	t.Run("handleCallback returns 500", func(t *testing.T) {
		p := newProviderWithFakeOIDC(t, bothSentinelFake())
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=abc", nil)
		p.handleCallback(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("callback status=%d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})

	t.Run("handleRefresh returns 500", func(t *testing.T) {
		p := newProviderWithFakeOIDC(t, bothSentinelFake())
		body, _ := json.Marshal(map[string]string{"refresh_token": "r"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh",
			bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		p.handleRefresh(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("refresh status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func assertErrorShape(t *testing.T, rec *httptest.ResponseRecorder, wantErrType auth.ErrorType) {
	t.Helper()
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["errorType"] != string(wantErrType) {
		t.Errorf("errorType = %v, want %q", body["errorType"], wantErrType)
	}
	if message, _ := body["message"].(string); message == "" {
		t.Error("message should be non-empty")
	}
	if errID, _ := body["errorId"].(string); errID == "" {
		t.Error("errorId should be non-empty")
	}
}
