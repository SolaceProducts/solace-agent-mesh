// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateEntrypointRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateEntrypointRequest is config apply's copy of
apiv2.CreateEntrypointRequest.`,
		Fields: map[string]apidoc.Field{
			"Values": {Doc: "Values holds the entrypoint type's settings, whose keys and value types each type sets. Required for email, teams, whatsapp, event_mesh, webhook and google_chat entrypoints: a create that omits it, or sends anything but an object, is rejected with \"<type> entrypoint requires an object 'values' payload\". Slack and MCP entrypoints accept none."},
		},
	}
}

func (UpdateEntrypointRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateEntrypointRequest is config apply's copy of
apiv2.UpdateEntrypointRequest, a PATCH body. The entrypoint's ID goes in the
path, not the body. The server merges values into the stored settings and
keeps any it is not sent, so config apply manages only the settings the YAML
names: one removed from the YAML is kept, and "" clears it.`,
	}
}

func (EntrypointResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "EntrypointResponse is config apply's copy of apiv2.Entrypoint.",
	}
}
