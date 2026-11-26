// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func selfSignedRefreshServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "refreshed-at",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func seedExpiredToken(t *testing.T) (*mockCredentialService, *tool.Context) {
	t.Helper()
	credSvc := newMockCredentialService()
	storeToken(t, credSvc, &OAuthToken{
		AccessToken:  "expired-at",
		RefreshToken: "old-rt",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	})
	return credSvc, testToolCtx(credSvc)
}

func TestGetOAuthToken_DefaultClient_RejectsSelfSignedTokenEndpoint(t *testing.T) {
	srv := selfSignedRefreshServer(t)
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme:        &OAuthScheme{TokenURL: srv.URL + "/token"},
	}, slog.Default())
	credSvc, toolCtx := seedExpiredToken(t)

	_, err := h.GetOAuthToken(t.Context(), toolCtx)
	if !errors.Is(err, ErrTransientRefresh) {
		t.Fatalf("expected ErrTransientRefresh (x509 refresh failure), got %v", err)
	}
	if got, _ := credSvc.Get(t.Context(), "TestAgent", "TestAgent", "user1", "test_key"); got == "" {
		t.Error("transient (TLS) refresh failure must not clear the stored credential")
	}
}

func TestGetOAuthToken_InsecureSkipVerify_TrustsSelfSignedTokenEndpoint(t *testing.T) {
	srv := selfSignedRefreshServer(t)
	h := NewAuthHandler(&AuthConfig{
		Type:               "oauth2",
		CredentialKey:      "test_key",
		Scheme:             &OAuthScheme{TokenURL: srv.URL + "/token"},
		InsecureSkipVerify: true,
	}, slog.Default())
	_, toolCtx := seedExpiredToken(t)

	tok, err := h.GetOAuthToken(t.Context(), toolCtx)
	if err != nil {
		t.Fatalf("insecure_skip_verify should trust the self-signed endpoint, got %v", err)
	}
	if tok.AccessToken != "refreshed-at" {
		t.Errorf("AccessToken = %q, want refreshed-at", tok.AccessToken)
	}
}

func TestGetOAuthToken_CACertPath_TrustsSelfSignedTokenEndpoint(t *testing.T) {
	srv := selfSignedRefreshServer(t)

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write ca pem: %v", err)
	}

	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme:        &OAuthScheme{TokenURL: srv.URL + "/token"},
		CACertPath:    caPath,
	}, slog.Default())
	_, toolCtx := seedExpiredToken(t)

	tok, err := h.GetOAuthToken(t.Context(), toolCtx)
	if err != nil {
		t.Fatalf("ca_cert_path should trust the pinned self-signed endpoint, got %v", err)
	}
	if tok.AccessToken != "refreshed-at" {
		t.Errorf("AccessToken = %q, want refreshed-at", tok.AccessToken)
	}
}

func TestNewAuthHandler_UnreadableCACertPath_FailsOpenToDefaultTrustStore(t *testing.T) {
	srv := selfSignedRefreshServer(t)
	h := NewAuthHandler(&AuthConfig{
		Type:          "oauth2",
		CredentialKey: "test_key",
		Scheme:        &OAuthScheme{TokenURL: srv.URL + "/token"},
		CACertPath:    filepath.Join(t.TempDir(), "does-not-exist.pem"),
	}, slog.Default())

	if h.baseTransport != nil {
		t.Fatal("a failed CA build must leave baseTransport nil (fall back to default trust store)")
	}

	_, toolCtx := seedExpiredToken(t)
	_, err := h.GetOAuthToken(t.Context(), toolCtx)
	if !errors.Is(err, ErrTransientRefresh) {
		t.Fatalf("fail-open fallback must still verify (reject self-signed) → ErrTransientRefresh, got %v", err)
	}
}
