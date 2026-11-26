// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func testProvider(cfg *Config) *Provider {
	return &Provider{config: cfg, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func baseProviderConfig() *Config {
	return &Config{
		OIDC:                 &OIDCConfig{Issuer: "https://idp.example.com"},
		ExternalAuthProvider: "azure",
		AuthorizationService: AuthorizationServiceConfig{},
	}
}

func TestBuildUserRecord_PopulatesFromClaims(t *testing.T) {
	p := testProvider(baseProviderConfig())
	userInfo := &UserInfo{
		Sub:         "sub-alice",
		Email:       "alice@example.com",
		Name:        "Alice Example",
		RawResponse: map[string]any{"sub": "sub-alice", "email": "alice@example.com"},
	}
	claims := map[string]any{
		"groups":                []any{"sam-admins", "sam-users"},
		"exp":                   float64(1893456000),
		"phone_number":          "+15551234567",
		"phone_number_verified": true,
		"preferred_username":    "alice",
	}

	rec, ok := p.buildUserRecord(userInfo, claims)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if rec.Issuer != "https://idp.example.com" || rec.Sub != "sub-alice" || rec.Email != "alice@example.com" {
		t.Errorf("identity fields wrong: %+v", rec)
	}
	if rec.DisplayName != "Alice Example" {
		t.Errorf("displayName = %q, want Alice Example", rec.DisplayName)
	}
	if rec.UserName != "alice" {
		t.Errorf("userName = %q, want alice (from preferred_username)", rec.UserName)
	}
	if rec.OIDCProvider != "azure" {
		t.Errorf("oidcProvider = %q, want azure (from ExternalAuthProvider)", rec.OIDCProvider)
	}
	slices.Sort(rec.Groups)
	if !slices.Equal(rec.Groups, []string{"sam-admins", "sam-users"}) {
		t.Errorf("groups = %v", rec.Groups)
	}
	if rec.Phone != "+15551234567" {
		t.Errorf("phone = %q, want the verified number", rec.Phone)
	}
	if rec.Source == "" {
		t.Error("expected source JSON to be populated")
	}
	if !strings.Contains(rec.Source, "sam-admins") {
		t.Errorf("source should contain merged id_token claims (groups); got %s", rec.Source)
	}
}

func TestBuildUserRecord_DisplayNameFallbackAndSentinel(t *testing.T) {
	p := testProvider(baseProviderConfig())

	rec, _ := p.buildUserRecord(&UserInfo{Sub: "sub-1"}, map[string]any{"name": "Claim Name"})
	if rec.DisplayName != "Claim Name" {
		t.Errorf("displayName = %q, want fallback to claim name", rec.DisplayName)
	}

	rec, _ = p.buildUserRecord(&UserInfo{Sub: "sub-1", Name: "Unknown"}, map[string]any{"preferred_username": "unknown"})
	if rec.DisplayName != "" {
		t.Errorf("displayName = %q, want empty for sentinel", rec.DisplayName)
	}
	if rec.UserName != "" {
		t.Errorf("userName = %q, want empty for sentinel", rec.UserName)
	}
}

func TestBuildUserRecord_PhoneOnlyWhenVerified(t *testing.T) {
	p := testProvider(baseProviderConfig())
	userInfo := &UserInfo{Sub: "sub-1", Email: "a@example.com"}

	rec, _ := p.buildUserRecord(userInfo, map[string]any{
		"phone_number":          "+15551234567",
		"phone_number_verified": false,
	})
	if rec.Phone != "" {
		t.Errorf("phone must be empty when not verified, got %q", rec.Phone)
	}

	rec, _ = p.buildUserRecord(userInfo, map[string]any{"phone_number": "+15551234567"})
	if rec.Phone != "" {
		t.Errorf("phone must be empty when verified claim absent, got %q", rec.Phone)
	}

	rec, _ = p.buildUserRecord(userInfo, map[string]any{
		"phone_number":          "+15551234567",
		"phone_number_verified": "true",
	})
	if rec.Phone != "+15551234567" {
		t.Errorf("phone must persist when verified is string \"true\", got %q", rec.Phone)
	}
}

func TestBuildUserRecord_NoIdentitySkips(t *testing.T) {
	p := testProvider(&Config{AuthorizationService: AuthorizationServiceConfig{}})
	if _, ok := p.buildUserRecord(&UserInfo{Sub: "sub-1"}, nil); ok {
		t.Error("expected ok=false with no issuer")
	}

	p = testProvider(baseProviderConfig())
	if _, ok := p.buildUserRecord(&UserInfo{Sub: ""}, nil); ok {
		t.Error("expected ok=false with empty sub")
	}
}

func TestBuildUserRecord_RejectsReservedSystemSub(t *testing.T) {
	p := testProvider(baseProviderConfig())
	if _, ok := p.buildUserRecord(&UserInfo{Sub: "system:default", Email: "x@example.com"}, nil); ok {
		t.Error("expected ok=false for a reserved system: subject")
	}
	if _, ok := p.buildUserRecord(&UserInfo{Sub: "system:anything"}, nil); ok {
		t.Error("expected ok=false for any system: subject")
	}
}

func TestBuildUserRecord_GroupClaimKeyConfigurable(t *testing.T) {
	cfg := baseProviderConfig()
	cfg.AuthorizationService.IDPClaimsConfig = &IDPClaimsConfig{ClaimKey: "roles"}
	p := testProvider(cfg)

	rec, _ := p.buildUserRecord(&UserInfo{Sub: "sub-1"}, map[string]any{
		"roles":  []any{"r1"},
		"groups": []any{"ignored"},
	})
	if !slices.Equal(rec.Groups, []string{"r1"}) {
		t.Errorf("groups = %v, want [r1] from configured claim_key", rec.Groups)
	}
}

func TestBuildUserRecord_DropsUnverifiedEmail(t *testing.T) {
	p := testProvider(baseProviderConfig())
	userInfo := &UserInfo{Sub: "sub-1", Email: "alice@example.com"}

	rec, ok := p.buildUserRecord(userInfo, map[string]any{"email_verified": false})
	if !ok {
		t.Fatal("expected ok=true")
	}
	if rec.Email != "" {
		t.Errorf("email = %q, want empty when email_verified is false", rec.Email)
	}

	if rec, _ = p.buildUserRecord(userInfo, nil); rec.Email != "alice@example.com" {
		t.Errorf("email = %q, want retained when email_verified absent", rec.Email)
	}

	if rec, _ = p.buildUserRecord(userInfo, map[string]any{"email_verified": true}); rec.Email != "alice@example.com" {
		t.Errorf("email = %q, want retained when verified", rec.Email)
	}
}

type fakeUpsertClient struct {
	ch           chan auth.UserRecord
	err          error
	rolesChanged bool
}

func (f *fakeUpsertClient) Upsert(_ context.Context, rec auth.UserRecord) (auth.UserUpsertResult, error) {
	f.ch <- rec
	return auth.UserUpsertResult{ID: "user-id-fake", RolesChanged: f.rolesChanged}, f.err
}

func TestProvisionUserAtLogin_ReturnsIDWhenClientWired(t *testing.T) {
	p := testProvider(baseProviderConfig())
	fake := &fakeUpsertClient{ch: make(chan auth.UserRecord, 1)}
	p.userUpsertClient = fake

	id := p.provisionUserAtLogin(t.Context(), &UserInfo{Sub: "sub-1", Email: "a@example.com"}, nil).ID
	if id != "user-id-fake" {
		t.Errorf("id = %q, want user-id-fake", id)
	}
	select {
	case rec := <-fake.ch:
		if rec.Sub != "sub-1" {
			t.Errorf("unexpected record: %+v", rec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected the upsert client to be called")
	}
}

func TestProvisionUserAtLogin_NoClientIsNoOp(t *testing.T) {
	p := testProvider(baseProviderConfig())
	if id := p.provisionUserAtLogin(t.Context(), &UserInfo{Sub: "sub-1"}, nil).ID; id != "" {
		t.Errorf("id = %q, want empty", id)
	}
}

func TestProvisionUserAtLogin_SkipsWhenPoolFull(t *testing.T) {
	p := testProvider(baseProviderConfig())
	fake := &fakeUpsertClient{ch: make(chan auth.UserRecord, 1)}
	p.userUpsertClient = fake
	p.userUpsertSem = make(chan struct{}, 1)
	p.userUpsertSem <- struct{}{}

	done := make(chan string, 1)
	go func() {
		done <- p.provisionUserAtLogin(t.Context(), &UserInfo{Sub: "sub-1", Email: "a@example.com"}, nil).ID
	}()
	select {
	case id := <-done:
		if id != "" {
			t.Errorf("id = %q, want empty (skipped)", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provisionUserAtLogin blocked when the pool was full")
	}
	select {
	case <-fake.ch:
		t.Fatal("client was called despite a full pool; provisioning should have been skipped")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestProvisionUserAtLogin_SwallowsClientError(t *testing.T) {
	p := testProvider(baseProviderConfig())
	fake := &fakeUpsertClient{ch: make(chan auth.UserRecord, 1), err: errors.New("platform down")}
	p.userUpsertClient = fake

	if id := p.provisionUserAtLogin(t.Context(), &UserInfo{Sub: "sub-1", Email: "a@example.com"}, nil).ID; id != "" {
		t.Errorf("id = %q, want empty on client error", id)
	}
	select {
	case rec := <-fake.ch:
		if rec.Sub != "sub-1" {
			t.Errorf("unexpected record: %+v", rec)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected the upsert client to be called")
	}
}

func TestOIDCCallback_PersistsUser(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "idp-at",
		userInfo: &UserInfo{
			Sub:         "user-123",
			Email:       "alice@example.com",
			Name:        "Alice",
			RawResponse: map[string]any{"sub": "user-123", "email": "alice@example.com"},
		},
		tokenResponse: &TokenResponse{AccessToken: "idp-at", RefreshToken: "idp-rt", TokenType: "Bearer", ExpiresIn: 3600},
	}
	p := newTestProvider(t, fake)
	p.config.FrontendRedirectURL = "http://localhost:3000"
	capt := &fakeUpsertClient{ch: make(chan auth.UserRecord, 1)}
	p.userUpsertClient = capt

	req := httptest.NewRequest("GET", "/api/v1/auth/callback?code=auth-code", nil)
	rr := httptest.NewRecorder()
	p.handleCallback(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", rr.Code)
	}
	select {
	case rec := <-capt.ch:
		if rec.Sub != "user-123" || rec.Email != "alice@example.com" {
			t.Errorf("persisted record = %+v, want sub=user-123 email=alice@example.com", rec)
		}
		if rec.Issuer == "" {
			t.Error("persisted record has empty issuer")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("login did not persist a user")
	}
}

func TestHandleRefresh_PersistsUser(t *testing.T) {
	fake := &fakeOIDCClient{
		expectedToken: "refreshed-at",
		userInfo: &UserInfo{
			Sub:         "user-123",
			Email:       "user@example.com",
			Name:        "Test User",
			RawResponse: map[string]any{"sub": "user-123", "email": "user@example.com", "name": "Test User"},
		},
		tokenResponse: &TokenResponse{AccessToken: "refreshed-at", RefreshToken: "refreshed-rt", TokenType: "Bearer", ExpiresIn: 3600},
	}
	p := newTestProvider(t, fake)
	upsert := &fakeUpsertClient{ch: make(chan auth.UserRecord, 1)}
	p.SetUserUpsertClient(upsert)

	req := httptest.NewRequest("POST", "/api/v1/auth/refresh", strings.NewReader(`{"refresh_token":"old-rt"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.handleRefresh(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	select {
	case got := <-upsert.ch:
		if got.Sub != "user-123" || got.Email != "user@example.com" {
			t.Errorf("persisted record on refresh = %+v, want sub=user-123 email=user@example.com", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected the persist hook to fire on token refresh")
	}
}
