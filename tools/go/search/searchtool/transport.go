// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

const (
	clusterDialTimeout         = 10 * time.Second
	clusterTLSHandshakeTimeout = 10 * time.Second
	defaultMaxIdleConns        = 100
	defaultIdleConnTimeout     = 90 * time.Second

	clusterResponseHeaderTimeout = 60 * time.Second
)

func outboundHTTPTransport(insecureSkipVerify bool) *http.Transport {
	tr := baseTransport()
	tr.Proxy = http.ProxyFromEnvironment
	tr.DialContext = (&net.Dialer{Timeout: clusterDialTimeout}).DialContext
	tr.TLSHandshakeTimeout = clusterTLSHandshakeTimeout
	tr.ResponseHeaderTimeout = clusterResponseHeaderTimeout
	tr.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: insecureSkipVerify, //nolint:gosec // explicit opt-in via YAML insecure_skip_verify
	}
	return tr
}

func baseTransport() *http.Transport {
	if def, ok := http.DefaultTransport.(*http.Transport); ok {
		return def.Clone()
	}
	return &http.Transport{
		MaxIdleConns:          defaultMaxIdleConns,
		IdleConnTimeout:       defaultIdleConnTimeout,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}
}
