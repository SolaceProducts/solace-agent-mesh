// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package egressproxy

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func startProxy(t *testing.T, allow []string) (string, *Proxy) {
	t.Helper()
	p, err := Start(Policy{Allowlist: allow, PolicyName: "test-policy"}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	addr := p.Address()
	return addr, p
}

func httpClientWithProxy(t *testing.T, proxyURL string) *http.Client {
	t.Helper()
	u, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(u),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test self-signed
		},
		Timeout: 5 * time.Second,
	}
}

func TestProxy_AllowsListedHost_HTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from upstream"))
	}))
	t.Cleanup(upstream.Close)

	proxyURL, proxy := startProxy(t, []string{"127.0.0.1"})
	client := httpClientWithProxy(t, proxyURL)

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	if string(body) != "hello from upstream" {
		t.Errorf("body = %q", body)
	}
	if s := proxy.Stats(); s.Allowed != 1 || s.Denied != 0 {
		t.Errorf("stats = %+v, want Allowed=1 Denied=0", s)
	}
}

func TestProxy_DenyOffAllowlist_HTTP(t *testing.T) {
	proxyURL, proxy := startProxy(t, []string{"allowed.example.com"})
	client := httpClientWithProxy(t, proxyURL)

	resp, err := client.Get("http://denied.example.com/path")
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	mustContain(t, string(body), "denied by SAM network policy")
	mustContain(t, string(body), "host=denied.example.com")
	mustContain(t, string(body), "policy=test-policy")
	if s := proxy.Stats(); s.Denied != 1 {
		t.Errorf("Denied = %d, want 1", s.Denied)
	}
}

func TestProxy_DoesNotFollowRedirectToDeniedHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://denied.invalid/secret", http.StatusFound)
	}))
	t.Cleanup(upstream.Close)

	proxyURL, proxy := startProxy(t, []string{"127.0.0.1"})
	client := httpClientWithProxy(t, proxyURL)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }

	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected the 302 passed through unfollowed; got %d (proxy must not follow a redirect to a non-allowlisted host)", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "http://denied.invalid/secret" {
		t.Errorf("Location = %q, want the denied redirect target preserved (not followed)", loc)
	}
	if s := proxy.Stats(); s.Allowed != 1 {
		t.Errorf("stats = %+v, want Allowed=1 (denied.invalid must not have been fetched)", s)
	}
}

func TestProxy_WildcardAllowsSubdomain(t *testing.T) {
	proxyURL, _ := startProxy(t, []string{"*.example.com"})
	client := httpClientWithProxy(t, proxyURL)

	resp, err := client.Get("http://example.com/x")
	if err != nil {
		t.Fatalf("client.Get example.com: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("bare base domain should be 403, got %d", resp.StatusCode)
	}

	resp, err = client.Get("http://foo.example.com/x")
	if err != nil {
		if !strings.Contains(err.Error(), "Forbidden") {
			return
		}
		t.Fatalf("foo.example.com unexpectedly 403'd: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("foo.example.com should not be 403; got 403")
	}
}

func TestProxy_DenyOnConnectTunnel(t *testing.T) {
	proxyURL, proxy := startProxy(t, []string{"allowed.example.com"})
	client := httpClientWithProxy(t, proxyURL)

	_, err := client.Get("https://denied.example.com/secret")
	if err == nil {
		t.Fatal("expected error for denied CONNECT, got nil")
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("error should indicate Forbidden, got %v", err)
	}
	if s := proxy.Stats(); s.Denied != 1 {
		t.Errorf("Denied = %d, want 1", s.Denied)
	}
}

func TestProxy_AddressShape(t *testing.T) {
	p, err := Start(Policy{Allowlist: []string{"x"}, PolicyName: "n"}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = p.Stop() }()
	addr := p.Address()
	if !strings.HasPrefix(addr, "http://127.0.0.1:") {
		t.Errorf("Address() = %q, want http://127.0.0.1:<port>", addr)
	}
}

func TestProxy_StopIsIdempotent(t *testing.T) {
	p, err := Start(Policy{Allowlist: []string{"x"}, PolicyName: "n"}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("first Stop: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

func TestMatchHost(t *testing.T) {
	if !(Policy{Allowlist: []string{"*.github.com"}}).MatchHost("api.github.com") {
		t.Error("MatchHost should allow a wildcard subdomain")
	}
	if (Policy{Allowlist: []string{"*.github.com"}}).MatchHost("github.com") {
		t.Error("MatchHost should not match the bare base of a wildcard")
	}
	if (Policy{Allowlist: []string{"allowed.example.com"}}).MatchHost("denied.example.com") {
		t.Error("MatchHost should deny an unlisted host")
	}
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected %q to contain %q", haystack, needle)
	}
}

func rawConnect(t *testing.T, proxyURL, target string) (net.Conn, *bufio.Reader, string) {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(proxyURL, "http://"))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	if _, werr := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); werr != nil {
		t.Fatalf("write CONNECT: %v", werr)
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read status line: %v", err)
	}
	for {
		line, derr := br.ReadString('\n')
		if derr != nil || line == "\r\n" || line == "\n" {
			break
		}
	}
	return conn, br, status
}

func TestProxy_AllowsConnectTunnel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		_, _ = io.Copy(c, c)
	}()

	proxyURL, proxy := startProxy(t, []string{"127.0.0.1"})
	conn, br, status := rawConnect(t, proxyURL, ln.Addr().String())
	defer func() { _ = conn.Close() }()
	if !strings.Contains(status, "200") {
		t.Fatalf("expected 200 Connection Established, got %q", status)
	}

	msg := "ping-through-tunnel"
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("write through tunnel: %v", err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(br, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(got) != msg {
		t.Errorf("tunnel echo = %q, want %q", got, msg)
	}
	if s := proxy.Stats(); s.Allowed != 1 {
		t.Errorf("Allowed = %d, want 1", s.Allowed)
	}
}

func TestProxy_StopClosesOpenTunnel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, aerr := ln.Accept()
		if aerr == nil {
			t.Cleanup(func() { _ = c.Close() })
		}
	}()

	p, err := Start(Policy{Allowlist: []string{"127.0.0.1"}, PolicyName: "n"}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	conn, br, status := rawConnect(t, p.Address(), ln.Addr().String())
	defer func() { _ = conn.Close() }()
	if !strings.Contains(status, "200") {
		t.Fatalf("expected tunnel established, got %q", status)
	}

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := br.ReadByte(); err == nil {
		t.Error("expected the tunnel to be closed after Stop, but read succeeded")
	}
}

func TestProxy_ConnectUpstreamDialFails(t *testing.T) {
	proxyURL, proxy := startProxy(t, []string{"127.0.0.1"})
	conn, err := net.Dial("tcp", strings.TrimPrefix(proxyURL, "http://"))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, werr := fmt.Fprintf(conn, "CONNECT 127.0.0.1:1 HTTP/1.1\r\nHost: 127.0.0.1:1\r\n\r\n"); werr != nil {
		t.Fatalf("write CONNECT: %v", werr)
	}
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(status, "502") {
		t.Errorf("expected 502 on upstream dial failure, got %q", status)
	}
	if s := proxy.Stats(); s.Upstream != 1 {
		t.Errorf("Upstream = %d, want 1", s.Upstream)
	}
}

func TestProxy_ForwardRequiresAbsoluteURL(t *testing.T) {
	proxyURL, _ := startProxy(t, []string{"127.0.0.1"})
	conn, err := net.Dial("tcp", strings.TrimPrefix(proxyURL, "http://"))
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, werr := fmt.Fprintf(conn, "GET /relative HTTP/1.1\r\nHost: example.com\r\n\r\n"); werr != nil {
		t.Fatalf("write request: %v", werr)
	}
	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(status, "400") {
		t.Errorf("expected 400 for non-absolute URL, got %q", status)
	}
}

func TestProxy_StopNilServer(t *testing.T) {
	if err := (&Proxy{}).Stop(); err != nil {
		t.Errorf("Stop on un-Started proxy should be a no-op, got %v", err)
	}
}

func TestProxy_ForwardStripsHopByHopHeaders(t *testing.T) {
	var sawConnectionHeader bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hop-Test") != "" && r.Header.Get("Connection") != "" {
			sawConnectionHeader = true
		}
		w.Header().Set("Keep-Alive", "timeout=5")
		w.Header().Set("X-Real", "kept")
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)

	proxyURL, _ := startProxy(t, []string{"127.0.0.1"})
	client := httpClientWithProxy(t, proxyURL)
	req, _ := http.NewRequest(http.MethodGet, upstream.URL, nil)
	req.Header.Set("Connection", "X-Hop-Test")
	req.Header.Set("X-Hop-Test", "v")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client.Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("Keep-Alive") != "" {
		t.Error("hop-by-hop Keep-Alive response header should have been stripped")
	}
	if resp.Header.Get("X-Real") != "kept" {
		t.Error("non-hop-by-hop response header should be preserved")
	}
	_ = sawConnectionHeader
}
