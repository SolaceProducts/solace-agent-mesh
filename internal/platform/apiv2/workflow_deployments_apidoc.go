// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (WorkflowDeployment) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `WorkflowDeployment is one deploy, update, or undeploy of a workflow, kept
whether it succeeded or failed.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the deployment's UUID."},
			"Type":        {Doc: "Type is always \"workflowDeployment\"."},
			"WorkflowID":  {Doc: "WorkflowID is the UUID of the workflow deployed."},
			"Status":      {Doc: "Status is the deployment's outcome: success or failed."},
			"Action":      {Doc: "Action is what the deployment did: deploy, update, or undeploy."},
			"Failure":     {Doc: "Failure says why the deployment failed. Null when it succeeded."},
			"CreatedBy":   {Doc: "CreatedBy identifies the user who ran the deployment: their email, or their user ID when no email is known."},
			"CreatedTime": {Doc: "CreatedTime is when the deployment was requested."},
			"UpdatedTime": {Doc: "UpdatedTime is when the deployment record last changed."},
		},
	}
}

func (CreateWorkflowDeploymentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateWorkflowDeploymentRequest deploys, updates, or undeploys a workflow.",
		Fields: map[string]apidoc.Field{
			"WorkflowID": {Doc: "WorkflowID is the UUID of the workflow to deploy."},
			"Action":     {Doc: "Action is what to do: deploy, update, or undeploy."},
			"TestMode": {Doc: `TestMode, when true, adds an invoke scope for the caller alone to the deployed
configuration. The workflow runtime does not enforce it yet. Defaults to false. Must be false
for undeploy and with deploymentId.`},
			"DeploymentID": {Doc: `DeploymentID rolls the workflow back: the deploy or update sends the configuration this earlier
deployment ran with instead of the workflow's current one. It must be a successful deploy or
update of the same workflow. Leave it out for undeploy.`},
		},
	}
}
