// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package decision

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const secretLocation = "/login/SECRETPATH?token=SECRETQUERY&state=abc"

func assertNoLocation(t *testing.T, err error, host string) {
	t.Helper()
	if err == nil {
		t.Fatal("PostJSON succeeded, want a transport failure")
	}
	msg := err.Error()
	for _, leaked := range []string{"SECRETPATH", "SECRETQUERY", "/login"} {
		if strings.Contains(msg, leaked) {
			t.Errorf("error = %q, want no part of the redirect's Location (%s)", msg, leaked)
		}
	}
	if host != "" && strings.Contains(msg, host) {
		t.Errorf("error = %q, want no redirect host (%s)", msg, host)
	}
}

func resetConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		t.Errorf("hijack: %v", err)
		return
	}
	_ = conn.Close()
}

func closedPortAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func postWithBearerKey(t *testing.T, endpoint string) error {
	t.Helper()
	_, err := PostJSON(t.Context(), &http.Client{}, endpoint, APIKey{Value: "test-key"}, []byte(`{}`), nil)
	return err
}

func TestPostJSONKeepsAFollowedRedirectsLocationOutOfItsError(t *testing.T) {
	deadAddr := closedPortAddr(t)
	cases := map[string]struct {
		location string
		host     string
	}{
		"same origin, connection reset": {location: secretLocation},
		"another origin, refused":       {location: "http://" + deadAddr + secretLocation, host: deadAddr},
		"unsupported scheme":            {location: "ftp://secrethost.example" + secretLocation, host: "secrethost"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/login/") {
					resetConnection(t, w)
					return
				}
				http.Redirect(w, r, tc.location, http.StatusTemporaryRedirect)
			}))
			t.Cleanup(srv.Close)

			err := postWithBearerKey(t, srv.URL+"/v1/decide")
			assertNoLocation(t, err, tc.host)
			if _, ok := errors.AsType[*redirectedRequestError](err); !ok {
				t.Errorf("err = %v, want it marked as a failure after a redirect", err)
			}
			if kind := ClassifyTransport(err); kind != FailureUnreachable {
				t.Errorf("kind = %s, want %s: the cause still classifies the failure", kind, FailureUnreachable)
			}
		})
	}
}

const (
	unresolvableHost = "secret-target.invalid"
	slowHost         = "slow-target.invalid"
)

type dialTimeout struct{}

func (dialTimeout) Error() string   { return "dial timeout" }
func (dialTimeout) Timeout() bool   { return true }
func (dialTimeout) Temporary() bool { return true }

func dialFailingRedirectTargets(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, _ := net.SplitHostPort(addr)
	switch host {
	case unresolvableHost:
		return nil, &net.OpError{Op: "dial", Net: network, Err: &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}}
	case slowHost:
		return nil, &net.OpError{Op: "dial", Net: network, Err: dialTimeout{}}
	}
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

func TestPostJSONClassifiesAFailureAfterARedirectByItsCause(t *testing.T) {
	untrusted := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(untrusted.Close)
	cases := map[string]struct {
		location     string
		wait         time.Duration
		misconfigure bool
		timeout      bool
		kind         FailureKind
	}{
		"host does not resolve":   {location: "http://" + unresolvableHost + secretLocation, misconfigure: true, kind: FailureConfig},
		"untrusted certificate":   {location: untrusted.URL + secretLocation, misconfigure: true, kind: FailureConfig},
		"dial timeout":            {location: "http://" + slowHost + secretLocation, timeout: true, kind: FailureTimeout},
		"deadline on the new hop": {location: secretLocation, wait: time.Second, timeout: true, kind: FailureTimeout},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/login/") {
					select {
					case <-time.After(tc.wait):
					case <-r.Context().Done():
					}
					return
				}
				http.Redirect(w, r, tc.location, http.StatusTemporaryRedirect)
			}))
			t.Cleanup(srv.Close)
			client := &http.Client{Transport: &http.Transport{DialContext: dialFailingRedirectTargets}}
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()

			_, err := PostJSON(ctx, client, srv.URL+"/v1/decide", APIKey{Value: "test-key"}, []byte(`{}`), nil)
			assertNoLocation(t, err, strings.TrimPrefix(untrusted.URL, "https://"))
			if _, ok := errors.AsType[*redirectedRequestError](err); !ok {
				t.Fatalf("err = %v, want it marked as a failure after a redirect", err)
			}
			if got := IsMisconfiguredEndpoint(err); got != tc.misconfigure {
				t.Errorf("IsMisconfiguredEndpoint = %v, want %v", got, tc.misconfigure)
			}
			if got := IsTimeout(err); got != tc.timeout {
				t.Errorf("IsTimeout = %v, want %v", got, tc.timeout)
			}
			if kind := ClassifyTransport(err); kind != tc.kind {
				t.Errorf("kind = %s, want %s", kind, tc.kind)
			}
		})
	}
}

func TestPostJSONReportsABearerRedirectLoopWithoutTheLocation(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, secretLocation, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)

	err := postWithBearerKey(t, srv.URL+"/v1/decide")
	assertNoLocation(t, err, "")
	if errors.Is(err, ErrTooManyRedirects) {
		t.Errorf("err = %v, want net/http's own limit for a bearer-key client", err)
	}
	if kind := ClassifyTransport(err); kind != FailureUnreachable {
		t.Errorf("kind = %s, want %s", kind, FailureUnreachable)
	}
	if requests.Load() != maxRedirects {
		t.Errorf("requests = %d, want %d", requests.Load(), maxRedirects)
	}
}

func TestPostJSONReportsAnUnparsableLocationAsConfig(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Location", "http://[::1%SECRETPATH/?token=SECRETQUERY")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)

	err := postWithBearerKey(t, srv.URL+"/v1/decide")
	assertNoLocation(t, err, "")
	if !errors.Is(err, errUnparsableLocation) || ClassifyTransport(err) != FailureConfig {
		t.Errorf("err = %v (kind %s), want an unparsable-Location config failure", err, ClassifyTransport(err))
	}
	if requests.Load() != 1 {
		t.Errorf("requests = %d, want 1", requests.Load())
	}
}

func TestPostJSONKeepsAnUnredirectedFailureAsNetHTTPReportsIt(t *testing.T) {
	_, err := PostJSON(t.Context(), &http.Client{}, "http://"+closedPortAddr(t)+"/v1/decide", APIKey{Value: "bad\nkey"}, []byte(`{}`), nil)
	if !IsMalformedHeader(err) {
		t.Errorf("err = %v, want a malformed-header failure", err)
	}
	if _, ok := errors.AsType[*redirectedRequestError](err); ok {
		t.Errorf("err = %v, want it not marked as a failure after a redirect", err)
	}
}
