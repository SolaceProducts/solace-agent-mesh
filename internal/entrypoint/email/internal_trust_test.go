// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import "testing"

var exchangeOnlineAuthservs = []string{"protection.outlook.com"}

var gmailAuthservs = []string{"mx.google.com"}

func TestEvaluateInternalTrust_Disabled(t *testing.T) {
	t.Parallel()

	headers := map[string][]string{
		"X-MS-Exchange-Organization-AuthAs": {"Internal"},
		"X-MS-Exchange-CrossTenant-Id":      {"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		"X-MS-Exchange-Organization-AuthSource": {
			"MAILBOX01.prod.exchangelabs.com",
		},
	}
	got := EvaluateInternalTrust(headers, "", exchangeOnlineAuthservs)
	if got.Trusted {
		t.Errorf("Trusted = true with empty trustedTenantID; want false (feature disabled)")
	}
}

func TestEvaluateInternalTrust_RejectsOnNonExchangeMTA(t *testing.T) {
	t.Parallel()

	const tenant = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	headers := map[string][]string{
		"X-MS-Exchange-Organization-AuthAs":     {"Internal"},
		"X-MS-Exchange-CrossTenant-Id":          {tenant},
		"X-MS-Exchange-Organization-AuthSource": {"MAILBOX01.prod.exchangelabs.com"},
	}

	for _, authservs := range [][]string{
		nil,
		{},
		gmailAuthservs,
		{"mx.example.com"},
		{"protection.example.com"},
	} {
		t.Run("authservs="+joinAuthservs(authservs), func(t *testing.T) {
			got := EvaluateInternalTrust(headers, tenant, authservs)
			if got.Trusted {
				t.Errorf("Trusted = true with non-Exchange authservs %v; want false (forged X-MS-Exchange-* must not be honoured)", authservs)
			}
		})
	}
}

func TestEvaluateInternalTrust_AcceptsExchangeSubdomains(t *testing.T) {
	t.Parallel()

	const tenant = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	headers := map[string][]string{
		"X-MS-Exchange-Organization-AuthAs":     {"Internal"},
		"X-MS-Exchange-CrossTenant-Id":          {tenant},
		"X-MS-Exchange-Organization-AuthSource": {"MAILBOX01.prod.exchangelabs.com"},
	}
	for _, authserv := range []string{
		"protection.outlook.com",
		"eur05.protection.outlook.com",
		"NAM10.protection.outlook.com",
	} {
		t.Run(authserv, func(t *testing.T) {
			got := EvaluateInternalTrust(headers, tenant, []string{authserv})
			if !got.Trusted {
				t.Errorf("Trusted = false with %q; want true (Exchange Online suffix)", authserv)
			}
		})
	}
}

func TestEvaluateInternalTrust_HappyPath(t *testing.T) {
	t.Parallel()

	const tenant = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	headers := map[string][]string{
		"X-MS-Exchange-Organization-AuthAs":     {"Internal"},
		"X-MS-Exchange-CrossTenant-Id":          {tenant},
		"X-MS-Exchange-Organization-AuthSource": {"MAILBOX01.prod.exchangelabs.com"},
	}
	got := EvaluateInternalTrust(headers, tenant, exchangeOnlineAuthservs)
	if !got.Trusted {
		t.Errorf("Trusted = false; want true. Reason=%q", got.Reason)
	}
	if got.TenantID != tenant {
		t.Errorf("TenantID = %q, want %q", got.TenantID, tenant)
	}
	if got.Reason == "" {
		t.Error("Reason is empty; should record why we trusted the message")
	}
}

func TestEvaluateInternalTrust_TenantMismatch(t *testing.T) {
	t.Parallel()

	headers := map[string][]string{
		"X-MS-Exchange-Organization-AuthAs":     {"Internal"},
		"X-MS-Exchange-CrossTenant-Id":          {"99999999-9999-9999-9999-999999999999"},
		"X-MS-Exchange-Organization-AuthSource": {"OTHERTENANT.prod.exchangelabs.com"},
	}
	got := EvaluateInternalTrust(headers, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", exchangeOnlineAuthservs)
	if got.Trusted {
		t.Errorf("Trusted = true with mismatched tenant; want false")
	}
}

func TestEvaluateInternalTrust_IntraTenantNoCrossTenantHeader(t *testing.T) {
	t.Parallel()

	const tenant = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	headers := map[string][]string{
		"X-MS-Exchange-Organization-AuthAs":     {"Internal"},
		"X-MS-Exchange-Organization-AuthSource": {"MAILBOX01.prod.exchangelabs.com"},
	}
	got := EvaluateInternalTrust(headers, tenant, exchangeOnlineAuthservs)
	if !got.Trusted {
		t.Fatalf("intra-tenant mail without CrossTenant-Id should be trusted; got %+v", got)
	}
	if got.TenantID != tenant {
		t.Errorf("TenantID = %q, want configured tenant %q", got.TenantID, tenant)
	}
	if got.Reason != "exchange_internal_authas+intra_tenant" {
		t.Errorf("Reason = %q, want exchange_internal_authas+intra_tenant", got.Reason)
	}
}

func TestEvaluateInternalTrust_AuthAsExternal(t *testing.T) {
	t.Parallel()

	const tenant = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	cases := []string{"External", "Anonymous", "Partner", ""}
	for _, authAs := range cases {
		t.Run("authAs="+authAs, func(t *testing.T) {
			headers := map[string][]string{
				"X-MS-Exchange-CrossTenant-Id":          {tenant},
				"X-MS-Exchange-Organization-AuthSource": {"MAILBOX01.prod.exchangelabs.com"},
			}
			if authAs != "" {
				headers["X-MS-Exchange-Organization-AuthAs"] = []string{authAs}
			}
			got := EvaluateInternalTrust(headers, tenant, exchangeOnlineAuthservs)
			if got.Trusted {
				t.Errorf("Trusted = true with AuthAs=%q; want false", authAs)
			}
		})
	}
}

func TestEvaluateInternalTrust_MissingAuthSource(t *testing.T) {
	t.Parallel()

	const tenant = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	headers := map[string][]string{
		"X-MS-Exchange-Organization-AuthAs": {"Internal"},
		"X-MS-Exchange-CrossTenant-Id":      {tenant},
	}
	got := EvaluateInternalTrust(headers, tenant, exchangeOnlineAuthservs)
	if got.Trusted {
		t.Error("Trusted = true with missing AuthSource; want false")
	}
}

func TestEvaluateInternalTrust_CaseInsensitiveAuthAs(t *testing.T) {
	t.Parallel()

	const tenant = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for _, authAs := range []string{"internal", "INTERNAL", "Internal"} {
		t.Run(authAs, func(t *testing.T) {
			headers := map[string][]string{
				"X-MS-Exchange-Organization-AuthAs":     {authAs},
				"X-MS-Exchange-CrossTenant-Id":          {tenant},
				"X-MS-Exchange-Organization-AuthSource": {"MAILBOX01.prod.exchangelabs.com"},
			}
			got := EvaluateInternalTrust(headers, tenant, exchangeOnlineAuthservs)
			if !got.Trusted {
				t.Errorf("Trusted = false for AuthAs=%q; want true (case-insensitive)", authAs)
			}
		})
	}
}

func TestEvaluateInternalTrust_TenantIDCaseInsensitive(t *testing.T) {
	t.Parallel()

	headers := map[string][]string{
		"X-MS-Exchange-Organization-AuthAs":     {"Internal"},
		"X-MS-Exchange-CrossTenant-Id":          {"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"},
		"X-MS-Exchange-Organization-AuthSource": {"MAILBOX01.prod.exchangelabs.com"},
	}
	got := EvaluateInternalTrust(headers, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", exchangeOnlineAuthservs)
	if !got.Trusted {
		t.Error("Trusted = false on case-mismatched but otherwise valid tenant; want true")
	}
}

func joinAuthservs(authservs []string) string {
	if len(authservs) == 0 {
		return "<empty>"
	}
	out := authservs[0]
	for _, a := range authservs[1:] {
		out += "," + a
	}
	return out
}
