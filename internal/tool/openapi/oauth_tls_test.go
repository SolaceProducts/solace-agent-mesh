// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func selfSignedClientCredentialsServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "cc-tls-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func clientCredentialsHandler(t *testing.T, srv *httptest.Server, caCertPath string, insecure bool) *openAPIAuthHandler {
	t.Helper()
	h, err := NewAuthHandlerForOpenAPI(&AuthConfig{
		Type:               AuthTypeOAuth2ClientCredentials,
		TokenURL:           srv.URL + "/token",
		ClientID:           "svc-cid",
		ClientSecret:       "svc-csecret",
		Scopes:             []string{"api.read"},
		CACertPath:         caCertPath,
		InsecureSkipVerify: insecure,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewAuthHandlerForOpenAPI: %v", err)
	}
	return h
}

func TestClientCredentials_DefaultClient_RejectsSelfSignedTokenEndpoint(t *testing.T) {
	srv := selfSignedClientCredentialsServer(t)
	h := clientCredentialsHandler(t, srv, "", false)

	_, err := h.GetAuthHeaders(t.Context(), makeToolCtx(newMemCredSvc()))
	if err == nil {
		t.Fatal("expected x509 verification failure against self-signed token endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "x509") {
		t.Errorf("expected a TLS/x509 error, got %v", err)
	}
}

func TestClientCredentials_InsecureSkipVerify_TrustsSelfSignedTokenEndpoint(t *testing.T) {
	srv := selfSignedClientCredentialsServer(t)
	h := clientCredentialsHandler(t, srv, "", true)

	hdr, err := h.GetAuthHeaders(t.Context(), makeToolCtx(newMemCredSvc()))
	if err != nil {
		t.Fatalf("insecure_skip_verify should trust the self-signed endpoint, got %v", err)
	}
	if got := hdr.Get("Authorization"); got != "Bearer cc-tls-token" {
		t.Errorf("Authorization = %q, want Bearer cc-tls-token", got)
	}
}

func TestClientCredentials_CACertPath_TrustsSelfSignedTokenEndpoint(t *testing.T) {
	srv := selfSignedClientCredentialsServer(t)

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write ca pem: %v", err)
	}
	h := clientCredentialsHandler(t, srv, caPath, false)

	hdr, err := h.GetAuthHeaders(t.Context(), makeToolCtx(newMemCredSvc()))
	if err != nil {
		t.Fatalf("ca_cert_path should trust the pinned self-signed endpoint, got %v", err)
	}
	if got := hdr.Get("Authorization"); got != "Bearer cc-tls-token" {
		t.Errorf("Authorization = %q, want Bearer cc-tls-token", got)
	}
}

func selfSignedServiceAccountServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" || r.Form.Get("assertion") == "" {
			http.Error(w, "bad jwt-bearer request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"sa-tls-token","token_type":"Bearer","expires_in":3600}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func serviceAccountHandler(t *testing.T, srv *httptest.Server, caCertPath string, insecure bool) *openAPIAuthHandler {
	t.Helper()
	saJSON, _ := mintServiceAccountJSONForTest(t)
	var sa map[string]any
	if err := json.Unmarshal([]byte(saJSON), &sa); err != nil {
		t.Fatalf("unmarshal sa json: %v", err)
	}
	sa["token_uri"] = srv.URL + "/token"
	rewritten, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("marshal sa json: %v", err)
	}
	h, err := NewAuthHandlerForOpenAPI(&AuthConfig{
		Type:               AuthTypeServiceAccount,
		ServiceAccountJSON: string(rewritten),
		Scopes:             []string{"https://www.googleapis.com/auth/cloud-platform"},
		CACertPath:         caCertPath,
		InsecureSkipVerify: insecure,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewAuthHandlerForOpenAPI: %v", err)
	}
	return h
}

func TestServiceAccount_DefaultClient_RejectsSelfSignedTokenEndpoint(t *testing.T) {
	srv := selfSignedServiceAccountServer(t)
	h := serviceAccountHandler(t, srv, "", false)

	_, err := h.GetAuthHeaders(t.Context(), makeToolCtx(newMemCredSvc()))
	if err == nil {
		t.Fatal("expected x509 verification failure against self-signed token endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "certificate") && !strings.Contains(err.Error(), "x509") {
		t.Errorf("expected a TLS/x509 error, got %v", err)
	}
}

func TestServiceAccount_InsecureSkipVerify_TrustsSelfSignedTokenEndpoint(t *testing.T) {
	srv := selfSignedServiceAccountServer(t)
	h := serviceAccountHandler(t, srv, "", true)

	hdr, err := h.GetAuthHeaders(t.Context(), makeToolCtx(newMemCredSvc()))
	if err != nil {
		t.Fatalf("insecure_skip_verify should trust the self-signed endpoint, got %v", err)
	}
	if got := hdr.Get("Authorization"); got != "Bearer sa-tls-token" {
		t.Errorf("Authorization = %q, want Bearer sa-tls-token", got)
	}
}

func TestServiceAccount_CACertPath_TrustsSelfSignedTokenEndpoint(t *testing.T) {
	srv := selfSignedServiceAccountServer(t)

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write ca pem: %v", err)
	}
	h := serviceAccountHandler(t, srv, caPath, false)

	hdr, err := h.GetAuthHeaders(t.Context(), makeToolCtx(newMemCredSvc()))
	if err != nil {
		t.Fatalf("ca_cert_path should trust the pinned self-signed endpoint, got %v", err)
	}
	if got := hdr.Get("Authorization"); got != "Bearer sa-tls-token" {
		t.Errorf("Authorization = %q, want Bearer sa-tls-token", got)
	}
}
