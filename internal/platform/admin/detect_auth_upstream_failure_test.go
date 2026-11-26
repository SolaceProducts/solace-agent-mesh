// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDetectAuth_UpstreamFailuresAreBadGatewayNotInternalError(t *testing.T) {
	t.Setenv(allowPrivateMCPEnvVar, "true")
	statusServer := func(code int) *internalTarget {
		return newInternalTarget(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
	}
	closed := newInternalTarget(t, nil)
	closedURL := closed.srv.URL
	closed.srv.Close()

	const reachable = " Verify the URL and that it is reachable from the platform."
	tests := []struct {
		name    string
		url     string
		wantMsg string
	}{
		{"not found", statusServer(http.StatusNotFound).srv.URL + "/spec.json", "Could not fetch the OpenAPI specification from the URL (HTTP 404)." + reachable},
		{"server error", statusServer(http.StatusInternalServerError).srv.URL + "/spec.json", "Could not fetch the OpenAPI specification from the URL (HTTP 500)." + reachable},
		{"connection refused", closedURL + "/spec.json?token=abc123secret", "Could not fetch the OpenAPI specification from the URL." + reachable},
	}
	endpoints := newSSRFEndpoints(t, nil, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := endpoints.postDetectAuth(t, tt.url)
			if res.status != http.StatusBadGateway || res.message(t) != tt.wantMsg {
				t.Fatalf("got %d %s, want 502 %q", res.status, res.body, tt.wantMsg)
			}
			if strings.Contains(string(res.body), "abc123secret") {
				t.Errorf("response echoes the query: %s", res.body)
			}
		})
	}

	t.Run("reachable but not a spec stays a validation error", func(t *testing.T) {
		notSpec := newInternalTarget(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"hello":"world"}`) })
		res := endpoints.postDetectAuth(t, notSpec.srv.URL+"/spec.json")
		if res.status != http.StatusUnprocessableEntity {
			t.Fatalf("got %d %s, want 422", res.status, res.body)
		}
	})
}
