// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
)

func newSSRFSafeClient(timeout time.Duration, allowLoopback bool) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: NewSSRFSafeTransport(allowLoopback),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= defaultMaxRedirects {
				return fmt.Errorf("too many redirects (>= %d)", defaultMaxRedirects)
			}
			if !IsSafeURL(req.URL.String(), allowLoopback) {
				return fmt.Errorf("redirect to unsafe URL")
			}
			return nil
		},
	}
}

var lookupIPAddr = net.DefaultResolver.LookupIPAddr

var reservedCIDRs []*net.IPNet

func init() {
	cidrs := []string{
		"100.64.0.0/10",
		"169.254.0.0/16",
		"192.0.0.0/24",
		"192.0.2.0/24",
		"198.18.0.0/15",
		"198.51.100.0/24",
		"203.0.113.0/24",
	}
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("ssrf: bad CIDR %q: %v", cidr, err))
		}
		reservedCIDRs = append(reservedCIDRs, ipNet)
	}
}

func NewSSRFSafeTransport(allowLoopback bool) *http.Transport {
	return &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			return httpproxy.FromEnvironment().ProxyFunc()(req.URL)
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if _, isProxyHop := proxyEnvHostPorts()[addr]; isProxyHop {
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, addr)
			}

			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("ssrf dialer: split host/port: %w", err)
			}

			ips, err := lookupIPAddr(ctx, host)
			if err != nil {
				return nil, &dialTargetError{err: fmt.Errorf("resolve %s: %w", host, err)}
			}
			if len(ips) == 0 {
				return nil, &dialTargetError{err: fmt.Errorf("no addresses for %s: %w", host, errUnsafeURL)}
			}

			for _, ipAddr := range ips {
				if !IsPublicIP(ipAddr.IP, allowLoopback) {
					return nil, &dialTargetError{err: fmt.Errorf("resolved IP %s is not public: %w", ipAddr.IP, errUnsafeURL)}
				}
			}

			var dialer net.Dialer
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
	}
}

func proxyEnvHostPorts() map[string]struct{} {
	out := map[string]struct{}{}
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		raw := os.Getenv(name)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		host := strings.ToLower(u.Hostname())
		port := u.Port()
		if port == "" {
			switch strings.ToLower(u.Scheme) {
			case "https":
				port = "443"
			default:
				port = "80"
			}
		}
		out[net.JoinHostPort(host, port)] = struct{}{}
	}
	return out
}

var errUnsafeURL = errors.New("URL is not safe to request")

type dialTargetError struct {
	err error
}

func (e *dialTargetError) Error() string { return "ssrf dialer: " + e.err.Error() }

func (e *dialTargetError) Unwrap() error { return e.err }

func IsSafeURL(rawURL string, allowLoopback bool) bool {
	return checkURLSafety(context.Background(), rawURL, allowLoopback) == nil
}

func checkURLSafety(ctx context.Context, rawURL string, allowLoopback bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errUnsafeURL
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errUnsafeURL
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return errUnsafeURL
	}

	if ip := net.ParseIP(hostname); ip != nil {
		if !IsPublicIP(ip, allowLoopback) {
			return errUnsafeURL
		}
		return nil
	}

	addrs, err := lookupIPAddr(ctx, hostname)
	if err != nil {
		if dnsErr, ok := errors.AsType[*net.DNSError](err); ok {
			return dnsErr
		}
		return errUnsafeURL
	}
	if len(addrs) == 0 {
		return errUnsafeURL
	}

	for _, addr := range addrs {
		if !IsPublicIP(addr.IP, allowLoopback) {
			return errUnsafeURL
		}
	}

	return nil
}

func IsPublicIP(ip net.IP, allowLoopback bool) bool {
	if allowLoopback && ip.IsLoopback() {
		return true
	}

	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}

	for _, cidr := range reservedCIDRs {
		if cidr.Contains(ip) {
			return false
		}
	}

	return true
}
