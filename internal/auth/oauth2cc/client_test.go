// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oauth2cc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func tokenHandler(accessToken string, expiresIn int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := map[string]any{"access_token": accessToken}
		if expiresIn > 0 {
			resp["expires_in"] = expiresIn
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

func TestTokenManager_Validation(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"empty TokenURL", Config{TokenURL: "", ClientID: "id", ClientSecret: "secret", AllowInsecureTokenURL: true}},
		{"empty ClientID", Config{TokenURL: "https://example.com/token", ClientID: "", ClientSecret: "secret"}},
		{"empty ClientSecret", Config{TokenURL: "https://example.com/token", ClientID: "id", ClientSecret: ""}},
		{"negative refresh buffer", Config{TokenURL: "https://example.com/token", ClientID: "id", ClientSecret: "secret", TokenRefreshBufferSeconds: -1}},
		{"negative max retries", Config{TokenURL: "https://example.com/token", ClientID: "id", ClientSecret: "secret", MaxRetries: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTokenManager(tt.cfg, nil)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestTokenManager_FetchAndCache(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		tokenHandler("test-token-abc", 3600)(w, r)
	}))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:              server.URL,
		ClientID:              "test-client",
		ClientSecret:          "test-secret",
		AllowInsecureTokenURL: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	token, err := tm.GetToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if token != "test-token-abc" {
		t.Errorf("token = %q, want %q", token, "test-token-abc")
	}

	token2, err := tm.GetToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if token2 != "test-token-abc" {
		t.Errorf("token = %q, want %q", token2, "test-token-abc")
	}

	if got := requestCount.Load(); got != 1 {
		t.Errorf("expected 1 HTTP request, got %d", got)
	}
}

func TestTokenManager_RefreshBeforeExpiry(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		token := "token-v1"
		if count > 1 {
			token = "token-v2"
		}
		tokenHandler(token, 1)(w, r)
	}))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:                  server.URL,
		ClientID:                  "test-client",
		ClientSecret:              "test-secret",
		TokenRefreshBufferSeconds: 1,
		AllowInsecureTokenURL:     true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	token1, err := tm.GetToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if token1 != "token-v1" {
		t.Errorf("token = %q, want token-v1", token1)
	}

	tm.mu.Lock()
	tm.expiresAt = time.Now().Add(-1 * time.Second)
	tm.mu.Unlock()

	token2, err := tm.GetToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if token2 != "token-v2" {
		t.Errorf("token = %q, want token-v2", token2)
	}

	if got := requestCount.Load(); got != 2 {
		t.Errorf("expected 2 HTTP requests, got %d", got)
	}
}

func TestTokenManager_ConcurrentAccess(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		time.Sleep(10 * time.Millisecond)
		tokenHandler("concurrent-token", 3600)(w, r)
	}))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:              server.URL,
		ClientID:              "test-client",
		ClientSecret:          "test-secret",
		AllowInsecureTokenURL: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 50
	errCh := make(chan error, goroutines)
	for range goroutines {
		go func() {
			token, err := tm.GetToken(t.Context())
			if err != nil {
				errCh <- err
				return
			}
			if token != "concurrent-token" {
				errCh <- fmt.Errorf("unexpected token: %q", token)
				return
			}
			errCh <- nil
		}()
	}

	for range goroutines {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}

	if got := requestCount.Load(); got != 1 {
		t.Errorf("expected exactly 1 HTTP request, got %d", got)
	}
}

func TestTokenManager_RetryOnFailure(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requestCount.Add(1)
		if count <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server error"))
			return
		}
		tokenHandler("retry-token", 3600)(w, r)
	}))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:              server.URL,
		ClientID:              "test-client",
		ClientSecret:          "test-secret",
		MaxRetries:            3,
		AllowInsecureTokenURL: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	token, err := tm.GetToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if token != "retry-token" {
		t.Errorf("token = %q, want retry-token", token)
	}
	if got := requestCount.Load(); got != 3 {
		t.Errorf("expected 3 HTTP requests, got %d", got)
	}
}

func TestTokenManager_AllRetriesFail(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("always failing"))
	}))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:              server.URL,
		ClientID:              "test-client",
		ClientSecret:          "test-secret",
		MaxRetries:            2,
		AllowInsecureTokenURL: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tm.GetToken(t.Context())
	if err == nil {
		t.Fatal("expected error after all retries")
	}

	if got := requestCount.Load(); got != 3 {
		t.Errorf("expected 3 HTTP requests (1 + 2 retries), got %d", got)
	}
}

func TestTokenManager_NoRetryOn4xx(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:              server.URL,
		ClientID:              "bad-client",
		ClientSecret:          "bad-secret",
		MaxRetries:            3,
		AllowInsecureTokenURL: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tm.GetToken(t.Context())
	if err == nil {
		t.Fatal("expected error for 401")
	}

	if got := requestCount.Load(); got != 1 {
		t.Errorf("expected 1 HTTP request (no retry on 4xx), got %d", got)
	}
}

func TestTokenManager_4xxErrorDoesNotLeakResponseBody(t *testing.T) {
	const secret = "sk-planted-secret-do-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, `{"error":"invalid_client","error_description":%q}`, secret)
	}))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:              server.URL,
		ClientID:              "bad-client",
		ClientSecret:          "bad-secret",
		AllowInsecureTokenURL: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tm.GetToken(t.Context())
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error returned by GetToken must never contain the IdP response body, got: %v", err)
	}
}

func TestTokenManager_HTTPSEnforcement(t *testing.T) {
	_, err := NewTokenManager(Config{
		TokenURL:     "http://localhost:1234/token",
		ClientID:     "id",
		ClientSecret: "secret",
	}, nil)
	if err == nil {
		t.Fatal("expected error for non-HTTPS token URL")
	}

	server := httptest.NewServer(tokenHandler("insecure-token", 3600))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:              server.URL,
		ClientID:              "id",
		ClientSecret:          "secret",
		AllowInsecureTokenURL: true,
	}, nil)
	if err != nil {
		t.Fatalf("expected success with AllowInsecureTokenURL: %v", err)
	}
	token, err := tm.GetToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if token != "insecure-token" {
		t.Errorf("token = %q, want insecure-token", token)
	}
}

func TestTokenManager_InvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token_type":"bearer"}`))
	}))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:              server.URL,
		ClientID:              "id",
		ClientSecret:          "secret",
		MaxRetries:            1,
		AllowInsecureTokenURL: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tm.GetToken(t.Context())
	if err == nil {
		t.Fatal("expected error for missing access_token")
	}
}

func TestTokenManager_DefaultExpiresIn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"no-expiry-token"}`))
	}))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:                  server.URL,
		ClientID:                  "id",
		ClientSecret:              "secret",
		TokenRefreshBufferSeconds: 300,
		AllowInsecureTokenURL:     true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	token, err := tm.GetToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if token != "no-expiry-token" {
		t.Errorf("token = %q, want no-expiry-token", token)
	}

	tm.mu.Lock()
	ttl := time.Until(tm.expiresAt)
	tm.mu.Unlock()
	if ttl < 3200*time.Second || ttl > 3400*time.Second {
		t.Errorf("cache TTL = %v, want ~3300s", ttl)
	}
}

func TestTokenManager_CacheTTLFloor(t *testing.T) {
	server := httptest.NewServer(tokenHandler("floor-token", 10))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:                  server.URL,
		ClientID:                  "id",
		ClientSecret:              "secret",
		TokenRefreshBufferSeconds: 300,
		AllowInsecureTokenURL:     true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = tm.GetToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	tm.mu.Lock()
	ttl := time.Until(tm.expiresAt)
	tm.mu.Unlock()
	if ttl < 5*time.Second || ttl > 15*time.Second {
		t.Errorf("cache TTL = %v, want ~10s (capped at expires_in)", ttl)
	}
}

func TestTokenManager_CustomCACert(t *testing.T) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		t.Fatal(err)
	}
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertDER})

	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverCertDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCertDER})
	serverKeyDER, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyDER})

	serverTLSCert, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(tokenHandler("ca-cert-token", 3600))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{serverTLSCert}}
	server.StartTLS()
	defer server.Close()

	caFile := t.TempDir() + "/ca.pem"
	if writeErr := os.WriteFile(caFile, caCertPEM, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}

	tm, err := NewTokenManager(Config{
		TokenURL:     server.URL + "/token",
		ClientID:     "id",
		ClientSecret: "secret",
		CACertPath:   caFile,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	token, err := tm.GetToken(t.Context())
	if err != nil {
		t.Fatalf("expected success with custom CA cert: %v", err)
	}
	if token != "ca-cert-token" {
		t.Errorf("token = %q, want ca-cert-token", token)
	}
}

func TestTokenManager_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		tokenHandler("slow-token", 3600)(w, r)
	}))
	defer server.Close()

	tm, err := NewTokenManager(Config{
		TokenURL:              server.URL,
		ClientID:              "id",
		ClientSecret:          "secret",
		AllowInsecureTokenURL: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err = tm.GetToken(ctx)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
}
