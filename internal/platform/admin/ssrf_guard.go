// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/egressguard"
)

const allowPrivateMCPEnvVar = "SAM_PLATFORM_ALLOW_PRIVATE_MCP"

var errSSRFBlocked = errors.New("address rejected by SSRF guard")

var (
	errRedirectRefused  = errors.New("the redirect target is not an allowed http(s) URL")
	errTooManyRedirects = errors.New("stopped after 5 redirects")
)

func validateOutboundURL(rawURL string) error {
	return validateOutboundURLField("serverUrl", rawURL)
}

func validateOutboundURLField(field, rawURL string) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return &ValidationError{Message: fmt.Sprintf("invalid %s: not a valid URL", field)}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &ValidationError{Message: fmt.Sprintf("%s scheme %q is not allowed; use http or https", field, u.Scheme)}
	}
	if u.Host == "" {
		return &ValidationError{Message: field + " is missing a host"}
	}
	return nil
}

func allowPrivateMCP() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(allowPrivateMCPEnvVar)))
	return v == "1" || v == "true" || v == "yes"
}

func dialerWithSSRFGuard() *net.Dialer {
	base := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	allowPrivate := allowPrivateMCP()
	return &net.Dialer{
		Timeout:   base.Timeout,
		KeepAlive: base.KeepAlive,
		Resolver:  base.Resolver,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("dial %s: %w", address, err)
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("dial %s: %w (host did not resolve to an IP)", address, errSSRFBlocked)
			}
			if blocked, why := blockedAddress(ip, allowPrivate); blocked {
				return fmt.Errorf("dial %s (%s): %w: %s", address, ip, errSSRFBlocked, why)
			}
			return nil
		},
	}
}

func httpClientWithSSRFGuard(tlsCfg *tls.Config) *http.Client {
	d := dialerWithSSRFGuard()
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if host, _, err := net.SplitHostPort(addr); err == nil {
				if err := rejectNonCanonicalHost(host); err != nil {
					return nil, err
				}
			}
			return d.DialContext(ctx, network, addr)
		},
		TLSClientConfig:       tlsCfg,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errTooManyRedirects
			}
			if validateOutboundURL(req.URL.String()) != nil {
				return errRedirectRefused
			}
			return nil
		},
	}
}

const mcpTLSVerifyEnvVar = "SAM_MCP_CONNECTOR_TLS_VERIFY"

func mcpDiscoverySkipTLSVerify() bool {
	raw, ok := os.LookupEnv(mcpTLSVerifyEnvVar)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "false", "0", "no", "off", "n", "f":
		return true
	default:
		return false
	}
}

func blockedAddress(ip net.IP, allowPrivate bool) (bool, string) {
	return egressguard.BlockedAddress(ip, allowPrivate)
}

var proxyEnvNames = []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"}

func warnIfProxyEnvIgnored(logger *slog.Logger) {
	if logger == nil {
		return
	}
	var set []string
	for _, name := range proxyEnvNames {
		if os.Getenv(name) != "" {
			set = append(set, name)
		}
	}
	if len(set) > 0 {
		logger.Warn("SSRF-guarded platform fetches ignore the proxy environment", "proxyVariables", set)
	}
}
