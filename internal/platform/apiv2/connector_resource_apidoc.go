// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ConnectorSpecification) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ConnectorSpecification is the OpenAPI document an api/openapi connector keeps
at an external specification_url. Agent Mesh reads it from that host rather
than storing it, so it has no id.`,
		Fields: map[string]apidoc.Field{
			"Content": {Doc: "Content is the document exactly as the host served it, left unparsed."},
			"Format": {Doc: `Format says how to parse content, json or yaml. The platform decides it
from the bytes, never from the type the host claimed.`},
		},
	}
}

func (uploadResourceRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "uploadResourceRequest replaces a connector's stored OpenAPI specification.",
		Fields: map[string]apidoc.Field{
			"ID":   {Doc: "ID is the UUID of the connector whose specification is being replaced."},
			"Spec": {Doc: "Spec is the specification file, sent as the multipart part named file."},
		},
	}
}
