// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import "github.com/SolaceProducts/solace-agent-mesh/internal/apidoc"

func (CreateAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "CreateAgentRequest is config apply's copy of apiv2.CreateAgentRequest.",
		Fields: map[string]apidoc.Field{
			"SystemPrompt":             {Doc: "SystemPrompt is the agent's base instructions. Required for standard agents (the only supported type today): a create or update that omits it is rejected with HTTP 422 \"system prompt is required for standard agents\". Length is bounded by agent_instruction_min_chars and agent_instruction_max_chars in the platform config (1 and 32000 by default), counted in characters rather than bytes."},
			"AdditionalConfigurations": {Doc: "AdditionalConfigurations is a JSON-object catch-all for agent config keys that have no dedicated field, written in camelCase (e.g. supportsStreaming, agentCardPublishing.intervalSeconds). Size, depth and key collisions are checked at create/update time, and known keys are type-checked. Authored values are deep-merged into the deploy-time YAML. Forbidden keys, rejected with HTTP 422: `tools` (computed by the platform from spec.toolsets at deploy time. Declare built-in toolset IDs under spec.toolsets instead), plus any key that duplicates a structured field above. The runtime `tools:` / `group_name` shape from the AWE agent YAML does NOT belong here."},
		},
	}
}

func (UpdateAgentRequest) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: `UpdateAgentRequest is config apply's copy of apiv2.UpdateAgentRequest. The
agent's ID goes in the path, not the body.`,
	}
}

func (AgentResponse) APIDoc() apidoc.Doc {
	return apidoc.Doc{
		Type: "AgentResponse is config apply's copy of apiv2.Agent.",
	}
}
