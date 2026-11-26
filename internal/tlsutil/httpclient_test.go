// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func generateCAAndLeaf(t *testing.T) (caPEM []byte, leafCert tls.Certificate) {
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
	leafCert = tls.Certificate{
		Certificate: [][]byte{leafDER},
		PrivateKey:  leafKey,
	}
	return caPEM, leafCert
}

func writeTempPEM(t *testing.T, data []byte, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write PEM: %v", err)
	}
	return path
}

func startTLSServer(t *testing.T, leafCert tls.Certificate) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{leafCert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestBuildHTTPClient_NoConfig(t *testing.T) {
	c, err := BuildHTTPClient("", false)
	if err != nil {
		t.Fatalf("BuildHTTPClient: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", c.Timeout)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
}

func TestBuildHTTPClient_SkipVerify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	plain, err := BuildHTTPClient("", false)
	if err != nil {
		t.Fatalf("BuildHTTPClient (plain): %v", err)
	}
	if _, getErr := plain.Get(srv.URL); getErr == nil {
		t.Fatal("expected x509 error against untrusted TLS server")
	} else if !strings.Contains(getErr.Error(), "x509") && !strings.Contains(getErr.Error(), "certificate") {
		t.Fatalf("expected x509/certificate error, got: %v", getErr)
	}

	skip, err := BuildHTTPClient("", true)
	if err != nil {
		t.Fatalf("BuildHTTPClient (skip): %v", err)
	}
	resp, err := skip.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET with skip-verify: %v", err)
	}
	_ = resp.Body.Close()
}

func TestBuildHTTPClient_CustomCA(t *testing.T) {
	caPEM, leafCert := generateCAAndLeaf(t)
	caPath := writeTempPEM(t, caPEM, "ca.pem")
	srv := startTLSServer(t, leafCert)

	c, err := BuildHTTPClient(caPath, false)
	if err != nil {
		t.Fatalf("BuildHTTPClient (CA): %v", err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET with custom CA: %v", err)
	}
	_ = resp.Body.Close()

	plain, err := BuildHTTPClient("", false)
	if err != nil {
		t.Fatalf("BuildHTTPClient (plain): %v", err)
	}
	if _, getErr := plain.Get(srv.URL); getErr == nil {
		t.Fatal("expected x509 error against server without CA bundle")
	}
}

func TestBuildHTTPClient_MissingFile(t *testing.T) {
	_, err := BuildHTTPClient("/nonexistent/path/ca.pem", false)
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
	if !strings.Contains(err.Error(), "/nonexistent/path/ca.pem") {
		t.Errorf("expected error to mention path, got: %v", err)
	}
}

func TestBuildHTTPClient_InvalidPEM(t *testing.T) {
	path := writeTempPEM(t, []byte("not a pem"), "garbage.pem")
	_, err := BuildHTTPClient(path, false)
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
	if !strings.Contains(err.Error(), "no valid PEM blocks") {
		t.Errorf("expected 'no valid PEM blocks' in error, got: %v", err)
	}
}

func TestBuildHTTPClient_PreservesProxyFromEnvironment(t *testing.T) {
	caPEM, _ := generateCAAndLeaf(t)
	caPath := writeTempPEM(t, caPEM, "ca.pem")

	cases := []struct {
		name               string
		caCertPath         string
		insecureSkipVerify bool
	}{
		{"skip-verify", "", true},
		{"custom-ca", caPath, false},
	}

	wantProxyPtr := reflect.ValueOf(http.ProxyFromEnvironment).Pointer()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := BuildHTTPClient(tc.caCertPath, tc.insecureSkipVerify)
			if err != nil {
				t.Fatalf("BuildHTTPClient: %v", err)
			}
			tr, ok := c.Transport.(*http.Transport)
			if !ok {
				t.Fatalf("Transport type = %T, want *http.Transport", c.Transport)
			}
			if tr.Proxy == nil {
				t.Fatal("Transport.Proxy is nil — HTTPS_PROXY/HTTP_PROXY/NO_PROXY will be ignored")
			}
			gotProxyPtr := reflect.ValueOf(tr.Proxy).Pointer()
			if gotProxyPtr != wantProxyPtr {
				t.Errorf("Transport.Proxy does not point to http.ProxyFromEnvironment")
			}
		})
	}
}
