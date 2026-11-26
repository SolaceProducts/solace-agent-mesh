// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func projectIdentityClaims(rawSource string) ([]store.UserIdentityClaim, []unverifiedClaimNote) {
	claims := decodeClaimSource(rawSource)
	if len(claims) == 0 {
		return nil, nil
	}
	var out []store.UserIdentityClaim
	var notes []unverifiedClaimNote
	for _, key := range slices.Sorted(maps.Keys(claims)) {
		if perTokenClaim(key) || timestampClaim(key) || verificationCompanionClaim(key) {
			continue
		}
		policy, ok := policyForClaimKey(key)
		if !ok {
			continue
		}
		verdict := claimVerified(claims, policy)
		projected, unreadable, implausible := 0, 0, 0
		for _, raw := range claimValues(claims, key) {
			canonical := policy.canonical(raw)
			if canonical == "" || len(canonical) > maxClaimValueBytes {
				unreadable++
				continue
			}
			if policy.plausible != nil && !policy.plausible(canonical) {
				implausible++
			}
			projected++
			out = append(out, store.UserIdentityClaim{
				ClaimKey:            key,
				ClaimValueCanonical: canonical,
				Verified:            verdict.verified(),
			})
			if policy.alternates != nil {
				for _, alt := range policy.alternates(canonical) {
					out = append(out, store.UserIdentityClaim{
						ClaimKey:            key,
						ClaimValueCanonical: alt,
						Verified:            verdict.verified(),
					})
				}
			}
		}
		unreadable += unreadableValues(claims, key)
		if unreadable > 0 {
			notes = append(notes, unverifiedClaimNote{
				ClaimKey: key,
				Reason:   reasonValueUnreadable,
				Values:   unreadable,
			})
		}
		if implausible > 0 {
			notes = append(notes, unverifiedClaimNote{
				ClaimKey: key,
				Reason:   reasonValueImplausible,
				Values:   implausible,
			})
		}
		if projected > 0 && !verdict.verified() {
			notes = append(notes, unverifiedClaimNote{
				ClaimKey:          key,
				VerificationClaim: policy.verificationClaim,
				Reason:            verdict.reason(),
				Values:            projected,
			})
		}
	}
	return out, notes
}

const maxClaimValueBytes = 512

const reasonValueUnreadable = "claim_value_unreadable"

const reasonValueImplausible = "claim_value_implausible"

func perTokenClaim(key string) bool {
	switch canonicalTextValue(key) {
	case "nonce", "at_hash", "c_hash", "sid", "session_state", "jti", "uti", "aio", "rh":
		return true
	default:
		return false
	}
}

func timestampClaim(key string) bool {
	switch canonicalTextValue(key) {
	case "exp", "nbf", "iat", "auth_time", "updated_at", "xms_tcdt":
		return true
	default:
		return false
	}
}

func verificationCompanionClaim(key string) bool {
	return strings.HasSuffix(canonicalTextValue(key), "_verified")
}

func unreadableValues(claims map[string]any, key string) int {
	switch v := claims[key].(type) {
	case nil, string, []string:
		return 0
	case []any:
		n := 0
		for _, item := range v {
			if s, ok := item.(string); !ok || s == "" {
				n++
			}
		}
		return n
	default:
		return 1
	}
}

type unverifiedClaimNote struct {
	ClaimKey          string
	VerificationClaim string
	Reason            string
	Values            int
}

func decodeClaimSource(rawSource string) map[string]any {
	if strings.TrimSpace(rawSource) == "" {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal([]byte(rawSource), &claims); err != nil {
		return nil
	}
	return claims
}

func claimValues(claims map[string]any, key string) []string {
	switch v := claims[key].(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

type verificationVerdict int

const (
	verificationVerified verificationVerdict = iota
	verificationDenied
	verificationUnreadable
)

func (v verificationVerdict) verified() bool { return v == verificationVerified }

func (v verificationVerdict) reason() string {
	switch v {
	case verificationDenied:
		return "provider_says_unverified"
	case verificationUnreadable:
		return "verification_claim_unreadable"
	default:
		return ""
	}
}

func claimVerified(claims map[string]any, policy claimKeyPolicy) verificationVerdict {
	if policy.verificationClaim == "" {
		return verificationVerified
	}
	present, verdict := companionVerdict(claims, policy.verificationClaim)
	if !present {
		if policy.absentIsVerified {
			return verificationVerified
		}
		return verificationDenied
	}
	return verdict
}

func companionVerdict(claims map[string]any, name string) (present bool, verdict verificationVerdict) {
	for _, key := range slices.Sorted(maps.Keys(claims)) {
		if claims[key] == nil || !identity.SameClaimKey(key, name) {
			continue
		}
		v := verdictOf(claims[key])
		if !present || worse(v, verdict) {
			present, verdict = true, v
		}
	}
	return present, verdict
}

func worse(a, b verificationVerdict) bool {
	rank := map[verificationVerdict]int{verificationVerified: 0, verificationUnreadable: 1, verificationDenied: 2}
	return rank[a] > rank[b]
}

func verdictOf(raw any) verificationVerdict {
	switch v := raw.(type) {
	case bool:
		if v {
			return verificationVerified
		}
		return verificationDenied
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			return verificationVerified
		case "false", "0", "no", "off":
			return verificationDenied
		default:
			return verificationUnreadable
		}
	default:
		return verificationUnreadable
	}
}

func projectionFingerprint(rawSource string) []string {
	rows, _ := projectIdentityClaims(rawSource)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		verified := "0"
		if r.Verified {
			verified = "1"
		}
		out = append(out, r.ClaimKey+"\x00"+r.ClaimValueCanonical+"\x00"+verified)
	}
	slices.Sort(out)
	return out
}

func projectionChanged(beforeSource, afterSource string) bool {
	return !slices.Equal(projectionFingerprint(beforeSource), projectionFingerprint(afterSource))
}
