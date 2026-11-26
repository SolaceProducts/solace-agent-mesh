// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"
)

func TestSafeTransportErrorDetail_NeverContainsURLOrHost(t *testing.T) {
	secretURL := "https://victim.example.com/path?apiKey=super-secret-value"
	cases := []struct {
		name string
		err  error
	}{
		{"url.Error wrapping a dial failure", &url.Error{Op: "Get", URL: secretURL, Err: errors.New("dial tcp 10.0.0.1:443: connect: connection refused")}},
		{"context deadline exceeded", &url.Error{Op: "Get", URL: secretURL, Err: context.DeadlineExceeded}},
		{"context canceled", &url.Error{Op: "Get", URL: secretURL, Err: context.Canceled}},
		{"DNS error", &url.Error{Op: "Get", URL: secretURL, Err: &net.DNSError{Err: "no such host", Name: "victim.example.com"}}},
		{"bare net.OpError", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}},
		{"unrecognized error", errors.New("something with " + secretURL + " embedded")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeTransportErrorDetail(tc.err)
			if strings.Contains(got, "victim.example.com") || strings.Contains(got, "apiKey") || strings.Contains(got, "super-secret-value") {
				t.Fatalf("SafeTransportErrorDetail leaked sensitive content: %q", got)
			}
			if got == "" {
				t.Fatalf("SafeTransportErrorDetail returned empty string for a non-nil error")
			}
		})
	}
}

func TestSafeTransportErrorDetail_Nil(t *testing.T) {
	if got := SafeTransportErrorDetail(nil); got != "" {
		t.Errorf("SafeTransportErrorDetail(nil) = %q, want empty string", got)
	}
}

func TestTransportErrorCause_StripsURL(t *testing.T) {
	secretURL := "https://victim.example.com/path?apiKey=super-secret-value"
	cause := errors.New("dial tcp 10.0.0.1:443: connect: connection refused")
	err := &url.Error{Op: "Get", URL: secretURL, Err: cause}

	got := TransportErrorCause(err)
	if got != cause {
		t.Fatalf("TransportErrorCause() = %v, want the unwrapped cause %v", got, cause)
	}
	if strings.Contains(got.Error(), "apiKey") || strings.Contains(got.Error(), "victim.example.com") {
		t.Fatalf("TransportErrorCause() leaked the URL: %q", got.Error())
	}
}

func TestTransportErrorCause_NonURLErrorUnchanged(t *testing.T) {
	plain := errors.New("boom")
	if got := TransportErrorCause(plain); got != plain {
		t.Errorf("TransportErrorCause(%v) = %v, want unchanged", plain, got)
	}
}

func TestSafeTransportErrorDetail_Classifications(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"deadline exceeded", context.DeadlineExceeded, "connection timed out"},
		{"canceled", context.Canceled, "request canceled"},
		{"dns error", &net.DNSError{Err: "no such host", Name: "x"}, "DNS lookup failed"},
		{"op error", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}, "network connection failed"},
		{"op error wrapped via fmt.Errorf", fmt.Errorf("outbound: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}), "network connection failed"},
		{"unrecognized", errors.New("boom"), "request failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SafeTransportErrorDetail(tc.err); got != tc.want {
				t.Errorf("SafeTransportErrorDetail(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
