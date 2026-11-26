// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import "strings"

type InternalTrustResult struct {
	Trusted  bool
	Reason   string
	TenantID string
}

var exchangeOnlineAuthservSuffixes = []string{
	"protection.outlook.com",
}

func receivingMTAIsExchangeOnline(trustedAuthservIDs []string) bool {
	for _, id := range trustedAuthservIDs {
		idLower := strings.ToLower(strings.TrimSpace(id))
		for _, suffix := range exchangeOnlineAuthservSuffixes {
			if idLower == suffix || strings.HasSuffix(idLower, "."+suffix) {
				return true
			}
		}
	}
	return false
}

func EvaluateInternalTrust(headers map[string][]string, trustedTenantID string, trustedAuthservIDs []string) InternalTrustResult {
	if trustedTenantID == "" {
		return InternalTrustResult{}
	}
	if !receivingMTAIsExchangeOnline(trustedAuthservIDs) {
		return InternalTrustResult{}
	}

	authAs := strings.ToLower(strings.TrimSpace(firstHeader(headers, "X-MS-Exchange-Organization-AuthAs")))
	if authAs != "internal" {
		return InternalTrustResult{}
	}

	authSource := strings.TrimSpace(firstHeader(headers, "X-MS-Exchange-Organization-AuthSource"))
	if authSource == "" {
		return InternalTrustResult{}
	}

	tenantID := strings.TrimSpace(firstHeader(headers, "X-MS-Exchange-CrossTenant-Id"))
	switch {
	case tenantID == "":
		return InternalTrustResult{
			Trusted:  true,
			Reason:   "exchange_internal_authas+intra_tenant",
			TenantID: trustedTenantID,
		}
	case strings.EqualFold(tenantID, trustedTenantID):
		return InternalTrustResult{
			Trusted:  true,
			Reason:   "exchange_internal_authas+tenant_match",
			TenantID: tenantID,
		}
	default:
		return InternalTrustResult{}
	}
}

func firstHeader(headers map[string][]string, name string) string {
	if v, ok := headers[name]; ok && len(v) > 0 {
		return v[0]
	}
	for k, v := range headers {
		if strings.EqualFold(k, name) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}
