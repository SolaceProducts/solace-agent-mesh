// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
)

func TestSelectTokenAuthMethod(t *testing.T) {
	cases := []struct {
		name      string
		supported []string
		want      string
	}{
		{name: "empty defaults to client_secret_basic", supported: nil, want: "client_secret_basic"},
		{name: "empty slice defaults to client_secret_basic", supported: []string{}, want: "client_secret_basic"},
		{name: "server lists basic first", supported: []string{"client_secret_basic", "client_secret_post", "none"}, want: "client_secret_basic"},
		{name: "server lists post first wins over basic", supported: []string{"client_secret_post", "client_secret_basic"}, want: "client_secret_post"},
		{name: "post when basic unsupported", supported: []string{"client_secret_post", "none"}, want: "client_secret_post"},
		{name: "none fallback", supported: []string{"none"}, want: "none"},
		{name: "unsupported method falls back to basic default", supported: []string{"private_key_jwt"}, want: "client_secret_basic"},
		{name: "skips unsupported then picks supported", supported: []string{"private_key_jwt", "client_secret_post"}, want: "client_secret_post"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := selectTokenAuthMethod(tc.supported)
			if got != tc.want {
				t.Errorf("selectTokenAuthMethod(%v) = %q, want %q", tc.supported, got, tc.want)
			}
		})
	}
}

func TestRegisterOAuthClient_DelegatesToToolauth(t *testing.T) {
	var (
		gotAccept string
		gotBody   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "cid",
			"client_secret":              "shh",
			"token_endpoint_auth_method": "client_secret_basic",
		})
	}))
	defer srv.Close()

	reg, err := registerOAuthClient(
		context.Background(),
		srv.URL,
		"http://localhost/cb",
		"email profile",
		&toolauth.OAuthMetadata{
			TokenEndpointAuthMethodsSupported: []string{"client_secret_basic"},
		},
		slog.Default(),
	)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if reg.ClientID != "cid" || reg.ClientSecret != "shh" {
		t.Errorf("unexpected registration response: %+v", reg)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept header = %q, want application/json", gotAccept)
	}
	scope, _ := gotBody["scope"].(string)
	if scope != "email profile" {
		t.Errorf("scope in request body = %q, want %q", scope, "email profile")
	}
	if gotBody["token_endpoint_auth_method"] != "client_secret_basic" {
		t.Errorf("token_endpoint_auth_method = %v, want client_secret_basic", gotBody["token_endpoint_auth_method"])
	}
}

func TestRegisterOAuthClient_ParsesRFC7591ErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_redirect_uri","error_description":"nope"}`))
	}))
	defer srv.Close()

	_, err := registerOAuthClient(
		context.Background(),
		srv.URL,
		"http://localhost/cb",
		"",
		nil,
		slog.Default(),
	)
	if err == nil {
		t.Fatal("expected error from server rejection")
	}
	if !strings.Contains(err.Error(), "registration") && !strings.Contains(err.Error(), "client_id") {
		t.Errorf("error does not surface registration failure: %v", err)
	}
}

func TestRegisterOAuthClient_SuppressesEnvFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	t.Setenv("TOOL_OAUTH_CLIENT_ID", "leaked-platform-client")
	t.Setenv("TOOL_OAUTH_CLIENT_SECRET", "leaked-platform-secret")
	t.Setenv("MCP_CLIENT_ID", "leaked-mcp-client")
	t.Setenv("MCP_CLIENT_SECRET", "leaked-mcp-secret")

	toolauth.TightenDiscoveryRetryForTest(t)

	reg, err := registerOAuthClient(
		context.Background(),
		srv.URL,
		"http://localhost/cb",
		"",
		nil,
		slog.Default(),
	)
	if err == nil {
		t.Fatalf("expected hard error from DCR failure, got reg=%+v", reg)
	}
	if reg != nil {
		t.Errorf("expected nil registration, got %+v (env fallback leaked)", reg)
	}
	if strings.Contains(err.Error(), "leaked-") {
		t.Errorf("error contains env-var-derived credentials: %v", err)
	}
}
