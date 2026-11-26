// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

var RBACClaimMappingSchema = KindSchema{
	Kind:        "rbacClaimMapping",
	PluralPath:  "rbacClaimMappings",
	Description: "Grants a set of roles to every identity whose OIDC token carries a matching claim value, so access follows the IdP's group membership instead of a per-user grant. The OIDC claim key comes from the deployment's external_auth_claim_key, not this mapping. Files live under rbac/claim-mappings/.",
	Fields: []FieldSchema{
		{
			JSONName:    "name",
			GoType:      "string",
			MinLen:      -1,
			MaxLen:      -1,
			Description: "A human-readable label for the mapping. Defaults to the resource header name when omitted.",
		},
		{
			JSONName:    "oidcProvider",
			GoType:      "string",
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "Names the configured OIDC provider; must match an entry in the platform's providers catalog.",
		},
		{
			JSONName:    "claimValue",
			GoType:      "string",
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "The value of the configured claim key that grants the roles (for example sam-admins).",
		},
		{
			JSONName:    "roleNames",
			GoType:      goTypeStringSlice,
			Required:    true,
			MinLen:      -1,
			MaxLen:      -1,
			Description: "The roles to grant, referenced by name; each is resolved to its platform role id at plan time. Must be DB-managed rbacRoles.",
		},
	},
}
