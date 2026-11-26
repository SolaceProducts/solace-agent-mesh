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
	"slices"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcpauth"
)

func TestMCPSAMMinter_RoundTrip(t *testing.T) {
	signer := newTestTrustSigner(t, "mcp-gw-test")
	minter := NewMCPSAMMinter(signer, time.Hour)

	if got := minter.TTL(); got != time.Hour {
		t.Errorf("TTL: got %v, want %v", got, time.Hour)
	}

	token, err := minter.MintForUser("u@example.com", "", "u@example.com", "U", []string{"viewer"}, []string{"agent:*:invoke"}, time.Hour)
	if err != nil {
		t.Fatalf("MintForUser: %v", err)
	}
	if token == "" {
		t.Fatal("MintForUser returned empty token")
	}

	claims, err := ValidateSAMToken(token, signer)
	if err != nil {
		t.Fatalf("ValidateSAMToken: %v", err)
	}
	if claims.Subject != "u@example.com" {
		t.Errorf("Subject: got %q, want %q (passed-through userID)", claims.Subject, "u@example.com")
	}
	if claims.Email != "u@example.com" {
		t.Errorf("Email: got %q, want %q", claims.Email, "u@example.com")
	}
	if !slices.Equal(claims.Roles, []string{"viewer"}) {
		t.Errorf("Roles: got %v, want [viewer]", claims.Roles)
	}
	if !slices.Equal(claims.Scopes, []string{"agent:*:invoke"}) {
		t.Errorf("Scopes: got %v, want [agent:*:invoke]", claims.Scopes)
	}
}

func TestMCPSAMMinter_PassesThroughUserID(t *testing.T) {
	signer := newTestTrustSigner(t, "mcp-gw-passthrough")
	minter := NewMCPSAMMinter(signer, time.Hour)

	token, err := minter.MintForUser("u-sub-1", "", "u@example.com", "U", nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("MintForUser: %v", err)
	}
	claims, err := ValidateSAMToken(token, signer)
	if err != nil {
		t.Fatalf("ValidateSAMToken: %v", err)
	}
	if claims.Subject != "u-sub-1" {
		t.Errorf("Subject: got %q, want %q (caller's userID must pass through verbatim)", claims.Subject, "u-sub-1")
	}
	if claims.Email != "u@example.com" {
		t.Errorf("Email: got %q, want %q", claims.Email, "u@example.com")
	}
}

func TestMCPSAMMinter_TTLOverride(t *testing.T) {
	signer := newTestTrustSigner(t, "mcp-gw-ttl")
	minter := NewMCPSAMMinter(signer, time.Hour)

	token, err := minter.MintForUser("u-1", "", "u@example.com", "U", nil, nil, 5*time.Minute)
	if err != nil {
		t.Fatalf("MintForUser: %v", err)
	}
	claims, err := ValidateSAMToken(token, signer)
	if err != nil {
		t.Fatalf("ValidateSAMToken: %v", err)
	}
	delta := claims.Expiry - claims.IssuedAt
	if delta < 290 || delta > 310 {
		t.Errorf("expected TTL ~300s (per-call override), got delta=%d", delta)
	}
}

func TestMCPAuthzResolver_DelegatesToService(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := NewAuthorizationService(AuthorizationServiceConfig{
		Type:         "none",
		DefaultRoles: []string{"default-role"},
	}, "gw-test", logger)
	if err != nil {
		t.Fatalf("NewAuthorizationService: %v", err)
	}
	resolver := NewMCPAuthzResolver(svc)

	_, scopes, _, err := resolver.Lookup(t.Context(), "u-1", nil)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !slices.Equal(scopes, []string{"*"}) {
		t.Errorf("Lookup scopes: got %v, want [*]", scopes)
	}
}

type stubOIDCClient struct {
	authURL       string
	authURLErr    error
	tokens        *TokenResponse
	tokensErr     error
	refreshTokens *TokenResponse
	refreshErr    error
	userInfo      *UserInfo
	userInfoErr   error

	gotState, gotRedirect, gotChallenge, gotChalMethod string
	gotCode, gotExchangeRedirect, gotVerifier          string
	gotRefreshToken                                    string
	gotAccessToken                                     string
}

func (s *stubOIDCClient) BuildAuthURL(_ context.Context, state, redirectURI, codeChallenge, codeChallengeMethod string) (string, error) {
	s.gotState, s.gotRedirect = state, redirectURI
	s.gotChallenge, s.gotChalMethod = codeChallenge, codeChallengeMethod
	return s.authURL, s.authURLErr
}
func (s *stubOIDCClient) ExchangeCode(_ context.Context, code, redirectURI, codeVerifier string) (*TokenResponse, error) {
	s.gotCode, s.gotExchangeRedirect, s.gotVerifier = code, redirectURI, codeVerifier
	return s.tokens, s.tokensErr
}
func (s *stubOIDCClient) RefreshToken(_ context.Context, refreshToken string) (*TokenResponse, error) {
	s.gotRefreshToken = refreshToken
	return s.refreshTokens, s.refreshErr
}
func (s *stubOIDCClient) GetUserInfo(_ context.Context, accessToken string) (*UserInfo, error) {
	s.gotAccessToken = accessToken
	return s.userInfo, s.userInfoErr
}
func (s *stubOIDCClient) ValidateToken(context.Context, string) (bool, error) { return true, nil }

func TestMCPIDPClient_BuildAuthURL_PassThrough(t *testing.T) {
	stub := &stubOIDCClient{authURL: "https://idp.example.com/authz?state=s"}
	c := NewMCPIDPClient(stub)

	got, err := c.BuildAuthURL(t.Context(), "s", "https://gw/cb", "ch", "S256")
	if err != nil {
		t.Fatalf("BuildAuthURL: %v", err)
	}
	if got != "https://idp.example.com/authz?state=s" {
		t.Errorf("authURL: got %q", got)
	}
	if stub.gotState != "s" || stub.gotRedirect != "https://gw/cb" ||
		stub.gotChallenge != "ch" || stub.gotChalMethod != "S256" {
		t.Errorf("inputs not forwarded: %+v", stub)
	}
}

func TestMCPIDPClient_BuildAuthURL_ErrorPropagates(t *testing.T) {
	stub := &stubOIDCClient{authURLErr: errors.New("discovery failed")}
	c := NewMCPIDPClient(stub)
	if _, err := c.BuildAuthURL(t.Context(), "s", "r", "", ""); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestMCPIDPClient_ExchangeCode_FieldMapping(t *testing.T) {
	stub := &stubOIDCClient{
		tokens: &TokenResponse{
			AccessToken: "at-1", RefreshToken: "rt-1",
			IDToken: "id-1", ExpiresIn: 3600,
		},
	}
	c := NewMCPIDPClient(stub)

	got, err := c.ExchangeCode(t.Context(), "code-1", "https://gw/cb", "verifier")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	want := &mcpauth.IDPTokens{
		AccessToken: "at-1", RefreshToken: "rt-1",
		IDToken: "id-1", ExpiresIn: 3600,
	}
	if *got != *want {
		t.Errorf("tokens mapped wrong: got %+v, want %+v", got, want)
	}
	if stub.gotCode != "code-1" || stub.gotExchangeRedirect != "https://gw/cb" || stub.gotVerifier != "verifier" {
		t.Errorf("inputs not forwarded: %+v", stub)
	}
}

func TestMCPIDPClient_ExchangeCode_ErrorPropagates(t *testing.T) {
	stub := &stubOIDCClient{tokensErr: errors.New("token endpoint 5xx")}
	c := NewMCPIDPClient(stub)
	if _, err := c.ExchangeCode(t.Context(), "c", "r", "v"); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestMCPIDPClient_GetUserInfo_RawIsMergedClaimsNotRawResponse(t *testing.T) {
	claims := map[string]any{"groups": []any{"admins"}, "tenant_id": "t-1"}
	rawResp := map[string]any{"different": "ignored"}
	stub := &stubOIDCClient{
		userInfo: &UserInfo{
			Sub: "u-1", Email: "u@example.com", Name: "U",
			Claims: claims, RawResponse: rawResp,
		},
	}
	c := NewMCPIDPClient(stub)

	got, err := c.GetUserInfo(t.Context(), "access-token-1")
	if err != nil {
		t.Fatalf("GetUserInfo: %v", err)
	}
	if got.Sub != "u-1" || got.Email != "u@example.com" || got.Name != "U" {
		t.Errorf("identity fields wrong: %+v", got)
	}
	if g, ok := got.Raw["groups"]; !ok {
		t.Errorf("Raw missing 'groups' — wrapper mapped RawResponse instead of Claims? got %+v", got.Raw)
	} else if gs, _ := g.([]any); len(gs) != 1 || gs[0] != "admins" {
		t.Errorf("Raw groups wrong: %v", g)
	}
	if _, ok := got.Raw["different"]; ok {
		t.Errorf("Raw must come from Claims, not RawResponse; got %+v", got.Raw)
	}
	if stub.gotAccessToken != "access-token-1" {
		t.Errorf("access token not forwarded: %q", stub.gotAccessToken)
	}
}

func TestMCPIDPClient_GetUserInfo_ErrorPropagates(t *testing.T) {
	stub := &stubOIDCClient{userInfoErr: errors.New("userinfo 401")}
	c := NewMCPIDPClient(stub)
	if _, err := c.GetUserInfo(t.Context(), "at"); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestMCPIDPClient_RefreshToken_FieldMapping(t *testing.T) {
	stub := &stubOIDCClient{
		refreshTokens: &TokenResponse{
			AccessToken: "at-2", RefreshToken: "rt-2",
			IDToken: "id-2", ExpiresIn: 1800,
		},
	}
	c := NewMCPIDPClient(stub)

	got, err := c.RefreshToken(t.Context(), "rt-1")
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	want := &mcpauth.IDPTokens{
		AccessToken: "at-2", RefreshToken: "rt-2",
		IDToken: "id-2", ExpiresIn: 1800,
	}
	if *got != *want {
		t.Errorf("refresh tokens mapped wrong: got %+v, want %+v", got, want)
	}
	if stub.gotRefreshToken != "rt-1" {
		t.Errorf("refresh token not forwarded: %q", stub.gotRefreshToken)
	}
}

func TestMCPIDPClient_RefreshToken_ErrorPropagates(t *testing.T) {
	stub := &stubOIDCClient{refreshErr: errors.New("refresh 400")}
	c := NewMCPIDPClient(stub)
	if _, err := c.RefreshToken(t.Context(), "rt"); err == nil {
		t.Fatal("expected error to propagate")
	}
}
