// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const yamlAssignmentKeyDelim = "|"

func yamlRoleResponses(state rbac.YAMLState) []RBACRoleResponse {
	if len(state.Roles.Roles) == 0 {
		return nil
	}
	out := make([]RBACRoleResponse, 0, len(state.Roles.Roles))
	for name, def := range state.Roles.Roles {
		if name == rbac.RoleSystemChannelInvoke {
			continue
		}
		out = append(out, RBACRoleResponse{
			ID:          name,
			Name:        name,
			Description: def.Description,
			Scopes:      apicontract.NonNilList(def.Scopes),
			Inherits:    def.Inherits,
			Origin:      RBACOriginYAML,
		})
	}
	return out
}

func yamlRoleByName(state rbac.YAMLState, name string) *RBACRoleResponse {
	def, ok := state.Roles.Roles[name]
	if !ok {
		return nil
	}
	return &RBACRoleResponse{
		ID:          name,
		Name:        name,
		Description: def.Description,
		Scopes:      apicontract.NonNilList(def.Scopes),
		Inherits:    def.Inherits,
		Origin:      RBACOriginYAML,
	}
}

func yamlGrantResponses(state rbac.YAMLState) []GrantResponse {
	if len(state.Users.Users) == 0 {
		return nil
	}
	var out []GrantResponse
	for identity, assign := range state.Users.Users {
		if strings.HasPrefix(identity, yamlGatewayPrefix) {
			continue
		}
		for _, role := range assign.Roles {
			out = append(out, GrantResponse{
				ID:      identity + yamlAssignmentKeyDelim + role,
				Subject: &GrantSubject{Type: store.PendingSubjectLegacy, Value: identity},
				RoleID:  role,
				Origin:  RBACOriginYAML,
			})
		}
	}
	return out
}

func yamlClaimMappingResponses(state rbac.YAMLState) []RBACClaimMappingResponse {
	if len(state.ClaimMappings) == 0 {
		return nil
	}
	type groupKey struct{ provider, value string }
	order := make([]groupKey, 0, len(state.ClaimMappings))
	roles := make(map[groupKey][]string, len(state.ClaimMappings))
	claimKeys := make(map[groupKey]string, len(state.ClaimMappings))
	for _, m := range state.ClaimMappings {
		k := groupKey{m.OIDCProvider, m.ClaimValue}
		if _, seen := roles[k]; !seen {
			order = append(order, k)
			claimKeys[k] = m.ClaimKey
		}
		roles[k] = appendUniqueStr(roles[k], m.RoleID)
	}
	out := make([]RBACClaimMappingResponse, 0, len(order))
	for _, k := range order {
		out = append(out, RBACClaimMappingResponse{
			ID:           synthClaimMappingID(k.provider, k.value),
			OIDCProvider: k.provider,
			ClaimKey:     claimKeys[k],
			ClaimValue:   k.value,
			RoleIDs:      roles[k],
			Origin:       RBACOriginYAML,
		})
	}
	return out
}

func appendUniqueStr(s []string, v string) []string {
	for _, e := range s {
		if e == v {
			return s
		}
	}
	return append(s, v)
}

func synthClaimMappingID(provider, value string) string {
	return provider + yamlAssignmentKeyDelim + value
}

func yamlHasRoleName(state rbac.YAMLState, name string) bool {
	if name == "" {
		return false
	}
	_, ok := state.Roles.Roles[name]
	return ok
}

const (
	yamlRefDefaultRoles   = "default roles"
	yamlRefClaimMappings  = "claim mappings"
	yamlRefUserAssignment = "user assignments"
	yamlRefRoleInherits   = "role inheritance"
)

func yamlReferenceKind(state rbac.YAMLState, name string) string {
	if name == "" || yamlHasRoleName(state, name) {
		return ""
	}
	if slices.Contains(state.DefaultRoles, name) {
		return yamlRefDefaultRoles
	}
	for _, m := range state.ClaimMappings {
		if m.RoleID == name {
			return yamlRefClaimMappings
		}
	}
	for _, assign := range state.Users.Users {
		if slices.Contains(assign.Roles, name) {
			return yamlRefUserAssignment
		}
	}
	for _, def := range state.Roles.Roles {
		if slices.Contains(def.Inherits, name) {
			return yamlRefRoleInherits
		}
	}
	return ""
}

func yamlHasClaimMapping(state rbac.YAMLState, provider, claimValue, roleName string) bool {
	for _, m := range state.ClaimMappings {
		if m.OIDCProvider == provider &&
			m.ClaimValue == claimValue &&
			m.RoleID == roleName {
			return true
		}
	}
	return false
}

func yamlHasGrant(state rbac.YAMLState, subjectType, identity, roleName string) bool {
	if identity == "" {
		return false
	}
	var assign rbac.UserAssignment
	var ok bool
	if subjectType == store.PendingSubjectSub {
		assign, ok = state.Users.Users[identity]
	} else {
		assign, ok = rbac.BuildEmailFoldIndex(state.Users.Users).ResolveBare(state.Users.Users, identity)
	}
	if !ok {
		return false
	}
	return slices.Contains(assign.Roles, roleName)
}
