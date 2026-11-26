// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ExperimentToolAuth) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ExperimentToolAuth is the tool credentials every agent in an experiment's
delegation graph holds for the service account its runs execute as. The response
is one object per experiment, not a list of agents.`,
		Fields: map[string]apidoc.Field{
			"RunAs":               {Doc: "RunAs is the service account the credentials are held under, as system:<name>."},
			"Agents":              {Doc: "Agents are the agent or workflow the experiment evaluates, then, for a workflow, the agents its nodes call, then the agents those delegate to, plus the agents added by hand, at most agentScanLimit of them."},
			"AllowListIsWildcard": {Doc: "AllowListIsWildcard is true when the target's delegates, or a workflow's node agents, could not all be listed automatically. Any running agent can then be added with POST /evaluations/experiments/{experimentId}/toolAuth/agents."},
			"TotalAgents":         {Doc: "TotalAgents counts the whole delegation graph, which the agents list cuts off at agentScanLimit."},
			"TotalRunningAgents":  {Doc: "TotalRunningAgents counts the running agents in the mesh, of which a candidate listing looks at agentScanLimit."},
			"AgentScanLimit":      {Doc: "AgentScanLimit is how many agents one request looks at, 60."},
		},
	}
}

func (ToolAuthAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ToolAuthAgent is one agent's credentials in an experiment's checklist.",
		Fields: map[string]apidoc.Field{
			"AgentName": {Doc: "AgentName is the agent's name."},
			"IsTarget":  {Doc: "IsTarget is true for the agent or workflow the experiment evaluates."},
			"AddedBy":   {Doc: "AddedBy identifies the user who added the agent by hand, in the form of createdBy. Null for an agent not added by hand, even when the graph also found it. Only an added agent can be removed."},
			"NodeIDs":   {Doc: "NodeIDs are the workflow's agent nodes that call this agent, when the experiment evaluates a workflow. A node inside a nested workflow is named by the node ids leading to it, joined with \"/\". Empty for every agent of an agent experiment, for the workflow itself, for an agent found only as a delegate, and for an agent added by hand that no node calls. A node agent that was also added by hand keeps its node ids."},
			"Reachable": {Doc: "Reachable is false when the agent could not be asked, because it is stopped, undeployed, or runs a version without tool authorization. Its tools are then unknown, not empty."},
			"Tools":     {Doc: "Tools are the credentials the agent can hold, empty when it has none or is not reachable."},
		},
	}
}

func (ToolAuthCredential) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ToolAuthCredential is one credential an agent holds, or can hold, for the
experiment's service account.`,
		Fields: map[string]apidoc.Field{
			"CredentialKey":      {Doc: "CredentialKey names the credential. Unique only within one agent."},
			"DisplayName":        {Doc: "DisplayName is the credential's label, or null when it has none."},
			"AuthType":           {Doc: "AuthType is how the credential authenticates, such as oauth2, or null when the agent does not say."},
			"Interactive":        {Doc: "Interactive is true when a person can authorize the credential from a browser with PUT /evaluations/experiments/{experimentId}/toolAuth/begin."},
			"Status":             {Doc: "Status is the credential's state: connected when the service account holds a working credential, otherwise not_connected."},
			"ExpiresTime":        {Doc: "ExpiresTime is when the credential expires, or null when it does not or is not connected."},
			"LastAuthorizedBy":   {Doc: "LastAuthorizedBy identifies the user who last authorized the credential through Agent Mesh, in the form of createdBy, or is null when none did."},
			"LastAuthorizedTime": {Doc: "LastAuthorizedTime is when the credential was last authorized through Agent Mesh, or null when it never was."},
		},
	}
}

func (ToolAuthAuthorization) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ToolAuthAuthorization is an authorization flow begun on one credential.",
		Fields: map[string]apidoc.Field{
			"AuthURI":       {Doc: "AuthURI is the provider's page to open in a browser. The person signs in there, and the credential is stored for the experiment's service account."},
			"CredentialKey": {Doc: "CredentialKey names the credential being authorized."},
			"ExpiresTime":   {Doc: "ExpiresTime is when the authorization link stops working."},
		},
	}
}

func (ToolAuthCandidateAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ToolAuthCandidateAgent is a running agent holding a credential a person can
authorize from a browser.`,
		Fields: map[string]apidoc.Field{
			"AgentName": {Doc: "AgentName is the agent's name, which POST /evaluations/experiments/{experimentId}/toolAuth/agents accepts."},
		},
	}
}

func (PinnedToolAuthAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "PinnedToolAuthAgent is an agent added to an experiment's checklist by hand.",
		Fields: map[string]apidoc.Field{
			"AgentName": {Doc: "AgentName is the agent's name as the checklist lists it, which addresses the agent in DELETE /evaluations/experiments/{experimentId}/toolAuth/agents/{agentName}."},
			"AddedBy":   {Doc: "AddedBy identifies the user who added the agent, in the form of createdBy."},
			"AddedTime": {Doc: "AddedTime is when the agent was added."},
		},
	}
}

func (ToolAuthCredentialRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "ToolAuthCredentialRequest names one credential of one agent.",
		Fields: map[string]apidoc.Field{
			"ExperimentID":  {Doc: "ExperimentID is the UUID of the experiment."},
			"AgentName":     {Doc: "AgentName is the agent that holds the credential: the experiment's target agent or an agent on its checklist."},
			"CredentialKey": {Doc: "CredentialKey names the credential, as the checklist lists it."},
		},
	}
}

func (AddToolAuthAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "AddToolAuthAgentRequest adds an agent to an experiment's checklist.",
		Fields: map[string]apidoc.Field{
			"ExperimentID": {Doc: "ExperimentID is the UUID of the experiment."},
			"AgentName":    {Doc: "AgentName is a running agent to add. Agents can be added only while the checklist's allowListIsWildcard is true, and not when the target agent is configured not to delegate to the agent."},
		},
	}
}

func (experimentToolAuthRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "experimentToolAuthRequest addresses one experiment's tool authorization.",
		Fields: map[string]apidoc.Field{
			"ExperimentID": {Doc: "ExperimentID is the UUID of the experiment."},
		},
	}
}

func (listToolAuthCandidatesRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "listToolAuthCandidatesRequest selects a page of candidate agents.",
		Fields: map[string]apidoc.Field{
			"ExperimentID": {Doc: "ExperimentID is the UUID of the experiment."},
			"Page":         {Doc: "Page is the pageNumber (1–1,000,000, default 1) and pageSize (1–100, default 20) query parameters."},
		},
	}
}

func (removeToolAuthAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "removeToolAuthAgentRequest addresses an agent added to a checklist.",
		Fields: map[string]apidoc.Field{
			"ExperimentID": {Doc: "ExperimentID is the UUID of the experiment."},
			"AgentName":    {Doc: "AgentName is the name of an agent added by hand."},
		},
	}
}
