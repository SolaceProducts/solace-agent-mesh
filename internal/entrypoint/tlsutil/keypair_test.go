// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/youmark/pkcs8"
)

func TestLoadX509KeyPair_UnencryptedNoPassword(t *testing.T) {
	certFile, keyFile := generateUnencryptedCert(t)

	cert, err := LoadX509KeyPair(certFile, keyFile, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("expected at least one certificate")
	}
}

func TestLoadX509KeyPair_EncryptedECDSA(t *testing.T) {
	certFile, keyFile := generateEncryptedCert(t, "test-password", "ecdsa")

	cert, err := LoadX509KeyPair(certFile, keyFile, "test-password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("expected at least one certificate")
	}
}

func TestLoadX509KeyPair_EncryptedRSA(t *testing.T) {
	certFile, keyFile := generateEncryptedCert(t, "rsa-pass", "rsa")

	cert, err := LoadX509KeyPair(certFile, keyFile, "rsa-pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("expected at least one certificate")
	}
}

func TestLoadX509KeyPair_WrongPassword(t *testing.T) {
	certFile, keyFile := generateEncryptedCert(t, "correct-password", "ecdsa")

	_, err := LoadX509KeyPair(certFile, keyFile, "wrong-password")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestLoadX509KeyPair_EncryptedKeyEmptyPassword(t *testing.T) {
	certFile, keyFile := generateEncryptedCert(t, "some-password", "ecdsa")

	_, err := LoadX509KeyPair(certFile, keyFile, "")
	if err == nil {
		t.Fatal("expected error when loading encrypted key without password")
	}
}

func TestLoadX509KeyPair_UnencryptedKeyWithPassword(t *testing.T) {
	certFile, keyFile := generateUnencryptedCert(t)

	cert, err := LoadX509KeyPair(certFile, keyFile, "unnecessary-password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("expected at least one certificate")
	}
}

func TestLoadX509KeyPair_NonExistentCert(t *testing.T) {
	_, keyFile := generateUnencryptedCert(t)

	_, err := LoadX509KeyPair("/nonexistent/cert.pem", keyFile, "password")
	if err == nil {
		t.Fatal("expected error for nonexistent cert file")
	}
}

func TestLoadX509KeyPair_NonExistentKey(t *testing.T) {
	certFile, _ := generateUnencryptedCert(t)

	_, err := LoadX509KeyPair(certFile, "/nonexistent/key.pem", "password")
	if err == nil {
		t.Fatal("expected error for nonexistent key file")
	}
}

func TestLoadX509KeyPair_InvalidPEM(t *testing.T) {
	dir := t.TempDir()
	certFile, _ := generateUnencryptedCert(t)
	keyFile := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(keyFile, []byte("not a pem block"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadX509KeyPair(certFile, keyFile, "password")
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestLoadX509KeyPair_TLSHandshake(t *testing.T) {
	certFile, keyFile := generateEncryptedCert(t, "handshake-pass", "ecdsa")

	cert, err := LoadX509KeyPair(certFile, keyFile, "handshake-pass")
	if err != nil {
		t.Fatalf("load keypair: %v", err)
	}

	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("failed to add cert to pool")
	}

	serverCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	clientCfg := &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan error, 1)
	go func() {
		srvConn, acceptErr := ln.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer func() { _ = srvConn.Close() }()
		_, writeErr := srvConn.Write([]byte("hello"))
		done <- writeErr
	}()

	conn, err := tls.Dial("tcp", ln.Addr().String(), clientCfg)
	if err != nil {
		t.Fatalf("TLS dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	buf := make([]byte, 5)
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("TLS read: %v", err)
	}
	if string(buf) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(buf))
	}

	if err := <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func generateUnencryptedCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	certDER := selfSignedCert(t, &key.PublicKey, key)

	certPath := filepath.Join(dir, "cert.pem")
	writePEM(t, certPath, "CERTIFICATE", certDER)

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "key.pem")
	writePEM(t, keyPath, "EC PRIVATE KEY", keyDER)

	return certPath, keyPath
}

func generateEncryptedCert(t *testing.T, password, keyType string) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()

	var privKey any
	var pubKey any

	switch keyType {
	case "rsa":
		rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		privKey = rsaKey
		pubKey = &rsaKey.PublicKey
	default:
		ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		privKey = ecKey
		pubKey = &ecKey.PublicKey
	}

	certDER := selfSignedCert(t, pubKey, privKey)

	certPath := filepath.Join(dir, "cert.pem")
	writePEM(t, certPath, "CERTIFICATE", certDER)

	encPEM, err := pkcs8.MarshalPrivateKey(privKey, []byte(password), nil)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "key.pem")
	writePEM(t, keyPath, "ENCRYPTED PRIVATE KEY", encPEM)

	return certPath, keyPath
}

func selfSignedCert(t *testing.T, pubKey any, privKey any) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"test"}},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pubKey, privKey)
	if err != nil {
		t.Fatal(err)
	}
	return certDER
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
