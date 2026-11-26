// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/youmark/pkcs8"
)

func TestGatewayProxy_HealthEndpoint(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	handler := proxy.Handler()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); body != "ok" {
		t.Fatalf("expected 'ok', got %q", body)
	}
}

func TestGatewayProxy_RequestGateCoversEveryRoute(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	served := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	proxy.RegisterPrefixHandler("/api/v1/platform", served)
	proxy.RegisterPrefixHandler("/", served)
	proxy.RegisterHandler("mcp-1", served)
	proxy.SetRequestGate(func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	})
	handler := proxy.Handler()

	for _, path := range []string{"/api/v1/platform/agents", "/api/v1/sessions", "/gw/mcp-1/mcp"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusForbidden {
			t.Errorf("[%s] expected the gate's 403, got %d", path, w.Code)
		}
		if w.Header().Get("X-Request-ID") == "" {
			t.Errorf("[%s] the gate's answer must still carry a request id", path)
		}
	}
}

func TestGatewayProxy_PrefixRouting(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	platform := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "platform:%s", r.URL.Path)
	})
	proxy.RegisterPrefixHandler("/api/v1/platform", platform)
	proxy.RegisterPrefixHandler("/api/v1/notify", platform)

	handler := proxy.Handler()

	tests := []struct {
		path     string
		expected string
	}{
		{"/api/v1/platform/health", "platform:/api/v1/platform/health"},
		{"/api/v1/platform/agents", "platform:/api/v1/platform/agents"},
		{"/api/v1/notify/inbox", "platform:/api/v1/notify/inbox"},
	}

	for _, tc := range tests {
		req := httptest.NewRequest("GET", tc.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("[%s] expected 200, got %d", tc.path, w.Code)
		}
		if body := w.Body.String(); body != tc.expected {
			t.Errorf("[%s] expected %q, got %q", tc.path, tc.expected, body)
		}
	}
}

func TestGatewayProxy_PrefixRouting_LongestMatchWins(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/api/v1", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "short")
	}))
	proxy.RegisterPrefixHandler("/api/v1/platform", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "long")
	}))

	handler := proxy.Handler()

	req := httptest.NewRequest("GET", "/api/v1/platform/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if body := w.Body.String(); body != "long" {
		t.Fatalf("longest-prefix should win; got %q", body)
	}

	req = httptest.NewRequest("GET", "/api/v1/other", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if body := w.Body.String(); body != "short" {
		t.Fatalf("shorter prefix should match when longer doesn't; got %q", body)
	}
}

func TestGatewayProxy_PrefixRouting_BoundaryMatch(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/api/v1/platform", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "platform")
	}))
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "primary")
	}))

	handler := proxy.Handler()

	req := httptest.NewRequest("GET", "/api/v1/platforms", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if body := w.Body.String(); body != "primary" {
		t.Fatalf("boundary mismatch should fall through to primary; got %q", body)
	}

	req = httptest.NewRequest("GET", "/api/v1/platform", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if body := w.Body.String(); body != "platform" {
		t.Fatalf("exact prefix match expected; got %q", body)
	}
}

func TestGatewayProxy_UnregisterPrefixHandler(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/api/v1/platform", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "platform")
	}))
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "primary")
	}))

	handler := proxy.Handler()

	req := httptest.NewRequest("GET", "/api/v1/platform/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if body := w.Body.String(); body != "platform" {
		t.Fatalf("before unregister expected 'platform', got %q", body)
	}

	proxy.UnregisterPrefixHandler("/api/v1/platform")

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req)
	if body := w2.Body.String(); body != "primary" {
		t.Fatalf("after unregister expected fall-through to primary, got %q", body)
	}
}

func TestGatewayProxy_PrimaryHandler(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "primary:%s", r.URL.Path)
	}))

	handler := proxy.Handler()

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); body != "primary:/" {
		t.Fatalf("expected 'primary:/', got %q", body)
	}
}

func TestGatewayProxy_NamedGateway(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterHandler("teams", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "teams:%s", r.URL.Path)
	}))

	handler := proxy.Handler()

	req := httptest.NewRequest("GET", "/gw/teams/webhook", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); body != "teams:/webhook" {
		t.Fatalf("expected 'teams:/webhook', got %q", body)
	}
}

func TestGatewayProxy_NamedGateway_NotFound(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	handler := proxy.Handler()

	req := httptest.NewRequest("GET", "/gw/unknown/path", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGatewayProxy_PrimaryNotReady(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	handler := proxy.Handler()

	req := httptest.NewRequest("GET", "/some/path", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestGatewayProxy_UnregisterHandler(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterHandler("slack", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "slack")
	}))

	handler := proxy.Handler()

	req := httptest.NewRequest("GET", "/gw/slack/events", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 before unregister, got %d", w.Code)
	}

	proxy.UnregisterHandler("slack")

	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req)
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after unregister, got %d", w2.Code)
	}
}

func TestGatewayProxy_StartStop(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "ok")
	}))

	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Stop(t.Context()) }()

	addr := proxy.Addr()
	if addr == "" {
		t.Fatal("expected non-empty addr after Start")
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if string(body) != "ok" {
		t.Fatalf("expected 'ok', got %q", body)
	}
}

func TestGatewayProxy_StaticFS_ServesSPA(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.SetStaticFS(fstest.MapFS{
		"index.html": {Data: []byte("<!DOCTYPE html>spa")},
	})
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/known":
			_, _ = fmt.Fprint(w, "primary-known")
		case strings.HasPrefix(r.URL.Path, "/api/"):
			_, _ = fmt.Fprint(w, "primary-api")
		default:
			http.NotFound(w, r)
		}
	}))
	handler := proxy.Handler()

	req := httptest.NewRequest(http.MethodGet, "/known", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if body := w.Body.String(); body != "primary-known" {
		t.Fatalf("rootHandler-owned route must not be masked by SPA; got %q", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/some/deep/route", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if body := w.Body.String(); body != "<!DOCTYPE html>spa" {
		t.Fatalf("non-API 404 should fall back to SPA; got %q", body)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("SPA fallback should return 200, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/whatever", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if body := w.Body.String(); body != "primary-api" {
		t.Fatalf("/api/* GET should bypass SPA; got %q", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/nope-no-route", nil)
	w = httptest.NewRecorder()
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	proxy.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("/api/* 404 must NOT fall back to SPA; got status %d body %q", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/submit", nil)
	w = httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST should NOT fall back to SPA; got status %d body %q", w.Code, w.Body.String())
	}
}

func TestGatewayProxy_StaticFS_HeadDistinguishesPresentFromMissing(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.SetStaticFS(fstest.MapFS{
		"index.html":    {Data: []byte("<!DOCTYPE html>spa")},
		"assets/app.js": {Data: []byte("// js")},
	})
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	handler := proxy.Handler()

	req := httptest.NewRequest(http.MethodHead, "/assets/app.js", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("HEAD of a present asset: got %d, want 200 — the probe cannot tell it apart from a deleted one", w.Code)
	}

	req = httptest.NewRequest(http.MethodHead, "/assets/main-OLD1234.js", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("HEAD of a missing asset: got %d, want 404", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("HEAD of a missing asset: Cache-Control = %q, want %q", got, "no-store")
	}
}

func TestGatewayProxy_StaticFS_NoRootHandler(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.SetStaticFS(fstest.MapFS{
		"index.html": {Data: []byte("<!DOCTYPE html>spa")},
	})
	handler := proxy.Handler()

	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if body := w.Body.String(); body != "<!DOCTYPE html>spa" {
		t.Fatalf("non-API GET with no rootHandler should serve SPA; got %q", body)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/foo", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("/api/* with no rootHandler should 503, got %d", w.Code)
	}
}

func TestGatewayProxy_StaticFS_Nil_FallsThrough(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "primary")
	}))
	handler := proxy.Handler()

	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if body := w.Body.String(); body != "primary" {
		t.Fatalf("no SPA configured should fall through to primary; got %q", body)
	}
}

func generateSelfSignedCert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return writeSelfSignedCert(t, dir, key)
}

func generateSelfSignedRSACert(t *testing.T, dir string) (certFile, keyFile string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return writeSelfSignedCert(t, dir, key)
}

func writeSelfSignedCert(t *testing.T, dir string, key crypto.Signer) (certFile, keyFile string) {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, "tls.crt")
	keyFile = filepath.Join(dir, "tls.key")

	certOut, _ := os.Create(certFile)
	_ = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	_ = certOut.Close()

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyOut, _ := os.Create(keyFile)
	_ = pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	_ = keyOut.Close()
	return
}

func TestGatewayProxy_TLS12_RejectsCBCCipherSuites(t *testing.T) {
	type keyPair struct{ certFile, keyFile string }
	var ecdsaPair, rsaPair keyPair
	ecdsaPair.certFile, ecdsaPair.keyFile = generateSelfSignedCert(t, t.TempDir())
	rsaPair.certFile, rsaPair.keyFile = generateSelfSignedRSACert(t, t.TempDir())

	tests := []struct {
		name         string
		serverCert   keyPair
		suite        uint16
		wantAccepted bool
	}{
		{"ECDSA AES-128-CBC rejected", ecdsaPair, tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA, false},
		{"ECDSA AES-256-CBC rejected", ecdsaPair, tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA, false},
		{"RSA AES-128-CBC rejected", rsaPair, tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA, false},
		{"RSA AES-256-CBC rejected", rsaPair, tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA, false},
		{"ECDSA AES-128-GCM accepted", ecdsaPair, tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, true},
		{"RSA AES-128-GCM accepted", rsaPair, tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, true},
		{"RSA CHACHA20 accepted", rsaPair, tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
			proxy.SetTLS(&ProxyTLSConfig{CertFile: tt.serverCert.certFile, KeyFile: tt.serverCert.keyFile}, "")
			if err := proxy.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = proxy.Stop(t.Context()) }()

			conn, err := tls.Dial("tcp", proxy.Addr(), &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec // self-signed test cert
				MaxVersion:         tls.VersionTLS12,
				CipherSuites:       []uint16{tt.suite},
			})
			if err == nil {
				_ = conn.Close()
			}
			if accepted := err == nil; accepted != tt.wantAccepted {
				t.Fatalf("handshake accepted=%v, want %v (err=%v)", accepted, tt.wantAccepted, err)
			}
		})
	}
}

func TestGatewayProxy_TLS_SinglePort(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateSelfSignedCert(t, dir)

	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "tls-ok")
	}))
	proxy.SetTLS(&ProxyTLSConfig{CertFile: certFile, KeyFile: keyFile}, "")

	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Stop(t.Context()) }()

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp, err := client.Get(fmt.Sprintf("https://%s/health", proxy.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("expected 'ok', got %q", body)
	}
}

func TestGatewayProxy_TLS_DualPort(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateSelfSignedCert(t, dir)

	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "dual-ok")
	}))
	proxy.SetTLS(&ProxyTLSConfig{CertFile: certFile, KeyFile: keyFile}, "127.0.0.1:0")

	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Stop(t.Context()) }()

	httpAddr := proxy.Addr()

	resp, err := http.Get(fmt.Sprintf("http://%s/health", httpAddr))
	if err != nil {
		t.Fatal("HTTP health check failed:", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTTP health: expected 200, got %d", resp.StatusCode)
	}

	tlsAddr := proxy.TLSAddr()
	if tlsAddr == "" {
		t.Fatal("TLS listener has no bound address after Start")
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp2, err := client.Get(fmt.Sprintf("https://%s/health", tlsAddr))
	if err != nil {
		t.Fatal("HTTPS health check failed:", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("HTTPS health: expected 200, got %d", resp2.StatusCode)
	}
}

func TestGatewayProxy_HSTS_OnlyOnTLSResponses(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateSelfSignedCert(t, dir)

	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.SetTLS(&ProxyTLSConfig{CertFile: certFile, KeyFile: keyFile}, "127.0.0.1:0")
	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Stop(t.Context()) }()

	tlsClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	tests := []struct {
		name   string
		client *http.Client
		url    string
		want   string
	}{
		{"https", tlsClient, fmt.Sprintf("https://%s/health", proxy.TLSAddr()), "max-age=63072000; includeSubDomains; preload"},
		{"plain http", http.DefaultClient, fmt.Sprintf("http://%s/health", proxy.Addr()), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := tt.client.Get(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if got := resp.Header.Get("Strict-Transport-Security"); got != tt.want {
				t.Errorf("Strict-Transport-Security = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGatewayProxy_TLS_BadCertPath(t *testing.T) {
	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.SetTLS(&ProxyTLSConfig{CertFile: "/does/not/exist/cert.pem", KeyFile: "/does/not/exist/key.pem"}, "")

	err := proxy.Start(t.Context())
	if err == nil {
		_ = proxy.Stop(t.Context())
		t.Fatal("expected error for bad cert path, got nil")
	}
}

func TestGatewayProxy_TLS_BadKeyPath(t *testing.T) {
	dir := t.TempDir()
	certFile, _ := generateSelfSignedCert(t, dir)

	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.SetTLS(&ProxyTLSConfig{CertFile: certFile, KeyFile: "/does/not/exist/key.pem"}, "")

	err := proxy.Start(t.Context())
	if err == nil {
		_ = proxy.Stop(t.Context())
		t.Fatal("expected error for bad key path, got nil")
	}
}

func TestGatewayProxy_TLS_EncryptedKey_WrongPassword(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateEncryptedSelfSignedCert(t, dir, "correct-pw")

	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.SetTLS(&ProxyTLSConfig{CertFile: certFile, KeyFile: keyFile, Password: "wrong-pw"}, "")

	err := proxy.Start(t.Context())
	if err == nil {
		_ = proxy.Stop(t.Context())
		t.Fatal("expected error for wrong key password, got nil")
	}
}

func TestGatewayProxy_TLS_EncryptedKey(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateEncryptedSelfSignedCert(t, dir, "proxy-pass")

	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "enc-ok")
	}))
	proxy.SetTLS(&ProxyTLSConfig{CertFile: certFile, KeyFile: keyFile, Password: "proxy-pass"}, "")

	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Stop(t.Context()) }()

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp, err := client.Get(fmt.Sprintf("https://%s/health", proxy.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("expected 'ok', got %q", body)
	}
}

func generateEncryptedSelfSignedCert(t *testing.T, dir, password string) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, "tls.crt")
	keyFile = filepath.Join(dir, "tls.key")

	certOut, _ := os.Create(certFile)
	_ = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	_ = certOut.Close()

	encDER, err := pkcs8.MarshalPrivateKey(key, []byte(password), nil)
	if err != nil {
		t.Fatal(err)
	}
	keyOut, _ := os.Create(keyFile)
	_ = pem.Encode(keyOut, &pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: encDER})
	_ = keyOut.Close()
	return
}

func TestGatewayProxy_DefaultTimeoutsApplied(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Stop(t.Context()) }()

	if got := proxy.server.ReadHeaderTimeout; got != DefaultServerTimeouts.ReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout: got %v, want %v", got, DefaultServerTimeouts.ReadHeaderTimeout)
	}
	if got := proxy.server.ReadTimeout; got != DefaultServerTimeouts.ReadTimeout {
		t.Errorf("ReadTimeout: got %v, want %v", got, DefaultServerTimeouts.ReadTimeout)
	}
	if got := proxy.server.IdleTimeout; got != DefaultServerTimeouts.IdleTimeout {
		t.Errorf("IdleTimeout: got %v, want %v", got, DefaultServerTimeouts.IdleTimeout)
	}
	if got := proxy.server.WriteTimeout; got != 0 {
		t.Errorf("WriteTimeout: got %v, want 0 (SSE-friendly)", got)
	}
}

func TestGatewayProxy_SetTimeoutsOverride(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.SetTimeouts(ServerTimeouts{
		ReadHeaderTimeout: 1 * time.Second,
		ReadTimeout:       2 * time.Second,
		IdleTimeout:       3 * time.Second,
	})
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Stop(t.Context()) }()

	if got := proxy.server.ReadHeaderTimeout; got != 1*time.Second {
		t.Errorf("ReadHeaderTimeout override not applied: %v", got)
	}
	if got := proxy.server.ReadTimeout; got != 2*time.Second {
		t.Errorf("ReadTimeout override not applied: %v", got)
	}
	if got := proxy.server.IdleTimeout; got != 3*time.Second {
		t.Errorf("IdleTimeout override not applied: %v", got)
	}
}

func TestGatewayProxy_TLS_DualPort_TimeoutsApplied(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateSelfSignedCert(t, dir)

	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	proxy.SetTLS(&ProxyTLSConfig{CertFile: certFile, KeyFile: keyFile}, "127.0.0.1:0")
	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxy.Stop(t.Context()) }()

	if got := proxy.tlsServer.ReadTimeout; got != DefaultServerTimeouts.ReadTimeout {
		t.Errorf("TLS ReadTimeout: got %v, want %v", got, DefaultServerTimeouts.ReadTimeout)
	}
	if got := proxy.tlsServer.IdleTimeout; got != DefaultServerTimeouts.IdleTimeout {
		t.Errorf("TLS IdleTimeout: got %v, want %v", got, DefaultServerTimeouts.IdleTimeout)
	}
}

func TestGatewayProxy_OnServeFailureFiresOnHTTPServeError(t *testing.T) {
	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())

	failed := make(chan error, 1)
	proxy.OnServeFailure = func(err error) { failed <- err }

	if err := proxy.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = proxy.Stop(t.Context()) })

	if err := proxy.listener.Close(); err != nil {
		t.Fatalf("listener close: %v", err)
	}

	select {
	case err := <-failed:
		if errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("expected non-ErrServerClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnServeFailure was not invoked within 2s")
	}
}

func TestGatewayProxy_OnServeFailureFiresOnSinglePortTLSError(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateSelfSignedCert(t, dir)

	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.SetTLS(&ProxyTLSConfig{CertFile: certFile, KeyFile: keyFile}, "")

	failed := make(chan error, 1)
	proxy.OnServeFailure = func(err error) { failed <- err }

	if err := proxy.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = proxy.Stop(t.Context()) })

	if err := proxy.listener.Close(); err != nil {
		t.Fatalf("listener close: %v", err)
	}

	select {
	case err := <-failed:
		if errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("expected non-ErrServerClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnServeFailure was not invoked within 2s")
	}
}

func TestGatewayProxy_OnServeFailureFiresOnDualPortError(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := generateSelfSignedCert(t, dir)

	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.SetTLS(&ProxyTLSConfig{CertFile: certFile, KeyFile: keyFile}, "127.0.0.1:0")

	failed := make(chan error, 1)
	proxy.OnServeFailure = func(err error) {
		select {
		case failed <- err:
		default:
		}
	}

	if err := proxy.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = proxy.Stop(t.Context()) })

	if err := proxy.listener.Close(); err != nil {
		t.Fatalf("listener close: %v", err)
	}

	select {
	case err := <-failed:
		if errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("expected non-ErrServerClosed, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnServeFailure was not invoked within 2s")
	}
}

type recordingHandler struct {
	calls    int
	lastPath string
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls++
	h.lastPath = r.URL.Path
	w.WriteHeader(http.StatusOK)
}

func TestProxy_RoutesMCPProtectedResourceMetadata(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	rec := &recordingHandler{}
	proxy.RegisterMCPMetadataRoutes("go-mcp-gw", rec)

	req := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/gw/go-mcp-gw/mcp", nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if rec.calls != 1 {
		t.Fatalf("expected handler invoked once, got %d", rec.calls)
	}
	if rec.lastPath != "/.well-known/oauth-protected-resource" {
		t.Fatalf("expected rewritten path %q, got %q", "/.well-known/oauth-protected-resource", rec.lastPath)
	}
}

func TestProxy_RoutesMCPProtectedResourceMetadata_RootMountNoSuffix(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	rec := &recordingHandler{}
	proxy.RegisterMCPMetadataRoutes("mcp", rec)

	req := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/gw/mcp", nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if rec.calls != 1 {
		t.Fatalf("expected handler invoked once, got %d", rec.calls)
	}
	if rec.lastPath != "/.well-known/oauth-protected-resource" {
		t.Fatalf("expected rewritten path %q, got %q", "/.well-known/oauth-protected-resource", rec.lastPath)
	}
}

func TestProxy_RoutesMCPAuthorizationServerMetadata(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	rec := &recordingHandler{}
	proxy.RegisterMCPMetadataRoutes("go-mcp-gw", rec)

	req := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server/gw/go-mcp-gw", nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if rec.calls != 1 {
		t.Fatalf("expected handler invoked once, got %d", rec.calls)
	}
	if rec.lastPath != "/.well-known/oauth-authorization-server" {
		t.Fatalf("expected rewritten path %q, got %q", "/.well-known/oauth-authorization-server", rec.lastPath)
	}
}

func TestProxy_OIDCDiscoveryAlias(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	rec := &recordingHandler{}
	proxy.RegisterMCPMetadataRoutes("go-mcp-gw", rec)

	req := httptest.NewRequest("GET", "/.well-known/openid-configuration/gw/go-mcp-gw", nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if rec.calls != 1 {
		t.Fatalf("expected handler invoked once, got %d", rec.calls)
	}
	if rec.lastPath != "/.well-known/oauth-authorization-server" {
		t.Fatalf("expected rewritten path %q, got %q", "/.well-known/oauth-authorization-server", rec.lastPath)
	}
}

func TestProxy_BareMetadataPath_SingleMCPGateway(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	rec := &recordingHandler{}
	proxy.RegisterMCPMetadataRoutes("only-mcp-gw", rec)

	req := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if rec.calls != 1 {
		t.Fatalf("expected handler invoked once, got %d", rec.calls)
	}
	if rec.lastPath != "/.well-known/oauth-authorization-server" {
		t.Fatalf("expected rewritten path %q, got %q", "/.well-known/oauth-authorization-server", rec.lastPath)
	}
}

func TestProxy_BareMetadataPath_MultipleGatewaysFallsThrough(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	a := &recordingHandler{}
	b := &recordingHandler{}
	proxy.RegisterMCPMetadataRoutes("mcp-a", a)
	proxy.RegisterMCPMetadataRoutes("mcp-b", b)

	req := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 fall-through, got %d", w.Code)
	}
	if a.calls != 0 || b.calls != 0 {
		t.Fatalf("expected no MCP handler invocation; got a=%d b=%d", a.calls, b.calls)
	}
}

func TestProxy_UnknownGatewayInMetadataPathFallsThrough(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	rec := &recordingHandler{}
	proxy.RegisterMCPMetadataRoutes("go-mcp-gw", rec)

	req := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/gw/unknown/mcp", nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 fall-through, got %d", w.Code)
	}
	if rec.calls != 0 {
		t.Fatalf("expected registered handler NOT invoked for unknown gateway; got %d calls", rec.calls)
	}
}

func TestProxy_UnknownGatewayInMetadataPath_RootMountNoSuffixFallsThrough(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	rec := &recordingHandler{}
	proxy.RegisterMCPMetadataRoutes("go-mcp-gw", rec)

	req := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/gw/unknown", nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 fall-through, got %d", w.Code)
	}
	if rec.calls != 0 {
		t.Fatalf("expected registered handler NOT invoked for unknown gateway; got %d calls", rec.calls)
	}
}

func TestProxy_RegisterMCPMetadataRoutes_GuardsAgainstEmptyOrNil(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	rec := &recordingHandler{}

	proxy.RegisterMCPMetadataRoutes("", rec)
	proxy.RegisterMCPMetadataRoutes("go-mcp-gw", nil)

	req := httptest.NewRequest("GET", "/.well-known/oauth-authorization-server", nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (no registrations took effect), got %d", w.Code)
	}
	if rec.calls != 0 {
		t.Fatalf("recordingHandler invoked despite empty gatewayID; got %d calls", rec.calls)
	}
}

func TestProxy_PRMetadataPathRequiresGatewaySuffix(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	rec := &recordingHandler{}
	proxy.RegisterMCPMetadataRoutes("go-mcp-gw", rec)

	req := httptest.NewRequest("GET", "/.well-known/oauth-protected-resource/notgw/whatever", nil)
	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, req)

	if rec.calls != 0 {
		t.Fatalf("expected handler NOT invoked for non-gw suffix; got %d calls", rec.calls)
	}
}

func TestExtractGatewayIDFromMetadataSuffix_NoGwPrefix(t *testing.T) {
	gw, rest, hasSuffix := extractGatewayIDFromMetadataSuffix("notgw/foo")
	if gw != "" || rest != "" || hasSuffix {
		t.Errorf("got (%q,%q,%v); want (\"\",\"\",false)", gw, rest, hasSuffix)
	}
}

func TestProxy_HasMCPMetadataRoute(t *testing.T) {
	p := NewGatewayProxy(":0", slog.Default())
	if p.HasMCPMetadataRoute("gw-x") {
		t.Error("HasMCPMetadataRoute must be false before Register")
	}
	p.RegisterMCPMetadataRoutes("gw-x", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if !p.HasMCPMetadataRoute("gw-x") {
		t.Error("HasMCPMetadataRoute must be true after Register")
	}
	if p.HasMCPMetadataRoute("gw-other") {
		t.Error("HasMCPMetadataRoute must be false for non-registered ID")
	}
	if p.HasMCPMetadataRoute("") {
		t.Error("HasMCPMetadataRoute must be false for empty ID")
	}
}

func TestProxy_UnregisterMCPMetadataRoutes(t *testing.T) {
	p := NewGatewayProxy(":0", slog.Default())

	var handled atomic.Int32
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { handled.Add(1) })
	p.RegisterMCPMetadataRoutes("gw-u", h)
	if !p.HasMCPMetadataRoute("gw-u") {
		t.Fatal("HasMCPMetadataRoute must be true after Register")
	}

	p.UnregisterMCPMetadataRoutes("gw-u")
	if p.HasMCPMetadataRoute("gw-u") {
		t.Error("HasMCPMetadataRoute must be false after Unregister")
	}

	p.UnregisterMCPMetadataRoutes("gw-u")
	p.UnregisterMCPMetadataRoutes("never-registered")

	srv := httptest.NewServer(p.Handler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server/gw/gw-u")
	if err != nil {
		t.Fatalf("GET well-known: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("well-known path returned 200 after Unregister; stale handler was not removed")
	}
	if handled.Load() != 0 {
		t.Errorf("stale handler was called %d times after Unregister, want 0", handled.Load())
	}
}

func TestGatewayProxy_SecurityHeadersOnEveryResponse(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.SetStaticFS(fstest.MapFS{
		"index.html":    {Data: []byte("<!DOCTYPE html>spa")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	})
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/known" || strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{}`)
			return
		}
		http.NotFound(w, r)
	}))
	handler := proxy.Handler()

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
		"Permissions-Policy":     "camera=(), geolocation=(), payment=(), usb=(), microphone=(self)",
	}
	for _, path := range []string{"/", "/index.html", "/assets/app.js", "/some/deep/route", "/known", "/api/v1/sessions", "/gw/unknown/x", "/health"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			for name, value := range want {
				if got := w.Header().Get(name); got != value {
					t.Errorf("%s = %q, want %q (status %d)", name, got, value, w.Code)
				}
			}
		})
	}
}

func TestGatewayProxy_SecurityHeadersHandlerOverrideWins(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/api/v1/auth", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback", nil))

	if got := w.Header().Values("Referrer-Policy"); len(got) != 1 || got[0] != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want the handler's stricter [no-referrer]", got)
	}
}

func TestGatewayProxy_NoStoreOnAPIResponsesOnly(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.SetStaticFS(fstest.MapFS{
		"index.html":             {Data: []byte("<!DOCTYPE html>spa")},
		"assets/app-Ab12Cd34.js": {Data: []byte("console.log(1)")},
	})
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{}`)
			return
		}
		http.NotFound(w, r)
	}))
	handler := proxy.Handler()

	for _, path := range []string{"/api/v1/sessions", "/api/v1/platform/agents", "/api/v1/does-not-exist"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store (status %d)", got, w.Code)
			}
			if got := w.Header().Get("Pragma"); got != "no-cache" {
				t.Errorf("Pragma = %q, want no-cache (status %d)", got, w.Code)
			}
		})
	}
	for _, path := range []string{"/", "/index.html", "/assets/app-Ab12Cd34.js", "/some/deep/route", "/health"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			if got := w.Header().Get("Pragma"); got != "" {
				t.Errorf("Pragma = %q on a non-API path, want unset", got)
			}
		})
	}

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/assets/app-Ab12Cd34.js", nil))
	if got := w.Header().Get("Cache-Control"); got != immutableCacheControl {
		t.Errorf("hashed asset Cache-Control = %q, want the SPA's %q", got, immutableCacheControl)
	}
}

func TestGatewayProxy_NoStoreHandlerOverrideWins(t *testing.T) {
	proxy := NewGatewayProxy(":0", slog.Default())
	proxy.RegisterPrefixHandler("/api/v1/sse", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
	}))

	w := httptest.NewRecorder()
	proxy.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/sse/subscribe/t1", nil))

	if got := w.Header().Values("Cache-Control"); len(got) != 1 || got[0] != "no-cache" {
		t.Fatalf("Cache-Control = %q, want the handler's own [no-cache]", got)
	}
}
