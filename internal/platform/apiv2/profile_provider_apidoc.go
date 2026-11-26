// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ProfileProvider) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ProfileProvider says which toolset enriches a signed-in user's profile, and
which user attribute it is asked to look up. Agent Mesh keeps one for the
whole platform, so it has no id.`,
		Fields: map[string]apidoc.Field{
			"ToolsetID": {Doc: `ToolsetID is the UUID of the toolset that enriches the user's profile. Null when none is
wired, and then profiles are not enriched.`},
			"LookupKey": {Doc: `LookupKey is the user attribute Agent Mesh sends the toolset to find the user by, such as
email or upn. Defaults to email.`},
			"CreatedBy": {Doc: `CreatedBy identifies who first wired a toolset: their email, or their user ID when no email
is known. Null until a toolset has been wired.`},
			"UpdatedBy": {Doc: `UpdatedBy identifies who last changed the profile provider, in the same form as createdBy.
Null until a toolset has been wired.`},
			"CreatedTime": {Doc: "CreatedTime is when a toolset was first wired. Null until then."},
			"UpdatedTime": {Doc: "UpdatedTime is when the profile provider last changed. Null until a toolset has been wired."},
		},
	}
}

func (ReplaceProfileProviderRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReplaceProfileProviderRequest is the body of a PUT, which sets the whole
profile provider.`,
		Fields: map[string]apidoc.Field{
			"ToolsetID": {Doc: `ToolsetID is the UUID of the toolset to wire. It must be ready, declare the profileProvider
capability, and expose the enrich_authenticated_user tool.`},
			"LookupKey": {Doc: `LookupKey is the user attribute Agent Mesh sends the toolset to find the user by, such as
email or upn. Required, because a PUT replaces the whole profile provider and does not fall
back to email.`},
		},
	}
}
