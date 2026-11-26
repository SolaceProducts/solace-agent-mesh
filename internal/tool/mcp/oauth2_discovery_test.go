// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestDiscoverOAuthMetadata_WWWAuthenticateHintPointsAtRFC9728Document(t *testing.T) {
	discoveryCacheMu.Lock()
	*discoveryCache = make(map[string]*OAuthMetadata)
	discoveryCacheMu.Unlock()

	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://auth.example.com",
			"authorization_endpoint": "https://auth.example.com/authorize",
			"token_endpoint":         "https://auth.example.com/token",
			"registration_endpoint":  "https://auth.example.com/register",
			"scopes_supported":       []string{"read", "write"},
		})
	}))
	defer authSrv.Close()

	rmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              "custom",
			"authorization_servers": []string{authSrv.URL},
		})
	}))
	defer rmSrv.Close()

	resSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+rmSrv.URL+`"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer resSrv.Close()

	meta, err := DiscoverOAuthMetadata(context.Background(), resSrv.URL)
	if err != nil {
		t.Fatalf("DiscoverOAuthMetadata() error: %v", err)
	}

	if meta.AuthorizationEndpoint != "https://auth.example.com/authorize" {
		t.Errorf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
	}
	if meta.TokenEndpoint != "https://auth.example.com/token" {
		t.Errorf("TokenEndpoint = %q", meta.TokenEndpoint)
	}
	if meta.RegistrationEndpoint != "https://auth.example.com/register" {
		t.Errorf("RegistrationEndpoint = %q", meta.RegistrationEndpoint)
	}
}

func TestDiscoverOAuthMetadata_WellKnown(t *testing.T) {
	discoveryCacheMu.Lock()
	*discoveryCache = make(map[string]*OAuthMetadata)
	discoveryCacheMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 "https://auth.example.com",
			"authorization_endpoint": "https://auth.example.com/authorize",
			"token_endpoint":         "https://auth.example.com/token",
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			return
		}
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	meta, err := DiscoverOAuthMetadata(context.Background(), srv.URL+"/resource")
	if err != nil {
		t.Fatalf("DiscoverOAuthMetadata() error: %v", err)
	}

	if meta.AuthorizationEndpoint != "https://auth.example.com/authorize" {
		t.Errorf("AuthorizationEndpoint = %q", meta.AuthorizationEndpoint)
	}
}

func TestDiscoverOAuthMetadata_NoDiscoveryEndpointsReturnsError(t *testing.T) {
	discoveryCacheMu.Lock()
	*discoveryCache = make(map[string]*OAuthMetadata)
	discoveryCacheMu.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := DiscoverOAuthMetadata(context.Background(), srv.URL)
	if err == nil {
		t.Error("expected error when server has no discovery endpoints")
	}
}

func TestParseResourceMetadataURL(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"empty", "", ""},
		{"no resource_metadata", "Bearer realm=\"example\"", ""},
		{"quoted", `Bearer resource_metadata="https://auth.example.com/.well-known/oauth"`, "https://auth.example.com/.well-known/oauth"},
		{"unquoted", `Bearer resource_metadata=https://auth.example.com/.well-known/oauth`, "https://auth.example.com/.well-known/oauth"},
		{"with other params", `Bearer realm="example", resource_metadata="https://auth.example.com/meta", scope="read"`, "https://auth.example.com/meta"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseResourceMetadataURL(tt.header); got != tt.want {
				t.Errorf("parseResourceMetadataURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildWellKnownURLs(t *testing.T) {
	tests := []struct {
		name       string
		authServer string
		resource   string
		expected   []string
	}{
		{
			"nil authServer, root resource",
			"",
			"https://example.com",
			[]string{
				"https://example.com/.well-known/oauth-authorization-server",
				"https://example.com/.well-known/openid-configuration",
			},
		},
		{
			"nil authServer, resource with path",
			"",
			"https://example.com/api/v1",
			[]string{
				"https://example.com/.well-known/oauth-authorization-server/api/v1",
				"https://example.com/.well-known/oauth-authorization-server",
				"https://example.com/.well-known/openid-configuration/api/v1",
				"https://example.com/.well-known/openid-configuration",
			},
		},
		{
			"distinct authServer and resource",
			"https://auth.example.com",
			"https://mcp.example.com",
			[]string{
				"https://auth.example.com/.well-known/oauth-authorization-server",
				"https://auth.example.com/.well-known/openid-configuration",
				"https://mcp.example.com/.well-known/openid-configuration",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, _ := url.Parse(tt.resource)
			var as *url.URL
			if tt.authServer != "" {
				as, _ = url.Parse(tt.authServer)
			}
			got := buildWellKnownURLs(as, res)
			if len(got) != len(tt.expected) {
				t.Fatalf("len = %d, want %d: %v", len(got), len(tt.expected), got)
			}
			for i, want := range tt.expected {
				if got[i] != want {
					t.Errorf("url[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestRegisterClient_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)

		if req["client_name"] != "Solace Agent Mesh" {
			t.Errorf("client_name = %v", req["client_name"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                  "registered-client-id",
			"client_secret":              "registered-secret",
			"token_endpoint_auth_method": "client_secret_post",
		})
	}))
	defer srv.Close()

	reg, err := RegisterClient(context.Background(), srv.URL+"/register", "http://localhost/callback", nil)
	if err != nil {
		t.Fatalf("RegisterClient() error: %v", err)
	}

	if reg.ClientID != "registered-client-id" {
		t.Errorf("ClientID = %q", reg.ClientID)
	}
	if reg.ClientSecret != "registered-secret" {
		t.Errorf("ClientSecret = %q", reg.ClientSecret)
	}
}

func TestRegisterClient_FallbackToEnv(t *testing.T) {
	t.Setenv("MCP_CLIENT_ID", "env-client-id")
	t.Setenv("MCP_CLIENT_SECRET", "env-secret")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	reg, err := RegisterClient(context.Background(), srv.URL+"/register", "http://localhost/callback", nil)
	if err != nil {
		t.Fatalf("RegisterClient() error: %v", err)
	}

	if reg.ClientID != "env-client-id" {
		t.Errorf("ClientID = %q, want env fallback", reg.ClientID)
	}
}

func TestRegisterClient_NoURLFallbackToEnv(t *testing.T) {
	t.Setenv("MCP_CLIENT_ID", "env-id")
	t.Setenv("MCP_CLIENT_SECRET", "")

	reg, err := RegisterClient(context.Background(), "", "http://localhost/callback", nil)
	if err != nil {
		t.Fatalf("RegisterClient() error: %v", err)
	}

	if reg.ClientID != "env-id" {
		t.Errorf("ClientID = %q", reg.ClientID)
	}
}

func TestRegisterClient_NoURLNoEnv(t *testing.T) {
	t.Setenv("MCP_CLIENT_ID", "")
	t.Setenv("MCP_CLIENT_SECRET", "")

	_, err := RegisterClient(context.Background(), "", "http://localhost/callback", nil)
	if err == nil {
		t.Fatal("expected error when no registration URL and no env vars")
	}
}

func TestOAuthMetadata_IsExpired(t *testing.T) {
	if m := (*OAuthMetadata)(nil); !m.IsExpired() {
		t.Error("nil metadata should be expired")
	}

	m := &OAuthMetadata{DiscoveredAt: time.Now()}
	if m.IsExpired() {
		t.Error("fresh metadata should not be expired")
	}
}
