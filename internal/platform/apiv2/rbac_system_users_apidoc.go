// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ServiceAccount) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ServiceAccount is a principal that Agent Mesh owns rather than an identity
provider. An entrypoint runs as it through run_as and receives the scopes of
the roles it holds.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the service account's UUID."},
			"Type":        {Doc: "Type is always \"serviceAccount\"."},
			"Name":        {Doc: "Name is the service account's name, fixed when it is created."},
			"Sub":         {Doc: "Sub is the principal that an entrypoint's run_as uses to refer to the service account: the name prefixed with system:, such as system:support-webhook."},
			"DisplayName": {Doc: "DisplayName is a label for people to read. Null when it has none."},
			"RoleIDs": {Doc: `RoleIDs are the ids of the roles the service account holds. A built-in service account reports the roles
defined in the configuration that it holds.`},
			"Origin": {Doc: `Origin says who maintains the service account: custom (created through the API, and editable) or
builtin (shipped by Solace). A write to a built-in service account fails with 409.`},
			"CreatedBy": {Doc: `CreatedBy identifies the user who created the service account: their email, or their user ID when no
email is known. Always system for a built-in service account. Null when unknown.`},
			"UpdatedBy": {Doc: `UpdatedBy identifies the user who last changed the service account, in the same form as createdBy.
Always system for a built-in service account. Null when unknown.`},
			"CreatedTime": {Doc: "CreatedTime is when the service account was created. Null when unknown."},
			"UpdatedTime": {Doc: "UpdatedTime is when the service account was last changed. Null when unknown."},
		},
	}
}

func (SystemUser) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `SystemUser is a service account as the deprecated systemUsers routes answer with it:
the same fields as ServiceAccount, with type systemUser.

Deprecated: Reason: system users were renamed to service accounts; use /rbac/serviceAccounts instead. Removal Date: 2027-12-01.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the service account's UUID."},
			"Type":        {Doc: "Type is always \"systemUser\"."},
			"Name":        {Doc: "Name is the service account's name, fixed when it is created."},
			"Sub":         {Doc: "Sub is the principal that an entrypoint's run_as uses to refer to the service account: the name prefixed with system:, such as system:support-webhook."},
			"DisplayName": {Doc: "DisplayName is a label for people to read. Null when it has none."},
			"RoleIDs": {Doc: `RoleIDs are the ids of the roles the service account holds. A built-in service account reports the roles
defined in the configuration that it holds.`},
			"Origin": {Doc: `Origin says who maintains the service account: custom (created through the API, and editable) or
builtin (shipped by Solace). A write to a built-in service account fails with 409.`},
			"CreatedBy": {Doc: `CreatedBy identifies the user who created the service account: their email, or their user ID when no
email is known. Always system for a built-in service account. Null when unknown.`},
			"UpdatedBy": {Doc: `UpdatedBy identifies the user who last changed the service account, in the same form as createdBy.
Always system for a built-in service account. Null when unknown.`},
			"CreatedTime": {Doc: "CreatedTime is when the service account was created. Null when unknown."},
			"UpdatedTime": {Doc: "UpdatedTime is when the service account was last changed. Null when unknown."},
		},
	}
}

func (CreateServiceAccountRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateServiceAccountRequest is the body that creates a service account.",
		Fields: map[string]apidoc.Field{
			"Name": {Doc: `Name is the service account's name. It must not be taken by another service account or reserved for a
built-in one, such as default.`},
			"DisplayName": {Doc: "DisplayName is a label for people to read."},
			"RoleIDs": {Doc: `RoleIDs are the ids of the custom or built-in roles to grant. At least one. A role defined in
the configuration cannot be granted through the API.`},
		},
	}
}

func (CreateSystemUserRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateSystemUserRequest is the body the deprecated POST /rbac/systemUsers takes: the same as
CreateServiceAccountRequest.

Deprecated: Reason: system users were renamed to service accounts; use /rbac/serviceAccounts instead. Removal Date: 2027-12-01.`,
		Fields: map[string]apidoc.Field{
			"Name": {Doc: `Name is the service account's name. It must not be taken by another service account or reserved for a
built-in one, such as default.`},
			"DisplayName": {Doc: "DisplayName is a label for people to read."},
			"RoleIDs": {Doc: `RoleIDs are the ids of the custom or built-in roles to grant. At least one. A role defined in
the configuration cannot be granted through the API.`},
		},
	}
}

func (ReplaceServiceAccountRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReplaceServiceAccountRequest is the body that replaces a custom service account's roles and display
name. The name is the service account's identity and cannot be changed.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the UUID of the service account to replace."},
			"DisplayName": {Doc: "DisplayName replaces the display name. Leave it out to clear it."},
			"RoleIDs": {Doc: `RoleIDs replaces the roles the service account holds, so a role left out is revoked. At least one.
Each must be a custom or built-in role. A role defined in the configuration cannot be granted
through the API.`},
		},
	}
}

func (ReplaceSystemUserRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReplaceSystemUserRequest is the body the deprecated PUT /rbac/systemUsers/{id} takes: the same as
ReplaceServiceAccountRequest.

Deprecated: Reason: system users were renamed to service accounts; use /rbac/serviceAccounts instead. Removal Date: 2027-12-01.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the UUID of the service account to replace."},
			"DisplayName": {Doc: "DisplayName replaces the display name. Leave it out to clear it."},
			"RoleIDs": {Doc: `RoleIDs replaces the roles the service account holds, so a role left out is revoked. At least one.
Each must be a custom or built-in role. A role defined in the configuration cannot be granted
through the API.`},
		},
	}
}

func (listServiceAccountsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listServiceAccountsRequest selects a page of service accounts.",
		Fields: map[string]apidoc.Field{
			"Page":   {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort":   {Doc: "Sort orders the service accounts by name, as name:asc or name:desc; leave it out to sort by name."},
			"Search": {Doc: "Search limits the list to service accounts whose name or display name contains it, ignoring case."},
		},
	}
}

func (listSystemUsersRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listSystemUsersRequest selects a page of service accounts through the deprecated GET /rbac/systemUsers.",
		Fields: map[string]apidoc.Field{
			"Page":   {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort":   {Doc: "Sort orders the service accounts by name, as name:asc or name:desc; leave it out to sort by name."},
			"Search": {Doc: "Search limits the list to service accounts whose name or display name contains it, ignoring case."},
		},
	}
}
