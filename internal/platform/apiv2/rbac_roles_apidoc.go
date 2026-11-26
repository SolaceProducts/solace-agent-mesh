// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Role) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Role is a named set of scopes that can be granted to users and system
users. A role also holds every scope of the roles it inherits.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the role's UUID."},
			"Type":        {Doc: "Type is always \"role\"."},
			"Name":        {Doc: "Name is the role's name, unique among custom and built-in roles."},
			"Description": {Doc: "Description says what the role is for. Null when it has none."},
			"Scopes": {Doc: `Scopes are the scopes the role grants directly, such as agent:*:invoke. Empty when it grants
none of its own.`},
			"Inherits": {Doc: `Inherits are the ids of the roles whose scopes this role also grants. Empty when it inherits
none.`},
			"Origin": {Doc: `Origin says who maintains the role: custom (created through the API), yaml (defined in the
configuration), or builtin (shipped by Solace). Changing or deleting a role that is not custom
fails with 409.`},
			"NameConflict": {Doc: `NameConflict is true when a role defined in the configuration and a custom or built-in role
share this role's name. The two stay separate roles. The flag only points out the ambiguity.`},
			"CreatedBy": {Doc: `CreatedBy identifies the user who created the role: their email, or their user ID when no
email is known. Always system for a built-in role. Null for a role defined in the configuration.`},
			"UpdatedBy": {Doc: `UpdatedBy identifies the user who last changed the role, in the same form as createdBy. Always
system for a built-in role. Null for a role defined in the configuration.`},
			"CreatedTime": {Doc: "CreatedTime is when the role was created. Null for a role defined in the configuration."},
			"UpdatedTime": {Doc: "UpdatedTime is when the role was last changed. Null for a role defined in the configuration."},
		},
	}
}

func (CreateRoleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateRoleRequest is the body that creates a role.",
		Fields: map[string]apidoc.Field{
			"Name": {Doc: `Name is the role's name: not blank, with no control characters. It must not be the name of
another custom or built-in role, or of a role defined in the configuration.`},
			"Description": {Doc: "Description says what the role is for."},
			"Scopes":      {Doc: "Scopes are the scopes the role grants directly, each in category:resource:verb form."},
			"Inherits": {Doc: `Inherits are the ids of the custom or built-in roles whose scopes this role also grants. A role
defined in the configuration cannot be inherited, and the inheritance must not form a cycle.`},
		},
	}
}

func (ReplaceRoleRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ReplaceRoleRequest is the body that replaces a custom role. A field left out is cleared.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the role to replace."},
			"Name": {Doc: `Name is the role's new name: not blank, with no control characters. No other role can already
have it, whatever its origin. A role the configuration refers to by name cannot be renamed.`},
			"Description": {Doc: "Description replaces the description. Leave it out to clear it."},
			"Scopes":      {Doc: "Scopes replaces the scopes the role grants directly. Leave it out to clear them."},
			"Inherits": {Doc: `Inherits replaces the ids of the roles whose scopes this role also grants. Leave it out to
clear them. A role defined in the configuration cannot be inherited, and the inheritance must
not form a cycle.`},
		},
	}
}

func (listRolesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listRolesRequest selects a page of roles.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort": {Doc: `Sort orders the custom and built-in roles by name, createdTime or updatedTime, as field:asc or
field:desc; leave it out to sort by name. Roles defined in the configuration come first, by
name, descending only with name:desc.`},
			"Origin": {Doc: "Origin limits the list to one origin: custom, yaml, or builtin. Leave it out to list every role."},
			"Search": {Doc: "Search limits the list to roles whose name or description contains it, ignoring case."},
		},
	}
}
