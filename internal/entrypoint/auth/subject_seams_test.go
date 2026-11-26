// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

func TestFillScopes_FillsAnEmptySet(t *testing.T) {
	s := &Subject{userID: "alice", fullIdentity: true}
	s.FillScopes([]string{"agent:a:invoke"})
	if !slices.Equal(s.scopes, []string{"agent:a:invoke"}) {
		t.Errorf("Scopes = %v, want the supplied set — a caller built without scopes must be fillable", s.scopes)
	}
}

func TestFillScopes_WillNotReplaceAPopulatedSet(t *testing.T) {
	s := &Subject{userID: "alice", fullIdentity: true, scopes: []string{"agent:narrow:invoke"}}
	s.FillScopes([]string{"*"})
	if !slices.Equal(s.scopes, []string{"agent:narrow:invoke"}) {
		t.Errorf("Scopes = %v, want the original — FillScopes must never widen an entitlement", s.scopes)
	}
}

func TestFillScopes_WillNotFillAResolvedEmptySet(t *testing.T) {
	s := &Subject{userID: "whatsapp:15550005555", fullIdentity: true, scopesResolved: true}
	s.FillScopes([]string{"*"})
	if len(s.scopes) != 0 {
		t.Errorf("Scopes = %v, want none — a resolved empty set must stay empty", s.scopes)
	}
}

func TestFillScopes_WillNotArmACallerWithoutAFullIdentity(t *testing.T) {
	for name, s := range map[string]*Subject{
		"zero Subject": {},
		"user id only": ForUserIDOnly("alice"),
	} {
		s.FillScopes([]string{"*"})
		if len(s.scopes) != 0 {
			t.Errorf("%s: Scopes = %v, want none", name, s.scopes)
		}
	}
}

func TestFillScopes_EmptyFillLeavesScopesOpen(t *testing.T) {
	s := &Subject{userID: "alice", fullIdentity: true}
	s.FillScopes([]string{})
	s.FillScopes([]string{"agent:a:invoke"})
	if !slices.Equal(s.scopes, []string{"agent:a:invoke"}) {
		t.Errorf("Scopes = %v, want the second fill: an empty fill must not close the scopes", s.scopes)
	}
}

func TestFillScopes_ClonesSoTheCallerCannotMutateLater(t *testing.T) {
	src := []string{"agent:a:invoke"}
	s := &Subject{userID: "alice", fullIdentity: true}
	s.FillScopes(src)
	src[0] = "*"
	if s.scopes[0] != "agent:a:invoke" {
		t.Errorf("Scopes[0] = %q; the caller's slice is aliased, so it can widen the entitlement after the fact", s.scopes[0])
	}
}

func TestApplyEnrichment_AppliesToAnUnenrichedCaller(t *testing.T) {
	s := &Subject{userID: "alice"}
	s.ApplyEnrichment(&identity.Profile{ID: "alice"}, identity.EnrichmentOK, "")
	if s.profile == nil || s.enrichmentStatus != identity.EnrichmentOK {
		t.Errorf("Profile=%v Status=%q, want the enrichment applied", s.profile, s.enrichmentStatus)
	}
}

func TestApplyEnrichment_IsANoOpOnceAttempted(t *testing.T) {
	withProfile := &Subject{userID: "alice", profile: &identity.Profile{ID: "first"}}
	withProfile.ApplyEnrichment(&identity.Profile{ID: "second"}, identity.EnrichmentOK, "")
	if withProfile.profile.ID != "first" {
		t.Errorf("Profile.ID = %q, want %q — an existing profile must not be replaced", withProfile.profile.ID, "first")
	}

	degraded := &Subject{userID: "alice", enrichmentStatus: identity.EnrichmentUnavailable, enrichmentReason: "lookup failed"}
	degraded.ApplyEnrichment(&identity.Profile{ID: "late"}, identity.EnrichmentOK, "")
	if degraded.profile != nil || degraded.enrichmentStatus != identity.EnrichmentUnavailable {
		t.Errorf("Profile=%v Status=%q, want the recorded degradation kept", degraded.profile, degraded.enrichmentStatus)
	}
}

func TestSeams_AreNilSafe(t *testing.T) {
	var s *Subject
	s.FillScopes([]string{"*"})
	s.ApplyEnrichment(nil, identity.EnrichmentOK, "")
}

func TestFillPlatformUserID_FillsAVerifiedSender(t *testing.T) {
	s := &Subject{userID: "alice", fullIdentity: true, senderVerified: true}
	s.FillPlatformUserID("row-1")
	if s.platformUserID != "row-1" {
		t.Errorf("platformUserID = %q, want row-1", s.platformUserID)
	}
}

func TestFillPlatformUserID_WillNotReplaceACarriedRow(t *testing.T) {
	s := &Subject{userID: "alice", fullIdentity: true, senderVerified: true, platformUserID: "row-1"}
	s.FillPlatformUserID("other")
	if s.platformUserID != "row-1" {
		t.Errorf("platformUserID = %q, want row-1 kept", s.platformUserID)
	}
}

func TestFillPlatformUserID_LeavesAnUnverifiedSenderAlone(t *testing.T) {
	s := &Subject{userID: "alice", fullIdentity: true}
	s.FillPlatformUserID("row-1")
	if s.platformUserID != "" {
		t.Errorf("platformUserID = %q, want none for an unverified sender", s.platformUserID)
	}
}
