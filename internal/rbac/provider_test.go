// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import "testing"

func TestIsSentinelProvider(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"", false},
		{ProviderSentinelEnterprise, true},
		{ProviderSentinelGeneric, true},
		{"oidc", false},
		{"azure", false},
		{"Enterprise", false},
		{"enterprise-idp", false},
	} {
		if got := IsSentinelProvider(tc.name); got != tc.want {
			t.Errorf("IsSentinelProvider(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProviderUnsetOrSentinel(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"", true},
		{ProviderSentinelEnterprise, true},
		{ProviderSentinelGeneric, true},
		{"azure", false},
		{"Enterprise", false},
	} {
		if got := ProviderUnsetOrSentinel(tc.name); got != tc.want {
			t.Errorf("ProviderUnsetOrSentinel(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProviderCannotMatch(t *testing.T) {
	for _, tc := range []struct {
		why        string
		provider   string
		configured string
		want       bool
	}{
		{"nothing configured to compare against", ProviderSentinelEnterprise, "", false},
		{"neither side known", "", "", false},
		{"a real provider with nothing configured", "azure", "", false},
		{"unset against a real configured provider", "", "azure", true},
		{"enterprise against a real configured provider", ProviderSentinelEnterprise, "azure", true},
		{"generic against a real configured provider", ProviderSentinelGeneric, "azure", true},
		{"a different real provider is a legitimate per-mapping override", "okta", "azure", false},
		{"the deployment is genuinely named enterprise", ProviderSentinelEnterprise, ProviderSentinelEnterprise, false},
		{"cross-sentinel: generic can never match an enterprise deployment", ProviderSentinelGeneric, ProviderSentinelEnterprise, true},
		{"case-sensitive: Enterprise is not the sentinel", "Enterprise", "azure", false},
	} {
		if got := ProviderCannotMatch(tc.provider, tc.configured); got != tc.want {
			t.Errorf("ProviderCannotMatch(%q, %q) = %v, want %v — %s", tc.provider, tc.configured, got, tc.want, tc.why)
		}
	}
}
