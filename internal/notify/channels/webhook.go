// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const WebhookTimeout = 30 * time.Second

var blockedIPNets = func() []net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"::1/128",
		"fc00::/7",
		"fe80::/10",
	}
	nets := make([]net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("bad CIDR: " + cidr)
		}
		nets = append(nets, *ipnet)
	}
	return nets
}()

var DeniedWebhookHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"host":                true,
	"proxy-authorization": true,
	"x-forwarded-for":     true,
	"x-forwarded-host":    true,
	"x-forwarded-proto":   true,
	"x-real-ip":           true,
}

func NewSSRFSafeHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: checkDialTargetAtConnect,
	}
	return &http.Client{
		Timeout: WebhookTimeout,
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}
}

func checkDialTargetAtConnect(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("ssrf guard: bad dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("ssrf guard: non-IP dial address %q", host)
	}
	if isBlockedIP(ip) {
		return fmt.Errorf("ssrf guard: connection to blocked IP %s denied", ip)
	}
	return nil
}

func SendWebhook(ctx context.Context, client *http.Client, config map[string]any, payload Payload, skipURLValidation bool) error {
	rawURL, _ := config["url"].(string)
	if rawURL == "" {
		return fmt.Errorf("webhook URL not configured")
	}

	if !skipURLValidation {
		if err := ValidateWebhookURL(rawURL); err != nil {
			return fmt.Errorf("webhook URL validation: %w", err)
		}
	}

	method, _ := config["method"].(string)
	if method == "" {
		method = "POST"
	}
	method = strings.ToUpper(method)

	headers, _ := config["headers"].(map[string]any)
	for k := range headers {
		if DeniedWebhookHeaders[strings.ToLower(k)] {
			return fmt.Errorf("webhook headers contain denied security-sensitive key: %s", k)
		}
	}

	body, err := json.Marshal(FormatWebhookPayload(payload, config))
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if s, ok := v.(string); ok {
			req.Header.Set(k, s)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func ValidateWebhookURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme '%s'; only http and https are allowed", parsed.Scheme)
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return fmt.Errorf("URL must have a hostname")
	}

	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("could not resolve hostname: %s", hostname)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("webhook URL resolves to blocked IP range (%s); private, loopback, and link-local addresses are not allowed", ip)
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	for i := range blockedIPNets {
		if blockedIPNets[i].Contains(ip) {
			return true
		}
	}
	return false
}
