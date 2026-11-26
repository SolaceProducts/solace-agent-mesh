// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ClaimMapping) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ClaimMapping grants roles to every identity whose token carries a claim with
a given value from a given identity provider.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the claim mapping's UUID."},
			"Type": {Doc: "Type is always \"claimMapping\"."},
			"Name": {Doc: "Name is the mapping's name. Null for a mapping defined in the configuration, which has none."},
			"OIDCProvider": {Doc: `OIDCProvider is the identity provider whose tokens the mapping matches. Null when the mapping
names none, such as one created on a deployment with no OpenID Connect (OIDC) provider configured.`},
			"ClaimKey": {Doc: `ClaimKey is the token claim the mapping reads, such as groups. Null when the mapping names
none, such as one created on a deployment with no claim key configured.`},
			"ClaimValue": {Doc: "ClaimValue is the value of the claim that triggers the mapping."},
			"RoleIDs":    {Doc: "RoleIDs are the ids of the roles the mapping grants."},
			"Origin": {Doc: `Origin says who maintains the mapping: custom (created through the API, and editable) or yaml
(defined in the deployment's configuration, and read-only).`},
			"CreatedBy": {Doc: `CreatedBy identifies the user who created the mapping: their email, or their user ID when no
email is known. Null for a mapping defined in the configuration.`},
			"UpdatedBy": {Doc: `UpdatedBy identifies the user who last changed the mapping, in the same form as createdBy. Null
for a mapping defined in the configuration.`},
			"CreatedTime": {Doc: "CreatedTime is when the mapping was created. Null for a mapping defined in the configuration."},
			"UpdatedTime": {Doc: "UpdatedTime is when the mapping was last changed. Null for a mapping defined in the configuration."},
		},
	}
}

func (CreateClaimMappingRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateClaimMappingRequest is the body that creates a claim mapping.",
		Fields: map[string]apidoc.Field{
			"Name": {Doc: "Name is the mapping's name."},
			"OIDCProvider": {Doc: `OIDCProvider is the identity provider whose tokens the mapping matches. Leave it out to use the
deployment's configured provider.`},
			"ClaimKey": {Doc: `ClaimKey is the token claim the mapping reads. Leave it out to use the deployment's configured
claim key.`},
			"ClaimValue": {Doc: `ClaimValue is the claim value that triggers the mapping. Up to 256 characters. If another
mapping uses this provider and value, or the configuration maps them to one of these roles, the
request fails with 409.`},
			"RoleIDs": {Doc: `RoleIDs are the ids of the custom or built-in roles to grant. At least one. A role defined in
the configuration cannot be granted by a mapping created through the API.`},
		},
	}
}

func (ReplaceClaimMappingRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ReplaceClaimMappingRequest is the body that replaces a custom claim mapping.",
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the UUID of the claim mapping to replace."},
			"Name": {Doc: "Name is the mapping's new name."},
			"OIDCProvider": {Doc: `OIDCProvider is the identity provider whose tokens the mapping matches. Leave it out to use the
deployment's configured provider, which replaces the mapping's current one.`},
			"ClaimKey": {Doc: `ClaimKey is the token claim the mapping reads. Leave it out to use the deployment's configured
claim key, which replaces the mapping's current one.`},
			"ClaimValue": {Doc: `ClaimValue is the claim value that triggers the mapping. Up to 256 characters. If another
mapping uses this provider and value, or the configuration maps them to one of these roles, the
request fails with 409.`},
			"RoleIDs": {Doc: `RoleIDs are the ids of the roles the mapping grants, replacing the current ones. At least one.
A role defined in the configuration cannot be granted by a mapping created through the API.`},
		},
	}
}

func (listClaimMappingsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listClaimMappingsRequest selects a page of claim mappings.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort": {Doc: `Sort orders the custom mappings by name, oidcProvider, claimValue, createdTime or updatedTime,
as field:asc or field:desc. Mappings defined in the configuration come first, by claim value.`},
			"Origin":       {Doc: "Origin limits the list to one origin: custom or yaml. Leave it out to list every mapping."},
			"OIDCProvider": {Doc: "OIDCProvider limits the list to mappings of one identity provider."},
			"RoleID":       {Doc: "RoleID limits the list to mappings that grant the role with this id. A value that is not a UUID fails with 400."},
			"Search":       {Doc: "Search limits the list to mappings whose name or claim value contains it, ignoring case."},
		},
	}
}
