// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (DeploymentFailure) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `DeploymentFailure says why a deployment failed. The underlying error text is
never returned, only logged under errorId.`,
		Fields: map[string]apidoc.Field{
			"Reason": {Doc: `Reason is the kind of failure: rejected (Agent Mesh refused the settings), invalid_config
(Agent Mesh could not build a runnable configuration from them), runtime_error (the runtime
refused the change or did not answer), or unknown (the failure was recorded before Agent Mesh
kept a reason).`},
			"Message": {Doc: "Message describes the failure."},
			"ErrorID": {Doc: `ErrorID is the ID the failure's cause was logged under. Null when the failure was recorded
before Agent Mesh kept one.`},
		},
	}
}
