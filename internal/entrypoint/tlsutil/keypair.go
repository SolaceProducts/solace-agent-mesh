// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/youmark/pkcs8"
)

func LoadX509KeyPair(certFile, keyFile, password string) (tls.Certificate, error) {
	if password == "" {
		return tls.LoadX509KeyPair(certFile, keyFile)
	}

	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read TLS certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read TLS key: %w", err)
	}

	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return tls.Certificate{}, fmt.Errorf("no PEM block found in key file")
	}

	switch block.Type {
	case "ENCRYPTED PRIVATE KEY":
		decryptedKeyPEM, err := decryptPKCS8(block.Bytes, password)
		if err != nil {
			return tls.Certificate{}, err
		}
		return tls.X509KeyPair(certPEM, decryptedKeyPEM)

	case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
		return tls.X509KeyPair(certPEM, keyPEM)

	default:
		if block.Headers["Proc-Type"] == "4,ENCRYPTED" {
			return tls.Certificate{}, fmt.Errorf(
				"legacy DEK-Info encrypted keys are not supported; convert to PKCS#8 format",
			)
		}
		return tls.Certificate{}, fmt.Errorf("unsupported PEM block type %q", block.Type)
	}
}

func decryptPKCS8(der []byte, password string) ([]byte, error) {
	key, err := pkcs8.ParsePKCS8PrivateKey(der, []byte(password))
	if err != nil {
		return nil, fmt.Errorf("decrypt PKCS#8 private key: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal decrypted private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), nil
}
