// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestYAMLReferenceKind(t *testing.T) {
	defined := rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
		"owned": {Scopes: []string{"tool:_:read"}},
	}}

	tests := []struct {
		name  string
		state rbac.YAMLState
		role  string
		want  string
	}{
		{"empty name", rbac.YAMLState{DefaultRoles: []string{""}}, "", ""},
		{"unreferenced", rbac.YAMLState{DefaultRoles: []string{"other"}}, "target", ""},
		{"default role", rbac.YAMLState{DefaultRoles: []string{"target"}}, "target", yamlRefDefaultRoles},
		{
			"claim mapping",
			rbac.YAMLState{ClaimMappings: []rbac.DBClaimMapping{{OIDCProvider: "azure", ClaimValue: "eng", RoleID: "target"}}},
			"target", yamlRefClaimMappings,
		},
		{
			"user assignment",
			rbac.YAMLState{Users: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
				"alice@example.com": {Roles: []string{"target"}},
			}}},
			"target", yamlRefUserAssignment,
		},
		{
			"inherited by a YAML role",
			rbac.YAMLState{Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
				"editor": {Inherits: []string{"target"}},
			}}},
			"target", yamlRefRoleInherits,
		},
		{
			"inherits names a different role",
			rbac.YAMLState{Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
				"editor": {Inherits: []string{"other"}},
			}}},
			"target", "",
		},
		{"definition owns the name, also a default", rbac.YAMLState{Roles: defined, DefaultRoles: []string{"owned"}}, "owned", ""},
		{
			"definition owns the name, also inherited",
			rbac.YAMLState{Roles: rbac.RolesConfig{Roles: map[string]rbac.RoleDefinition{
				"owned":  {Scopes: []string{"tool:_:read"}},
				"editor": {Inherits: []string{"owned"}},
			}}},
			"owned", "",
		},
		{
			"definition owns the name, also claim-mapped",
			rbac.YAMLState{Roles: defined, ClaimMappings: []rbac.DBClaimMapping{{RoleID: "owned"}}},
			"owned", "",
		},
		{
			"definition owns the name, also assigned",
			rbac.YAMLState{Roles: defined, Users: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
				"alice@example.com": {Roles: []string{"owned"}},
			}}},
			"owned", "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := yamlReferenceKind(tc.state, tc.role); got != tc.want {
				t.Errorf("yamlReferenceKind(%q) = %q, want %q", tc.role, got, tc.want)
			}
		})
	}
}

func TestYamlHasGrant_MixedCaseEmailSeedBlocksConflictingDBGrant(t *testing.T) {
	state := rbac.YAMLState{Users: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
		"Mixed.Case@example.com": {Roles: []string{"sam_admin"}},
	}}}

	if !yamlHasGrant(state, store.PendingSubjectEmail, "mixed.case@example.com", "sam_admin") {
		t.Error("expected the normalized (lowercased) identity to match the mixed-case YAML seed via the fallback")
	}
	if yamlHasGrant(state, store.PendingSubjectEmail, "mixed.case@example.com", "sam_user") {
		t.Error("must not report a grant for a role the seed doesn't actually assign")
	}
}

func TestYamlHasGrant_SubTypeNeverFoldsAcrossAnUnrelatedEmailSeed(t *testing.T) {
	state := rbac.YAMLState{Users: rbac.UsersConfig{Users: map[string]rbac.UserAssignment{
		"mixed.case@example.com": {Roles: []string{"sam_admin"}},
	}}}

	if yamlHasGrant(state, store.PendingSubjectSub, "Mixed.Case@example.com", "sam_admin") {
		t.Error("a sub value must only match its own exact case, never fold onto an unrelated email seed")
	}
	if !yamlHasGrant(state, store.PendingSubjectSub, "mixed.case@example.com", "sam_admin") {
		t.Error("a sub value that exactly matches the seed key must still be reported as a conflict")
	}
}
