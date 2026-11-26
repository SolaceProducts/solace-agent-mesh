// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"context"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

const GateAgentVisible apicontract.Gate = "agentVisible"

var agentsTag = apicontract.Tag{Name: "Chat Agents", Group: tagGroup, Text: "Find the agents the caller may chat with and choose the default one."}

const (
	AgentTypeStandard = "standard"
	AgentTypeWorkflow = "workflow"
)

type ChatAgentSkill struct {
	Key         string   `json:"key" example:"summarize"`
	Name        string   `json:"name" example:"Summarize"`
	Description string   `json:"description" example:"Summarize a body of text."`
	Tags        []string `json:"tags" example:"[\"text\",\"nlp\"]"`
}

type ChatAgent struct {
	ID          string           `json:"id" example:"Orchestrator"`
	Type        string           `json:"type" example:"agent"`
	DisplayName string           `json:"displayName" example:"Orchestrator"`
	Description *string          `json:"description" example:"Routes a request to the agent that can answer it."`
	Version     *string          `json:"version" example:"1.0.0"`
	AgentType   string           `json:"agentType" example:"standard"`
	Priority    int              `json:"priority" example:"1000"`
	Internal    bool             `json:"internal" example:"false"`
	Skills      []ChatAgentSkill `json:"skills"`
}

type ListAgentsRequest struct {
	apicontract.StrictRequest
	Page apicontract.Page `json:"-"`
}

func AgentRoutes(deps Deps) []apicontract.Route {
	list := deps.ListAgents
	if list == nil {
		list = func(context.Context, ListAgentsRequest) (l apicontract.List[ChatAgent], err error) { return l, nil }
	}
	return []apicontract.Route{
		{Method: http.MethodGet, Path: "/agents", Gate: GateAgentVisible, Stability: apicontract.StabilityDraft,
			Doc: apicontract.Doc{ID: "listChatAgents", Summary: "Get a list of agents",
				Text: "Returns one page of the agents and workflows the caller may send a message to, sorted by displayName without regard to case. " +
					"To send a message, put the agent's id in metadata.agent_name. A send without it fails with 400. " +
					"A client picks the default agent the way the web UI does. " +
					"First read every page of the list. " +
					"Then drop the agents whose internal is true and the agents whose agentType is workflow. " +
					"Take the agent with the highest priority, and break a tie by displayName. " +
					"If none is left, use the first agent in the list.",
				Tag: agentsTag},
			Handler: apicontract.Handle(list, "Unable to list agents")},
	}
}
