// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestIsBlockedIP_ReservedRanges(t *testing.T) {
	tests := []struct {
		name string
		ip   string
	}{
		{"unspecified IPv4 reaches localhost on Linux", "0.0.0.0"},
		{"this-network", "0.1.2.3"},
		{"CGNAT", "100.64.0.1"},
		{"Alibaba metadata", "100.100.100.200"},
		{"Azure WireServer", "168.63.129.16"},
		{"IETF protocol assignments", "192.0.0.8"},
		{"benchmark range", "198.18.0.1"},
		{"multicast", "224.0.0.1"},
		{"reserved class E", "240.0.0.1"},
		{"unspecified IPv6", "::"},
		{"IPv4-mapped loopback", "::ffff:127.0.0.1"},
		{"NAT64 wrapping loopback", "64:ff9b::7f00:1"},
		{"NAT64 wrapping a private address", "64:ff9b::a00:1"},
		{"NAT64 local-use prefix", "64:ff9b:1::a00:1"},
		{"6to4 wrapping loopback", "2002:7f00:1::"},
		{"IPv6 multicast", "ff02::1"},
		{"IPv6 unique local", "fd00::1"},
		{"IPv6 link-local", "fe80::1"},
		{"IPv6 site-local", "fec0::1"},
		{"last of 10/8", "10.255.255.255"},
		{"last of 127/8", "127.255.255.254"},
		{"last of 172.16/12", "172.31.255.255"},
		{"last of 192.168/16", "192.168.255.254"},
		{"last of 192.0.0/24", "192.0.0.255"},
		{"last of 198.18/15", "198.19.255.254"},
		{"last of 100.64/10", "100.127.255.254"},
		{"last of multicast", "239.255.255.255"},
		{"broadcast", "255.255.255.255"},
		{"last of fe80::/10", "febf::1"},
		{"last of fec0::/10", "feff::1"},
		{"last of ff00::/8", "ffff::1"},
		{"last of NAT64 local-use", "64:ff9b:1:ffff::1"},
		{"6to4 wrapping a private address", "2002:c0a8:101::"},
		{"last of this-network", "0.255.255.255"},
		{"last of link-local IPv4", "169.254.255.254"},
		{"first of multicast", "224.0.0.0"},
		{"last of unique local IPv6", "fdff:ffff::1"},
		{"first of unique local IPv6", "fc00::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("bad test IP %q", tt.ip)
			}
			if !isBlockedIP(ip) {
				t.Errorf("isBlockedIP(%s) = false, want true", tt.ip)
			}
		})
	}
}

func TestIsBlockedIP_PublicAddressesAllowed(t *testing.T) {
	for _, s := range []string{
		"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111",
		"64:ff9b::808:808", "2002:808:808::",
		"11.0.0.1", "126.255.255.255", "100.63.255.255", "100.128.0.1", "172.32.0.1",
		"192.169.0.1", "192.0.1.1", "198.20.0.1", "223.255.255.255",
		"1.0.0.1", "9.255.255.255", "168.63.129.15", "168.63.129.17", "169.253.255.255", "169.255.0.1",
		"172.15.255.255", "192.167.255.255", "198.17.255.255", "fbff::1",
	} {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("isBlockedIP(%s) = true, want false", s)
		}
	}
}

func TestIsBlockedIP_UnparsableAddressIsBlocked(t *testing.T) {
	if !isBlockedIP(net.IP{1, 2, 3}) {
		t.Error("isBlockedIP(malformed) = false, want true")
	}
	if !isBlockedIP(nil) {
		t.Error("isBlockedIP(nil) = false, want true")
	}
}

func TestValidateWebhookURL_BlocksUnspecifiedAddress(t *testing.T) {
	if err := validateWebhookURL("http://0.0.0.0:8080/hook"); err == nil {
		t.Fatal("expected http://0.0.0.0 to be rejected")
	}
}

func TestValidateWebhookURL_ErrorDoesNotRevealResolvedAddress(t *testing.T) {
	err := validateWebhookURL("http://10.1.2.3/hook")
	if err == nil {
		t.Fatal("expected rejection")
	}
	if strings.Contains(err.Error(), "10.1.2.3") {
		t.Errorf("error reveals the resolved address: %v", err)
	}
}

func TestValidateWebhookURL_MalformedURLErrorDoesNotContainTheURL(t *testing.T) {
	err := validateWebhookURL("http://[::1/SECRETTOKEN")
	if err == nil {
		t.Fatal("expected rejection")
	}
	if strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Errorf("error leaks the URL: %v", err)
	}
}

func TestValidateNotificationConfig_RejectsAPrivateWebhookTarget(t *testing.T) {
	for _, target := range []string{
		"http://10.0.0.1/hook", "http://127.0.0.1:9/hook", "http://[::1]/hook", "http://0.0.0.0/hook",
		"http://169.254.169.254/latest/meta-data", "http://100.100.100.200/hook",
	} {
		cfg := `{"on_failure":true,"channels":[{"type":"webhook","config":{"url":"` + target + `"}}]}`
		if err := ValidateNotificationConfig(cfg); err == nil {
			t.Errorf("target %s accepted, want rejection", target)
		}
	}
	public := `{"on_failure":true,"channels":[{"type":"webhook","config":{"url":"https://93.184.216.34/hook"}}]}`
	if err := ValidateNotificationConfig(public); err != nil {
		t.Errorf("a public target was rejected: %v", err)
	}
}

func TestValidateNotificationConfig_RejectsAWebhookWithoutAURL(t *testing.T) {
	cfg := `{"on_failure":true,"channels":[{"type":"webhook","config":{}}]}`
	if err := ValidateNotificationConfig(cfg); err == nil {
		t.Error("a webhook channel with no url was accepted")
	}
}

func TestValidateNotificationConfig_RejectsUnsafeWebhookMethod(t *testing.T) {
	for _, method := range []string{"DELETE", "patch", "GET", "OPTIONS"} {
		cfg := `{"on_failure":true,"channels":[{"type":"webhook","config":{"url":"https://93.184.216.34/hook","method":"` + method + `"}}]}`
		if err := ValidateNotificationConfig(cfg); err == nil {
			t.Errorf("method %s accepted, want rejection", method)
		}
	}
	for _, method := range []string{"POST", "put"} {
		cfg := `{"on_failure":true,"channels":[{"type":"webhook","config":{"url":"https://93.184.216.34/hook","method":"` + method + `"}}]}`
		if err := ValidateNotificationConfig(cfg); err != nil {
			t.Errorf("method %s rejected: %v", method, err)
		}
	}
}

func stubLookup(calls *atomic.Int32, ips ...string) ipLookup {
	return func(context.Context, string) ([]net.IPAddr, error) {
		calls.Add(1)
		addrs := make([]net.IPAddr, 0, len(ips))
		for _, s := range ips {
			addrs = append(addrs, net.IPAddr{IP: net.ParseIP(s)})
		}
		return addrs, nil
	}
}

type recordingDial struct {
	addrs   []string
	failFor map[string]bool
}

func (d *recordingDial) dial(_ context.Context, _, addr string) (net.Conn, error) {
	d.addrs = append(d.addrs, addr)
	if d.failFor[addr] {
		return nil, errors.New("unreachable")
	}
	client, server := net.Pipe()
	_ = server.Close()
	return client, nil
}

func TestGuardedDial_DialsTheCheckedAddressOnTheRequestedPort(t *testing.T) {
	var calls atomic.Int32
	rec := &recordingDial{}
	dial := guardedDialContext(rec.dial, stubLookup(&calls, "93.184.216.34"))

	conn, err := dial(t.Context(), "tcp", "hooks.example.test:8443")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()

	if len(rec.addrs) != 1 || rec.addrs[0] != "93.184.216.34:8443" {
		t.Fatalf("dialed %v, want exactly [93.184.216.34:8443]", rec.addrs)
	}
	if calls.Load() != 1 {
		t.Errorf("name resolved %d times, want 1", calls.Load())
	}
}

func TestGuardedDial_FallsBackToTheNextCheckedAddress(t *testing.T) {
	var calls atomic.Int32
	rec := &recordingDial{failFor: map[string]bool{"93.184.216.34:443": true}}
	dial := guardedDialContext(rec.dial, stubLookup(&calls, "93.184.216.34", "8.8.8.8"))

	conn, err := dial(t.Context(), "tcp", "hooks.example.test:443")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()

	want := []string{"93.184.216.34:443", "8.8.8.8:443"}
	if len(rec.addrs) != 2 || rec.addrs[0] != want[0] || rec.addrs[1] != want[1] {
		t.Fatalf("dialed %v, want %v", rec.addrs, want)
	}
}

func TestGuardedDial_FailsClosedWithoutDialing(t *testing.T) {
	tests := []struct {
		name        string
		addr        string
		lookup      ipLookup
		wantBlocked bool
	}{
		{
			name: "loopback answer", addr: "hooks.example.test:80", wantBlocked: true,
			lookup: stubLookup(new(atomic.Int32), "127.0.0.1"),
		},
		{
			name: "public answer mixed with a blocked one", addr: "hooks.example.test:80", wantBlocked: true,
			lookup: stubLookup(new(atomic.Int32), "93.184.216.34", "10.0.0.1"),
		},
		{
			name: "blocked answer first, public second", addr: "hooks.example.test:80", wantBlocked: true,
			lookup: stubLookup(new(atomic.Int32), "10.0.0.1", "93.184.216.34"),
		},
		{
			name: "malformed address", addr: "no-port", wantBlocked: true,
			lookup: stubLookup(new(atomic.Int32), "93.184.216.34"),
		},
		{
			name: "empty answer", addr: "hooks.example.test:80",
			lookup: func(context.Context, string) ([]net.IPAddr, error) { return nil, nil },
		},
		{
			name: "lookup error", addr: "hooks.example.test:80",
			lookup: func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("servfail") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recordingDial{}
			conn, err := guardedDialContext(rec.dial, tt.lookup)(t.Context(), "tcp", tt.addr)
			if err == nil {
				_ = conn.Close()
				t.Fatal("expected an error")
			}
			if len(rec.addrs) != 0 {
				t.Fatalf("dialed %v, want no dial", rec.addrs)
			}
			if tt.wantBlocked && !errors.Is(err, errWebhookDestinationBlocked) {
				t.Errorf("err = %v, want errWebhookDestinationBlocked", err)
			}
		})
	}
}

func TestWebhookClient_DoesNotReachLoopbackServerBehindHostname(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)

	var calls atomic.Int32
	client := newWebhookHTTPClient(stubLookup(&calls, "127.0.0.1"))
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://rebind.example.test:"+port+"/x", nil)
	if resp, err := client.Do(req); err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected the request to be blocked")
	}
	if hits.Load() != 0 {
		t.Fatalf("loopback server received %d requests, want 0", hits.Load())
	}
	if calls.Load() != 1 {
		t.Errorf("name resolved %d times for one request, want 1", calls.Load())
	}
}

func TestWebhookClient_TransportIgnoresProxyEnvironment(t *testing.T) {
	client := newWebhookHTTPClient(stubLookup(new(atomic.Int32), "93.184.216.34"))
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("transport has a proxy function, so HTTP_PROXY could route around the address guard")
	}
}

func TestWebhookClient_KeepsConnectionTuningOfTheDefaultTransport(t *testing.T) {
	client := newWebhookHTTPClient(stubLookup(new(atomic.Int32), "93.184.216.34"))
	transport := client.Transport.(*http.Transport)
	if transport.IdleConnTimeout == 0 || transport.MaxIdleConns == 0 || !transport.ForceAttemptHTTP2 {
		t.Fatalf("transport lost the default tuning: idle timeout %v, max idle %d, http2 %v",
			transport.IdleConnTimeout, transport.MaxIdleConns, transport.ForceAttemptHTTP2)
	}
}

const (
	webhookTestURL     = "http://hooks.example.test/hook"
	redirectLandingURL = "http://other.example.test/landing"
	webhookHostAddr    = "93.184.216.34:80"
	redirectTargetAddr = "93.184.216.35:80"
)

func newWebhookTestService(t *testing.T, server *httptest.Server, redirectTarget ...*httptest.Server) *NotificationService {
	t.Helper()
	routes := map[string]string{webhookHostAddr: server.Listener.Addr().String()}
	if len(redirectTarget) > 0 {
		routes[redirectTargetAddr] = redirectTarget[0].Listener.Addr().String()
	}
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "hooks.example.test":
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
		case "other.example.test":
			return []net.IPAddr{{IP: net.ParseIP("93.184.216.35")}}, nil
		}
		return nil, errors.New("unknown host " + host)
	}
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		target, ok := routes[addr]
		if !ok {
			return nil, errors.New("no test route for " + addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
	svc := NewNotificationService(nil, testLogger())
	svc.skipURLValidation = true
	svc.httpClient = newWebhookHTTPClientWithDial(dial, lookup)
	return svc
}

func TestSendWebhook_OnlyA2xxAnswerIsDelivered(t *testing.T) {
	tests := []struct {
		status  int
		wantErr bool
	}{
		{http.StatusOK, false},
		{http.StatusNoContent, false},
		{http.StatusMovedPermanently, true},
		{http.StatusFound, true},
		{http.StatusTemporaryRedirect, true},
		{http.StatusPermanentRedirect, true},
		{http.StatusBadRequest, true},
		{http.StatusInternalServerError, true},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			var redirectTargetHits atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectTargetHits.Add(1) }))
			t.Cleanup(target.Close)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", redirectLandingURL+"/SECRETLOCATION")
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(srv.Close)

			err := newWebhookTestService(t, srv, target).sendWebhook(t.Context(), map[string]any{"url": webhookTestURL}, map[string]any{})

			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "SECRETLOCATION") {
				t.Errorf("error leaks the redirect location: %v", err)
			}
			if redirectTargetHits.Load() != 0 {
				t.Errorf("redirect target received %d requests, want 0", redirectTargetHits.Load())
			}
		})
	}
}

func TestSendWebhook_RefusesAnUnsafeMethodWithoutSendingARequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)

	err := newWebhookTestService(t, srv).sendWebhook(t.Context(),
		map[string]any{"url": webhookTestURL, "method": "DELETE"}, map[string]any{})

	if err == nil {
		t.Fatal("expected an error for method DELETE")
	}
	if hits.Load() != 0 {
		t.Fatalf("server received %d requests, want 0", hits.Load())
	}
}

func TestSendWebhook_ErrorDoesNotContainWebhookURL(t *testing.T) {
	svc := NewNotificationService(nil, testLogger())
	svc.skipURLValidation = true
	svc.httpClient = newWebhookHTTPClient(stubLookup(new(atomic.Int32), "127.0.0.1"))

	for _, rawURL := range []string{
		"https://hooks.example.test/services/T0/B0/SECRETTOKEN",
		"http://[::1/SECRETTOKEN",
	} {
		err := svc.sendWebhook(t.Context(), map[string]any{"url": rawURL}, map[string]any{})
		if err == nil {
			t.Fatalf("expected an error for %q", rawURL)
		}
		if strings.Contains(err.Error(), "SECRETTOKEN") || strings.Contains(err.Error(), "hooks.example.test") {
			t.Errorf("error leaks the webhook URL: %v", err)
		}
	}
}
