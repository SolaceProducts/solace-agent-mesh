// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolbind

import (
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/builtinskills"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

type ConnectorRuntime struct{ Label, Type string }

var ConnectorRuntimes = map[string]ConnectorRuntime{
	"execute_sql_query":  {Label: "SQL", Type: "sql"},
	"graph_db_query":     {Label: "graph database", Type: "graph_db"},
	"document_db_query":  {Label: "document database", Type: "document_db"},
	"search_query":       {Label: "search", Type: "search"},
	"query_bedrock_kb":   {Label: "knowledge base", Type: "knowledge_base"},
	"slack":              {Label: "Slack", Type: "slack"},
	"slack_update":       {Label: "Slack", Type: "slack"},
	"send_email":         {Label: "email", Type: "email"},
	"event_mesh_request": {Label: "event mesh", Type: "event_mesh"},
}

type route int

const (
	routeUnknown route = iota
	routeSkill
	routeConnector
	routeAutoRegistered
	routeUngrouped
)

func routeOf(tool string) (route, string) {
	if skill, ok := skillProviding(tool); ok {
		return routeSkill, skill
	}
	if _, ok := ConnectorRuntimes[tool]; ok {
		return routeConnector, ""
	}
	if schema.AutoRegisteredBuiltinToolNames[tool] {
		return routeAutoRegistered, ""
	}
	if schema.UngroupedBuiltinToolNames[tool] {
		return routeUngrouped, ""
	}
	return routeUnknown, ""
}

func Advice(tool string) string {
	switch r, skill := routeOf(tool); r {
	case routeSkill:
		return fmt.Sprintf("it is provided by the built-in skill %s: drop this tools: entry, list the skill under skills: (- name: %s) and have the instruction load it", skill, skill)
	case routeConnector:
		runtime := ConnectorRuntimes[tool]
		return fmt.Sprintf("it is the runtime behind %s connectors (`type: %s`), not a tool an agent binds by name: drop this tools: entry. Add a %s connector to the build, in the agent's build-manifest entry under both depends_on and connectors, only when the user named the system; otherwise leave it out and say a connector has to be attached", runtime.Label, runtime.Type, runtime.Label)
	case routeAutoRegistered:
		return fmt.Sprintf("%s, so no tools: entry is needed: drop this entry", autoRegisteredAvailability(tool))
	case routeUngrouped:
		return "it is an in-process tool that belongs to no built-in group, so an agent cannot bind it by name: drop this tools: entry and tell the user it is not available to a built-in agent"
	}
	return "check the exact snake_case name against the groups in the tool schema, or use tool_type: builtin-group with the group that owns it"
}

func FindingAdvice(tool string) string {
	switch r, skill := routeOf(tool); r {
	case routeSkill:
		return fmt.Sprintf("the built-in skill %s provides it: remove it from this configuration and attach the skill %s to the agent", skill, skill)
	case routeConnector:
		runtime := ConnectorRuntimes[tool]
		return fmt.Sprintf("it is the runtime behind %s connectors (`type: %s`), not a tool an agent is given by name: remove it from this configuration and attach a %s connector to the agent", runtime.Label, runtime.Type, runtime.Label)
	case routeAutoRegistered:
		return fmt.Sprintf("%s, so the configuration need not name it", autoRegisteredAvailability(tool))
	case routeUngrouped:
		return "it is an in-process tool that belongs to no built-in group, so no toolset grants it and an agent's tools: list cannot be edited: declare it in the front matter tools: of a custom skill the agent attaches (tool_type: builtin), or remove it from this configuration"
	}
	return "nothing this agent is given provides it: if a toolset on this deployment provides it, grant that toolset to the agent; otherwise remove it from this configuration, or replace it with a tool the agent has"
}

func autoRegisteredAvailability(tool string) string {
	if tool == "index_search" {
		return "it is offered to the model in a project chat whose files are indexed"
	}
	return "every agent already has it"
}

func skillProviding(tool string) (string, bool) {
	if prefix, bare, namespaced := strings.Cut(tool, "__"); namespaced && builtinskills.HasName(prefix) {
		if skill, ok := builtinskills.SkillProvidingTool(bare); ok && skill == prefix {
			return skill, true
		}
	}
	return builtinskills.SkillProvidingTool(tool)
}

func Explain(tools []string) string {
	parts := make([]string, len(tools))
	for i, tool := range tools {
		parts[i] = fmt.Sprintf("%q: %s", tool, Advice(tool))
	}
	return strings.Join(parts, "; ")
}
