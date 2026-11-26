// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"slices"
	"strings"
)

type AllowlistValidator struct {
	domains map[string]struct{}
}

func NewAllowlistValidator(domains []string) *AllowlistValidator {
	m := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		m[strings.ToLower(d)] = struct{}{}
	}
	return &AllowlistValidator{domains: m}
}

func (v *AllowlistValidator) ValidateRecipients(recipients []string) error {
	for _, addr := range recipients {
		domain := extractDomainFromEmail(addr)
		if _, allowed := v.domains[strings.ToLower(domain)]; !allowed {
			return &AllowlistError{
				Recipient:      addr,
				Domain:         domain,
				AllowedDomains: v.AllowedDomains(),
			}
		}
	}
	return nil
}

func (v *AllowlistValidator) AllowedDomains() []string {
	result := make([]string, 0, len(v.domains))
	for d := range v.domains {
		result = append(result, d)
	}
	slices.Sort(result)
	return result
}

func extractDomainFromEmail(email string) string {
	if idx := strings.LastIndex(email, "@"); idx >= 0 && idx < len(email)-1 {
		return email[idx+1:]
	}
	return ""
}
