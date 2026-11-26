// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"time"
)

func BuildHTTPClient(caCertPath string, insecureSkipVerify bool) (*http.Client, error) {
	if insecureSkipVerify {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // explicit dev/lab opt-in
		}
		return &http.Client{Timeout: 30 * time.Second, Transport: t}, nil
	}
	if caCertPath == "" {
		return &http.Client{Timeout: 30 * time.Second}, nil
	}

	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, fmt.Errorf("read CA cert %s: %w", caCertPath, err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to parse CA cert from %s: no valid PEM blocks found", caCertPath)
	}

	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: t}, nil
}
