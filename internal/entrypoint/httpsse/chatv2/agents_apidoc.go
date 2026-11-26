// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (ChatAgentSkill) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ChatAgentSkill is one thing an agent says it can do.`,
		Fields: map[string]apidoc.Field{
			"Key":         {Doc: `Key identifies the skill within the agent.`},
			"Name":        {Doc: `Name is the skill's title.`},
			"Description": {Doc: `Description says what the skill does.`},
			"Tags":        {Doc: `Tags are the skill's keywords.`},
		},
	}
}

func (ChatAgent) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ChatAgent is an agent or workflow the caller may send a message to.`,
		Fields: map[string]apidoc.Field{
			"ID":          {Doc: `ID is the agent's name. Send it as metadata.agent_name when you send a message to this agent.`},
			"Type":        {Doc: `Type is always "agent".`},
			"DisplayName": {Doc: `DisplayName is the name to show people. It equals id when the agent has no separate display name.`},
			"Description": {Doc: `Description says what the agent does. Null when the card has none.`},
			"Version":     {Doc: `Version is the version of the agent. Null when the card has none.`},
			"AgentType":   {Doc: `AgentType is standard for an agent and workflow for a workflow.`},
			"Priority":    {Doc: `Priority ranks the agent for the default choice, and the highest wins. The seeded Orchestrator has 1000. An agent that sets none has 0.`},
			"Internal":    {Doc: `Internal is true for an agent the gateway runs for its own features, such as the agent builder. A chat client does not offer these as a default.`},
			"Skills":      {Doc: `Skills are what the agent says it can do.`},
		},
	}
}

func (ListAgentsRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `ListAgentsRequest selects a page of the agents the caller may use.`,
		Fields: map[string]apidoc.Field{
			"Page": {Doc: `Page is the pageNumber (1-based, default 1) and pageSize (1–100, default 20) query parameters.`},
		},
	}
}
