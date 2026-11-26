// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

var RBACGrantSchema = KindSchema{
	Kind:        "rbacGrant",
	PluralPath:  "rbacGrants",
	Description: "Grants a role to a set of identities. Use this for a managed/in-DB role that has no config definition to carry spec.users; for a config-defined role, prefer spec.users on the role.",
	Fields: []FieldSchema{
		{
			JSONName:    "roleName",
			GoType:      "string",
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Name of the role to grant. May be a config-defined role or a managed/in-DB role that exists only on the platform.",
		},
		{
			JSONName:    "users",
			GoType:      goTypeStringSlice,
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Identities granted the role. Each entry is a bare identity string (legacy, email-preferred) or a typed subject { type: email|sub, value, issuer? }, identical to spec.users on a role. Each fans out into one platform grant at apply time.",
		},
	},
}
