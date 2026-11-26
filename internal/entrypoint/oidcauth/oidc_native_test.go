// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewNativeOIDCClient_IssuerInitiallyUnreachable(t *testing.T) {
	var responding atomic.Bool
	hits := make(chan struct{}, 10)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case hits <- struct{}{}:
		default:
		}
		if !responding.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 srvIssuer(r),
			"authorization_endpoint": srvIssuer(r) + "/authorize",
			"token_endpoint":         srvIssuer(r) + "/token",
			"userinfo_endpoint":      srvIssuer(r) + "/userinfo",
			"jwks_uri":               srvIssuer(r) + "/jwks",
		})
	}))
	defer srv.Close()

	client, err := NewNativeOIDCClient(OIDCConfig{
		Issuer:   srv.URL,
		ClientID: "client-id",
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient must not fail when issuer is temporarily unreachable: %v", err)
	}

	if _, authErr := client.AuthorizationURL(context.Background(), "state-1"); authErr == nil {
		t.Fatalf("AuthorizationURL: expected error while issuer is unreachable")
	}
	if got := client.discovery.Load(); got != nil {
		t.Fatalf("discovery should not be cached after a failed fetch")
	}

	responding.Store(true)

	authURL, err := client.AuthorizationURL(context.Background(), "state-2")
	if err != nil {
		t.Fatalf("AuthorizationURL after recovery: %v", err)
	}
	if !strings.Contains(authURL, "/authorize") {
		t.Fatalf("authURL missing authorize endpoint: %q", authURL)
	}
	if got := client.discovery.Load(); got == nil {
		t.Fatalf("discovery should be cached after a successful fetch")
	}

	drain(hits)
	if _, err := client.AuthorizationURL(context.Background(), "state-3"); err != nil {
		t.Fatalf("AuthorizationURL (cached): %v", err)
	}
	select {
	case <-hits:
		t.Fatalf("expected cached discovery — issuer was hit again")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestNativeOIDCClient_WarmDiscoveryIsBestEffort(t *testing.T) {
	client, err := NewNativeOIDCClient(OIDCConfig{
		Issuer:   "http://127.0.0.1:1",
		ClientID: "cid",
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.WarmDiscovery(ctx); err == nil {
		t.Fatalf("WarmDiscovery expected to fail against unreachable issuer")
	}
}

func srvIssuer(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func drain[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func generateTestCAAndLeaf(t *testing.T) (caPEM []byte, leafCert tls.Certificate) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen CA key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen leaf key: %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caTmpl, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	return caPEM, tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}
}

func startTLSDiscoveryServer(t *testing.T, leafCert tls.Certificate) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			http.NotFound(w, r)
			return
		}
		base := "https://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 base,
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
			"userinfo_endpoint":      base + "/userinfo",
			"jwks_uri":               base + "/jwks",
		})
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{leafCert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestNewNativeOIDCClient_CustomCA_EndToEnd(t *testing.T) {
	caPEM, leafCert := generateTestCAAndLeaf(t)
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	srv := startTLSDiscoveryServer(t, leafCert)

	client, err := NewNativeOIDCClient(OIDCConfig{
		Issuer:     srv.URL,
		ClientID:   "client-id",
		CACertPath: caPath,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient (with CA): %v", err)
	}
	if warmErr := client.WarmDiscovery(t.Context()); warmErr != nil {
		t.Fatalf("WarmDiscovery (with CA): %v", warmErr)
	}
	authURL, err := client.AuthorizationURL(t.Context(), "state")
	if err != nil {
		t.Fatalf("AuthorizationURL: %v", err)
	}
	if !strings.Contains(authURL, "/authorize") {
		t.Errorf("authURL missing /authorize: %q", authURL)
	}

	plain, err := NewNativeOIDCClient(OIDCConfig{
		Issuer:   srv.URL,
		ClientID: "client-id",
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient (plain): %v", err)
	}
	plainErr := plain.WarmDiscovery(t.Context())
	if plainErr == nil {
		t.Fatal("expected x509 error against untrusted TLS server")
	}
	if !strings.Contains(plainErr.Error(), "x509") && !strings.Contains(plainErr.Error(), "certificate") {
		t.Errorf("expected x509/certificate error, got: %v", plainErr)
	}
}

func TestNewNativeOIDCClient_InsecureSkipVerify_EmitsWarnings(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	_, err := NewNativeOIDCClient(OIDCConfig{
		Issuer:             "https://idp.example.com",
		ClientID:           "client-id",
		CACertPath:         "/nonexistent/ca.pem",
		InsecureSkipVerify: true,
	}, logger)
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "TLS certificate verification disabled") {
		t.Errorf("expected TLS-disabled warning; got: %q", out)
	}
	if !strings.Contains(out, "ca_cert_path is ignored") {
		t.Errorf("expected ca_cert_path-ignored warning; got: %q", out)
	}
}

func TestNewNativeOIDCClient_InsecureSkipVerify_NoCA(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	_, err := NewNativeOIDCClient(OIDCConfig{
		Issuer:             "https://idp.example.com",
		ClientID:           "client-id",
		InsecureSkipVerify: true,
	}, logger)
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "TLS certificate verification disabled") {
		t.Errorf("expected TLS-disabled warning; got: %q", out)
	}
	if strings.Contains(out, "ca_cert_path is ignored") {
		t.Errorf("did NOT expect ca_cert_path-ignored warning when no CA path; got: %q", out)
	}
}

func TestNativeOIDCClient_BuildAuthURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 srvIssuer(r),
			"authorization_endpoint": srvIssuer(r) + "/authorize",
			"token_endpoint":         srvIssuer(r) + "/token",
			"userinfo_endpoint":      srvIssuer(r) + "/userinfo",
			"jwks_uri":               srvIssuer(r) + "/jwks",
		})
	}))
	defer srv.Close()

	client, err := NewNativeOIDCClient(OIDCConfig{
		Issuer:   srv.URL,
		ClientID: "client-id",
		Scopes:   []string{"openid", "email", "profile"},
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}
	if err = client.WarmDiscovery(t.Context()); err != nil {
		t.Fatalf("WarmDiscovery: %v", err)
	}

	got, err := client.BuildAuthURL(t.Context(), "state-abc", "https://gw.example/cb", "challenge-xyz", "S256")
	if err != nil {
		t.Fatalf("BuildAuthURL after successful discovery: %v", err)
	}
	if got == "" {
		t.Fatal("BuildAuthURL returned empty string after successful discovery")
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if !strings.HasSuffix(parsed.Path, "/authorize") {
		t.Errorf("expected /authorize path, got %q", parsed.Path)
	}
	q := parsed.Query()
	cases := map[string]string{
		"response_type":         "code",
		"client_id":             "client-id",
		"redirect_uri":          "https://gw.example/cb",
		"state":                 "state-abc",
		"scope":                 "openid email profile",
		"code_challenge":        "challenge-xyz",
		"code_challenge_method": "S256",
	}
	for k, want := range cases {
		if got := q.Get(k); got != want {
			t.Errorf("query %s: got %q, want %q", k, got, want)
		}
	}
}

func TestNativeOIDCClient_BuildAuthURL_NoPKCE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 srvIssuer(r),
			"authorization_endpoint": srvIssuer(r) + "/authorize",
			"token_endpoint":         srvIssuer(r) + "/token",
			"userinfo_endpoint":      srvIssuer(r) + "/userinfo",
		})
	}))
	defer srv.Close()

	client, err := NewNativeOIDCClient(OIDCConfig{Issuer: srv.URL, ClientID: "cid"}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}
	if err = client.WarmDiscovery(t.Context()); err != nil {
		t.Fatalf("WarmDiscovery: %v", err)
	}

	got, err := client.BuildAuthURL(t.Context(), "st", "https://gw.example/cb", "", "")
	if err != nil {
		t.Fatalf("BuildAuthURL: %v", err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Query().Get("code_challenge") != "" {
		t.Errorf("expected no code_challenge param, got %q", parsed.Query().Get("code_challenge"))
	}
	if parsed.Query().Get("code_challenge_method") != "" {
		t.Errorf("expected no code_challenge_method param, got %q",
			parsed.Query().Get("code_challenge_method"))
	}
}

func TestNativeOIDCClient_BuildAuthURL_DiscoveryFails(t *testing.T) {
	client, err := NewNativeOIDCClient(OIDCConfig{
		Issuer:   "http://127.0.0.1:1",
		ClientID: "cid",
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}
	got, buildErr := client.BuildAuthURL(t.Context(), "st", "https://gw.example/cb", "", "")
	if buildErr == nil {
		t.Errorf("BuildAuthURL: expected error on discovery failure, got url=%q", got)
	}
	if got != "" {
		t.Errorf("BuildAuthURL: expected empty URL on discovery failure, got %q", got)
	}
}

func TestNewNativeOIDCClient_BadCABundle(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(badPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("write bad PEM: %v", err)
	}

	_, err := NewNativeOIDCClient(OIDCConfig{
		Issuer:     "https://idp.example.com",
		ClientID:   "client-id",
		CACertPath: badPath,
	}, slog.Default())
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
	if !strings.Contains(err.Error(), "no valid PEM blocks") {
		t.Errorf("expected 'no valid PEM blocks' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "build HTTP client") {
		t.Errorf("expected 'build HTTP client' wrap in error, got: %v", err)
	}
}

func TestNativeOIDCClient_ErrorStatusOmitsBody(t *testing.T) {
	const body = `{"error":"invalid_grant","error_description":"refresh token for alice@example.com expired"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/.well-known/openid-configuration") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 srvIssuer(r),
				"authorization_endpoint": srvIssuer(r) + "/authorize",
				"token_endpoint":         srvIssuer(r) + "/token",
				"userinfo_endpoint":      srvIssuer(r) + "/userinfo",
				"jwks_uri":               srvIssuer(r) + "/jwks",
			})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client, err := NewNativeOIDCClient(OIDCConfig{Issuer: srv.URL, ClientID: "client-id"}, slog.Default())
	if err != nil {
		t.Fatalf("NewNativeOIDCClient: %v", err)
	}
	_, refreshErr := client.RefreshToken(t.Context(), "rt")
	_, userinfoErr := client.GetUserInfo(t.Context(), "at")
	for name, err := range map[string]error{"refresh": refreshErr, "userinfo": userinfoErr} {
		if err == nil {
			t.Fatalf("%s: expected an error for HTTP 400", name)
		}
		if !strings.Contains(err.Error(), "status 400") {
			t.Errorf("%s: error should keep the status: %v", name, err)
		}
		if strings.Contains(err.Error(), "alice@example.com") {
			t.Errorf("%s: error keeps the response body: %v", name, err)
		}
	}
}
