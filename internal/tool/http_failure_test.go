// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"net/http"
	"strconv"
	"testing"
)

func TestHTTPFailureCode(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusOK, ""},
		{http.StatusFound, ""},
		{0, ""},
		{http.StatusBadRequest, "DISPATCH_BACKEND_4XX"},
		{http.StatusUnauthorized, "DISPATCH_AUTH_REQUIRED"},
		{http.StatusForbidden, "PERMISSION_DENIED"},
		{http.StatusNotFound, "DISPATCH_BACKEND_4XX"},
		{http.StatusRequestTimeout, "DISPATCH_TIMEOUT"},
		{http.StatusTooManyRequests, "RATE_LIMITED"},
		{499, "DISPATCH_BACKEND_4XX"},
		{http.StatusInternalServerError, "DISPATCH_BACKEND_5XX"},
		{http.StatusServiceUnavailable, "DISPATCH_BACKEND_5XX"},
		{http.StatusGatewayTimeout, "DISPATCH_TIMEOUT"},
		{599, "DISPATCH_BACKEND_5XX"},
		{600, ""},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.status), func(t *testing.T) {
			if got := HTTPFailureCode(tt.status); got != tt.want {
				t.Errorf("HTTPFailureCode(%d) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestHTTPFailureCodeUnauthorizedIsNotTheOAuthCode(t *testing.T) {
	if got := HTTPFailureCode(http.StatusUnauthorized); got == ErrorCodeAuthRequired {
		t.Fatalf("HTTPFailureCode(401) = %q, which triggers the OAuth flow", got)
	}
}
