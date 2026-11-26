// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
)

type ProfileEnricher interface {
	EnrichIdentity(ctx context.Context, id *Subject)
}

type ChannelPolicy struct {
	IsMultiParty              bool
	UseUserIdentityInChannels bool
	RunAs                     string
}

func (p ChannelPolicy) resolve(real *Subject) (*Subject, bool) {
	return ResolveRunAsIdentity(RunAsRequest{
		Real:                      real,
		IsMultiParty:              p.IsMultiParty,
		UseUserIdentityInChannels: p.UseUserIdentityInChannels,
		RunAs:                     p.RunAs,
	})
}

func ForChatUser(ctx context.Context, real *Subject, enricher ProfileEnricher, policy ChannelPolicy) (identity *Subject, swapped bool) {
	if enricher != nil {
		enricher.EnrichIdentity(ctx, real)
	}
	return policy.resolve(real)
}

func ForChatResponder(real *Subject, policy ChannelPolicy) *Subject {
	identity, _ := policy.resolve(real)
	return identity
}

const (
	AuthMethodSlack = "slack"
	AuthMethodTeams = "teams"
)

func ForSlackUser(userID, email, slackUserID, slackTeamID string) *Subject {
	return &Subject{
		fullIdentity:   true,
		userID:         userID,
		email:          email,
		authMethod:     AuthMethodSlack,
		senderVerified: true,
		metadata: map[string]any{
			"slack_user_id": slackUserID,
			"slack_team_id": slackTeamID,
		},
	}
}

func ForTeamsUser(userID, email, name, teamsUserID, aadObjectID, source string) *Subject {
	return &Subject{
		fullIdentity:   true,
		userID:         userID,
		email:          email,
		name:           name,
		authMethod:     AuthMethodTeams,
		senderVerified: true,
		metadata: map[string]any{
			"teams_user_id": teamsUserID,
			"aad_object_id": aadObjectID,
			"source":        source,
		},
	}
}

const AuthMethodGoogleChat = "google_chat"

const metadataGoogleChatUser = "google_chat_user"

func ForGoogleChatSender(email, displayName, chatUserName string, res SenderAuthorization) *Subject {
	s := &Subject{
		fullIdentity:   true,
		userID:         email,
		email:          email,
		name:           displayName,
		authenticated:  true,
		authMethod:     AuthMethodGoogleChat,
		senderVerified: true,
		metadata:       map[string]any{metadataGoogleChatUser: chatUserName},
	}
	applySenderAuthorization(s, res)
	return s
}

func ForGoogleChatUser(sender *Subject, policy ChannelPolicy) (identity *Subject, swapped bool) {
	return policy.resolve(sender)
}

func ForGoogleChatResponder(clicker *Subject, taskSwapped bool, runAs string) *Subject {
	if taskSwapped {
		return SystemUserIdentity(runAs, clicker.authMethod)
	}
	return clicker
}
