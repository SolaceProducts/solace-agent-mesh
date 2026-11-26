// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync/atomic"
	"syscall"
	"testing"
)

type proxyRecorder struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func newProxyRecorder(t *testing.T) *proxyRecorder {
	t.Helper()
	p := &proxyRecorder{}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		p.hits.Add(1)
		_, _ = io.WriteString(w, validCardJSON)
	}))
	t.Cleanup(p.srv.Close)
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"} {
		t.Setenv(name, p.srv.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	return p
}

func setAllowPrivate(t *testing.T, allow bool) {
	t.Helper()
	if allow {
		t.Setenv(allowPrivateMCPEnvVar, "true")
		return
	}
	t.Setenv(allowPrivateMCPEnvVar, "")
}

func privateLANAddress() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.To4() != nil && ipNet.IP.IsPrivate() {
			return ipNet.IP.String()
		}
	}
	return ""
}

func TestGuardedClient_IgnoresProxyConfiguration(t *testing.T) {
	t.Run("transport has no proxy function", func(t *testing.T) {
		newProxyRecorder(t)
		tr, ok := httpClientWithSSRFGuard(nil).Transport.(*http.Transport)
		if !ok || tr.Proxy != nil {
			t.Fatalf("Transport = %T with Proxy set: %v", tr, ok && tr.Proxy != nil)
		}
	})

	t.Run("a non-loopback target is dialled directly, not handed to the proxy", func(t *testing.T) {
		lan := privateLANAddress()
		if lan == "" {
			t.Skip("host has no private non-loopback IPv4 address")
		}
		setAllowPrivate(t, true)
		proxy := newProxyRecorder(t)
		var hits atomic.Int32
		l, err := net.Listen("tcp", net.JoinHostPort(lan, "0"))
		if err != nil {
			t.Skipf("cannot listen on %s: %v", lan, err)
		}
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			_, _ = io.WriteString(w, validCardJSON)
		}))
		srv.Listener = l
		srv.Start()
		t.Cleanup(srv.Close)

		resp, err := httpClientWithSSRFGuard(nil).Get(srv.URL)
		if err != nil {
			t.Fatalf("GET %s: %v", srv.URL, err)
		}
		_ = resp.Body.Close()
		if hits.Load() != 1 || proxy.hits.Load() != 0 {
			t.Fatalf("target hits = %d, proxy hits = %d; want 1 and 0", hits.Load(), proxy.hits.Load())
		}
	})
}

func TestGuardedClient_NonCanonicalHostSpellings(t *testing.T) {
	numeric := []string{
		"2130706433", "0x7f000001", "0X7F000001", "017700000001", "127.1", "10.1", "167772161", "2852039166", "0xa9fea9fe",
		"169.254.43518", "0251.0376.0251.0376", "0x7f.0x0.0x0.0x1", "0177.0.0.1", "127.0.0.1.", "example.123", "svc.0x1f",
		"[fe80::1%25eth0]",
	}
	mappedToLoopback := []string{"\uff11\uff12\uff17.\uff10.\uff10.\uff11", "127\u30020\u30020\u30021", "localhost\uff0e", "LOCALHOST"}

	type run struct {
		allowPrivate bool
		hosts        []string
	}
	for _, r := range []run{{false, append(slices.Clone(numeric), mappedToLoopback...)}, {true, numeric}} {
		setAllowPrivate(t, r.allowPrivate)
		target := newInternalTarget(t, nil)
		for _, host := range r.hosts {
			t.Run(fmt.Sprintf("%s/allowPrivate=%v", host, r.allowPrivate), func(t *testing.T) {
				resp, err := httpClientWithSSRFGuard(nil).Get("http://" + host + ":" + target.port + "/")
				if resp != nil {
					_ = resp.Body.Close()
				}
				if !errors.Is(err, errSSRFBlocked) {
					t.Fatalf("err = %v, want errSSRFBlocked", err)
				}
				if target.hits.Load() != 0 {
					t.Fatalf("a request got through: target %d", target.hits.Load())
				}
			})
		}
	}
}

func TestRejectNonCanonicalHost(t *testing.T) {
	tests := []struct {
		host    string
		refused bool
	}{
		{"127.0.0.1", false}, {"::1", false}, {"2606:4700::1111", false}, {"example.com", false}, {"localhost", false},
		{"my_service.internal", false}, {"bücher.example", false}, {"svc.cluster.local.", false}, {"v4.example", false},
		{"2130706433", true}, {"0x7f000001", true}, {"127.1", true}, {"10.1", true}, {"127.0.0.1.", true},
		{"example.123", true}, {"svc.0x1f", true}, {"fe80::1%eth0", true}, {"fe80::1%25eth0", true},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			err := rejectNonCanonicalHost(tt.host)
			if tt.refused != errors.Is(err, errSSRFBlocked) {
				t.Fatalf("rejectNonCanonicalHost(%q) = %v, refused want %v", tt.host, err, tt.refused)
			}
		})
	}
}

func TestEndsInNumber(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true}, {"127.1", true}, {"2130706433", true}, {"0x7f", true}, {"0x", true}, {"0XFF", true},
		{"a.b.9", true}, {"host.123.", true}, {"example.com", false}, {"v4.example", false}, {"1example", false},
		{"0xyz", false}, {"a.b.0xg", false}, {"x123", false}, {"", false}, {"localhost", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := endsInNumber(tt.host); got != tt.want {
				t.Fatalf("endsInNumber(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestGuardedClient_WithoutProxyStillGuardsAtDial(t *testing.T) {
	setAllowPrivate(t, false)
	target := newInternalTarget(t, nil)

	_, err := httpClientWithSSRFGuard(nil).Get(target.srv.URL)
	if !errors.Is(err, errSSRFBlocked) {
		t.Fatalf("err = %v, want errSSRFBlocked", err)
	}
	if hits := target.hits.Load(); hits != 0 {
		t.Fatalf("target received %d request(s)", hits)
	}
}

func TestDescribeTransportFailure(t *testing.T) {
	timeoutErr := &net.OpError{Op: "read", Err: context.DeadlineExceeded}
	urlErr := func(target string, err error) error { return &url.Error{Op: "Get", URL: target, Err: err} }
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"deadline", context.DeadlineExceeded, "the request timed out"},
		{"wrapped deadline", fmt.Errorf("Get x: %w", timeoutErr), "the request timed out"},
		{"canceled", context.Canceled, "the request was canceled"},
		{"dns", &net.DNSError{Err: "no such host", Name: "svc.internal", Server: "10.96.0.10:53"}, "the host could not be resolved"},
		{"refused", &net.OpError{Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}, Err: syscall.ECONNREFUSED}, "the connection was refused"},
		{"reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, "the connection was closed by the server"},
		{"host unreachable", &net.OpError{Op: "dial", Err: syscall.EHOSTUNREACH}, "the host is unreachable from the platform"},
		{"network unreachable", &net.OpError{Op: "dial", Err: syscall.ENETUNREACH}, "the host is unreachable from the platform"},
		{"eof", urlErr("http://h/", io.EOF), "the connection was closed by the server"},
		{"unknown authority", x509.UnknownAuthorityError{}, "the TLS handshake or certificate check failed"},
		{"hostname mismatch", x509.HostnameError{Host: "h"}, "the TLS handshake or certificate check failed"},
		{"verification error", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}, "the TLS handshake or certificate check failed"},
		{"tls record header", tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, "the TLS handshake or certificate check failed"},
		{"plain http to https client", urlErr("https://h/", http.ErrSchemeMismatch), "the server answered over plain HTTP; use an http:// URL"},
		{"timeout that is not a deadline", urlErr("http://h/", stubTimeoutError{}), "the request timed out"},
		{"certificate invalid", x509.CertificateInvalidError{Reason: x509.Expired}, "the TLS handshake or certificate check failed"},
		{"tls alert", tls.AlertError(40), "the TLS handshake or certificate check failed"},
		{"tls alert from the peer over tcp", urlErr("https://h/", &net.OpError{Op: "remote error", Err: errors.New("tls: bad certificate")}), "the TLS handshake or certificate check failed"},
		{"tls handshake the client refused", urlErr("https://h/", errors.New("tls: no supported versions satisfy MinVersion and MaxVersion")), "the TLS handshake or certificate check failed"},
		{"network error that is not tls", urlErr("http://h/", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("boom")}), "the request failed"},
		{"server bytes after a net/http prefix are not tls", urlErr("http://h/", errors.New(`net/http: HTTP/1.x transport connection broken: bad Content-Length "tls: x"`)), "the request failed"},
		{"tls: text outside a url.Error is not tls", errors.New("tls: x"), "the request failed"},
		{"redirect refused", fmt.Errorf("Get: %w", errRedirectRefused), errRedirectRefused.Error()},
		{"too many redirects", fmt.Errorf("Get: %w", errTooManyRedirects), errTooManyRedirects.Error()},
		{"anything else", errors.New("boom at 10.1.2.3:9"), "the request failed"},
		{"url that merely says certificate is not a tls failure", urlErr("http://h/certificate-agent/card.json", errors.New("boom")), "the request failed"},
		{"url that merely says tls: is not a tls failure", urlErr("http://h/tls:x509/card.json", errors.New("boom")), "the request failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeTransportFailure(tt.err); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

type stubTimeoutError struct{}

func (stubTimeoutError) Error() string   { return "stub timeout" }
func (stubTimeoutError) Timeout() bool   { return true }
func (stubTimeoutError) Temporary() bool { return true }
