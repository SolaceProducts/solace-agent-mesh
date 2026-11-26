// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const raceRepeats = 25

type closeStyle string

const (
	closeWithFIN         closeStyle = "FIN without close_notify"
	closeWithRST         closeStyle = "RST"
	closeWithCloseNotify closeStyle = "close_notify"
)

func startClosingTLSServer(t *testing.T, pki mtlsFixture, style closeStyle) string {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(pki.serverCert, pki.serverKey)
	if err != nil {
		t.Fatalf("load server keypair: %v", err)
	}
	clientCAs := x509.NewCertPool()
	clientCAs.AppendCertsFromPEM(pki.caPEM)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
		ClientCAs:    clientCAs,
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go endAfterHandshake(conn.(*tls.Conn), style)
		}
	}()
	return "https://" + ln.Addr().String() + "/mcp"
}

func endAfterHandshake(conn *tls.Conn, style closeStyle) {
	if err := conn.Handshake(); err != nil {
		_ = conn.NetConn().Close()
		return
	}
	switch style {
	case closeWithRST:
		if tcp, ok := conn.NetConn().(*net.TCPConn); ok {
			_ = tcp.SetLinger(0)
		}
		_ = conn.NetConn().Close()
	case closeWithFIN:
		_ = conn.NetConn().Close()
	case closeWithCloseNotify:
		_ = conn.Close()
	}
}

func newForeignClientPair(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	caKey := newTestKey(t)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "some-other-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create foreign CA: %v", err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse foreign CA: %v", err)
	}
	return writeLeaf(t, dir, "foreign", ca, caKey, &x509.Certificate{
		SerialNumber: big.NewInt(101),
		Subject:      pkix.Name{CommonName: "foreign-client"},
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
}

func discoverWith(t *testing.T, url string, ssl *MCPSSLConfig) error {
	t.Helper()
	_, err := NewMCPDiscoveryService(discardLogger(), nil).DiscoverTools(t.Context(), MCPDiscoverRequest{
		ServerURL:      url,
		ConnectionType: "streamable-http",
		SSLConfig:      ssl,
	})
	return err
}

type refusingServers map[string]string

func startRefusingServers(t *testing.T, pki mtlsFixture) refusingServers {
	t.Helper()
	return refusingServers{
		"TLS alert":                  startTLSMCPServer(t, pki, true).URL,
		string(closeWithFIN):         startClosingTLSServer(t, pki, closeWithFIN),
		string(closeWithRST):         startClosingTLSServer(t, pki, closeWithRST),
		string(closeWithCloseNotify): startClosingTLSServer(t, pki, closeWithCloseNotify),
	}
}

func TestMCPDiscoveryService_MissingClientCert_IsNamedHoweverTheServerRefuses(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)

	for name, url := range startRefusingServers(t, pki) {
		t.Run(name, func(t *testing.T) {
			for range raceRepeats {
				err := discoverWith(t, url, &MCPSSLConfig{CABundle: pki.caBundle})
				expectDiscoverySentence(t, err, msgMCPClientCertMissing)
			}
		})
	}
}

func TestMCPDiscoveryService_ClientCertFromAnotherCA_IsNamedAsNotAccepted(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	foreignCert, foreignKey := newForeignClientPair(t, pki.dir)

	for name, url := range startRefusingServers(t, pki) {
		t.Run(name, func(t *testing.T) {
			for range raceRepeats {
				err := discoverWith(t, url, &MCPSSLConfig{CABundle: pki.caBundle, ClientCert: foreignCert, ClientKey: foreignKey})
				expectDiscoverySentence(t, err, msgMCPClientCertNotAccepted)
			}
		})
	}
}

func TestMCPDiscoveryService_ClientCertTheServerRejects_IsNamedAsRejected(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	rejecting := startTLSMCPServerWith(t, pki, func(cfg *tls.Config) {
		clientCAs := x509.NewCertPool()
		clientCAs.AppendCertsFromPEM(pki.caPEM)
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.ClientCAs = clientCAs
		cfg.VerifyPeerCertificate = func([][]byte, [][]*x509.Certificate) error { return errors.New("not welcome") }
	})
	servers := refusingServers{
		"TLS alert":                  rejecting.URL,
		string(closeWithFIN):         startClosingTLSServer(t, pki, closeWithFIN),
		string(closeWithRST):         startClosingTLSServer(t, pki, closeWithRST),
		string(closeWithCloseNotify): startClosingTLSServer(t, pki, closeWithCloseNotify),
	}

	for name, url := range servers {
		t.Run(name, func(t *testing.T) {
			for range raceRepeats {
				err := discoverWith(t, url, &MCPSSLConfig{CABundle: pki.caBundle, ClientCert: pki.clientCert, ClientKey: pki.clientKey})
				expectDiscoverySentence(t, err, msgMCPTLSClientCert)
			}
		})
	}
}

func TestClientCertRequest_DoesNotBlameTheCertificateAfterTheServerServed(t *testing.T) {
	seen := &clientCertRequest{}
	seen.requested.Store(true)
	var calls atomic.Int32
	transport := clientCertTransport{seen: seen, base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}
		return nil, errors.New("connection closed")
	})}
	req, err := http.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	failure := errors.New("request failed")

	if _, blamed := errors.AsType[*afterClientCertRequestError](seen.mark(failure)); !blamed {
		t.Fatal("a failure before the server served anything was not attributed to the certificate it asked for")
	}
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("first round trip: %v", err)
	}
	_ = resp.Body.Close()

	if _, blamed := errors.AsType[*afterClientCertRequestError](seen.mark(failure)); blamed {
		t.Error("a failure after the server had served a request was attributed to the client certificate")
	}
}

func TestClientCertRequest_AnHTTPErrorIsNotServingAndIsRemembered(t *testing.T) {
	seen := &clientCertRequest{}
	seen.requested.Store(true)
	transport := clientCertTransport{seen: seen, base: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadRequest, Body: http.NoBody}, nil
	})}
	req, err := http.NewRequest(http.MethodPost, "https://mcp.example.com/mcp", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	_ = resp.Body.Close()

	after, blamed := errors.AsType[*afterClientCertRequestError](seen.mark(errors.New("refused")))
	if !blamed {
		t.Fatal("an HTTP 400 counted as the server serving, so a refusal in HTTP could not be attributed to the certificate")
	}
	if after.refusedWith != http.StatusBadRequest {
		t.Errorf("refusedWith = %d, want %d", after.refusedWith, http.StatusBadRequest)
	}
}

func startHTTPRefusingServer(t *testing.T, pki mtlsFixture, allow func(*x509.Certificate) bool) *httptest.Server {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pki.caPEM)
	mcp := http.HandlerFunc(echoToolMCPHandler(t))
	refusing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peers := r.TLS.PeerCertificates
		if len(peers) == 0 {
			http.Error(w, "No required SSL certificate was sent", http.StatusBadRequest)
			return
		}
		if _, err := peers[0].Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil || !allow(peers[0]) {
			http.Error(w, "The SSL certificate error", http.StatusBadRequest)
			return
		}
		mcp.ServeHTTP(w, r)
	})
	cert, err := tls.LoadX509KeyPair(pki.serverCert, pki.serverKey)
	if err != nil {
		t.Fatalf("load server keypair: %v", err)
	}
	srv := httptest.NewUnstartedServer(refusing)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequestClientCert,
		ClientCAs:    pool,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func allowEveryone(*x509.Certificate) bool { return true }

func TestMCPDiscoveryService_MissingClientCert_IsNamedWhenTheServerRefusesInHTTP(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startHTTPRefusingServer(t, pki, allowEveryone)

	for range raceRepeats {
		err := discoverWith(t, server.URL, &MCPSSLConfig{CABundle: pki.caBundle})
		expectDiscoverySentence(t, err, msgMCPStatus400AfterCertMissing)
	}
}

func TestMCPDiscoveryService_ClientCertFromAnotherCA_IsNamedAsNotAcceptedWhenTheServerRefusesInHTTP(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	foreignCert, foreignKey := newForeignClientPair(t, pki.dir)
	server := startHTTPRefusingServer(t, pki, allowEveryone)

	for range raceRepeats {
		err := discoverWith(t, server.URL, &MCPSSLConfig{CABundle: pki.caBundle, ClientCert: foreignCert, ClientKey: foreignKey})
		expectDiscoverySentence(t, err, msgMCPStatus400AfterCertUnmatched)
	}
}

func TestMCPDiscoveryService_ClientCertTheServerRefusesInHTTP_IsNamedAsRejected(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startHTTPRefusingServer(t, pki, func(*x509.Certificate) bool { return false })

	for range raceRepeats {
		err := discoverWith(t, server.URL, &MCPSSLConfig{CABundle: pki.caBundle, ClientCert: pki.clientCert, ClientKey: pki.clientKey})
		expectDiscoverySentence(t, err, msgMCPStatus400AfterCertSent)
	}
}

func TestMCPDiscoveryService_HTTPErrorsFromAServerThatNeverAskedForACertificate_KeepTheirSentences(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	tests := []struct {
		name   string
		status int
		asks   bool
		want   string
	}{
		{"a 400 from a server that did not ask", http.StatusBadRequest, false, msgMCPUnexpectedResponse},
		{"a 401 from a server that asked", http.StatusUnauthorized, true, msgMCPAuthRequired},
		{"a 403 from a server that asked", http.StatusForbidden, true, msgMCPUnexpectedResponse},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := startTLSMCPServerWith(t, pki, func(cfg *tls.Config) {
				if tt.asks {
					cfg.ClientAuth = tls.RequestClientCert
				}
			})
			server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "refused", tt.status)
			})

			err := discoverWith(t, server.URL, &MCPSSLConfig{CABundle: pki.caBundle})

			expectDiscoverySentence(t, err, tt.want)
		})
	}
}

func TestMCPDiscoveryService_AServersOwnErrorAfterAskingForACertificate_IsNamedAsTheServers(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	pki := newMTLSFixture(t)
	pki.useAsTLSDir(t)
	server := startTLSMCPServerWith(t, pki, func(cfg *tls.Config) { cfg.ClientAuth = tls.RequestClientCert })
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var call struct {
			ID any `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&call)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": call.ID,
			"error": map[string]any{"code": -32000, "message": "upstream unavailable"},
		})
	})

	err := discoverWith(t, server.URL, &MCPSSLConfig{CABundle: pki.caBundle})

	expectDiscoverySentence(t, err, msgMCPServerError)
}
