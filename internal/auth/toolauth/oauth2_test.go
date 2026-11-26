// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestOAuthToken_IsExpired(t *testing.T) {
	tests := []struct {
		name string
		tok  *OAuthToken
		want bool
	}{
		{"nil token", nil, true},
		{"empty access token", &OAuthToken{}, true},
		{"zero ExpiresAt treated as no-expiry", &OAuthToken{AccessToken: "tok", ExpiresAt: 0}, false},
		{"future expiry", &OAuthToken{AccessToken: "tok", ExpiresAt: time.Now().Add(2 * time.Hour).Unix()}, false},
		{"past expiry", &OAuthToken{AccessToken: "tok", ExpiresAt: time.Now().Add(-1 * time.Hour).Unix()}, true},
		{"within 60s buffer", &OAuthToken{AccessToken: "tok", ExpiresAt: time.Now().Add(30 * time.Second).Unix()}, true},
		{"just outside buffer", &OAuthToken{AccessToken: "tok", ExpiresAt: time.Now().Add(2 * time.Minute).Unix()}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.tok.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateVerifier(t *testing.T) {
	v := GenerateVerifier()
	if len(v) != 43 {
		t.Errorf("verifier length = %d, want 43", len(v))
	}
}

func TestGenerateVerifier_Uniqueness(t *testing.T) {
	v1 := GenerateVerifier()
	v2 := GenerateVerifier()
	if v1 == v2 {
		t.Error("two verifiers should not be identical")
	}
}

func TestGenerateState(t *testing.T) {
	state := GenerateState()
	if len(state) != 43 {
		t.Errorf("state length = %d, want 43", len(state))
	}
}

func TestGenerateState_Uniqueness(t *testing.T) {
	s1 := GenerateState()
	s2 := GenerateState()
	if s1 == s2 {
		t.Error("two state values should not be identical")
	}
}

func TestBuildAuthorizationURL(t *testing.T) {
	t.Run("nil scheme", func(t *testing.T) {
		_, err := BuildAuthorizationURL(nil, "cid", "state", "verifier", "http://localhost/cb")
		if err == nil {
			t.Error("expected error for nil scheme")
		}
	})

	t.Run("empty authorization_url", func(t *testing.T) {
		_, err := BuildAuthorizationURL(&OAuthScheme{}, "cid", "state", "verifier", "http://localhost/cb")
		if err == nil {
			t.Error("expected error for empty authorization_url")
		}
	})

	t.Run("valid without scopes", func(t *testing.T) {
		scheme := &OAuthScheme{AuthorizationURL: "https://auth.example.com/authorize"}
		verifier := "my-test-verifier-value-43chars-long-enough!"
		got, err := BuildAuthorizationURL(scheme, "my-client", "my-state", verifier, "http://localhost:8000/cb")
		if err != nil {
			t.Fatalf("error: %v", err)
		}

		u, _ := url.Parse(got)
		q := u.Query()
		if q.Get("response_type") != "code" {
			t.Errorf("response_type = %q", q.Get("response_type"))
		}
		if q.Get("client_id") != "my-client" {
			t.Errorf("client_id = %q", q.Get("client_id"))
		}
		if q.Get("state") != "my-state" {
			t.Errorf("state = %q", q.Get("state"))
		}
		expectedChallenge := oauth2.S256ChallengeFromVerifier(verifier)
		if q.Get("code_challenge") != expectedChallenge {
			t.Errorf("code_challenge = %q, want %q", q.Get("code_challenge"), expectedChallenge)
		}
		if q.Get("code_challenge_method") != "S256" {
			t.Errorf("code_challenge_method = %q", q.Get("code_challenge_method"))
		}
		if q.Get("scope") != "" {
			t.Errorf("scope should be empty, got %q", q.Get("scope"))
		}
	})

	t.Run("valid with scopes", func(t *testing.T) {
		scheme := &OAuthScheme{
			AuthorizationURL: "https://auth.example.com/authorize",
			Scopes:           []string{"read", "write"},
		}
		got, err := BuildAuthorizationURL(scheme, "cid", "st", "verifier", "http://localhost/cb")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		u, _ := url.Parse(got)
		if u.Query().Get("scope") != "read write" {
			t.Errorf("scope = %q, want %q", u.Query().Get("scope"), "read write")
		}
	})
}

func TestResolveRedirectURI(t *testing.T) {
	t.Run("OAUTH_TOOL_REDIRECT_URI set", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "https://custom.example.com/callback")
		t.Setenv("FRONTEND_REDIRECT_URI", "https://frontend.example.com")
		got := ResolveRedirectURI()
		if got != "https://custom.example.com/callback" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("FRONTEND_REDIRECT_URI fallback", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "")
		t.Setenv("FRONTEND_REDIRECT_URI", "https://frontend.example.com")
		got := ResolveRedirectURI()
		if got != "https://frontend.example.com/api/v1/auth/tool/callback" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("FRONTEND_REDIRECT_URI with trailing slash", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "")
		t.Setenv("FRONTEND_REDIRECT_URI", "https://frontend.example.com/")
		got := ResolveRedirectURI()
		if got != "https://frontend.example.com/api/v1/auth/tool/callback" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("default loopback IP", func(t *testing.T) {
		t.Setenv("OAUTH_TOOL_REDIRECT_URI", "")
		t.Setenv("FRONTEND_REDIRECT_URI", "")
		got := ResolveRedirectURI()
		if got != "http://127.0.0.1:8800/api/v1/auth/tool/callback" {
			t.Errorf("got %q", got)
		}
	})
}

func TestExchangeCode(t *testing.T) {
	t.Run("nil scheme", func(t *testing.T) {
		_, err := ExchangeCode(context.Background(), nil, nil, "code", "verifier", "http://localhost/cb")
		if err == nil {
			t.Error("expected error for nil scheme")
		}
	})

	t.Run("empty token_url", func(t *testing.T) {
		_, err := ExchangeCode(context.Background(), &OAuthScheme{}, nil, "code", "verifier", "http://localhost/cb")
		if err == nil {
			t.Error("expected error for empty token_url")
		}
	})

	t.Run("success with client_secret_post", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "authorization_code" {
				t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
			}
			if r.Form.Get("code") != "the-code" {
				t.Errorf("code = %q", r.Form.Get("code"))
			}
			if r.Form.Get("code_verifier") != "the-verifier" {
				t.Errorf("code_verifier = %q", r.Form.Get("code_verifier"))
			}
			if r.Form.Get("client_id") != "cid" {
				t.Errorf("client_id = %q", r.Form.Get("client_id"))
			}
			if r.Form.Get("client_secret") != "csecret" {
				t.Errorf("client_secret = %q", r.Form.Get("client_secret"))
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("client_secret_post should not set Authorization header")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "new-at",
				"refresh_token": "new-rt",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		}))
		defer srv.Close()

		scheme := &OAuthScheme{
			TokenURL:                srv.URL + "/token",
			TokenEndpointAuthMethod: "client_secret_post",
		}
		cred := &AuthCredential{ClientID: "cid", ClientSecret: "csecret"}

		tok, err := ExchangeCode(context.Background(), scheme, cred, "the-code", "the-verifier", "http://localhost/cb")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if tok.AccessToken != "new-at" {
			t.Errorf("AccessToken = %q", tok.AccessToken)
		}
	})

	t.Run("success with client_secret_basic", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			auth := r.Header.Get("Authorization")
			if auth == "" {
				t.Error("expected Authorization header for client_secret_basic")
			}
			if !strings.HasPrefix(auth, "Basic ") {
				t.Errorf("expected Basic auth, got %q", auth)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "basic-at",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}))
		defer srv.Close()

		scheme := &OAuthScheme{
			TokenURL:                srv.URL + "/token",
			TokenEndpointAuthMethod: "client_secret_basic",
		}
		cred := &AuthCredential{ClientID: "cid", ClientSecret: "csecret"}

		tok, err := ExchangeCode(context.Background(), scheme, cred, "code", "verifier", "http://localhost/cb")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if tok.AccessToken != "basic-at" {
			t.Errorf("AccessToken = %q", tok.AccessToken)
		}
	})

	t.Run("success with none (public client)", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			if r.Header.Get("Authorization") != "" {
				t.Error("public client should not set Authorization header")
			}
			if r.Form.Get("client_id") != "public-cid" {
				t.Errorf("client_id = %q", r.Form.Get("client_id"))
			}
			if r.Form.Get("client_secret") != "" {
				t.Error("public client should not send client_secret")
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "public-at",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}))
		defer srv.Close()

		scheme := &OAuthScheme{
			TokenURL:                srv.URL + "/token",
			TokenEndpointAuthMethod: "none",
		}
		cred := &AuthCredential{ClientID: "public-cid"}

		tok, err := ExchangeCode(context.Background(), scheme, cred, "code", "verifier", "http://localhost/cb")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if tok.AccessToken != "public-at" {
			t.Errorf("AccessToken = %q", tok.AccessToken)
		}
	})

	t.Run("400 response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		}))
		defer srv.Close()

		scheme := &OAuthScheme{TokenURL: srv.URL + "/token"}
		_, err := ExchangeCode(context.Background(), scheme, nil, "code", "verifier", "http://localhost/cb")
		if err == nil {
			t.Error("expected error for 400 response")
		}
	})

	t.Run("invalid JSON response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{not json`))
		}))
		defer srv.Close()

		scheme := &OAuthScheme{TokenURL: srv.URL + "/token"}
		_, err := ExchangeCode(context.Background(), scheme, nil, "code", "verifier", "http://localhost/cb")
		if err == nil {
			t.Error("expected error for invalid JSON")
		}
	})
}

func TestRefreshToken(t *testing.T) {
	t.Run("nil scheme", func(t *testing.T) {
		_, err := RefreshToken(context.Background(), nil, nil, "rt")
		if err == nil {
			t.Error("expected error for nil scheme")
		}
	})

	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "refresh_token" {
				t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
			}
			if r.Form.Get("refresh_token") != "old-rt" {
				t.Errorf("refresh_token = %q", r.Form.Get("refresh_token"))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "refreshed-at",
				"refresh_token": "new-rt",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		}))
		defer srv.Close()

		scheme := &OAuthScheme{
			TokenURL: srv.URL + "/token",
			Scopes:   []string{"read", "write"},
		}

		tok, err := RefreshToken(context.Background(), scheme, nil, "old-rt")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if tok.AccessToken != "refreshed-at" {
			t.Errorf("AccessToken = %q", tok.AccessToken)
		}
		if tok.RefreshToken != "new-rt" {
			t.Errorf("RefreshToken = %q", tok.RefreshToken)
		}
	})

	t.Run("endpoint failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(500)
			_, _ = w.Write([]byte("server error"))
		}))
		defer srv.Close()

		scheme := &OAuthScheme{TokenURL: srv.URL + "/token"}
		_, err := RefreshToken(context.Background(), scheme, nil, "rt")
		if err == nil {
			t.Error("expected error for 500 response")
		}
	})
}

func TestClassifyOAuth2Error(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		if got := ClassifyOAuth2Error(nil); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("non-oauth2 error", func(t *testing.T) {
		if got := ClassifyOAuth2Error(fmt.Errorf("network down")); got != "" {
			t.Errorf("got %q, want empty (not an OAuth2 protocol error)", got)
		}
	})

	t.Run("invalid_client from token endpoint", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"client revoked"}`))
		}))
		defer srv.Close()

		_, err := RefreshToken(context.Background(), &OAuthScheme{TokenURL: srv.URL}, nil, "rt")
		if err == nil {
			t.Fatal("expected error")
		}
		if got := ClassifyOAuth2Error(err); got != "invalid_client" {
			t.Errorf("got %q, want invalid_client", got)
		}
	})

	t.Run("invalid_grant from token endpoint", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		}))
		defer srv.Close()

		_, err := RefreshToken(context.Background(), &OAuthScheme{TokenURL: srv.URL}, nil, "rt")
		if err == nil {
			t.Fatal("expected error")
		}
		if got := ClassifyOAuth2Error(err); got != "invalid_grant" {
			t.Errorf("got %q, want invalid_grant", got)
		}
	})

	t.Run("HTTP 500 without OAuth2 body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Internal Server Error"))
		}))
		defer srv.Close()

		_, err := RefreshToken(context.Background(), &OAuthScheme{TokenURL: srv.URL}, nil, "rt")
		if err == nil {
			t.Fatal("expected error")
		}
		if got := ClassifyOAuth2Error(err); got != "" {
			t.Errorf("got %q, want empty for non-OAuth2 500 response", got)
		}
	})
}

func TestExchangeCode_InvalidGrant_WrapsErrPKCEVerificationFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"code_verifier does not match code_challenge"}`))
	}))
	defer srv.Close()

	scheme := &OAuthScheme{TokenURL: srv.URL}
	_, err := ExchangeCode(context.Background(), scheme, &AuthCredential{ClientID: "c", ClientSecret: "s"}, "code", "verifier", "http://localhost/cb")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrPKCEVerificationFailed) {
		t.Errorf("err should wrap ErrPKCEVerificationFailed, got %v", err)
	}
}

func TestExchangeCode_InvalidRequest_DoesNotWrapPKCE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_request","error_description":"missing redirect_uri"}`))
	}))
	defer srv.Close()

	scheme := &OAuthScheme{TokenURL: srv.URL}
	_, err := ExchangeCode(context.Background(), scheme, &AuthCredential{ClientID: "c"}, "code", "verifier", "http://localhost/cb")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrPKCEVerificationFailed) {
		t.Errorf("err should NOT wrap ErrPKCEVerificationFailed for invalid_request, got %v", err)
	}
}

func TestBuildAuthorizationURL_AccessTypeAndPrompt(t *testing.T) {
	scheme := &OAuthScheme{AuthorizationURL: "https://auth.example.com/authorize"}
	got, err := BuildAuthorizationURL(scheme, "cid", "st", "v", "http://localhost/cb")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	u, _ := url.Parse(got)
	if u.Query().Get("access_type") != "offline" {
		t.Errorf("access_type = %q, want offline", u.Query().Get("access_type"))
	}
	if u.Query().Get("prompt") != "consent" {
		t.Errorf("prompt = %q, want consent", u.Query().Get("prompt"))
	}
	if u.Query().Get("audience") != "" {
		t.Errorf("audience should be absent, got %q", u.Query().Get("audience"))
	}
}

func TestBuildAuthorizationURL_Audience(t *testing.T) {
	scheme := &OAuthScheme{
		AuthorizationURL: "https://auth.example.com/authorize",
		Audience:         "https://api.example.com",
	}
	got, err := BuildAuthorizationURL(scheme, "cid", "st", "v", "http://localhost/cb")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	u, _ := url.Parse(got)
	if u.Query().Get("audience") != "https://api.example.com" {
		t.Errorf("audience = %q, want https://api.example.com", u.Query().Get("audience"))
	}
}

func TestBuildAuthorizationURL_DisablePKCE(t *testing.T) {
	scheme := &OAuthScheme{AuthorizationURL: "https://auth.example.com/authorize"}
	got, err := BuildAuthorizationURL(scheme, "cid", "st", "", "http://localhost/cb")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	u, _ := url.Parse(got)
	if got := u.Query().Get("code_challenge"); got != "" {
		t.Errorf("code_challenge = %q, want empty (PKCE disabled)", got)
	}
	if got := u.Query().Get("code_challenge_method"); got != "" {
		t.Errorf("code_challenge_method = %q, want empty (PKCE disabled)", got)
	}
}

func TestBuildAuthorizationURL_PKCEEnabled(t *testing.T) {
	scheme := &OAuthScheme{AuthorizationURL: "https://auth.example.com/authorize"}
	got, err := BuildAuthorizationURL(scheme, "cid", "st", "verifier123", "http://localhost/cb")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	u, _ := url.Parse(got)
	if u.Query().Get("code_challenge") == "" {
		t.Error("code_challenge missing; PKCE should be enabled when verifier is non-empty")
	}
	if got := u.Query().Get("code_challenge_method"); got != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", got)
	}
}

func TestExchangeCode_DisablePKCE_OmitsVerifier(t *testing.T) {
	var hasKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_, hasKey = r.Form["code_verifier"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	scheme := &OAuthScheme{TokenURL: srv.URL}
	cred := &AuthCredential{ClientID: "cid", ClientSecret: "secret"}

	_, err := ExchangeCode(context.Background(), scheme, cred, "code", "", "http://localhost/cb")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if hasKey {
		t.Error("code_verifier form field present when PKCE disabled; should be omitted")
	}
}

func TestExchangeCode_NoneMethod_EmptySecret_OmitsClientSecret(t *testing.T) {
	var hasClientSecretKey, clientSecretValue string
	var hasKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_, hasKey = r.Form["client_secret"]
		hasClientSecretKey = r.Form.Get("client_secret")
		clientSecretValue = hasClientSecretKey
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	scheme := &OAuthScheme{TokenURL: srv.URL, TokenEndpointAuthMethod: "none"}
	cred := &AuthCredential{ClientID: "public-client", ClientSecret: ""}

	_, err := ExchangeCode(context.Background(), scheme, cred, "code", "verifier", "http://localhost/cb")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if hasKey {
		t.Errorf("client_secret form key should be absent for method=none with empty secret, got value=%q", clientSecretValue)
	}
}

func TestExchangeCode_NoneMethod_NonEmptySecret_StillOmitsSecret(t *testing.T) {
	var hasKey bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_, hasKey = r.Form["client_secret"]
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"tok","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	scheme := &OAuthScheme{TokenURL: srv.URL, TokenEndpointAuthMethod: "none"}
	cred := &AuthCredential{ClientID: "c", ClientSecret: "stale-secret-from-old-config"}

	_, err := ExchangeCode(context.Background(), scheme, cred, "code", "verifier", "http://localhost/cb")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if hasKey {
		t.Error("client_secret form key present for method=none; must be absent even when cred has a stale secret")
	}
}

func TestExchangeClientCredentials_Happy(t *testing.T) {
	var gotGrantType, gotClientID, gotClientSecret, gotScope string
	var gotAuthHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotGrantType = r.Form.Get("grant_type")
		gotClientID = r.Form.Get("client_id")
		gotClientSecret = r.Form.Get("client_secret")
		gotScope = r.Form.Get("scope")
		gotAuthHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "cc-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	tok, err := ExchangeClientCredentials(
		context.Background(),
		srv.URL+"/token",
		"my-cid", "my-csecret",
		[]string{"read", "write"},
		"client_secret_post",
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if tok.AccessToken != "cc-access-token" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if tok.RefreshToken != "" {
		t.Errorf("client_credentials must not return refresh_token, got %q", tok.RefreshToken)
	}
	if gotGrantType != "client_credentials" {
		t.Errorf("grant_type = %q", gotGrantType)
	}
	if gotClientID != "my-cid" || gotClientSecret != "my-csecret" {
		t.Errorf("body client_id/secret = %q/%q", gotClientID, gotClientSecret)
	}
	if gotAuthHeader != "" {
		t.Errorf("Authorization header should be absent for method=client_secret_post, got %q", gotAuthHeader)
	}
	if gotScope != "read write" {
		t.Errorf("scope = %q, want 'read write'", gotScope)
	}
}

func TestExchangeClientCredentials_ClientSecretBasic(t *testing.T) {
	var gotAuthHeader, gotClientSecretBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotAuthHeader = r.Header.Get("Authorization")
		gotClientSecretBody = r.Form.Get("client_secret")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"basic-cc-token","token_type":"Bearer","expires_in":3600}`))
	}))
	defer srv.Close()

	tok, err := ExchangeClientCredentials(
		context.Background(),
		srv.URL+"/token",
		"cid", "csecret", nil,
		"client_secret_basic",
	)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if tok.AccessToken != "basic-cc-token" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
	if !strings.HasPrefix(gotAuthHeader, "Basic ") {
		t.Errorf("expected Basic auth, got %q", gotAuthHeader)
	}
	if gotClientSecretBody != "" {
		t.Errorf("client_secret must NOT appear in body for method=client_secret_basic, got %q", gotClientSecretBody)
	}
}

func TestExchangeClientCredentials_MissingTokenURL(t *testing.T) {
	_, err := ExchangeClientCredentials(context.Background(), "", "cid", "csecret", nil, "")
	if err == nil {
		t.Fatal("expected error for empty token_url")
	}
	if !strings.Contains(err.Error(), "token_url") {
		t.Errorf("err should mention token_url, got %v", err)
	}
}

func TestExchangeClientCredentials_MissingClientID(t *testing.T) {
	_, err := ExchangeClientCredentials(context.Background(), "https://example.com/token", "", "csecret", nil, "")
	if err == nil {
		t.Fatal("expected error for empty client_id")
	}
	if !strings.Contains(err.Error(), "client_id") {
		t.Errorf("err should mention client_id, got %v", err)
	}
}

func TestExchangeClientCredentials_InvalidClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client","error_description":"bad secret"}`))
	}))
	defer srv.Close()

	_, err := ExchangeClientCredentials(context.Background(), srv.URL, "cid", "wrong", nil, "client_secret_post")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := ClassifyOAuth2Error(err); got != "invalid_client" {
		t.Errorf("ClassifyOAuth2Error = %q, want invalid_client", got)
	}
}

func TestExchangeClientCredentials_InvalidScope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_scope","error_description":"unknown scope admin"}`))
	}))
	defer srv.Close()

	_, err := ExchangeClientCredentials(context.Background(), srv.URL, "cid", "csecret", []string{"admin"}, "client_secret_post")
	if err == nil {
		t.Fatal("expected error")
	}
	if got := ClassifyOAuth2Error(err); got != "invalid_scope" {
		t.Errorf("ClassifyOAuth2Error = %q, want invalid_scope", got)
	}
}

func TestExchangeClientCredentials_HonoursContext(t *testing.T) {
	blocker := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-blocker
	}))
	defer srv.Close()
	defer close(blocker)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := ExchangeClientCredentials(ctx, srv.URL, "cid", "csecret", nil, "")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestLooksLikePKCEError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic network", fmt.Errorf("connection refused"), false},
		{"mentions code_verifier",
			fmt.Errorf("oauth2: invalid code_verifier"),
			true,
		},
		{"mentions PKCE",
			fmt.Errorf("PKCE challenge mismatch"),
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikePKCEError(tt.err); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildAuthorizationURL_Resource(t *testing.T) {
	t.Run("present when set", func(t *testing.T) {
		scheme := &OAuthScheme{
			AuthorizationURL: "https://auth.example.com/authorize",
			Resource:         "https://mcp.example.com/mcp",
		}
		got, err := BuildAuthorizationURL(scheme, "cid", "st", "v", "http://localhost/cb")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		u, _ := url.Parse(got)
		if u.Query().Get("resource") != "https://mcp.example.com/mcp" {
			t.Errorf("resource = %q, want https://mcp.example.com/mcp", u.Query().Get("resource"))
		}
	})

	t.Run("absent when unset", func(t *testing.T) {
		scheme := &OAuthScheme{AuthorizationURL: "https://auth.example.com/authorize"}
		got, err := BuildAuthorizationURL(scheme, "cid", "st", "v", "http://localhost/cb")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		u, _ := url.Parse(got)
		if u.Query().Get("resource") != "" {
			t.Errorf("resource should be absent, got %q", u.Query().Get("resource"))
		}
	})
}

func TestExchangeCode_Resource(t *testing.T) {
	var gotResource string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotResource = r.Form.Get("resource")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	scheme := &OAuthScheme{
		TokenURL: srv.URL + "/token",
		Resource: "https://mcp.example.com/mcp",
	}
	if _, err := ExchangeCode(context.Background(), scheme, nil, "code", "verifier", "http://localhost/cb"); err != nil {
		t.Fatalf("error: %v", err)
	}
	if gotResource != "https://mcp.example.com/mcp" {
		t.Errorf("token request resource = %q, want https://mcp.example.com/mcp", gotResource)
	}
}

func TestRefreshToken_Resource(t *testing.T) {
	var gotResource, gotGrant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotResource = r.Form.Get("resource")
		gotGrant = r.Form.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "refreshed-at",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	scheme := &OAuthScheme{
		TokenURL: srv.URL + "/token",
		Resource: "https://mcp.example.com/mcp",
	}
	if _, err := RefreshToken(context.Background(), scheme, nil, "old-rt"); err != nil {
		t.Fatalf("error: %v", err)
	}
	if gotGrant != "refresh_token" {
		t.Errorf("grant_type = %q, want refresh_token", gotGrant)
	}
	if gotResource != "https://mcp.example.com/mcp" {
		t.Errorf("refresh request resource = %q, want https://mcp.example.com/mcp", gotResource)
	}
}

func TestRefreshToken_NoResource(t *testing.T) {
	var hadResource bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		_, hadResource = r.Form["resource"]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "refreshed-at",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	scheme := &OAuthScheme{TokenURL: srv.URL + "/token"}
	if _, err := RefreshToken(context.Background(), scheme, nil, "old-rt"); err != nil {
		t.Fatalf("error: %v", err)
	}
	if hadResource {
		t.Error("refresh request carried a resource field when none was configured")
	}
}

type recordingRoundTripper struct {
	body        string
	contentType string
	contentLen  int64
	getBody     func() (io.ReadCloser, error)
}

func (rt *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		rt.body = string(b)
	}
	rt.contentType = req.Header.Get("Content-Type")
	rt.contentLen = req.ContentLength
	rt.getBody = req.GetBody
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
}

func formReq(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://idp.example.com/token", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestResourceParamTransport_RoundTrip(t *testing.T) {
	t.Run("appends resource to form body with consistent ContentLength and GetBody", func(t *testing.T) {
		rec := &recordingRoundTripper{}
		tr := &resourceParamTransport{inner: rec, resource: "https://mcp.example.com/mcp"}
		if _, err := tr.RoundTrip(formReq(t, "grant_type=refresh_token&refresh_token=rt")); err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		vals, _ := url.ParseQuery(rec.body)
		if vals.Get("resource") != "https://mcp.example.com/mcp" {
			t.Errorf("forwarded resource = %q, want injected value", vals.Get("resource"))
		}
		if vals.Get("grant_type") != "refresh_token" || vals.Get("refresh_token") != "rt" {
			t.Errorf("original params lost: %q", rec.body)
		}
		if rec.contentLen != int64(len(rec.body)) {
			t.Errorf("ContentLength = %d, want %d", rec.contentLen, len(rec.body))
		}
		if rec.getBody == nil {
			t.Fatal("GetBody must be set so redirects/retries can replay the body")
		}
		rc, err := rec.getBody()
		if err != nil {
			t.Fatalf("GetBody: %v", err)
		}
		replayed, _ := io.ReadAll(rc)
		if string(replayed) != rec.body {
			t.Errorf("GetBody replay = %q, want %q", string(replayed), rec.body)
		}
	})

	t.Run("does not clobber an existing resource", func(t *testing.T) {
		rec := &recordingRoundTripper{}
		tr := &resourceParamTransport{inner: rec, resource: "https://injected.example.com"}
		if _, err := tr.RoundTrip(formReq(t, "grant_type=refresh_token&resource=https%3A%2F%2Fpreset.example.com")); err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		vals, _ := url.ParseQuery(rec.body)
		if vals.Get("resource") != "https://preset.example.com" {
			t.Errorf("resource = %q, want the pre-existing value preserved", vals.Get("resource"))
		}
	})

	t.Run("passes non-form content-type through untouched", func(t *testing.T) {
		rec := &recordingRoundTripper{}
		tr := &resourceParamTransport{inner: rec, resource: "https://injected.example.com"}
		req, _ := http.NewRequest(http.MethodPost, "https://idp.example.com/token", strings.NewReader(`{"grant_type":"refresh_token"}`))
		req.Header.Set("Content-Type", "application/json")
		if _, err := tr.RoundTrip(req); err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		if strings.Contains(rec.body, "resource") {
			t.Errorf("resource injected into a non-form body: %q", rec.body)
		}
	})

	t.Run("forwards an unparseable body unchanged", func(t *testing.T) {
		rec := &recordingRoundTripper{}
		tr := &resourceParamTransport{inner: rec, resource: "https://injected.example.com"}
		if _, err := tr.RoundTrip(formReq(t, "%zz")); err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		if rec.body != "%zz" {
			t.Errorf("body = %q, want the original forwarded unchanged", rec.body)
		}
	})

	t.Run("no-op when resource is empty", func(t *testing.T) {
		rec := &recordingRoundTripper{}
		tr := &resourceParamTransport{inner: rec, resource: ""}
		if _, err := tr.RoundTrip(formReq(t, "grant_type=refresh_token")); err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
		if strings.Contains(rec.body, "resource") {
			t.Errorf("resource injected when none configured: %q", rec.body)
		}
	})
}
