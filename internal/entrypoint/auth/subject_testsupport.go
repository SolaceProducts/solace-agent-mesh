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

type SubjectFields struct {
	UserID           string
	Email            string
	Name             string
	AuthMethod       string
	CredentialID     string
	Authenticated    bool
	Roles            []string
	Scopes           []string
	Metadata         map[string]any
	Profile          *identity.Profile
	EnrichmentStatus identity.EnrichmentStatus
	EnrichmentReason string
	SystemInitiated  bool
	ScopesResolved   bool
	PlatformUserID   string
	SenderVerified   bool
}

func NewTestSubject(f SubjectFields) *Subject {
	return &Subject{
		userID:           f.UserID,
		email:            f.Email,
		name:             f.Name,
		authMethod:       f.AuthMethod,
		credentialID:     f.CredentialID,
		authenticated:    f.Authenticated,
		roles:            slices.Clone(f.Roles),
		scopes:           slices.Clone(f.Scopes),
		metadata:         maps.Clone(f.Metadata),
		profile:          f.Profile,
		enrichmentStatus: f.EnrichmentStatus,
		enrichmentReason: f.EnrichmentReason,
		systemInitiated:  f.SystemInitiated,
		scopesResolved:   f.ScopesResolved,
		platformUserID:   f.PlatformUserID,
		senderVerified:   f.SenderVerified,
		fullIdentity:     true,
	}
}
