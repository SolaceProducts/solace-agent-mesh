// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"errors"
	"syscall"
	"testing"
)

func TestNewOAuthRemoteMonitor_Labels(t *testing.T) {
	cases := []struct {
		op   string
		want string
	}{
		{OAuthOpValidateToken, "validate_token"},
		{OAuthOpGetUserInfo, "get_user_info"},
		{OAuthOpDiscovery, "discovery"},
		{OAuthOpGetJWKS, "get_jwks"},
	}

	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			metrics := installMetrics(t, &collectingMetrics{})
			m := NewOAuthRemoteMonitor(tc.op)
			m.Stop(nil)

			if len(metrics.histograms) != 1 {
				t.Fatalf("expected 1 histogram, got %d", len(metrics.histograms))
			}
			h := metrics.histograms[0]
			if h.name != "outbound.request.duration" {
				t.Errorf("metric = %q, want outbound.request.duration", h.name)
			}
			assertLabel(t, h.labels, "service.peer.name", "oauth_service")
			assertLabel(t, h.labels, "operation.name", tc.want)
			assertLabel(t, h.labels, "error.type", "")
		})
	}
}

func TestNewOAuthRemoteMonitor_RemoteClassifier(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr string
	}{
		{"connection refused", syscall.ECONNREFUSED, "connection_error"},
		{"unauthorized text", errors.New("unauthorized"), "auth_error"},
		{"timeout text", errors.New("context deadline exceeded: timeout"), "timeout"},
		{"generic", errors.New("some other failure"), "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := installMetrics(t, &collectingMetrics{})
			m := NewOAuthRemoteMonitor(OAuthOpGetUserInfo)
			err := tt.err
			m.Stop(&err)

			if len(metrics.histograms) != 1 {
				t.Fatalf("expected 1 histogram")
			}
			assertLabel(t, metrics.histograms[0].labels, "error.type", tt.wantErr)
		})
	}
}
