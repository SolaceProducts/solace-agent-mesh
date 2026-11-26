// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsLoopbackRedirectURI_Accepts(t *testing.T) {
	cases := []string{
		"http://127.0.0.1:55001/callback",
		"http://localhost:8080/callback",
		"http://127.5.5.5:30000/cb",
		"http://[::1]:55001/callback",
		"http://[0:0:0:0:0:0:0:1]/callback",
	}
	for _, in := range cases {
		got, err := IsLoopbackRedirectURI(in)
		if err != nil {
			t.Errorf("IsLoopbackRedirectURI(%q) err = %v; want ok", in, err)
		}
		if got == "" {
			t.Errorf("IsLoopbackRedirectURI(%q) returned empty cleaned URL", in)
		}
	}
}

func TestIsLoopbackRedirectURI_Rejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"https-scheme", "https://localhost/callback"},
		{"public-host", "http://example.com/callback"},
		{"rfc1918-10", "http://10.0.0.1/callback"},
		{"public-ipv6", "http://[2001:db8::1]/callback"},
		{"attacker-host", "http://attacker.tld:8080/cb"},
		{"file-scheme", "file:///tmp/x"},
		{"rfc1918-192", "http://192.168.1.1/cb"},
		{"empty", ""},
		{"malformed", "http:/missing-host"},
		{"unspecified", "http://0.0.0.0/callback"},
		{"userinfo-injection", "http://attacker.tld@127.0.0.1/callback"},
		{"userinfo-with-pass", "http://user:pass@127.0.0.1/callback"},
		{"prefix-imposter", "http://127.evil.example.com/callback"},
		{"fragment", "http://127.0.0.1:55001/callback#token=stolen"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := IsLoopbackRedirectURI(c.in); err == nil {
				t.Errorf("IsLoopbackRedirectURI(%q) accepted; want rejected", c.in)
			}
		})
	}
}

func TestRenderCLIConsentPage_Continue(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/login?response_mode=cli"+
			"&redirect_uri=http%3A%2F%2F127.0.0.1%3A55001%2Fcallback"+
			"&state=cli-state-123"+
			"&code_challenge=abc&code_challenge_method=S256", nil)
	w := httptest.NewRecorder()

	renderCLIConsentPage(w, r, "http://127.0.0.1:55001/callback", "cli-state-123", "https://sam.example.com")

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d; want 200", w.Code)
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q; want text/html...", got)
	}
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") {
		t.Errorf("Content-Security-Policy = %q; want default-src 'none' present", got)
	}
	if got := w.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q; want no-referrer", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q; want nosniff", got)
	}
	body := w.Body.String()
	for _, want := range []string{
		"response_mode=cli",
		"redirect_uri=http%3A%2F%2F127.0.0.1%3A55001%2Fcallback",
		"state=cli-state-123",
		"code_challenge=abc",
		"code_challenge_method=S256",
		"confirmed=1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("consent body missing %q in Continue href; body = %s", want, body)
		}
	}
	for _, want := range []string{
		"http://127.0.0.1:55001/callback",
		"error=access_denied",
		"state=cli-state-123",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("consent body missing %q in Cancel href; body = %s", want, body)
		}
	}
	if !strings.Contains(body, "sam.example.com") {
		t.Errorf("consent body missing platform label; body = %s", body)
	}
}

func TestBuildCLICancelURL(t *testing.T) {
	got, err := buildCLICancelURL("http://127.0.0.1:55001/callback", "abc")
	if err != nil {
		t.Fatalf("buildCLICancelURL: %v", err)
	}
	for _, want := range []string{"error=access_denied", "error_description=User+canceled", "state=abc"} {
		if !strings.Contains(got, want) {
			t.Errorf("got = %q; want to contain %q", got, want)
		}
	}
}

func TestBuildCLICallbackURL_OmitsEmpty(t *testing.T) {
	got, err := buildCLICallbackURL("http://127.0.0.1:55001/callback", "ax", "sam-token", "", "abc", 0)
	if err != nil {
		t.Fatalf("buildCLICallbackURL: %v", err)
	}
	want := "http://127.0.0.1:55001/callback?access_token=ax&sam_access_token=sam-token&state=abc"
	if got != want {
		t.Errorf("got = %q; want %q", got, want)
	}
}

func TestHandleCallback_CLIRedirect(t *testing.T) {
	loopbackRedirect := "http://127.0.0.1:55001/callback"
	fake := &fakeOIDCClient{
		expectedToken: "fake-access",
		userInfo: &UserInfo{
			Sub: "u1", Email: "alice@example.com", Name: "Alice",
			RawResponse: map[string]any{"sub": "u1", "email": "alice@example.com", "name": "Alice"},
		},
		tokenResponse: &TokenResponse{
			AccessToken:  "fake-access",
			RefreshToken: "fake-refresh",
			ExpiresIn:    3600,
		},
		validateState: false,
	}
	p := newProviderWithFakeOIDC(t, fake)

	w := httptest.NewRecorder()
	setCLIRedirectCookie(w, loopbackRedirect, p.config.SessionSecretKey, false)
	cliCookie := w.Result().Cookies()[0]

	cb := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=fake-code&state=any", nil)
	r.AddCookie(cliCookie)
	p.handleCallback(cb, r)

	if cb.Code != http.StatusFound {
		t.Fatalf("Code = %d; want 302; body = %s", cb.Code, cb.Body.String())
	}
	loc := cb.Header().Get("Location")
	if !strings.HasPrefix(loc, loopbackRedirect) {
		t.Errorf("Location = %q; want it to start with %q", loc, loopbackRedirect)
	}
	for _, want := range []string{"sam_access_token=", "access_token=fake-access", "refresh_token=fake-refresh", "expires_in=3600"} {
		if !strings.Contains(loc, want) {
			t.Errorf("Location missing %q; got %q", want, loc)
		}
	}
	if got := cb.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q; want no-referrer", got)
	}
	if got := cb.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q; want no-store", got)
	}
}

func TestHandleCallback_CLIRedirect_ExpiresInIsSAMTTL(t *testing.T) {
	loopbackRedirect := "http://127.0.0.1:55001/callback"
	fake := &fakeOIDCClient{
		expectedToken: "fake-access",
		userInfo: &UserInfo{
			Sub: "u1", Email: "alice@example.com", Name: "Alice",
			RawResponse: map[string]any{"sub": "u1", "email": "alice@example.com", "name": "Alice"},
		},
		tokenResponse: &TokenResponse{
			AccessToken:  "fake-access",
			RefreshToken: "fake-refresh",
			ExpiresIn:    3600,
		},
		validateState: false,
	}
	p := newProviderWithFakeOIDC(t, fake)
	p.config.SAMAccessToken = SAMAccessTokenConfig{TTLSeconds: 1800}

	w := httptest.NewRecorder()
	setCLIRedirectCookie(w, loopbackRedirect, p.config.SessionSecretKey, false)
	cliCookie := w.Result().Cookies()[0]

	cb := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=fake-code&state=any", nil)
	r.AddCookie(cliCookie)
	p.handleCallback(cb, r)

	if cb.Code != http.StatusFound {
		t.Fatalf("Code = %d; want 302; body = %s", cb.Code, cb.Body.String())
	}
	loc := cb.Header().Get("Location")
	if !strings.Contains(loc, "expires_in=1800") {
		t.Errorf("Location = %q\nwant expires_in=1800 (the SAM TTL); got the IdP TTL or wrong value", loc)
	}
	if strings.Contains(loc, "expires_in=3600") {
		t.Errorf("Location leaked IdP expires_in=3600 to the CLI: %q", loc)
	}
}

func TestHandleCallback_CLIRedirect_StateRoundTrip(t *testing.T) {
	const (
		loopbackRedirect = "http://127.0.0.1:55001/callback"
		cliState         = "cli-state-from-the-loopback-listener"
		idpState         = "unrelated-state-from-the-idp-roundtrip"
	)
	fake := &fakeOIDCClient{
		expectedToken: "fake-access",
		userInfo: &UserInfo{
			Sub: "u1", Email: "alice@example.com", Name: "Alice",
			RawResponse: map[string]any{"sub": "u1", "email": "alice@example.com", "name": "Alice"},
		},
		tokenResponse: &TokenResponse{
			AccessToken:  "fake-access",
			RefreshToken: "fake-refresh",
			ExpiresIn:    3600,
		},
		validateState: false,
	}
	p := newProviderWithFakeOIDC(t, fake)

	w := httptest.NewRecorder()
	setCLIRedirectCookie(w, loopbackRedirect, p.config.SessionSecretKey, false)
	setCLIStateCookie(w, cliState, p.config.SessionSecretKey, false)
	cookies := w.Result().Cookies()

	cb := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=fake-code&state="+idpState, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	p.handleCallback(cb, r)

	if cb.Code != http.StatusFound {
		t.Fatalf("Code = %d; want 302; body = %s", cb.Code, cb.Body.String())
	}
	loc := cb.Header().Get("Location")
	if !strings.Contains(loc, "state="+cliState) {
		t.Errorf("Location = %q\nwant to contain state=%q (the CLI's state, not the IdP's)", loc, cliState)
	}
	if strings.Contains(loc, "state="+idpState) {
		t.Errorf("Location leaked IdP-roundtrip state %q to the loopback: %q", idpState, loc)
	}
}

func TestHandleCallback_CLIRedirect_RejectsTamperedCookie(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "fake-access",
		userInfo:      &UserInfo{Sub: "u1", Email: "a@e.com"},
		tokenResponse: &TokenResponse{AccessToken: "fake-access", ExpiresIn: 3600},
	}
	p := newProviderWithFakeOIDC(t, fake)

	cb := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=fake-code&state=any", nil)
	r.AddCookie(&http.Cookie{Name: cliRedirectCookieName, Value: "http://attacker.example.com/cb"})
	p.handleCallback(cb, r)

	loc := cb.Header().Get("Location")
	if strings.Contains(loc, "attacker.example.com") {
		t.Fatalf("attacker URL leaked through unsigned cookie: %q", loc)
	}
}

func TestHandleCLIConfig_NoOIDC_Returns404(t *testing.T) {
	p := &Provider{config: &Config{}, logger: nil}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/cli-config", nil)
	p.handleCLIConfig(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("Code = %d; want 404", w.Code)
	}
}

func TestHandleCLIConfig_OK(t *testing.T) {
	p := &Provider{config: &Config{
		OIDC: &OIDCConfig{
			Issuer:   "https://idp.example.com",
			ClientID: "sam-cli",
			Scopes:   []string{"openid", "email"},
		},
	}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/cli-config", nil)
	p.handleCLIConfig(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d; want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`"issuer":"https://idp.example.com"`, `"client_id":"sam-cli"`, `"openid"`, `"email"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q; body = %s", want, body)
		}
	}
}
