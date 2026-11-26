// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateModelRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateModelRequest is config apply's copy of apiv2.CreateModelRequest.
authType is read-only because authors set the auth type as authConfig.type,
which the platform reads first.`,
		Fields: map[string]apidoc.Field{
			"ModelType": {Doc: "ModelType is chat (the default) or decision. A decision model uses provider typesafe, openai or google_ai_studio and serves workflow decision nodes; the type and a decision model's provider cannot change after creation."},
		},
	}
}

func (UpdateModelRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateModelRequest is config apply's copy of apiv2.UpdateModelRequest, a
PATCH body. The model's ID goes in the path, not the body. The server merges
authConfig into the stored credentials and drops the old type's secrets
when the type changes, so a removed setting is sent as null and a type
change sends the whole object. modelParams is replaced whole.`,
	}
}

func (ModelResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ModelResponse is config apply's copy of apiv2.Model.",
	}
}
