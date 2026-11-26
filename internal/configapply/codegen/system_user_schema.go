// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

var SystemUserSchema = KindSchema{
	Kind:        "serviceAccount",
	PluralPath:  "serviceAccounts",
	Description: "Provisions a non-human principal (subject system:<name>) holding a set of roles. Until 2027-12-01 the kind is also accepted as systemUser, under resources.systemUsers and rbac/system-users/, the spelling used before the kind was renamed. The file is the whole role set: adding a role to roleNames grants it and removing one revokes it, both on an ordinary apply. Removing the resource deletes the principal, gated behind --prune. spec.name cannot change in place — the subject is the identity, so a rename plans as a delete plus a create.",
	Fields: []FieldSchema{
		{
			JSONName:    "name",
			GoType:      "string",
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Service account name; the platform derives the subject system:<name>. Defaults to the resource name (the file's metadata name) when omitted.",
		},
		{
			JSONName:    "displayName",
			GoType:      "string",
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Friendly name shown in the Access Control UI. Optional.",
		},
		{
			JSONName:    "roleNames",
			GoType:      goTypeStringSlice,
			Required:    true,
			MinLen:      1,
			MaxLen:      -1,
			Description: "Roles granted to the service account, by name. Each is resolved to a role id at plan time (or at apply time for a role created earlier in the same apply). May name a config-defined role or a managed/in-DB role.",
		},
	},
}
