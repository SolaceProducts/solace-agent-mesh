// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	AuthMethodWebhook   = "webhook"
	AuthMethodEventMesh = "event_mesh"
)

func ForWebhookCaller(runAs string) *Subject {
	return SystemUserIdentity(runAs, AuthMethodWebhook)
}

func ForEventMeshCaller(ext *ExternalID, runAs string) (*Subject, error) {
	if ext == nil {
		return &Subject{
			fullIdentity:  true,
			userID:        rbac.SystemUserPrincipal(runAs),
			authenticated: true,
			authMethod:    AuthMethodEventMesh,
		}, nil
	}
	if err := validateExternal(*ext); err != nil {
		return nil, err
	}
	return &Subject{
		fullIdentity:  true,
		userID:        ext.Value,
		authenticated: true,
		authMethod:    AuthMethodEventMesh,
	}, nil
}

const (
	AuthMethodWhatsApp = "whatsapp"
	AuthMethodEmail    = "email"
)

func ForWhatsAppSender(sender *Subject, runAs string) (identity *Subject, swapped bool) {
	if runAs == "" {
		return sender, false
	}
	authMethod := AuthMethodWhatsApp
	if sender != nil {
		authMethod = sender.authMethod
	}
	return SystemUserIdentity(runAs, authMethod), true
}

const MetadataPlatformUserID = "platform_user_id"

func ForAuthorizedWhatsAppSender(sender *Subject, principal string, res SenderAuthorization) *Subject {
	resolved := *sender
	resolved.fullIdentity = true
	resolved.userID = principal
	applySenderAuthorization(&resolved, res)
	resolved.metadata = maps.Clone(sender.metadata)
	if res.ResolvedUserID != "" {
		if resolved.metadata == nil {
			resolved.metadata = map[string]any{}
		}
		resolved.metadata[MetadataPlatformUserID] = res.ResolvedUserID
	}
	return &resolved
}

func ForEmailSender(ext ExternalID, displayName string, senderVerified bool) (*Subject, error) {
	if ext.Type != IDEmail || !ext.valid() {
		return nil, ErrInvalidExternalID
	}
	return &Subject{
		fullIdentity:   true,
		userID:         ext.Value,
		email:          ext.Value,
		name:           displayName,
		authenticated:  true,
		authMethod:     AuthMethodEmail,
		senderVerified: senderVerified,
		metadata: map[string]any{
			"email_sender": ext.Value,
		},
	}, nil
}

func ForEmailHILResponder(userID string) *Subject {
	return &Subject{
		fullIdentity:  true,
		userID:        userID,
		email:         userID,
		authenticated: true,
		authMethod:    AuthMethodEmail,
	}
}

func ForWhatsAppSenderIdentity(waID, profileName string) *Subject {
	return &Subject{
		fullIdentity:  true,
		userID:        "whatsapp:" + waID,
		name:          profileName,
		authenticated: true,
		authMethod:    AuthMethodWhatsApp,
		metadata:      map[string]any{"whatsapp_wa_id": waID},
	}
}

func applySenderAuthorization(s *Subject, res SenderAuthorization) {
	s.roles = slices.Clone(res.Roles)
	s.scopes = slices.Clone(res.Scopes)
	s.scopesResolved = true
	s.profile = res.Profile
	s.enrichmentStatus = res.EnrichmentStatus
	s.enrichmentReason = res.EnrichmentReason
	if res.Resolution == identity.LookupMatched {
		s.platformUserID = res.ResolvedUserID
	}
}
