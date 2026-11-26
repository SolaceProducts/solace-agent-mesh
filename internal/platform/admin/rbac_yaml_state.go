// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/oidcauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func BuildRBACYAMLState(cfg oidcauth.AuthorizationServiceConfig, logger *slog.Logger) (rbac.YAMLState, error) {
	if logger == nil {
		logger = slog.Default()
	}
	embRoles, embUsers, err := rbac.EmbeddedDefaults()
	if err != nil {
		return rbac.YAMLState{}, fmt.Errorf("rbac yaml: %w", err)
	}
	state := rbac.YAMLState{DefaultRoles: cfg.DefaultRoles, Roles: embRoles, Users: embUsers}

	if cfg.RoleToScopeDefinitionsPath != "" {
		roles, err := rbac.LoadRolesFromFile(cfg.RoleToScopeDefinitionsPath)
		if err != nil {
			return state, fmt.Errorf("rbac yaml: roles: %w", err)
		}
		for name, def := range roles.Roles {
			cleaned := make([]string, 0, len(def.Scopes))
			for _, scope := range def.Scopes {
				if err := validateScopes([]string{scope}); err != nil {
					logger.Warn("dropping invalid scope from YAML-loaded role",
						"role", name,
						"scope", scope,
						"reason", err,
						"path", cfg.RoleToScopeDefinitionsPath,
					)
					continue
				}
				cleaned = append(cleaned, scope)
			}
			def.Scopes = cleaned
			state.Roles.Roles[name] = def
		}
	}

	if cfg.UserToRoleAssignmentsPath != "" {
		users, err := rbac.LoadUsersFromFile(cfg.UserToRoleAssignmentsPath, logger)
		if err != nil {
			return state, fmt.Errorf("rbac yaml: users: %w", err)
		}
		for id, assignment := range users.Users {
			state.Users.Users[id] = assignment
		}
	}

	if claims := cfg.IDPClaimsConfig; claims != nil {
		state.ClaimOIDCProvider = claims.OIDCProvider
		state.ClaimKey = claims.ClaimKey
		for value, roleNames := range claims.Mappings {
			for _, roleName := range roleNames {
				state.ClaimMappings = append(state.ClaimMappings, rbac.DBClaimMapping{
					OIDCProvider: claims.OIDCProvider,
					ClaimKey:     claims.ClaimKey,
					ClaimValue:   value,
					RoleID:       roleName,
				})
			}
		}
	}

	return state, nil
}
