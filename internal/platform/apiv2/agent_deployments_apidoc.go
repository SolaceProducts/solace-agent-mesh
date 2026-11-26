// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (AgentDeployment) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `AgentDeployment is one deploy, update, or undeploy of an agent or remote
agent, kept whether it succeeded or failed.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: "ID is the deployment's UUID."},
			"Type":        {Doc: "Type is always \"agentDeployment\"."},
			"AgentID":     {Doc: "AgentID is the UUID of the agent or remote agent deployed."},
			"Status":      {Doc: "Status is the deployment's outcome: success or failed."},
			"Action":      {Doc: "Action is what the deployment did: deploy, update, or undeploy."},
			"Failure":     {Doc: "Failure says why the deployment failed. Null when it succeeded."},
			"CreatedBy":   {Doc: "CreatedBy identifies the user who ran the deployment: their email, or their user ID when no email is known."},
			"CreatedTime": {Doc: "CreatedTime is when the deployment was requested."},
			"UpdatedTime": {Doc: "UpdatedTime is when the deployment record last changed."},
		},
	}
}

func (CreateAgentDeploymentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `CreateAgentDeploymentRequest deploys, updates, or undeploys an agent or
remote agent.`,
		Fields: map[string]apidoc.Field{
			"AgentID": {Doc: "AgentID is the UUID of the agent or remote agent to deploy."},
			"Action":  {Doc: "Action is what to do: deploy, update, or undeploy."},
			"SkipIfCurrent": {Doc: `SkipIfCurrent, when true, returns the latest deployment instead of restarting an agent that
already runs the configuration this deploy would send. Defaults to false. Must be false for
undeploy and with deploymentId.`},
			"DeploymentID": {Doc: `DeploymentID is the UUID of an earlier deployment to roll the agent back to. The deploy or
update then sends that deployment's configuration. It must be a successful deploy or update of
the same agent. Leave it out for undeploy. Remote agents cannot be rolled back.`},
		},
	}
}

func (listAgentDeploymentsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listAgentDeploymentsRequest selects a page of agent deployments.",
		Fields: map[string]apidoc.Field{
			"Page": {Doc: "Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters."},
			"AgentID": {Doc: `AgentID limits the list to the deployments of one agent or remote agent, by UUID. Leave it
out to list every deployment. If it is not a UUID, the request fails with 400.`},
		},
	}
}
