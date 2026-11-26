// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ServerCapabilities) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ServerCapabilities is what this server lets callers do, whatever their own permissions.",
		Fields: map[string]apidoc.Field{
			"ManageRbac": {Doc: `ManageRbac is true when roles, grants, and the other access-control records can be changed
through the API. False when Agent Mesh uses its built-in identity provider, which owns them.`},
			"CreateDecisionModels": {Doc: `CreateDecisionModels is true when a model of type decision can be created. Creating one needs
workflow decision nodes or decision-model evaluators to be enabled.`},
		},
	}
}
