// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

func (s *Subject) FillScopes(scopes []string) {
	if s == nil || !s.fullIdentity || len(s.scopes) > 0 || s.scopesResolved {
		return
	}
	s.scopes = slices.Clone(scopes)
}

func (s *Subject) FillPlatformUserID(platformUserID string) {
	if s == nil || s.platformUserID != "" || !s.senderVerified {
		return
	}
	s.platformUserID = platformUserID
}

func (s *Subject) WithPlatformUserID(platformUserID string) *Subject {
	c := *s
	c.platformUserID = platformUserID
	return &c
}

func (s *Subject) ApplyEnrichment(profile *identity.Profile, status identity.EnrichmentStatus, reason string) {
	if s == nil || s.profile != nil || s.enrichmentStatus != "" {
		return
	}
	s.profile = profile
	s.enrichmentStatus = status
	s.enrichmentReason = reason
}
