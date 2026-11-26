// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func clearDiscoveryCache() {
	toolauth.DiscoveryCacheMu.Lock()
	toolauth.DiscoveryCache = make(map[string]*toolauth.OAuthMetadata)
	toolauth.DiscoveryCacheMu.Unlock()
}

func TestMockMCPAuthServer_DiscoveryEndpoints(t *testing.T) {
	srv, err := NewMockMCPAuthServer("some-token")
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	defer srv.Close()

	t.Run("oauth-protected-resource returns RFC 9728 shape", func(t *testing.T) {
		resp, err := http.Get(srv.URL() + "/.well-known/oauth-protected-resource")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
		}
		body, _ := io.ReadAll(resp.Body)
		if !strings.Contains(string(body), `"authorization_servers"`) {
			t.Errorf("response missing authorization_servers: %s", body)
		}
		if !strings.Contains(string(body), srv.URL()) {
			t.Errorf("response should advertise the server as auth server: %s", body)
		}
	})

	t.Run("oauth-authorization-server returns RFC 8414 shape", func(t *testing.T) {
		resp, err := http.Get(srv.URL() + "/.well-known/oauth-authorization-server")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck
		body, _ := io.ReadAll(resp.Body)
		for _, field := range []string{
			"authorization_endpoint", "token_endpoint", "registration_endpoint",
		} {
			if !strings.Contains(string(body), field) {
				t.Errorf("response missing %s: %s", field, body)
			}
		}
	})

	t.Run("register returns fresh client_id per POST", func(t *testing.T) {
		resp1, err := http.Post(srv.URL()+"/register", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST 1: %v", err)
		}
		body1, _ := io.ReadAll(resp1.Body)
		resp1.Body.Close() //nolint:errcheck

		resp2, err := http.Post(srv.URL()+"/register", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST 2: %v", err)
		}
		body2, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close() //nolint:errcheck

		if string(body1) == string(body2) {
			t.Error("successive /register POSTs returned identical bodies — client_id should increment")
		}
	})

	t.Run("register does not burn counter values on rejected requests", func(t *testing.T) {
		srv.EnableOAuthDiscovery(false)
		for range 3 {
			resp, err := http.Post(srv.URL()+"/register", "application/json", strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			resp.Body.Close() //nolint:errcheck
		}
		srv.EnableOAuthDiscovery(true)

		before := srv.ClientIDCounter()
		resp, err := http.Post(srv.URL()+"/register", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST after re-enable: %v", err)
		}
		resp.Body.Close() //nolint:errcheck
		after := srv.ClientIDCounter()

		if after-before != 1 {
			t.Errorf("counter moved by %d, want 1 (three rejected POSTs must not have advanced the counter)", after-before)
		}
	})

	t.Run("EnableOAuthDiscovery(false) disables the endpoints", func(t *testing.T) {
		srv.EnableOAuthDiscovery(false)
		defer srv.EnableOAuthDiscovery(true)

		for _, path := range []string{
			"/.well-known/oauth-protected-resource",
			"/.well-known/oauth-authorization-server",
		} {
			resp, err := http.Get(srv.URL() + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("GET %s: status = %d, want 404", path, resp.StatusCode)
			}
			resp.Body.Close() //nolint:errcheck
		}
	})
}

func TestMCPOAuthDiscoveryIntegration_BuildAuthSignal(t *testing.T) {
	t.Setenv("OAUTH_TOOL_REDIRECT_URI", "http://localhost:8000/api/v1/auth/tool/callback")
	clearDiscoveryCache()

	srv, err := NewMockMCPAuthServer("ignored-token-not-used-for-auth-URL-build")
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	defer srv.Close()

	cfg := &toolauth.AuthConfig{
		Type:          "oauth2",
		CredentialKey: "integration_key",
		ResourceURL:   srv.URL(),
	}
	h := toolauth.NewAuthHandler(cfg, slog.Default())

	toolCtx := &tool.Context{
		AgentName:    "TestAgent",
		UserID:       "user1",
		ClientID:     "client1",
		ParentTaskID: "task-123",
		ToolCallID:   "call-456",
	}

	signal, err := h.BuildAuthSignal(t.Context(), toolCtx)
	if err != nil {
		t.Fatalf("BuildAuthSignal: %v", err)
	}

	if signal.AuthURI == "" {
		t.Fatal("signal.AuthURI is empty")
	}
	if !strings.Contains(signal.AuthURI, "/authorize") {
		t.Errorf("AuthURI should route to the discovered /authorize endpoint: %s", signal.AuthURI)
	}
	if !strings.Contains(signal.AuthURI, srv.URL()) {
		t.Errorf("AuthURI should reference the discovered auth server %s, got %s", srv.URL(), signal.AuthURI)
	}
	if !strings.Contains(signal.AuthURI, "mock-client-") {
		t.Errorf("AuthURI should carry the dynamically-registered client_id, got %s", signal.AuthURI)
	}
	if !strings.Contains(signal.AuthURI, "code_challenge=") {
		t.Errorf("AuthURI should carry a PKCE code_challenge, got %s", signal.AuthURI)
	}
	if signal.CodeVerifier == "" {
		t.Error("signal.CodeVerifier should be populated for PKCE")
	}
	if signal.CredentialKey != "integration_key" {
		t.Errorf("CredentialKey: got %q, want %q", signal.CredentialKey, "integration_key")
	}
}
