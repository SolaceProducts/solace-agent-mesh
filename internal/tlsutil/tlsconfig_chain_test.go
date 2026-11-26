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
	"slices"
	"testing"
	"time"
)

type chainPKI struct {
	rootPEM         []byte
	intermediatePEM []byte
	leafPEM         []byte
	leafKeyPEM      []byte
	server          tls.Certificate
	rootPool        *x509.CertPool
}

func issueCert(t *testing.T, tmpl, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*ecdsa.PrivateKey, *x509.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, signerKey := parent, parentKey
	if parent == nil {
		signer, signerKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, &key.PublicKey, signerKey)
	if err != nil {
		t.Fatalf("create certificate %q: %v", tmpl.Subject.CommonName, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate %q: %v", tmpl.Subject.CommonName, err)
	}
	return key, cert, der
}

func newChainPKI(t *testing.T) chainPKI {
	t.Helper()
	notBefore, notAfter := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)
	rootKey, rootCert, rootDER := issueCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "chain-test-root"},
		NotBefore: notBefore, NotAfter: notAfter,
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}, nil, nil)
	intermediateKey, intermediateCert, intermediateDER := issueCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "chain-test-intermediate"},
		NotBefore: notBefore, NotAfter: notAfter,
		IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign,
	}, rootCert, rootKey)
	leafKey, _, leafDER := issueCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "chain-test-client"},
		NotBefore: notBefore, NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, intermediateCert, intermediateKey)
	serverKey, _, serverDER := issueCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(4), Subject: pkix.Name{CommonName: "chain-test-server"},
		NotBefore: notBefore, NotAfter: notAfter, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, rootCert, rootKey)
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal client key: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(rootCert)
	return chainPKI{
		rootPEM:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}),
		intermediatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: intermediateDER}),
		leafPEM:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		leafKeyPEM:      pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER}),
		server:          tls.Certificate{Certificate: [][]byte{serverDER}, PrivateKey: serverKey},
		rootPool:        pool,
	}
}

func (p chainPKI) startServerTrustingOnlyTheRoot(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	srv.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{p.server},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    p.rootPool,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func writeTestFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestBuildTLSConfig_ClientCertificateFileHoldingAChain(t *testing.T) {
	t.Parallel()
	pki := newChainPKI(t)
	srv := pki.startServerTrustingOnlyTheRoot(t)
	dir := t.TempDir()
	caPath := writeTestFile(t, dir, "ca.pem", pki.rootPEM)
	keyPath := writeTestFile(t, dir, "client.key", pki.leafKeyPEM)

	tests := []struct {
		name      string
		certPEM   []byte
		wantServe bool
	}{
		{"certificate followed by its intermediate", slices.Concat(pki.leafPEM, pki.intermediatePEM), true},
		{"certificate alone", pki.leafPEM, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			certPath := writeTestFile(t, t.TempDir(), "client.crt", tt.certPEM)
			cfg, err := BuildTLSConfig(TLSSettings{CAFile: caPath, ClientCertFile: certPath, ClientKeyFile: keyPath}, os.ReadFile)
			if err != nil {
				t.Fatalf("BuildTLSConfig: %v", err)
			}
			client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: cfg}}
			t.Cleanup(client.CloseIdleConnections)

			resp, err := client.Get(srv.URL)
			if resp != nil {
				_ = resp.Body.Close()
			}
			switch {
			case tt.wantServe && err != nil:
				t.Errorf("server that trusts only the root refused a client certificate sent with its intermediate: %v", err)
			case !tt.wantServe && err == nil:
				t.Error("server that trusts only the root served a client certificate sent without its intermediate")
			}
		})
	}
}
