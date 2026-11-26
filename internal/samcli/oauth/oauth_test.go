// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLogin_HappyPath(t *testing.T) {
	browser := installFakeBrowser(t)

	const samToken = "sam-test-token"
	const accessToken = "idp-access"
	const refreshToken = "idp-refresh"
	const expiresIn = 3600

	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			q := r.URL.Query()
			redirectURI := q.Get("redirect_uri")
			if redirectURI == "" {
				http.Error(w, "missing redirect_uri", http.StatusBadRequest)
				return
			}
			if q.Get("response_mode") != "cli" {
				http.Error(w, "expected response_mode=cli", http.StatusBadRequest)
				return
			}
			if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
				http.Error(w, "missing pkce", http.StatusBadRequest)
				return
			}
			finalURL := fmt.Sprintf("%s?access_token=%s&sam_access_token=%s&refresh_token=%s&expires_in=%d&state=%s",
				redirectURI, accessToken, samToken, refreshToken, expiresIn, q.Get("state"))
			http.Redirect(w, r, finalURL, http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(platform.Close)

	c := NewClient()
	c.LoginTimeout = 5 * time.Second

	tokens, err := c.Login(context.Background(), Config{PlatformURL: platform.URL})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if tokens.SAMAccessToken != samToken {
		t.Errorf("SAMAccessToken = %q; want %q", tokens.SAMAccessToken, samToken)
	}
	if tokens.AccessToken != accessToken {
		t.Errorf("AccessToken = %q; want %q", tokens.AccessToken, accessToken)
	}
	if tokens.RefreshToken != refreshToken {
		t.Errorf("RefreshToken = %q; want %q", tokens.RefreshToken, refreshToken)
	}
	if tokens.ExpiresAt.IsZero() {
		t.Error("ExpiresAt is zero; expected ~1h in the future")
	}
	if !browser.opened.Load() {
		t.Error("browser launcher was not invoked")
	}
}

func TestLogin_StateMismatch(t *testing.T) {
	installFakeBrowser(t)

	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		finalURL := fmt.Sprintf("%s?sam_access_token=token&state=wrong-state", q.Get("redirect_uri"))
		http.Redirect(w, r, finalURL, http.StatusFound)
	}))
	t.Cleanup(platform.Close)

	c := NewClient()
	c.LoginTimeout = 5 * time.Second
	_, err := c.Login(context.Background(), Config{PlatformURL: platform.URL})
	if err == nil {
		t.Fatal("expected state-mismatch error")
	}
	if !strings.Contains(err.Error(), "state mismatch") {
		t.Errorf("err = %v; want a state mismatch", err)
	}
}

func TestLogin_IdPError(t *testing.T) {
	installFakeBrowser(t)

	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		finalURL := q.Get("redirect_uri") + "?error=access_denied&error_description=user+cancelled"
		http.Redirect(w, r, finalURL, http.StatusFound)
	}))
	t.Cleanup(platform.Close)

	c := NewClient()
	c.LoginTimeout = 5 * time.Second
	_, err := c.Login(context.Background(), Config{PlatformURL: platform.URL})
	if err == nil {
		t.Fatal("expected idp error")
	}
	if !strings.Contains(err.Error(), "access_denied") {
		t.Errorf("err = %v; want it to surface access_denied", err)
	}
}

func TestLogin_Timeout(t *testing.T) {
	prev := SetStderr(io.Discard)
	t.Cleanup(prev)
	OverrideBrowser(func(_ string) error { return nil })
	t.Cleanup(func() { OverrideBrowser(nil) })

	c := NewClient()
	c.LoginTimeout = 200 * time.Millisecond
	_, err := c.Login(context.Background(), Config{PlatformURL: "http://example.invalid"})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v; want a timeout error", err)
	}
}

func TestRefresh_HappyPath(t *testing.T) {
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/refresh" || r.Method != http.MethodPost {
			http.Error(w, "wrong route", http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer current-sam" {
			t.Errorf("Authorization = %q; want the current SAM token as bearer", got)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"access_token":"new-access","sam_access_token":"new-sam","refresh_token":"new-refresh","expires_in":1800}`))
	}))
	t.Cleanup(platform.Close)

	c := NewClient()
	tokens, err := c.Refresh(context.Background(), Config{PlatformURL: platform.URL}, "old-refresh", "current-sam")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tokens.SAMAccessToken != "new-sam" {
		t.Errorf("SAMAccessToken = %q", tokens.SAMAccessToken)
	}
	if tokens.RefreshToken != "new-refresh" {
		t.Errorf("RefreshToken = %q", tokens.RefreshToken)
	}
	if d := time.Until(tokens.ExpiresAt); d < 25*time.Minute || d > 35*time.Minute {
		t.Errorf("ExpiresAt = %v; want ~30m in the future", tokens.ExpiresAt)
	}
}

func TestRefresh_RefreshDenied(t *testing.T) {
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(platform.Close)

	c := NewClient()
	_, err := c.Refresh(context.Background(), Config{PlatformURL: platform.URL}, "old", "")
	if !errors.Is(err, ErrRefreshDenied) {
		t.Fatalf("err = %v; want ErrRefreshDenied", err)
	}
}

func TestDiscover_OK(t *testing.T) {
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"issuer":"https://idp.example.com","client_id":"sam-cli","scopes":["openid","email"]}`))
	}))
	t.Cleanup(platform.Close)

	cfg, err := NewClient().Discover(context.Background(), platform.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if cfg.Issuer != "https://idp.example.com" || cfg.ClientID != "sam-cli" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestDiscover_NotFound_ReturnsPartialConfig(t *testing.T) {
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	t.Cleanup(platform.Close)

	cfg, err := NewClient().Discover(context.Background(), platform.URL)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if cfg.PlatformURL != platform.URL {
		t.Errorf("PlatformURL = %q; want %q", cfg.PlatformURL, platform.URL)
	}
	if cfg.Issuer != "" {
		t.Errorf("Issuer = %q; want empty for 404 fallback", cfg.Issuer)
	}
}

func TestBuildLoginURL_Wireshape(t *testing.T) {
	got, err := buildLoginURL("https://platform.example.com", "http://127.0.0.1:55001/callback", "abc", "challenge-xyz")
	if err != nil {
		t.Fatalf("buildLoginURL: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Path != "/api/v1/auth/login" {
		t.Errorf("path = %q", u.Path)
	}
	q := u.Query()
	wants := map[string]string{
		"response_mode":         "cli",
		"redirect_uri":          "http://127.0.0.1:55001/callback",
		"state":                 "abc",
		"code_challenge":        "challenge-xyz",
		"code_challenge_method": "S256",
	}
	for k, v := range wants {
		if got := q.Get(k); got != v {
			t.Errorf("query[%q] = %q; want %q", k, got, v)
		}
	}
}

type browserSpy struct {
	opened atomicBool
}

type atomicBool struct{ v int32 }

func (a *atomicBool) Store(b bool) {
	if b {
		a.v = 1
	} else {
		a.v = 0
	}
}
func (a *atomicBool) Load() bool { return a.v == 1 }

func installFakeBrowser(t *testing.T) *browserSpy {
	t.Helper()
	prev := SetStderr(&bytes.Buffer{})
	t.Cleanup(prev)

	spy := &browserSpy{}
	OverrideBrowser(func(u string) error {
		spy.opened.Store(true)
		go func() {
			req, err := http.NewRequest(http.MethodGet, u, nil)
			if err != nil {
				return
			}
			client := &http.Client{
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			}
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer func() { _ = resp.Body.Close() }()
			if loc := resp.Header.Get("Location"); loc != "" {
				resp2, err := client.Get(loc)
				if err == nil {
					_ = resp2.Body.Close()
				}
			}
		}()
		return nil
	})
	t.Cleanup(func() { OverrideBrowser(nil) })
	return spy
}
