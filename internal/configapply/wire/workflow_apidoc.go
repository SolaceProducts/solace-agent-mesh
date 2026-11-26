// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateWorkflowRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateWorkflowRequest is config apply's copy of apiv2.CreateWorkflowRequest.",
	}
}

func (UpdateWorkflowRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateWorkflowRequest is config apply's copy of apiv2.UpdateWorkflowRequest.
The workflow's ID goes in the path, not the body.`,
	}
}

func (WorkflowResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "WorkflowResponse is config apply's copy of apiv2.Workflow.",
	}
}
