// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

const (
	testClientID     = "pkce-test-client"
	testClientSecret = "pkce-test-secret"
	testRedirectURI  = "http://localhost:8000/api/v1/auth/tool/callback"
	testUserID       = "pkce-user@example.com"
)

func exchangeCode(t *testing.T, tokenURL string, form url.Values) (status int, body map[string]any) {
	t.Helper()

	resp, err := http.PostForm(tokenURL, form)
	if err != nil {
		t.Fatalf("POST %s: %v", tokenURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	body = make(map[string]any)
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode response %q: %v", string(raw), err)
		}
	}
	return resp.StatusCode, body
}

func authCodeForm(code, verifier string) url.Values {
	f := url.Values{}
	f.Set("grant_type", "authorization_code")
	f.Set("code", code)
	f.Set("code_verifier", verifier)
	f.Set("redirect_uri", testRedirectURI)
	f.Set("client_id", testClientID)
	f.Set("client_secret", testClientSecret)
	return f
}

func TestMockOAuthProvider_PKCEValidation(t *testing.T) {
	provider, err := NewMockOAuthProvider(MockOAuthProviderConfig{
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
	})
	if err != nil {
		t.Fatalf("start provider: %v", err)
	}
	t.Cleanup(provider.Close)

	verifier := oauth2.GenerateVerifier()
	challenge := oauth2.S256ChallengeFromVerifier(verifier)

	code := provider.IssueAuthorizationCode(testUserID, challenge, testRedirectURI, testClientID)
	if code == "" {
		t.Fatal("IssueAuthorizationCode returned empty code")
	}

	status, body := exchangeCode(t, provider.TokenURL(), authCodeForm(code, verifier))
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %v", status, body)
	}

	accessToken, _ := body["access_token"].(string)
	if accessToken == "" {
		t.Errorf("response missing access_token: %v", body)
	}
	if tt, _ := body["token_type"].(string); !strings.EqualFold(tt, "Bearer") {
		t.Errorf("token_type = %q, want Bearer", tt)
	}
	if refresh, _ := body["refresh_token"].(string); refresh == "" {
		t.Error("response missing refresh_token (default config should issue one)")
	}

	if !provider.IsAccessTokenValid(accessToken) {
		t.Error("issued access token should be valid")
	}
	if got, want := provider.AccessTokenForUser(testUserID), accessToken; got != want {
		t.Errorf("AccessTokenForUser = %q, want %q", got, want)
	}
	if got := provider.AuthCodeGrantCount(); got != 1 {
		t.Errorf("AuthCodeGrantCount = %d, want 1", got)
	}
	if got := provider.ErrorCount(); got != 0 {
		t.Errorf("ErrorCount = %d, want 0", got)
	}
}

func TestMockOAuthProvider_PKCEFailure(t *testing.T) {
	provider, err := NewMockOAuthProvider(MockOAuthProviderConfig{
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
	})
	if err != nil {
		t.Fatalf("start provider: %v", err)
	}
	t.Cleanup(provider.Close)

	challenge := oauth2.S256ChallengeFromVerifier(oauth2.GenerateVerifier())
	code := provider.IssueAuthorizationCode(testUserID, challenge, testRedirectURI, testClientID)

	status, body := exchangeCode(t, provider.TokenURL(), authCodeForm(code, "totally-wrong-verifier"))
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %v, want 400", status, body)
	}
	if e, _ := body["error"].(string); e != "invalid_grant" {
		t.Errorf("error = %q, want invalid_grant", e)
	}

	if got := provider.AuthCodeGrantCount(); got != 0 {
		t.Errorf("AuthCodeGrantCount = %d, want 0", got)
	}
	if got := provider.ErrorCount(); got != 1 {
		t.Errorf("ErrorCount = %d, want 1", got)
	}
	if tok := provider.AccessTokenForUser(testUserID); tok != "" {
		t.Errorf("no token should be issued for failed PKCE, got %q", tok)
	}
}

func TestMockOAuthProvider_CodeReplay(t *testing.T) {
	provider, err := NewMockOAuthProvider(MockOAuthProviderConfig{
		ClientID:     testClientID,
		ClientSecret: testClientSecret,
	})
	if err != nil {
		t.Fatalf("start provider: %v", err)
	}
	t.Cleanup(provider.Close)

	verifier := oauth2.GenerateVerifier()
	challenge := oauth2.S256ChallengeFromVerifier(verifier)
	code := provider.IssueAuthorizationCode(testUserID, challenge, testRedirectURI, testClientID)

	status1, body1 := exchangeCode(t, provider.TokenURL(), authCodeForm(code, verifier))
	if status1 != http.StatusOK {
		t.Fatalf("first exchange: status = %d, body = %v", status1, body1)
	}

	status2, body2 := exchangeCode(t, provider.TokenURL(), authCodeForm(code, verifier))
	if status2 != http.StatusBadRequest {
		t.Fatalf("replay: status = %d, body = %v, want 400", status2, body2)
	}
	if e, _ := body2["error"].(string); e != "invalid_grant" {
		t.Errorf("replay error = %q, want invalid_grant", e)
	}

	if got := provider.AuthCodeGrantCount(); got != 1 {
		t.Errorf("AuthCodeGrantCount = %d, want 1 (only first exchange succeeds)", got)
	}
	if got := provider.ErrorCount(); got != 1 {
		t.Errorf("ErrorCount = %d, want 1 (replay)", got)
	}
}
