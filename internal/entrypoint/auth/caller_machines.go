// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"maps"
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	SyntheticUserID     = "synthetic-monitor"
	SyntheticUserEmail  = "synthetic-monitor@synthetics.invalid"
	SyntheticUserName   = "Synthetic Monitor"
	SyntheticAuthMethod = "synthetic"
)

func ForSyntheticMonitor(principalRoles []string, metadata map[string]any, scopes ScopeExpander) *Subject {
	roles := slices.Clone(principalRoles)
	id := &Subject{
		fullIdentity:  true,
		userID:        SyntheticUserID,
		email:         SyntheticUserEmail,
		name:          SyntheticUserName,
		authenticated: true,
		authMethod:    SyntheticAuthMethod,
		roles:         roles,
		metadata:      maps.Clone(metadata),
	}
	if scopes != nil {
		id.scopes = slices.Clone(scopes.ResolveScopes(SyntheticUserID, roles))
	}
	return id
}

type McpDefaults struct {
	DefaultUserIdentity string
	RunAs               string
}

func ForMcpDefaultCaller(cfg *McpDefaults, scopes ScopeExpander, authzEnforced bool) *Subject {
	id := DevIdentity()
	if cfg != nil {
		if cfg.DefaultUserIdentity != "" {
			id.userID = cfg.DefaultUserIdentity
		} else {
			id.userID = rbac.SystemUserPrincipal(cfg.RunAs)
		}
	}
	switch {
	case scopes != nil:
		id.scopes = slices.Clone(scopes.ResolveScopes(id.userID, id.roles))
	case authzEnforced:
		id.scopes = []string{}
	}
	return id
}

func ForSentinelMonitor(systemUser string, scopes []string) *Subject {
	return &Subject{
		fullIdentity:    true,
		userID:          systemUser,
		authenticated:   true,
		systemInitiated: true,
		scopes:          slices.Clone(scopes),
		platformUserID:  rbac.DefaultSystemUserID,
	}
}
