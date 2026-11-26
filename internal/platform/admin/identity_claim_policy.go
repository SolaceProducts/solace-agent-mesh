// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"unicode"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

const (
	ClaimKeyPhoneNumber = "phone_number"
	ClaimKeyEmail       = "email"
)

type claimKeyPolicy struct {
	canonical func(string) string

	verificationClaim string

	alternates func(string) []string

	plausible func(string) bool

	absentIsVerified bool
}

var builtinClaimKeyPolicies = map[string]claimKeyPolicy{
	ClaimKeyPhoneNumber: {
		canonical:         canonicalPhoneValue,
		alternates:        alternatePhoneForms,
		plausible:         plausiblePhoneValue,
		verificationClaim: "phone_number_verified",
		absentIsVerified:  true,
	},
	ClaimKeyEmail: {
		canonical:         canonicalEmailValue,
		verificationClaim: "email_verified",
		absentIsVerified:  true,
	},
}

func policyForClaimKey(key string) (claimKeyPolicy, bool) {
	if key == "" {
		return claimKeyPolicy{}, false
	}
	if p, ok := builtinClaimKeyPolicies[canonicalTextValue(key)]; ok {
		return p, true
	}
	verificationClaim := key + "_verified"
	if strings.HasSuffix(canonicalTextValue(key), "_verified") {
		verificationClaim = ""
	}
	return claimKeyPolicy{
		canonical:         canonicalTextValue,
		verificationClaim: verificationClaim,
		absentIsVerified:  true,
	}, true
}

func canonicalPhoneValue(raw string) string {
	value := strings.TrimSpace(raw)
	value = cutRFC3966ParameterTail(value)
	var b strings.Builder
	b.Grow(len(value))
	for _, r := range value {
		if d, ok := decimalDigit(r); ok {
			b.WriteByte(byte('0' + d))
			continue
		}
		if isPhoneSeparator(r) {
			continue
		}
		break
	}
	return strings.TrimPrefix(b.String(), phoneIDDPrefix)
}

func cutRFC3966ParameterTail(value string) string {
	if i := strings.IndexByte(value, ';'); i >= 0 {
		return value[:i]
	}
	return value
}

const phoneIDDPrefix = "00"

func isPhoneSeparator(r rune) bool {
	switch r {
	case ' ', '\t', '-', '.', '(', ')', '/', '+', '\u00a0', '\u202f', '\u2011', '\u2013':
		return true
	default:
		return false
	}
}

func decimalDigit(r rune) (int, bool) {
	if r >= '0' && r <= '9' {
		return int(r - '0'), true
	}
	if !unicode.Is(unicode.Nd, r) {
		return 0, false
	}
	for _, rr := range unicode.Nd.R16 {
		if rr.Stride == 1 && rune(rr.Lo) <= r && r <= rune(rr.Hi) {
			return int(r-rune(rr.Lo)) % 10, true
		}
	}
	for _, rr := range unicode.Nd.R32 {
		if rr.Stride == 1 && rune(rr.Lo) <= r && r <= rune(rr.Hi) {
			return int(r-rune(rr.Lo)) % 10, true
		}
	}
	return 0, false
}

const (
	minPhoneDigits = 7
	maxPhoneDigits = 15
)

func plausiblePhoneValue(canonical string) bool {
	return len(canonical) >= minPhoneDigits && len(canonical) <= maxPhoneDigits
}

func alternatePhoneForms(canonical string) []string {
	const (
		mxCountryCode = "52"
		brCountryCode = "55"
	)
	switch {
	case strings.HasPrefix(canonical, mxCountryCode+"1") && len(canonical) == 13:
		return []string{mxCountryCode + canonical[3:]}
	case strings.HasPrefix(canonical, mxCountryCode) && len(canonical) == 12:
		return []string{mxCountryCode + "1" + canonical[2:]}
	case strings.HasPrefix(canonical, brCountryCode) && len(canonical) == 13 && canonical[4] == '9' && brazilianMobileLead(canonical[5]):
		return []string{canonical[:4] + canonical[5:]}
	case strings.HasPrefix(canonical, brCountryCode) && len(canonical) == 12 && brazilianMobileLead(canonical[4]):
		return []string{canonical[:4] + "9" + canonical[4:]}
	default:
		return nil
	}
}

func brazilianMobileLead(d byte) bool {
	return d >= '6' && d <= '9'
}

func canonicalEmailValue(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func canonicalTextValue(raw string) string {
	return identity.CanonicalText(raw)
}
