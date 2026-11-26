// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ScopeCatalogEntry) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ScopeCatalogEntry is one scope that can be granted to a role.",
		Fields: map[string]apidoc.Field{
			"Scope": {Doc: `Scope is the scope in category:resource:verb form. A resource of * stands for any instance,
or for the whole collection where there is no instance to name. Some entries still show _,
the older spelling of *: a role holding a _ scope is treated as holding the matching *
scope, so grant the * form. An entry from expand=resourceScopes names one instance.`},
			"Name":        {Doc: "Name is a short label for the scope."},
			"Description": {Doc: "Description says what holding the scope allows. Null when there is none."},
			"ResourceName": {Doc: `ResourceName is the name of the agent or workflow that an expand=resourceScopes entry is for.
Null on every other entry.`},
		},
	}
}

func (listScopesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listScopesRequest selects a page of the scope catalog.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"Sort": {Doc: `Sort orders the scopes by scope or name, as field:asc or field:desc; leave it out to sort by
scope.`},
			"Category": {Doc: "Category limits the list to the scopes of one category, such as agent."},
			"Search":   {Doc: "Search limits the list to scopes whose scope or name contains it, ignoring case."},
			"Expand": {Doc: `Expand takes one value, resourceScopes, which adds an entry for each agent and workflow under
every agent or workflow scope that covers any instance. Any other value fails with 422.`},
		},
	}
}
