// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseToolsetConfig_SSL_MTLS_SNI(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "streamable-http",
			"url":  "https://mcp.example.com/v1",
			"ssl_config": map[string]any{
				"verify":      true,
				"ca_bundle":   "/path/ca.pem",
				"client_cert": "/path/client.crt",
				"client_key":  "/path/client.key",
				"server_name": "backend.internal",
			},
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.SSLVerify == nil || !*cfg.SSLVerify {
		t.Errorf("SSLVerify = %v, want *true", cfg.SSLVerify)
	}
	if cfg.SSLCABundle != "/path/ca.pem" {
		t.Errorf("SSLCABundle = %q", cfg.SSLCABundle)
	}
	if cfg.SSLClientCert != "/path/client.crt" {
		t.Errorf("SSLClientCert = %q", cfg.SSLClientCert)
	}
	if cfg.SSLClientKey != "/path/client.key" {
		t.Errorf("SSLClientKey = %q", cfg.SSLClientKey)
	}
	if cfg.SSLServerName != "backend.internal" {
		t.Errorf("SSLServerName = %q", cfg.SSLServerName)
	}
}

func TestParseToolsetConfig_SSL_MTLS_MismatchedPairRejected(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ssl  map[string]any
	}{
		{
			name: "cert without key",
			ssl:  map[string]any{"client_cert": "/p/c.crt"},
		},
		{
			name: "key without cert",
			ssl:  map[string]any{"client_key": "/p/c.key"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{
				"tool_type": "mcp",
				"connection_params": map[string]any{
					"type":       "streamable-http",
					"url":        "https://mcp.example.com/v1",
					"ssl_config": tc.ssl,
				},
			}
			_, err := ParseToolsetConfig(raw)
			if err == nil {
				t.Fatal("expected parse error for mismatched mTLS pair")
			}
			if !strings.Contains(err.Error(), "client_cert") || !strings.Contains(err.Error(), "client_key") {
				t.Errorf("error %q should mention both client_cert and client_key", err.Error())
			}
		})
	}
}

func TestParseToolsetConfig_SSL_BackwardsCompatibility(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"tool_type": "mcp",
		"connection_params": map[string]any{
			"type": "sse",
			"url":  "https://mcp.example.com/v1/sse",
			"ssl_config": map[string]any{
				"verify":    false,
				"ca_bundle": "/etc/ssl/company-ca.pem",
			},
		},
	}
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.SSLVerify == nil || *cfg.SSLVerify {
		t.Errorf("SSLVerify = %v, want *false", cfg.SSLVerify)
	}
	if cfg.SSLCABundle != "/etc/ssl/company-ca.pem" {
		t.Errorf("SSLCABundle = %q", cfg.SSLCABundle)
	}
	if cfg.SSLClientCert != "" || cfg.SSLClientKey != "" {
		t.Errorf("new mTLS fields unexpectedly set: cert=%q, key=%q", cfg.SSLClientCert, cfg.SSLClientKey)
	}
	if cfg.SSLServerName != "" {
		t.Errorf("new SSLServerName unexpectedly set: %q", cfg.SSLServerName)
	}
}

func TestHasSSLConfig_TriggersOnMTLSAndSNI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  ToolsetConfig
		want bool
	}{
		{"mtls only", ToolsetConfig{SSLClientCert: "/p/c.crt", SSLClientKey: "/p/c.key"}, true},
		{"sni only", ToolsetConfig{SSLServerName: "backend.internal"}, true},
		{"mtls + sni", ToolsetConfig{SSLClientCert: "/p/c.crt", SSLClientKey: "/p/c.key", SSLServerName: "backend.internal"}, true},
		{"sni alongside verify=true (default)", ToolsetConfig{SSLServerName: "x"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.HasSSLConfig(); got != tc.want {
				t.Errorf("HasSSLConfig() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildHTTPClient_MTLS_LoadsKeypair(t *testing.T) {
	t.Parallel()
	certPath, keyPath := writeSelfSignedClientKeypair(t)

	cfg := &ToolsetConfig{SSLClientCert: certPath, SSLClientKey: keyPath}
	client, err := cfg.BuildHTTPClient()
	if err != nil {
		t.Fatalf("BuildHTTPClient: %v", err)
	}
	tlsCfg := fullTLSConfig(t, client)
	if len(tlsCfg.Certificates) != 1 {
		t.Fatalf("len(Certificates) = %d, want 1", len(tlsCfg.Certificates))
	}
	cert := tlsCfg.Certificates[0]
	if len(cert.Certificate) == 0 {
		t.Error("client Certificate[0].Certificate is empty — DER was not loaded")
	}
	if cert.PrivateKey == nil {
		t.Error("client Certificate[0].PrivateKey is nil — key was not loaded")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	if leaf.Subject.CommonName != "mcp-test-client" {
		t.Errorf("loaded cert CN = %q, want mcp-test-client", leaf.Subject.CommonName)
	}
}

func TestBuildHTTPClient_MTLS_BadCertPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.pem")
	_, keyPath := writeSelfSignedClientKeypair(t)

	cfg := &ToolsetConfig{SSLClientCert: missing, SSLClientKey: keyPath}
	if _, err := cfg.BuildHTTPClient(); err == nil {
		t.Fatal("expected error for missing client cert")
	}
}

func TestBuildHTTPClient_MTLS_BadKeyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.key")
	certPath, _ := writeSelfSignedClientKeypair(t)

	cfg := &ToolsetConfig{SSLClientCert: certPath, SSLClientKey: missing}
	if _, err := cfg.BuildHTTPClient(); err == nil {
		t.Fatal("expected error for missing client key")
	}
}

func TestBuildHTTPClient_MTLS_MismatchedPairRejectedAtBuild(t *testing.T) {
	t.Parallel()
	tests := []*ToolsetConfig{
		{SSLClientCert: "/nonexistent.crt"},
		{SSLClientKey: "/nonexistent.key"},
	}
	for i, cfg := range tests {
		_, err := cfg.BuildHTTPClient()
		if err == nil {
			t.Errorf("case %d: expected error for incomplete mTLS pair, got nil (cfg=%+v)", i, cfg)
			continue
		}
		if !strings.Contains(err.Error(), "client_cert") || !strings.Contains(err.Error(), "client_key") {
			t.Errorf("case %d: error %q should mention both client_cert and client_key", i, err.Error())
		}
	}
}

func TestBuildHTTPClient_SNI_SetsServerName(t *testing.T) {
	t.Parallel()
	cfg := &ToolsetConfig{SSLServerName: "mcp-backend.internal"}
	client, err := cfg.BuildHTTPClient()
	if err != nil {
		t.Fatalf("BuildHTTPClient: %v", err)
	}
	tlsCfg := fullTLSConfig(t, client)
	if tlsCfg.ServerName != "mcp-backend.internal" {
		t.Errorf("ServerName = %q, want mcp-backend.internal", tlsCfg.ServerName)
	}
}

func TestBuildHTTPClient_AllSSLOptionsCompose(t *testing.T) {
	t.Parallel()
	verify := true
	caPath := writeSelfSignedCA(t)
	certPath, keyPath := writeSelfSignedClientKeypair(t)

	cfg := &ToolsetConfig{
		SSLVerify:     &verify,
		SSLCABundle:   caPath,
		SSLClientCert: certPath,
		SSLClientKey:  keyPath,
		SSLServerName: "backend.svc.cluster.local",
	}
	client, err := cfg.BuildHTTPClient()
	if err != nil {
		t.Fatalf("BuildHTTPClient: %v", err)
	}
	tlsCfg := fullTLSConfig(t, client)

	if tlsCfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = true, want false (verify=true)")
	}
	if tlsCfg.RootCAs == nil {
		t.Error("RootCAs = nil, want populated from ca_bundle")
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("len(Certificates) = %d, want 1 (mTLS keypair)", len(tlsCfg.Certificates))
	}
	if tlsCfg.ServerName != "backend.svc.cluster.local" {
		t.Errorf("ServerName = %q, want backend.svc.cluster.local", tlsCfg.ServerName)
	}
	if tlsCfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2 (%d)", tlsCfg.MinVersion, tls.VersionTLS12)
	}
}

func TestBuildHTTPClient_MTLS_InvalidKeypair(t *testing.T) {
	t.Parallel()
	cert1, _ := writeSelfSignedClientKeypair(t)
	_, key2 := writeSelfSignedClientKeypair(t)

	cfg := &ToolsetConfig{SSLClientCert: cert1, SSLClientKey: key2}
	_, err := cfg.BuildHTTPClient()
	if err == nil {
		t.Fatal("expected error when cert and key do not match")
	}
	if !strings.Contains(err.Error(), "mTLS") {
		t.Errorf("error %q should mention mTLS context for debuggability", err.Error())
	}
}

func fullTLSConfig(t *testing.T, client *http.Client) *tls.Config {
	t.Helper()
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client.Transport is %T, want *http.Transport", client.Transport)
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("TLSClientConfig is nil")
	}
	return tr.TLSClientConfig
}

func writeSelfSignedClientKeypair(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mcp-test-client"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})

	dir := t.TempDir()
	certPath = filepath.Join(dir, "client.crt")
	keyPath = filepath.Join(dir, "client.key")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certPath, keyPath
}
