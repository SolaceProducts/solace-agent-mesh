// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

type TLSSettings struct {
	InsecureSkipVerify bool
	CAFile             string
	ClientCertFile     string
	ClientKeyFile      string
	ServerName         string
}

func BuildTLSConfig(s TLSSettings, readFile func(path string) ([]byte, error)) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: s.InsecureSkipVerify, //nolint:gosec // explicit per-connector or operator opt-in
		ServerName:         s.ServerName,
	}
	if s.CAFile != "" {
		pemData, err := readFile(s.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA bundle %q: %w", s.CAFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, fmt.Errorf("parse CA bundle %q: no PEM certs found", s.CAFile)
		}
		cfg.RootCAs = pool
	}
	if s.ClientCertFile != "" || s.ClientKeyFile != "" {
		pair, err := loadClientKeyPair(s, readFile)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

func loadClientKeyPair(s TLSSettings, readFile func(path string) ([]byte, error)) (tls.Certificate, error) {
	if s.ClientCertFile == "" || s.ClientKeyFile == "" {
		return tls.Certificate{}, fmt.Errorf("mTLS: client_cert and client_key must be set together (got client_cert=%q, client_key=%q)",
			s.ClientCertFile, s.ClientKeyFile)
	}
	certPEM, err := readFile(s.ClientCertFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read client certificate %q: %w", s.ClientCertFile, err)
	}
	keyPEM, err := readFile(s.ClientKeyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("read client key %q: %w", s.ClientKeyFile, err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load mTLS keypair (cert=%q, key=%q): %w", s.ClientCertFile, s.ClientKeyFile, err)
	}
	return pair, nil
}
