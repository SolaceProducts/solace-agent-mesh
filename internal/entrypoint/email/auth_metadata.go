// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

const EmailAuthMetadataKey = "email_auth"

const EmailContentOrigin = "external_email"

func buildEmailAuthMetadata(verdict AuthVerdict, mode PolicyMode, trust InternalTrustResult) map[string]any {
	m := map[string]any{
		"content_origin":   EmailContentOrigin,
		"spf":              string(verdict.SPF),
		"dkim":             string(verdict.DKIM),
		"dmarc":            string(verdict.DMARC),
		"all_pass":         verdict.SPF == AuthPass && verdict.DKIM == AuthPass && verdict.DMARC == AuthPass,
		"policy_mode":      string(mode),
		"internal_trusted": trust.Trusted,
	}
	if verdict.AlignedFromDomain != "" {
		m["aligned_from_domain"] = verdict.AlignedFromDomain
	}
	if trust.Trusted {
		m["internal_trust_reason"] = trust.Reason
		m["internal_trust_tenant_id"] = trust.TenantID
	}
	return m
}
