// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (User) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `User is a person or service account known to the platform, with the roles that
currently apply to them.`,
		Fields: map[string]apidoc.Field{
			"ID":           {Doc: "ID is the user's UUID."},
			"Type":         {Doc: "Type is always \"user\"."},
			"DisplayName":  {Doc: "DisplayName is the user's display name from their identity provider. Null when the provider supplied none."},
			"UserName":     {Doc: "UserName is the user's username from their identity provider. Null when the provider supplied none."},
			"Email":        {Doc: "Email is the user's email address. Null when none is on record."},
			"Issuer":       {Doc: "Issuer is the identity provider that vouches for the user. Null when none is on record."},
			"Sub":          {Doc: "Sub is the user's subject identifier at the identity provider. Null when none is on record."},
			"OIDCProvider": {Doc: "OIDCProvider names the configured OIDC provider the user signs in through. Null when none is on record."},
			"ProvisionedVia": {Doc: `ProvisionedVia says how the user came to exist: jit (created at first sign-in), scim (provisioned by
SCIM), or system (a service account).`},
			"Active": {Doc: "Active is false for a user whose identity provider account has been deprovisioned."},
			"Groups": {Doc: "Groups are the identity provider groups the user belongs to. Empty when there are none."},
			"Roles": {Doc: `Roles are the roles that apply to the user, each with where it comes from. Empty when none apply
or when they could not be resolved.`},
			"LastLoginTime": {Doc: "LastLoginTime is when the user last signed in. Null when the user has never signed in."},
			"CreatedBy":     {Doc: "CreatedBy identifies who created the user record. Null when unknown."},
			"UpdatedBy":     {Doc: "UpdatedBy identifies who last changed the user record. Null when unknown."},
			"CreatedTime":   {Doc: "CreatedTime is when the user record was created."},
			"UpdatedTime":   {Doc: "UpdatedTime is when the user record was last changed."},
			"Budget": {Doc: `Budget is the budget that applies to the user. Null unless the request sets expand=budget, and
also when no budget applies.`},
			"BudgetTopUp": {Doc: `BudgetTopUp is the user's active top-up, which raises the budget until it expires. Null unless
the request sets expand=budget, and also when no top-up is active. Shown even for an unlimited
budget, which it does not change.`},
			"EffectiveBudgetAmount": {Doc: `EffectiveBudgetAmount is the budget's limit raised by the active top-up, as a decimal string.
Null unless the request sets expand=budget, and also while the budget is unlimited or no budget
applies.`},
		},
	}
}

func (ActiveTopUp) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ActiveTopUp is a temporary raise of the budget of a user or the mesh, active
until it expires.`,
		Fields: map[string]apidoc.Field{
			"Amount":      {Doc: "Amount is how much the top-up raises the budget, as a decimal string."},
			"ExpiresTime": {Doc: "ExpiresTime is when the top-up stops applying."},
		},
	}
}

func (UserRole) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UserRole is a role that applies to a user, with where it comes from.",
		Fields: map[string]apidoc.Field{
			"RoleID": {Doc: "RoleID is the role's UUID."},
			"Source": {Doc: `Source says where the role comes from: direct (granted to the user), claim (matched by a claim
mapping), yaml (assigned in the deployment's configuration), or default (a default role).`},
			"Manageable": {Doc: `Manageable is true when the role can be changed through the user's roleGrants: a direct role of
any user but a built-in service account. Roles from the other sources are changed where they come from.`},
		},
	}
}

func (AppliedBudget) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "AppliedBudget is the budget that applies to a user or the mesh.",
		Fields: map[string]apidoc.Field{
			"ID":     {Doc: "ID is the budget's UUID."},
			"Name":   {Doc: "Name is the budget's name."},
			"Amount": {Doc: "Amount is the budget's limit as a decimal string. Null for an unlimited budget."},
			"Source": {Doc: `Source says why the budget applies: assigned (assigned to the user) or default (the default budget
of the tier, which is always the source for the mesh).`},
			"AssignmentID": {Doc: `AssignmentID is the UUID of the assignment that gives the user the budget. Null when the default
budget applies, and always for the mesh.`},
		},
	}
}

func (UserRoleGrants) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UserRoleGrants are the roles granted directly to one user.",
		Fields: map[string]apidoc.Field{
			"RoleIDs": {Doc: `RoleIDs are the UUIDs of the roles granted directly to the user. Empty when there are none. Roles
the user has through a claim mapping, the configuration, or the default roles are not listed.`},
		},
	}
}

func (UserIdentityClaim) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UserIdentityClaim is one identity claim on file for a user, in the form an
entrypoint's sender lookup matches against.`,
		Fields: map[string]apidoc.Field{
			"ClaimKey":            {Doc: "ClaimKey names the claim, such as phone_number."},
			"ClaimValueCanonical": {Doc: "ClaimValueCanonical is the claim's value in the form stored for matching."},
			"Verified":            {Doc: "Verified is true when the identity provider vouched for the claim."},
			"UpdatedTime":         {Doc: "UpdatedTime is when the claim was last refreshed, which happens when the user signs in."},
		},
	}
}

func (listUsersRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listUsersRequest selects a page of users.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort": {Doc: `Sort orders the users by displayName, email, lastLoginTime, createdTime or updatedTime, as
field:asc or field:desc; leave it out to list the most recently updated first.`},
			"Search": {Doc: `Search limits the list to users whose display name or email contains it, ignoring case and
surrounding spaces.`},
			"RoleID": {Doc: `RoleID limits the list to users who hold this role from any source. A value that is not a UUID
fails with 400. An unknown role lists no users.`},
			"Group":        {Doc: "Group limits the list to members of this identity provider group."},
			"OIDCProvider": {Doc: "OIDCProvider limits the list to users who sign in through this OIDC provider."},
			"BudgetID": {Doc: `BudgetID limits the list to users this budget applies to. An empty value is the same as leaving
it out. A value that is not a UUID fails with 400. Without the budget:*:read scope, the request
fails with 403.`},
			"Expand": {Doc: `Expand names related data to include. The only value is budget, which fills each user's budget.
An empty value is the same as leaving it out. Any other value, or only commas, fails with 422.
Without the budget:*:read scope, the request fails with 403.`},
		},
	}
}

func (getUserRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "getUserRequest addresses one user.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the user. An unknown ID fails with 404."},
			"Expand": {Doc: `Expand names related data to include. The only value is budget. An empty value is the same as
leaving it out. Any other value, or only commas, fails with 422. Without the budget:*:read
scope, the request fails with 403.`},
		},
	}
}

func (ReplaceUserRoleGrantsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ReplaceUserRoleGrantsRequest is the body that replaces the roles granted directly to a user.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the UUID of the user. An unknown ID fails with 404."},
			"RoleIDs": {Doc: `RoleIDs are the UUIDs of the roles to grant the user directly, replacing every direct grant.
Leave it out or send an empty list to clear them. An unknown role, or one defined in the
configuration, fails with 422.`},
		},
	}
}

func (listUserIdentityClaimsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listUserIdentityClaimsRequest selects a page of a user's identity claims.",
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the UUID of the user. An unknown ID fails with 404."},
			"Page": {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
		},
	}
}
