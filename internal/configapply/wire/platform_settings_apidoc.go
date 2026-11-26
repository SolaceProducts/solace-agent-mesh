// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ReplaceWebuiSettingsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReplaceWebuiSettingsRequest is config apply's copy of
apiv2.ReplaceWebuiSettingsRequest.`,
	}
}

func (WebuiSettingsResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "WebuiSettingsResponse is config apply's copy of apiv2.WebuiSettings.",
	}
}

func (ReplaceProfileProviderRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ReplaceProfileProviderRequest is config apply's copy of
apiv2.ReplaceProfileProviderRequest.`,
	}
}

func (ProfileProviderResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ProfileProviderResponse is config apply's copy of apiv2.ProfileProvider.",
	}
}

func (CreateAPIKeyRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateAPIKeyRequest is config apply's copy of apiv2.CreateAPIKeyRequest.",
	}
}

func (UpdateAPIKeyRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateAPIKeyRequest is config apply's copy of apiv2.UpdateAPIKeyRequest. The
key's ID goes in the path, not the body.`,
	}
}

func (APIKeyResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `APIKeyResponse is config apply's copy of apiv2.APIKey. It has no secret
field: only the create response carries the plaintext, as CreatedAPIKey.`,
	}
}

func (CreatedAPIKey) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreatedAPIKey is config apply's copy of apiv2.CreatedAPIKey, the one
response that carries the key's plaintext.`,
	}
}
