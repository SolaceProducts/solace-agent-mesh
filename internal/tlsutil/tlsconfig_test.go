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
	"errors"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuildTLSConfig_ZeroVerifiesAgainstTheSystemTrustStore(t *testing.T) {
	t.Parallel()
	cfg, err := BuildTLSConfig(TLSSettings{}, os.ReadFile)
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if cfg.InsecureSkipVerify || cfg.RootCAs != nil || len(cfg.Certificates) != 0 || cfg.ServerName != "" {
		t.Errorf("zero settings = %+v, want verify on, system roots, no client cert, no SNI override", cfg)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2", cfg.MinVersion)
	}
}

func TestBuildTLSConfig_InsecureSkipVerify(t *testing.T) {
	t.Parallel()
	cfg, err := BuildTLSConfig(TLSSettings{InsecureSkipVerify: true}, os.ReadFile)
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if cfg == nil || !cfg.InsecureSkipVerify {
		t.Fatalf("InsecureSkipVerify not propagated: %+v", cfg)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2 (%d)", cfg.MinVersion, tls.VersionTLS12)
	}
}

func TestBuildTLSConfig_CABundleLoads(t *testing.T) {
	t.Parallel()
	caPath := writeSelfSignedCA(t)
	cfg, err := BuildTLSConfig(TLSSettings{CAFile: caPath}, os.ReadFile)
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatalf("RootCAs not populated: %+v", cfg)
	}
}

func TestBuildTLSConfig_CABundleBadPath(t *testing.T) {
	t.Parallel()
	_, err := BuildTLSConfig(TLSSettings{CAFile: filepath.Join(t.TempDir(), "nope.pem")}, os.ReadFile)
	if err == nil {
		t.Fatal("expected error for missing CA bundle")
	}
}

func TestBuildTLSConfig_CABundleEmptyPEM(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := BuildTLSConfig(TLSSettings{CAFile: path}, os.ReadFile)
	if err == nil {
		t.Fatal("expected error for CA bundle with no PEM certs")
	}
	if !strings.Contains(err.Error(), "no PEM certs found") {
		t.Errorf("error %q should mention no PEM certs", err)
	}
}

func TestBuildTLSConfig_MTLSLoadsKeypair(t *testing.T) {
	t.Parallel()
	certPath, keyPath := writeSelfSignedClientKeypair(t)
	cfg, err := BuildTLSConfig(TLSSettings{ClientCertFile: certPath, ClientKeyFile: keyPath}, os.ReadFile)
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if cfg == nil || len(cfg.Certificates) != 1 {
		t.Fatalf("Certificates not populated: %+v", cfg)
	}
	if cfg.Certificates[0].PrivateKey == nil {
		t.Error("PrivateKey not loaded")
	}
}

func TestBuildTLSConfig_MTLSRejectsHalfPair(t *testing.T) {
	t.Parallel()
	tests := []TLSSettings{
		{ClientCertFile: "/nope.crt"},
		{ClientKeyFile: "/nope.key"},
	}
	for i, s := range tests {
		_, err := BuildTLSConfig(s, os.ReadFile)
		if err == nil {
			t.Errorf("case %d: want error for half pair", i)
			continue
		}
		if !strings.Contains(err.Error(), "client_cert") || !strings.Contains(err.Error(), "client_key") {
			t.Errorf("case %d: error %q should mention both client_cert and client_key", i, err)
		}
	}
}

func TestBuildTLSConfig_MTLSMismatchedKeypair(t *testing.T) {
	t.Parallel()
	cert1, _ := writeSelfSignedClientKeypair(t)
	_, key2 := writeSelfSignedClientKeypair(t)
	_, err := BuildTLSConfig(TLSSettings{ClientCertFile: cert1, ClientKeyFile: key2}, os.ReadFile)
	if err == nil {
		t.Fatal("want error for mismatched keypair")
	}
	if !strings.Contains(err.Error(), "mTLS") {
		t.Errorf("error %q should mention mTLS for debuggability", err)
	}
}

func TestBuildTLSConfig_ServerNameOverride(t *testing.T) {
	t.Parallel()
	cfg, err := BuildTLSConfig(TLSSettings{ServerName: "backend.internal"}, os.ReadFile)
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if cfg == nil || cfg.ServerName != "backend.internal" {
		t.Fatalf("ServerName not propagated: %+v", cfg)
	}
}

func TestBuildTLSConfig_AllOptionsCompose(t *testing.T) {
	t.Parallel()
	caPath := writeSelfSignedCA(t)
	certPath, keyPath := writeSelfSignedClientKeypair(t)
	cfg, err := BuildTLSConfig(TLSSettings{
		CAFile:         caPath,
		ClientCertFile: certPath,
		ClientKeyFile:  keyPath,
		ServerName:     "backend.svc.cluster.local",
	}, os.ReadFile)
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg is nil")
	}
	if cfg.InsecureSkipVerify {
		t.Error("InsecureSkipVerify on, verify=on should be default")
	}
	if cfg.RootCAs == nil {
		t.Error("RootCAs not populated")
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("len(Certificates) = %d, want 1", len(cfg.Certificates))
	}
	if cfg.ServerName != "backend.svc.cluster.local" {
		t.Errorf("ServerName = %q", cfg.ServerName)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2", cfg.MinVersion)
	}
}

func writeSelfSignedClientKeypair(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "tlsutil-test-client"},
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

func writeSelfSignedCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "tlsutil-test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	dir := t.TempDir()
	caPath := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return caPath
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func TestBuildTLSConfig_ReadsEveryFileThroughTheReaderItIsGiven(t *testing.T) {
	t.Parallel()
	certPath, keyPath := writeSelfSignedClientKeypair(t)
	files := map[string][]byte{
		"/virtual/ca.pem":     readTestFile(t, writeSelfSignedCA(t)),
		"/virtual/client.crt": readTestFile(t, certPath),
		"/virtual/client.key": readTestFile(t, keyPath),
	}
	var asked []string
	read := func(path string) ([]byte, error) {
		asked = append(asked, path)
		data, ok := files[path]
		if !ok {
			return nil, fs.ErrNotExist
		}
		return data, nil
	}

	cfg, err := BuildTLSConfig(TLSSettings{
		CAFile: "/virtual/ca.pem", ClientCertFile: "/virtual/client.crt", ClientKeyFile: "/virtual/client.key",
	}, read)
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	if cfg.RootCAs == nil || len(cfg.Certificates) != 1 {
		t.Errorf("config = %+v, want the CA pool and one client certificate", cfg)
	}
	slices.Sort(asked)
	if want := []string{"/virtual/ca.pem", "/virtual/client.crt", "/virtual/client.key"}; !slices.Equal(asked, want) {
		t.Errorf("reader asked for %v, want %v", asked, want)
	}
}

func TestBuildTLSConfig_ReaderFailureSurfacesForEachFile(t *testing.T) {
	t.Parallel()
	errRefused := errors.New("reader refused")
	refuse := func(string) ([]byte, error) { return nil, errRefused }
	tests := map[string]TLSSettings{
		"ca bundle":   {CAFile: "/virtual/ca.pem"},
		"client cert": {ClientCertFile: "/virtual/client.crt", ClientKeyFile: "/virtual/client.key"},
	}
	for name, settings := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := BuildTLSConfig(settings, refuse); !errors.Is(err, errRefused) {
				t.Errorf("err = %v, want it to wrap the reader's error", err)
			}
		})
	}

	certPath, _ := writeSelfSignedClientKeypair(t)
	certOnly := func(path string) ([]byte, error) {
		if path == "/virtual/client.crt" {
			return readTestFile(t, certPath), nil
		}
		return nil, errRefused
	}
	_, err := BuildTLSConfig(TLSSettings{ClientCertFile: "/virtual/client.crt", ClientKeyFile: "/virtual/client.key"}, certOnly)
	if !errors.Is(err, errRefused) {
		t.Errorf("client key read failure: err = %v, want it to wrap the reader's error", err)
	}
}
