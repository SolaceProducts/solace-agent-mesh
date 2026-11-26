// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"
	"testing"
)

type fakeHTTPErr struct {
	code int
	msg  string
}

func (e *fakeHTTPErr) Error() string   { return e.msg }
func (e *fakeHTTPErr) StatusCode() int { return e.code }

type fakeRPCErr struct {
	code int
	msg  string
}

func (e *fakeRPCErr) Error() string         { return e.msg }
func (e *fakeRPCErr) JSONRPCErrorCode() int { return e.code }

func TestClassifyA2AError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},

		{"typed 401", &fakeHTTPErr{code: 401, msg: "unauthorized"}, "auth_error"},
		{"typed 403", &fakeHTTPErr{code: 403, msg: "forbidden"}, "auth_error"},
		{"typed 404", &fakeHTTPErr{code: 404, msg: "not found"}, "4xx_error"},
		{"typed 500", &fakeHTTPErr{code: 500, msg: "internal"}, "5xx_error"},
		{"typed 503", &fakeHTTPErr{code: 503, msg: "unavailable"}, "5xx_error"},

		{"typed rpc", &fakeRPCErr{code: -32602, msg: "invalid params"}, "jsonrpc_error"},

		{"text HTTP 401", errors.New("did not return a valid SSE streaming response (HTTP 401: ...)"), "auth_error"},
		{"text HTTP 403", errors.New("HTTP 403 forbidden"), "auth_error"},
		{"text HTTP 404", errors.New("got HTTP 404 from server"), "4xx_error"},
		{"text HTTP 502", errors.New("HTTP 502 bad gateway"), "5xx_error"},
		{"text HTTP 200 ignored", errors.New("HTTP 200 but body invalid"), "error"},

		{"downstream error -32001", errors.New("downstream error -32001: boom"), "jsonrpc_error"},
		{"jsonrpc word", errors.New("jsonrpc parse error -32700"), "jsonrpc_error"},

		{"syscall ECONNREFUSED", syscall.ECONNREFUSED, "connection_error"},
		{"wrapped ECONNREFUSED", fmt.Errorf("dial tcp: %w", syscall.ECONNREFUSED), "connection_error"},
		{"DNSError", &net.DNSError{Name: "no.such.example", IsNotFound: true}, "connection_error"},
		{"OpError", &net.OpError{Op: "dial", Err: errors.New("network unreachable")}, "connection_error"},
		{"url.Error refused", &url.Error{Op: "Get", URL: "http://x", Err: errors.New("connection refused")}, "connection_error"},

		{"timeout text", errors.New("context deadline exceeded: timeout"), "timeout"},
		{"deadline behind sanitized text", fmt.Errorf("connection timed out: %w", context.DeadlineExceeded), "timeout"},
		{"rate limit text", errors.New("rate limit exceeded"), "rate_limit"},
		{"generic", errors.New("something went wrong"), "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyA2AError(tt.err)
			if got != tt.want {
				t.Errorf("classifyA2AError(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestClassifyA2AError_HTTPErrorOverridesJSONRPCText(t *testing.T) {
	err := &fakeHTTPErr{code: 500, msg: "jsonrpc says -32000"}
	if got := classifyA2AError(err); got != "5xx_error" {
		t.Errorf("got %q, want 5xx_error — typed HTTP should override rpc text", got)
	}
}

func TestClassifyA2AError_ExportedWrapper(t *testing.T) {
	err := &fakeHTTPErr{code: 401, msg: "unauthorized"}
	if got := ClassifyA2AError(err); got != "auth_error" {
		t.Errorf("exported wrapper got %q, want auth_error", got)
	}
}
