// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"testing"
)

func assertMetadataField(t *testing.T, m map[string]any, key string, want any) {
	t.Helper()
	got, ok := m[key]
	if !ok {
		t.Errorf("metadata missing key %q", key)
		return
	}
	if got != want {
		t.Errorf("metadata[%q] = %v (%T); want %v (%T)", key, got, got, want, want)
	}
}

func assertMetadataAbsent(t *testing.T, m map[string]any, key string) {
	t.Helper()
	if v, ok := m[key]; ok {
		t.Errorf("metadata[%q] = %v; want absent", key, v)
	}
}

func TestBuildEmailAuthMetadata_AllPass(t *testing.T) {
	t.Parallel()
	got := buildEmailAuthMetadata(
		AuthVerdict{
			SPF:               AuthPass,
			DKIM:              AuthPass,
			DMARC:             AuthPass,
			AlignedFromDomain: "vendor.com",
		},
		ModeLogOnly,
		InternalTrustResult{},
	)
	assertMetadataField(t, got, "spf", "pass")
	assertMetadataField(t, got, "dkim", "pass")
	assertMetadataField(t, got, "dmarc", "pass")
	assertMetadataField(t, got, "all_pass", true)
	assertMetadataField(t, got, "aligned_from_domain", "vendor.com")
	assertMetadataField(t, got, "policy_mode", "log_only")
	assertMetadataField(t, got, "internal_trusted", false)
	assertMetadataAbsent(t, got, "internal_trust_reason")
	assertMetadataAbsent(t, got, "internal_trust_tenant_id")
}

func TestBuildEmailAuthMetadata_DMARCFail(t *testing.T) {
	t.Parallel()
	got := buildEmailAuthMetadata(
		AuthVerdict{
			SPF:   AuthPass,
			DKIM:  AuthPass,
			DMARC: AuthFail,
		},
		ModeLogOnly,
		InternalTrustResult{},
	)
	if got["all_pass"] != false {
		t.Errorf("all_pass = %v, want false", got["all_pass"])
	}
	if got["dmarc"] != "fail" {
		t.Errorf("dmarc = %v, want fail", got["dmarc"])
	}
	if got["spf"] != "pass" {
		t.Errorf("spf = %v, want pass", got["spf"])
	}
	if _, present := got["aligned_from_domain"]; present {
		t.Errorf("aligned_from_domain should be omitted when empty; got %v", got["aligned_from_domain"])
	}
}

func TestBuildEmailAuthMetadata_NoAuthHeader(t *testing.T) {
	t.Parallel()
	got := buildEmailAuthMetadata(
		AuthVerdict{
			SPF:   AuthNone,
			DKIM:  AuthNone,
			DMARC: AuthNone,
		},
		ModeLogOnly,
		InternalTrustResult{},
	)
	if got["all_pass"] != false {
		t.Errorf("all_pass = %v, want false (none verdicts must not be reported as pass)", got["all_pass"])
	}
	for _, k := range []string{"spf", "dkim", "dmarc"} {
		if got[k] != "none" {
			t.Errorf("%s = %v, want none", k, got[k])
		}
	}
}

func TestBuildEmailAuthMetadata_InternalTrustOmittedWhenAbsent(t *testing.T) {
	t.Parallel()
	got := buildEmailAuthMetadata(
		AuthVerdict{DMARC: AuthPass},
		ModeRequireDMARC,
		InternalTrustResult{},
	)
	if got["internal_trusted"] != false {
		t.Errorf("internal_trusted = %v, want false", got["internal_trusted"])
	}
	for _, k := range []string{"internal_trust_reason", "internal_trust_tenant_id"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s should be omitted when not trusted; got %v", k, got[k])
		}
	}
}

func TestBuildEmailAuthMetadata_InternalTrustPropagated(t *testing.T) {
	t.Parallel()
	got := buildEmailAuthMetadata(
		AuthVerdict{DMARC: AuthNone},
		ModeStrict,
		InternalTrustResult{
			Trusted:  true,
			Reason:   "exchange_internal_authas+tenant_match",
			TenantID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
	)
	if got["internal_trusted"] != true {
		t.Errorf("internal_trusted = %v, want true", got["internal_trusted"])
	}
	if got["internal_trust_reason"] != "exchange_internal_authas+tenant_match" {
		t.Errorf("internal_trust_reason = %v", got["internal_trust_reason"])
	}
	if got["internal_trust_tenant_id"] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("internal_trust_tenant_id = %v", got["internal_trust_tenant_id"])
	}
}
