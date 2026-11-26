// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package rbac

const ScopeProductAnalyticsConsentUpdate = "product_analytics_consent:*:update"

func init() {
	scopeMeta[ScopeProductAnalyticsConsentUpdate] = scopeMetaEntry{
		name:        "Change telemetry consent",
		description: "Grant or withdraw product-analytics collection for the whole install. Reading the current decision needs no scope.",
	}
}
