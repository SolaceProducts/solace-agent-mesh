// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateToolsetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateToolsetRequest is config apply's copy of apiv2.CreateToolsetRequest.",
	}
}

func (UpdateToolsetRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateToolsetRequest is config apply's copy of apiv2.UpdateToolsetRequest,
a PATCH body. The toolset's ID goes in the path, not the body. Config apply
finds a toolset by its name, so the name is never changed.`,
	}
}

func (ToolsetResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ToolsetResponse is config apply's copy of apiv2.Toolset.",
	}
}

func (ToolsetConfiguration) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ToolsetConfiguration is config apply's copy of apiv2.ToolsetConfiguration.",
	}
}

func (UpdateToolsetConfigurationRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateToolsetConfigurationRequest is config apply's copy of
apiv2.UpdateToolsetConfigurationRequest, a PATCH body. The server merges
each tool's settings into the stored ones, so a setting removed from the
YAML is sent as null under its tool. A removed secret is not: the server
reads it back redacted and keeps a secret sent as null.`,
	}
}
