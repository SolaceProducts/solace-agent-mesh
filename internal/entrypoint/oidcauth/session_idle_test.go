// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestParseSAMAccessTokenConfig_IdleTimeout(t *testing.T) {
	tests := []struct {
		name     string
		raw      map[string]any
		wantIdle time.Duration
		wantTTL  time.Duration
		wantErr  bool
	}{
		{"block absent: 4h default, ttl untouched", map[string]any{}, 4 * time.Hour, time.Hour, false},
		{"explicit 0 disables", map[string]any{"sam_access_token": map[string]any{"idle_timeout_seconds": 0}}, 0, time.Hour, false},
		{"null (an unset ${VAR}) keeps the default", map[string]any{"sam_access_token": map[string]any{"idle_timeout_seconds": nil}}, 4 * time.Hour, time.Hour, false},
		{"idle shorter than ttl caps the ttl", map[string]any{"sam_access_token": map[string]any{"ttl_seconds": 3600, "idle_timeout_seconds": 1800}}, 30 * time.Minute, 30 * time.Minute, false},
		{"idle longer than ttl leaves the ttl", map[string]any{"sam_access_token": map[string]any{"ttl_seconds": 600, "idle_timeout_seconds": 1800}}, 30 * time.Minute, 10 * time.Minute, false},
		{"env-substituted float parses", map[string]any{"sam_access_token": map[string]any{"idle_timeout_seconds": float64(900)}}, 15 * time.Minute, 15 * time.Minute, false},
		{"quoted number parses", map[string]any{"sam_access_token": map[string]any{"idle_timeout_seconds": "900"}}, 15 * time.Minute, 15 * time.Minute, false},
		{"negative is an error, not off", map[string]any{"sam_access_token": map[string]any{"idle_timeout_seconds": -1}}, 0, 0, true},
		{"garbage is an error, not off", map[string]any{"sam_access_token": map[string]any{"idle_timeout_seconds": "soon"}}, 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseSAMAccessTokenConfig(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", cfg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := cfg.IdleTimeout(); got != tt.wantIdle {
				t.Errorf("IdleTimeout() = %v, want %v", got, tt.wantIdle)
			}
			if got := cfg.TTL(); got != tt.wantTTL {
				t.Errorf("TTL() = %v, want %v", got, tt.wantTTL)
			}
		})
	}
}

func TestParseConfig_CarriesIdleTimeoutIntoProvider(t *testing.T) {
	catalog := map[string]*OIDCConfig{"azure": {Issuer: "https://idp.example.com", ClientID: "c", ClientSecret: "s"}}
	base := func(sat map[string]any) map[string]any {
		m := map[string]any{"external_auth_provider": "azure", "session_secret_key": "k"}
		if sat != nil {
			m["sam_access_token"] = sat
		}
		return m
	}
	tests := []struct {
		name string
		sat  map[string]any
		want int
	}{
		{"default", nil, DefaultIdleTimeoutSeconds},
		{"explicit 0", map[string]any{"idle_timeout_seconds": 0}, 0},
		{"custom", map[string]any{"idle_timeout_seconds": 1800}, 1800},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseConfig(base(tt.sat), catalog)
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}
			if cfg.SAMAccessToken.IdleTimeoutSeconds != tt.want {
				t.Errorf("IdleTimeoutSeconds = %d, want %d", cfg.SAMAccessToken.IdleTimeoutSeconds, tt.want)
			}
		})
	}
	if _, err := ParseConfig(base(map[string]any{"idle_timeout_seconds": "soon"}), catalog); err == nil {
		t.Error("expected ParseConfig to refuse a non-numeric idle timeout")
	}
}

func idleRefreshProvider(t *testing.T, idle time.Duration) *Provider {
	t.Helper()
	p := newTestProvider(t, &fakeOIDCClient{
		expectedToken: "refreshed-at",
		userInfo: &UserInfo{
			Sub:         "user-123",
			Email:       "user@example.com",
			Name:        "Test User",
			RawResponse: map[string]any{"sub": "user-123", "email": "user@example.com", "name": "Test User"},
		},
		tokenResponse: &TokenResponse{AccessToken: "refreshed-at", RefreshToken: "refreshed-rt", TokenType: "Bearer", ExpiresIn: 3600},
	})
	p.config.SAMAccessToken.IdleTimeoutSeconds = int(idle.Seconds())
	return p
}

func samTokenMintedAt(t *testing.T, p *Provider, mintedAt time.Time, ttl time.Duration) string {
	t.Helper()
	return samTokenFor(t, p, "user@example.com", mintedAt, ttl)
}

func samTokenFor(t *testing.T, p *Provider, subject string, mintedAt time.Time, ttl time.Duration) string {
	t.Helper()
	token, err := p.signer.SignTyped("", jwt.MapClaims{
		"iss": "test-gateway",
		"sub": subject,
		"iat": mintedAt.Unix(),
		"exp": mintedAt.Add(ttl).Unix(),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return token
}

func postRefresh(p *Provider, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"old-rt"}`))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	p.handleRefresh(rec, req)
	return rec
}

func decodeErrorType(t *testing.T, rec *httptest.ResponseRecorder) auth.ErrorType {
	t.Helper()
	var body struct {
		ErrorType auth.ErrorType `json:"errorType"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body.ErrorType
}

func TestHandleRefresh_IdleTimeout_RequiresCurrentSessionToken(t *testing.T) {
	p := idleRefreshProvider(t, 30*time.Minute)

	rec := postRefresh(p, "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := decodeErrorType(t, rec); got != auth.ErrorSessionExpired {
		t.Errorf("errorType = %q, want %q", got, auth.ErrorSessionExpired)
	}
}

func TestHandleRefresh_IdleTimeout_RefusesSessionOlderThanWindow(t *testing.T) {
	p := idleRefreshProvider(t, 30*time.Minute)
	stale := samTokenMintedAt(t, p, time.Now().Add(-31*time.Minute), time.Hour)

	rec := postRefresh(p, stale)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := decodeErrorType(t, rec); got != auth.ErrorSessionExpired {
		t.Errorf("errorType = %q, want %q", got, auth.ErrorSessionExpired)
	}
}

func TestHandleRefresh_IdleTimeout_AcceptsExpiredTokenInsideWindow(t *testing.T) {
	p := idleRefreshProvider(t, 30*time.Minute)
	lapsed := samTokenMintedAt(t, p, time.Now().Add(-20*time.Minute), 10*time.Minute)

	rec := postRefresh(p, lapsed)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp["expires_in"] != float64(1800) {
		t.Errorf("expires_in = %v, want 1800 (ttl capped at the idle window)", resp["expires_in"])
	}
}

func TestHandleRefresh_IdleTimeout_RejectsForeignSignature(t *testing.T) {
	p := idleRefreshProvider(t, 30*time.Minute)
	other := newTestProvider(t, nil)
	forged := samTokenMintedAt(t, other, time.Now(), time.Hour)

	rec := postRefresh(p, forged)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := decodeErrorType(t, rec); got != auth.ErrorInvalidToken {
		t.Errorf("errorType = %q, want %q", got, auth.ErrorInvalidToken)
	}
}

func TestHandleRefresh_IdleTimeout_RejectsBearerForAnotherUser(t *testing.T) {
	p := idleRefreshProvider(t, 30*time.Minute)
	mallory := samTokenFor(t, p, "mallory", time.Now(), time.Hour)

	rec := postRefresh(p, mallory)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := decodeErrorType(t, rec); got != auth.ErrorInvalidToken {
		t.Errorf("errorType = %q, want %q", got, auth.ErrorInvalidToken)
	}
	if cookie := rec.Header().Get("Set-Cookie"); strings.Contains(cookie, "sam_access_token=") && !strings.Contains(cookie, "Max-Age=0") {
		t.Errorf("a refused refresh must not hand out a session cookie: %q", cookie)
	}
}

func TestHandleRefresh_IdleTimeoutOff_NeedsNoSessionToken(t *testing.T) {
	p := idleRefreshProvider(t, 0)

	rec := postRefresh(p, "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}
