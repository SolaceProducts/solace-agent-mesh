// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (EntrypointDeployment) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `EntrypointDeployment is one deploy, update, or undeploy of an entrypoint,
kept whether it succeeded or failed.`,
		Fields: map[string]apidoc.Field{
			"ID":           {Doc: "ID is the deployment's UUID."},
			"Type":         {Doc: "Type is always \"entrypointDeployment\"."},
			"EntrypointID": {Doc: "EntrypointID is the UUID of the entrypoint that was deployed, updated, or undeployed."},
			"Status":       {Doc: "Status is the outcome: success or failed."},
			"Action":       {Doc: "Action is the action taken: deploy, update, or undeploy."},
			"Failure":      {Doc: "Failure says why the deployment failed. Null when it succeeded."},
			"CreatedBy": {Doc: `CreatedBy identifies the user who requested the deployment: their email, or their user ID
when no email is known.`},
			"CreatedTime": {Doc: "CreatedTime is when the deployment was recorded."},
			"UpdatedTime": {Doc: "UpdatedTime is when the deployment record was last changed."},
		},
	}
}

func (CreateEntrypointDeploymentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateEntrypointDeploymentRequest is a request to deploy, update, or undeploy
an entrypoint.`,
		Fields: map[string]apidoc.Field{
			"EntrypointID": {Doc: "EntrypointID is the UUID of the entrypoint to act on."},
			"Action":       {Doc: "Action is the action to take: deploy, update, or undeploy."},
		},
	}
}

func (listEntrypointDeploymentsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listEntrypointDeploymentsRequest selects a page of entrypoint deployments.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"EntrypointID": {Doc: `EntrypointID limits the list to the deployments of the entrypoint with this UUID. Leave it out
to list the deployments of every entrypoint.`},
		},
	}
}
