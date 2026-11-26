// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/migrate/eventmesh"
)

func rejectUnsupportedEventMeshAuthoring(values map[string]any) error {
	handlers, ok := values["event_handlers"].([]any)
	if !ok {
		return nil
	}
	details := map[string][]string{}
	for i, raw := range handlers {
		if eh, ok := raw.(map[string]any); ok {
			collectHandlerAuthoringProblems(fmt.Sprintf("event_handlers[%d]", i), eh, details)
		}
	}
	if len(details) == 0 {
		return nil
	}
	return &ValidationError{Message: invalidEventHandlersMessage, Details: details}
}

const invalidEventHandlersMessage = "event_mesh event_handlers are invalid"

func collectHandlerAuthoringProblems(path string, eh map[string]any, details map[string][]string) {
	name, _ := eh["name"].(string)
	usesDynamicTarget := false
	for _, k := range eventmesh.DynamicTargetKeys {
		if v, _ := eh[k].(string); v != "" {
			usesDynamicTarget = true
			details[path+"."+k] = append(details[path+"."+k], fmt.Sprintf(
				"event handler %q uses %s (dynamic target routing), which the Builder cannot create; use a fixed target_agent_name or target_workflow_name instead",
				name, k))
		}
	}
	for _, d := range eventmesh.DroppedHandlerKeys(eh) {
		details[path+"."+d.Key] = append(details[path+"."+d.Key],
			fmt.Sprintf("event handler %q: %s", name, d.Describe()))
	}
	if !usesDynamicTarget && !hasFixedHandlerTarget(eh) {
		details[path] = append(details[path], fmt.Sprintf(
			"event handler %q has no target; set target_agent_name or target_workflow_name", name))
	}
}

func hasFixedHandlerTarget(eh map[string]any) bool {
	agent, _ := eh["target_agent_name"].(string)
	workflow, _ := eh["target_workflow_name"].(string)
	return agent != "" || workflow != ""
}

func normalizeEventMeshAuthoredValues(values map[string]any) (map[string]any, []string) {
	if _, awe := values["event_handlers"]; !awe {
		return values, nil
	}

	migrated, warnings := eventmesh.MigrateValues(values)

	if rules, ok := migrated["event_rules"].([]any); ok {
		for _, raw := range rules {
			if rule, ok := raw.(map[string]any); ok {
				normalizeAgentRulePrompt(rule)
			}
		}
	}
	return migrated, warnings
}

func normalizeAgentRulePrompt(rule map[string]any) {
	if targetAgent, _ := rule["targetAgent"].(string); targetAgent == "" {
		return
	}
	if pt, _ := rule["promptTemplate"].(string); pt != "" {
		return
	}
	inputExpr, _ := rule["inputExpression"].(string)
	if inputExpr == "" || isPayloadPassthrough(inputExpr) {
		rule["promptTemplate"] = "{payload}"
		delete(rule, "inputExpression")
	}
}

func isPayloadPassthrough(expr string) bool {
	e := strings.TrimSpace(expr)
	e = strings.TrimSuffix(e, ":")
	return strings.TrimSpace(e) == "input.payload"
}
