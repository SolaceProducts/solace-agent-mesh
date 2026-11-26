// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package httpsse

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestProductAnalyticsRoutesAuthPosture(t *testing.T) {
	adapter := newCoverageAdapter(t)
	type posture struct {
		publicReason string
		scope        string
	}
	want := map[string]posture{
		"POST /api/v1/productAnalytics/ingest":     {publicReason: publicReasonTelemetryIngest},
		"POST /api/v1/productAnalytics/ingest/cli": {publicReason: publicReasonTelemetryIngest},
		"GET " + pathProductAnalyticsConsent:       {publicReason: publicReasonTelemetryConsentRead},
		"PUT " + pathProductAnalyticsConsent:       {scope: rbac.ScopeProductAnalyticsConsentUpdate},
	}

	seen := make(map[string]bool)
	for _, r := range adapter.Routes() {
		if !strings.HasPrefix(r.Path, "/api/v1/productAnalytics/") {
			continue
		}
		key := r.Method + " " + r.Path
		expected, ok := want[key]
		if !ok {
			t.Errorf("unexpected product-analytics route %s — add it to this test with its intended auth posture", key)
			continue
		}
		seen[key] = true
		if r.IsPublic != expected.publicReason {
			t.Errorf("route %s IsPublic = %q, want %q", key, r.IsPublic, expected.publicReason)
		}
		if r.Scope != expected.scope {
			t.Errorf("route %s Scope = %q, want %q", key, r.Scope, expected.scope)
		}
		if r.IsPublic == publicReasonAuthSkip {
			t.Errorf("route %s is auth-skipped — telemetry consent must stay behind authentication", key)
		}
	}

	for key := range want {
		if !seen[key] {
			t.Errorf("route %s not registered — the coverage adapter did not wire the telemetry surface", key)
		}
	}
}

func TestProductAnalyticsConsentWriteIsScopeGated(t *testing.T) {
	adapter := newCoverageAdapter(t)
	for _, r := range adapter.Routes() {
		if r.Method != "PUT" || r.Path != pathProductAnalyticsConsent {
			continue
		}
		if r.Scope != rbac.ScopeProductAnalyticsConsentUpdate {
			t.Fatalf("consent write Scope = %q, want %q", r.Scope, rbac.ScopeProductAnalyticsConsentUpdate)
		}
		return
	}
	t.Fatal("consent write route not registered")
}
