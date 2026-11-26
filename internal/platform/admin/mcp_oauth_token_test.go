// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type capturedTokenRequest struct {
	bodyClientID     string
	bodyClientSecret string
	authHeader       string
}

func captureExchange(t *testing.T, authMethod string) capturedTokenRequest {
	t.Helper()
	var got capturedTokenRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got.bodyClientID = r.Form.Get("client_id")
		got.bodyClientSecret = r.Form.Get("client_secret")
		got.authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","token_type":"Bearer"}`))
	}))
	defer srv.Close()

	token, err := exchangeOAuthCode(t.Context(), srv.URL, "code", "verifier", "http://localhost/cb", "cid", "csecret", authMethod)
	if err != nil {
		t.Fatalf("%s: exchangeOAuthCode error: %v", authMethod, err)
	}
	if token != "at" {
		t.Fatalf("%s: token = %q, want at", authMethod, token)
	}
	return got
}

func TestExchangeOAuthCode_SingleAuthMethod(t *testing.T) {
	t.Run("client_secret_basic uses header only, never a body client_id (Canva rejects it)", func(t *testing.T) {
		got := captureExchange(t, "client_secret_basic")
		if got.authHeader == "" {
			t.Error("client_secret_basic must send the Basic Authorization header")
		}
		if got.bodyClientID != "" {
			t.Errorf("client_secret_basic must NOT send client_id in the body, got %q", got.bodyClientID)
		}
		if got.bodyClientSecret != "" {
			t.Errorf("client_secret_basic must NOT send client_secret in the body, got %q", got.bodyClientSecret)
		}
	})

	t.Run("client_secret_post uses body only", func(t *testing.T) {
		got := captureExchange(t, "client_secret_post")
		if got.authHeader != "" {
			t.Errorf("client_secret_post must NOT set the Authorization header, got %q", got.authHeader)
		}
		if got.bodyClientID != "cid" {
			t.Errorf("client_secret_post must send client_id in the body, got %q", got.bodyClientID)
		}
		if got.bodyClientSecret != "csecret" {
			t.Errorf("client_secret_post must send client_secret in the body, got %q", got.bodyClientSecret)
		}
	})

	t.Run("none sends body client_id and no header or secret", func(t *testing.T) {
		got := captureExchange(t, "none")
		if got.authHeader != "" {
			t.Errorf("none must NOT set the Authorization header, got %q", got.authHeader)
		}
		if got.bodyClientID != "cid" {
			t.Errorf("none must send client_id in the body, got %q", got.bodyClientID)
		}
		if got.bodyClientSecret != "" {
			t.Errorf("none must NOT send client_secret, got %q", got.bodyClientSecret)
		}
	})
}
