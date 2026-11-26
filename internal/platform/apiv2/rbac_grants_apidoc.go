// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (Grant) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `Grant gives one identity one role. A grant is concrete when the identity
already has an Agent Mesh user. Otherwise it is pending until that user first
signs in.`,
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the grant's UUID."},
			"Type": {Doc: "Type is always \"grant\"."},
			"Subject": {Doc: `Subject is the identity the grant is for. Null for a grant given directly to a user who can no
longer be found.`},
			"UserID": {Doc: `UserID is the id of the user the grant applies to. Null while the grant is pending, and for a
grant defined in the configuration.`},
			"RoleID": {Doc: "RoleID is the id of the role the grant gives."},
			"State": {Doc: `State is the grant's state: concrete when it applies to an existing user, or pending until a
user matches its subject. Null for a grant defined in the configuration.`},
			"Origin": {Doc: `Origin says who maintains the grant: custom (created through the API, and deletable) or yaml
(defined in the deployment's configuration, and read-only).`},
			"CreatedBy": {Doc: `CreatedBy identifies the user who created the grant: their email, or their user ID when no
email is known. Null for a grant defined in the configuration.`},
			"UpdatedBy": {Doc: `UpdatedBy identifies the user who last changed the grant, in the same form as createdBy. Null
for a grant defined in the configuration.`},
			"CreatedTime": {Doc: "CreatedTime is when the grant was created. Null for a grant defined in the configuration."},
			"UpdatedTime": {Doc: "UpdatedTime is when the grant was last changed. Null for a grant defined in the configuration."},
			"User": {Doc: `User is how the grant's user signs in now. Null unless the list is read with expand=user, and
for a pending grant or a grant defined in the configuration.`},
		},
	}
}

func (GrantUser) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `GrantUser is how the user a concrete grant applies to signs in now. The subject a grant recorded
can stop naming its user, when the user's email changes or the grant is declared again under
another subject type, so a client matches the subject it declares to a grant through these fields.`,
		Fields: map[string]apidoc.Field{
			"Email":       {Doc: "Email is the user's current email. Null when the user has none."},
			"EmailShared": {Doc: "EmailShared is true when another user has the same email, so the email names neither of them."},
			"Sub":         {Doc: "Sub is the user's subject identifier at the identity provider."},
			"Issuer":      {Doc: "Issuer is the identity provider that issues sub."},
		},
	}
}

func (GrantSubject) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "GrantSubject names the identity a grant is for.",
		Fields: map[string]apidoc.Field{
			"Type": {Doc: `Type says what kind of identity value holds: email, sub (the identity provider's subject
claim), or legacy (an identity recorded before subjects were typed).`},
			"Value":  {Doc: "Value is the identity itself, such as an email address."},
			"Issuer": {Doc: "Issuer is the identity provider that issued a sub subject. Null for the other types."},
		},
	}
}

func (CreateGrantSubject) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateGrantSubject names the identity a new grant is for.",
		Fields: map[string]apidoc.Field{
			"Type": {Doc: `Type says what kind of identity value holds: email, sub (the identity provider's subject
claim), or legacy (an identity recorded before subjects were typed).`},
			"Value":  {Doc: "Value is the identity itself, such as an email address."},
			"Issuer": {Doc: "Issuer is the identity provider that issued the subject. Required when type is sub."},
		},
	}
}

func (CreateGrantRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateGrantRequest is the body that creates a grant.",
		Fields: map[string]apidoc.Field{
			"Subject": {Doc: "Subject is the identity to give the role to. If no user matches it yet, the grant is pending."},
			"RoleID": {Doc: `RoleID is the id of the custom or built-in role to give. A role defined in the configuration
cannot be granted through the API.`},
		},
	}
}

func (UpdateGrantRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "UpdateGrantRequest is the body that changes a grant.",
		Fields: map[string]apidoc.Field{
			"ID": {Doc: "ID is the grant's UUID."},
			"Subject": {Doc: `Subject is the subject to record the grant under, with the rules it has on create. It must
name the grant's user, so the user and the role never change. Leave it out to keep it. Null
fails with 422, because a grant needs a subject.`},
			"RoleID": {Doc: "RoleID is fixed when the grant is created. If the body includes it, the request fails with 422."},
			"UserID": {Doc: "UserID is fixed when the grant is created. If the body includes it, the request fails with 422."},
		},
	}
}

func (listGrantsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listGrantsRequest selects a page of grants.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort": {Doc: `Sort orders the custom grants by roleId, state, createdTime or updatedTime, as field:asc or
field:desc; leave it out to sort by roleId. Grants defined in the configuration come first.`},
			"Origin": {Doc: "Origin limits the list to one origin: custom or yaml. Leave it out to list every grant."},
			"State": {Doc: `State limits the list to concrete or pending grants. A grant defined in the configuration has
no state, so it is left out.`},
			"RoleID": {Doc: "RoleID limits the list to the grants of one role. A value that is not a UUID fails with 400."},
			"Expand": {Doc: `Expand takes one value, user, which fills each concrete grant's user with how that user signs in
now. Any other value fails with 422.`},
		},
	}
}
