// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

func (s *Subject) UserID() string { return s.userID }

func (s *Subject) Email() string { return s.email }

func (s *Subject) Name() string { return s.name }

func (s *Subject) AuthMethod() string { return s.authMethod }

func (s *Subject) CredentialID() string { return s.credentialID }

func (s *Subject) Authenticated() bool { return s.authenticated }

func (s *Subject) PlatformUserID() string { return s.platformUserID }

func (s *Subject) SenderVerified() bool { return s.senderVerified }

func (s *Subject) SystemInitiated() bool { return s.systemInitiated }

func (s *Subject) ScopesResolved() bool { return s.scopesResolved }

func (s *Subject) Profile() *identity.Profile { return s.profile }

func (s *Subject) EnrichmentStatus() identity.EnrichmentStatus { return s.enrichmentStatus }

func (s *Subject) EnrichmentReason() string { return s.enrichmentReason }

func (s *Subject) Roles() []string { return slices.Clone(s.roles) }

func (s *Subject) Scopes() []string { return slices.Clone(s.scopes) }

func (s *Subject) Metadata() map[string]any { return maps.Clone(s.metadata) }

func (s *Subject) IsZero() bool {
	return s == nil || (s.userID == "" && !s.authenticated && len(s.scopes) == 0 &&
		len(s.roles) == 0 && s.email == "" && s.name == "" && s.authMethod == "")
}
