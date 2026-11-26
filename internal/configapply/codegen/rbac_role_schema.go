// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

const goTypeStringSlice = "[]string"

var RBACRoleSchema = KindSchema{
	Kind:        "rbacRole",
	PluralPath:  "rbacRoles",
	Description: "A named set of RBAC scopes, optionally inheriting from other roles, plus the identities granted the role. Grants are declared inline via spec.users; use a standalone kind: rbacGrant file for a role that has no config definition of its own.",
	Fields: []FieldSchema{
		{
			JSONName:    "scopes",
			GoType:      goTypeStringSlice,
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Scope strings this role grants, each formatted <category>:<resource>:<verb> (e.g. agent_builder:*:update, agent:hr-bot:invoke).",
		},
		{
			JSONName:    "inherits",
			GoType:      goTypeStringSlice,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Names of other rbacRoles whose scopes this role also grants. Cycles are rejected at plan time.",
		},
		{
			JSONName:    "users",
			GoType:      goTypeStringSlice,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Identities granted this role. Each entry is a bare identity string (legacy, email-preferred) or a typed subject { type: email|sub, value, issuer? }, and fans out into one platform grant at apply time. This is the place to grant a config-defined role; to grant a managed/in-DB role that has no config definition, use a standalone rbacGrant file instead.",
		},
	},
}
