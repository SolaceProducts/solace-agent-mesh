// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (DefaultRoles) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DefaultRoles are the roles a user holds when nothing else gives them one: no
grant, claim mapping, or configuration assignment. Service accounts never get them.`,
		Fields: map[string]apidoc.Field{
			"RoleIDs": {Doc: "RoleIDs are the ids of the default roles. Empty when there are none."},
			"Origin": {Doc: `Origin says where the set comes from: custom (set through the API, replacing the
configuration's defaults entirely) or yaml (the configuration's defaults, in effect while no
override is set).`},
		},
	}
}

func (ReplaceDefaultRolesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ReplaceDefaultRolesRequest is the body that replaces the default roles.",
		Fields: map[string]apidoc.Field{
			"RoleIDs": {Doc: `RoleIDs are the ids of the custom or built-in roles that a user with no other role holds. Send an
empty list or leave it out to clear the override, so the configuration's defaults apply again.`},
		},
	}
}
