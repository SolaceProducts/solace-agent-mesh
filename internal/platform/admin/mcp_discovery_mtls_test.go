// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPDiscoveryService_MTLS_SucceedsWithClientCertAndCA(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)

	tools, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(t.Context(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
		SSLConfig:      &MCPSSLConfig{CABundle: pki.caBundle, ClientCert: pki.clientCert, ClientKey: pki.clientKey},
	})
	if err != nil {
		t.Fatalf("DiscoverTools with mTLS: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want one echo tool", tools)
	}
}

func TestConnectorService_SaveTimeDiscovery_PresentsTheClientCert(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)

	cs := &ConnectorService{logger: discardLogger(), mcpSvc: NewMCPDiscoveryService(discardLogger(), nil)}
	out, ok, _ := cs.enrichMCPManifestIfNeeded(t.Context(), "mcp", map[string]any{
		"server_url":      server.URL,
		"connection_type": "streamable-http",
		"ssl_config": map[string]any{
			"ca_bundle":   pki.caBundle,
			"client_cert": pki.clientCert,
			"client_key":  pki.clientKey,
		},
	})
	if !ok {
		t.Fatal("save-time discovery failed against the mTLS server")
	}
	if !hasNonEmptyManifest(out["manifest"]) {
		t.Fatalf("manifest = %v, want the discovered echo tool", out["manifest"])
	}
}

func TestMCPDiscoveryService_MTLS_NamesTheMissingClientCert(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)

	_, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(t.Context(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
		SSLConfig:      &MCPSSLConfig{CABundle: pki.caBundle},
	})
	expectDiscoverySentence(t, err, msgMCPClientCertMissing)
}

func TestMCPDiscoveryService_MTLS_NamesTheUntrustedServerCA(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, false)

	_, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(t.Context(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
		SSLConfig:      &MCPSSLConfig{ClientCert: pki.clientCert, ClientKey: pki.clientKey},
	})
	expectDiscoverySentence(t, err, msgMCPTLSUntrustedCA)
}

func TestMCPDiscoveryService_TLS_CustomCAOnly(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, false)

	tools, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(t.Context(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
		SSLConfig:      &MCPSSLConfig{CABundle: pki.caBundle},
	})
	if err != nil {
		t.Fatalf("DiscoverTools with a custom CA: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
}

func TestMCPDiscoveryService_TLS_VerifyOffOnlyThroughTheOperatorEnvVar(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, false)
	req := MCPDiscoverRequest{ServerURL: server.URL, ConnectionType: "streamable-http"}

	t.Setenv(mcpTLSVerifyEnvVar, "true")
	_, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(t.Context(), req)
	expectDiscoverySentence(t, err, msgMCPTLSUntrustedCA)

	t.Setenv(mcpTLSVerifyEnvVar, "false")
	tools, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(t.Context(), req)
	if err != nil {
		t.Fatalf("DiscoverTools with the operator's verify switch off: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("len(tools) = %d, want 1", len(tools))
	}
}

func TestMCPDiscoveryService_TLS_NoSSLConfig(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	srv := mcpToolsListServer(t, []map[string]any{{"name": "echo", "inputSchema": map[string]any{"type": "object"}}})
	t.Cleanup(srv.Close)

	tools, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(t.Context(), MCPDiscoverRequest{
		ServerURL:      srv.URL,
		ConnectionType: "streamable-http",
	})
	if err != nil {
		t.Fatalf("DiscoverTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want one echo tool", tools)
	}
}

func TestMCPDiscoveryService_MTLS_SSRFGuardStillBlocksLoopback(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServer(t, pki, true)

	_, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(t.Context(), MCPDiscoverRequest{
		ServerURL:      server.URL,
		ConnectionType: "streamable-http",
		SSLConfig:      &MCPSSLConfig{CABundle: pki.caBundle, ClientCert: pki.clientCert, ClientKey: pki.clientKey},
	})
	var svcErr *ExternalServiceError
	if !errors.As(err, &svcErr) || !strings.Contains(svcErr.Message, "SSRF protection") {
		t.Fatalf("err = %v, want the SSRF guard's refusal", err)
	}
}

func expectDiscoverySentence(t *testing.T, err error, want string) {
	t.Helper()
	var svcErr *ExternalServiceError
	if !errors.As(err, &svcErr) {
		t.Fatalf("err = %v (%T), want *ExternalServiceError", err, err)
	}
	if svcErr.Message != want {
		t.Errorf("message = %q, want %q", svcErr.Message, want)
	}
}

type mtlsFixture struct {
	dir             string
	caPEM           []byte
	caBundle        string
	serverCert      string
	serverKey       string
	clientCert      string
	clientKey       string
	namedServerCert string
	namedServerKey  string
}

const namedServerHost = "mcp.internal.test"

func (f mtlsFixture) useAsTLSDir(t *testing.T) {
	t.Helper()
	t.Setenv(mcpTLSDirEnvVar, f.dir)
}

func newMTLSFixture(t *testing.T) mtlsFixture {
	t.Helper()
	caKey := newTestKey(t)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mcp-mtls-test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}
	dir := t.TempDir()
	f := mtlsFixture{dir: dir, caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})}
	f.caBundle = writeTestFile(t, dir, "ca.pem", f.caPEM)
	f.serverCert, f.serverKey = writeLeaf(t, dir, "server", caCert, caKey, &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	})
	f.clientCert, f.clientKey = writeLeaf(t, dir, "client", caCert, caKey, &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "mcp-mtls-test-client"},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	f.namedServerCert, f.namedServerKey = writeLeaf(t, dir, "named-server", caCert, caKey, &x509.Certificate{
		SerialNumber: big.NewInt(4),
		Subject:      pkix.Name{CommonName: namedServerHost},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{namedServerHost},
	})
	return f
}

func newTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func writeLeaf(t *testing.T, dir, name string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, tmpl *x509.Certificate) (certPath, keyPath string) {
	t.Helper()
	key := newTestKey(t)
	tmpl.NotBefore = time.Now().Add(-time.Minute)
	tmpl.NotAfter = time.Now().Add(time.Hour)
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create %s cert: %v", name, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal %s key: %v", name, err)
	}
	certPath = writeTestFile(t, dir, name+".crt", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPath = writeTestFile(t, dir, name+".key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	return certPath, keyPath
}

func writeTestFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func startTLSMCPServer(t *testing.T, pki mtlsFixture, requireClientCert bool) *httptest.Server {
	t.Helper()
	return startTLSMCPServerWith(t, pki, func(cfg *tls.Config) {
		if requireClientCert {
			clientCAs := x509.NewCertPool()
			clientCAs.AppendCertsFromPEM(pki.caPEM)
			cfg.ClientAuth = tls.RequireAndVerifyClientCert
			cfg.ClientCAs = clientCAs
		}
	})
}

func startTLSMCPServerWith(t *testing.T, pki mtlsFixture, configure func(*tls.Config)) *httptest.Server {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(pki.serverCert, pki.serverKey)
	if err != nil {
		t.Fatalf("load server keypair: %v", err)
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	configure(tlsCfg)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(echoToolMCPHandler(t)))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = tlsCfg
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv.Listener = ln
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func echoToolMCPHandler(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
	t.Helper()
	writeResult := func(w http.ResponseWriter, id json.RawMessage, result any) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
			t.Errorf("write result: %v", err)
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "parse json-rpc", http.StatusBadRequest)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("mcp-session-id", "test-session")
			writeResult(w, req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "mcp-mtls-test", "version": "1.0.0"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeResult(w, req.ID, map[string]any{"tools": []map[string]any{
				{"name": "echo", "inputSchema": map[string]any{"type": "object"}},
			}})
		default:
			http.Error(w, "unexpected method "+req.Method, http.StatusBadRequest)
		}
	}
}
