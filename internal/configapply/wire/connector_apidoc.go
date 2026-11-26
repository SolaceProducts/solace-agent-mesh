// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateConnectorRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateConnectorRequest is config apply's copy of
apiv2.CreateConnectorRequest. Authors keep writing type and subtype, which v2
calls connectorType and connectorSubtype.`,
	}
}

func (UpdateConnectorRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateConnectorRequest is config apply's copy of
apiv2.UpdateConnectorRequest, a PATCH body. The connector's ID goes in the
path, not the body. The server replaces values whole, keeping a secret or a
hidden field (hil) the body leaves out, so values is cleared only by {}.`,
	}
}

func (ConnectorResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ConnectorResponse is config apply's copy of apiv2.Connector.",
	}
}
