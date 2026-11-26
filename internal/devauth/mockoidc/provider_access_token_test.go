// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockoidc

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
)

func signInAndClient(t *testing.T, p *MockOIDCProvider, sub string) (*oidcauth.NativeOIDCClient, *oidcauth.TokenResponse) {
	t.Helper()
	nc, err := oidcauth.NewNativeOIDCClient(oidcauth.OIDCConfig{Issuer: p.URL(), ClientID: "c", ClientSecret: "s"}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}
	redirectURI := "http://cb.example/cb"
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(fmt.Sprintf("%s/authorize?client_id=c&response_type=code&redirect_uri=%s&fake_user_sub=%s",
		p.URL(), url.QueryEscape(redirectURI), url.QueryEscape(sub)))
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	_ = resp.Body.Close()
	tok, err := nc.ExchangeCode(t.Context(), mustQueryParam(t, resp.Header.Get("Location"), "code"), redirectURI, "")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	return nc, tok
}

func TestMockOIDCProvider_AccessTokenClaimsPerGrant(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s", Users: []FakeOIDCUser{{Sub: "alice"}},
		AccessTokenClaims: func(sub, grantType string) map[string]any {
			return map[string]any{"grant": grantType, "for": sub}
		},
	})
	t.Cleanup(func() { _ = p.Close() })

	nc, tok := signInAndClient(t, p, "alice")
	sub, claims, ok := p.AccessToken(tok.AccessToken)
	if !ok || sub != "alice" || claims["grant"] != "authorization_code" {
		t.Fatalf("signed-in token = %q %v %v, want alice with the authorization_code claims", sub, claims, ok)
	}
	refreshed, err := nc.RefreshToken(t.Context(), tok.RefreshToken)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if _, claims, _ := p.AccessToken(refreshed.AccessToken); claims["grant"] != "refresh_token" {
		t.Errorf("refreshed token claims = %v, want the refresh_token claims", claims)
	}
	if _, _, ok := p.AccessToken("at_never_issued"); ok {
		t.Error("an access token the provider never issued was reported")
	}
}

func TestMockOIDCProvider_RotateRefreshTokens(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s", Users: []FakeOIDCUser{{Sub: "alice"}}, RotateRefreshTokens: true,
	})
	t.Cleanup(func() { _ = p.Close() })

	nc, tok := signInAndClient(t, p, "alice")
	rotated, err := nc.RefreshToken(t.Context(), tok.RefreshToken)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if rotated.RefreshToken == "" || rotated.RefreshToken == tok.RefreshToken {
		t.Fatalf("refresh returned refresh token %q, want a new one", rotated.RefreshToken)
	}
	if _, err := nc.RefreshToken(t.Context(), tok.RefreshToken); err == nil {
		t.Error("the retired refresh token was redeemed again")
	}
	if _, err := nc.RefreshToken(t.Context(), rotated.RefreshToken); err != nil {
		t.Errorf("the rotated refresh token was refused: %v", err)
	}
}

func TestMockOIDCProvider_RevokeTokens(t *testing.T) {
	p, _ := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s", Users: []FakeOIDCUser{{Sub: "alice"}, {Sub: "bob"}},
	})
	t.Cleanup(func() { _ = p.Close() })

	nc, alice := signInAndClient(t, p, "alice")
	_, bob := signInAndClient(t, p, "bob")
	p.RevokeTokens("alice")

	if _, _, ok := p.AccessToken(alice.AccessToken); ok {
		t.Error("alice's access token survived the revocation")
	}
	if _, err := nc.RefreshToken(t.Context(), alice.RefreshToken); err == nil {
		t.Error("alice's refresh token survived the revocation")
	}
	if _, _, ok := p.AccessToken(bob.AccessToken); !ok {
		t.Error("revoking alice's tokens revoked bob's")
	}
}
