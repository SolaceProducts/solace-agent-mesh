// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh/solaceheaders"
)

const templatePrefix = "template:"

const staticPrefix = "static:"

const translatorIdentityPrefix = "event-mesh-"

func MigrateValues(legacy map[string]any) (map[string]any, []string) {
	var warnings []string
	out := map[string]any{}

	if bc, ok := legacy["event_mesh_broker_config"].(map[string]any); ok {
		copyStringField(bc, "broker_url", out, "broker_url")
		copyStringField(bc, "broker_vpn", out, "broker_vpn")
		copyStringField(bc, "broker_username", out, "broker_username")
		copyStringField(bc, "broker_password", out, "broker_password")
		if v, ok := bc["tls_skip_verify"]; ok {
			out["tls_skip_verify"] = v
		}
	}

	eventHandlers, _ := legacy["event_handlers"].([]any)
	outputHandlers, _ := legacy["output_handlers"].([]any)

	outputByName := make(map[string]map[string]any, len(outputHandlers))
	for _, raw := range outputHandlers {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if n, _ := m["name"].(string); n != "" {
			outputByName[n] = m
		}
	}
	warnings = append(warnings, literalTemplateWarnings(outputByName)...)

	rules := make([]any, 0, len(eventHandlers))
	for _, raw := range eventHandlers {
		eh, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		rules = append(rules, buildRuleFromEventHandler(eh, outputByName))
		warnings = append(warnings, handlerKeyWarnings(eh)...)
	}
	if len(rules) > 0 {
		out["event_rules"] = rules
	}

	if uid, _ := legacy["default_user_identity"].(string); uid != "" && !strings.HasPrefix(uid, translatorIdentityPrefix) {
		for _, raw := range rules {
			rule, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if _, has := rule["defaultUserIdentity"]; !has {
				rule["defaultUserIdentity"] = uid
			}
		}
	}

	handled := map[string]bool{
		"event_mesh_broker_config": true,
		"event_handlers":           true,
		"output_handlers":          true,
		"default_user_identity":    true,
		"broker_url":               true,
		"broker_vpn":               true,
		"broker_username":          true,
		"broker_password":          true,
		"tls_skip_verify":          true,
		"event_rules":              true,
	}
	for k, v := range legacy {
		if handled[k] {
			continue
		}
		if k == "gateway_id" {
			warnings = append(warnings, fmt.Sprintf(
				"event_mesh: legacy %q dropped — the platform now assigns gateway IDs", k))
			continue
		}
		out[k] = v
		warnings = append(warnings, fmt.Sprintf(
			"event_mesh: legacy key %q preserved under spec.values — verify it's meaningful to the new shape", k))
	}

	return out, warnings
}

func buildRuleFromEventHandler(eh map[string]any, outputByName map[string]map[string]any) map[string]any {
	rule := map[string]any{}

	copyRuleBasics(eh, rule)
	copyRuleInputExpression(eh, rule)
	copyRuleIdentity(eh, rule)
	copyRuleStructuredInvocation(eh, rule)
	copyRuleAckPolicy(eh, rule)
	copyRuleArtifactProcessing(eh, rule)
	attachOutputs(eh, outputByName, rule)

	return rule
}

func copyRuleBasics(eh, rule map[string]any) {
	if v, ok := eh["name"]; ok {
		rule["name"] = v
	}
	if v, ok := eh["subscriptions"]; ok {
		rule["subscriptions"] = v
	}
	if v, _ := eh["existing_queue_name"].(string); v != "" {
		rule["existingQueueName"] = v
	}
	if v, _ := eh["payload_format"].(string); v != "" {
		rule["messageFormat"] = v
	}
	if v, _ := eh["payload_encoding"].(string); v != "" {
		rule["payloadEncoding"] = v
	}
	if v := eh["event_filter"]; v != nil {
		rule["eventFilter"] = v
	}
	if v, _ := eh["target_agent_name"].(string); v != "" {
		rule["targetAgent"] = v
	}
	if v, _ := eh["target_workflow_name"].(string); v != "" {
		rule["targetWorkflowName"] = v
	}
}

func copyRuleInputExpression(eh, rule map[string]any) {
	v, _ := eh["input_expression"].(string)
	if v == "" {
		return
	}
	if workflow, _ := eh["target_workflow_name"].(string); workflow != "" {
		rule["inputExpression"] = v
		return
	}
	if rest, found := strings.CutPrefix(v, templatePrefix); found {
		rule["promptTemplate"] = recoverPromptTemplatePlaceholders(rest)
		return
	}
	rule["inputExpression"] = v
}

func copyRuleIdentity(eh, rule map[string]any) {
	if v, _ := eh["default_user_identity"].(string); v != "" && !strings.HasPrefix(v, translatorIdentityPrefix) {
		rule["defaultUserIdentity"] = v
	}
	if v, _ := eh["user_identity_expression"].(string); v != "" {
		rule["userIdentityExpression"] = v
	}
	if v, _ := eh["run_as"].(string); v != "" {
		rule["runAs"] = v
	}
	if v, ok := eh["forward_context"]; ok {
		rule["forwardContext"] = v
	}
}

func copyRuleStructuredInvocation(eh, rule map[string]any) {
	si, ok := eh["structured_invocation"].(map[string]any)
	if !ok {
		return
	}
	siBlock := map[string]any{}
	if v, ok := si["input_schema"]; ok {
		siBlock["inputSchema"] = v
	}
	if v, ok := si["output_schema"]; ok {
		siBlock["outputSchema"] = v
	}
	if len(siBlock) > 0 {
		rule["structuredInvocation"] = siBlock
	}
}

func copyRuleAckPolicy(eh, rule map[string]any) {
	ack, ok := eh["acknowledgment_policy"].(map[string]any)
	if !ok {
		return
	}
	ackBlock := map[string]any{}
	if v, _ := ack["mode"].(string); v != "" {
		ackBlock["mode"] = v
	}
	if v, ok := ack["timeout_seconds"]; ok {
		ackBlock["timeoutSeconds"] = v
	}
	if onFail, ok := ack["on_failure"].(map[string]any); ok {
		if ofBlock := buildOnFailureBlock(onFail); len(ofBlock) > 0 {
			ackBlock["onFailure"] = ofBlock
		}
	}
	if len(ackBlock) > 0 {
		rule["acknowledgmentPolicy"] = ackBlock
	}
}

func buildOnFailureBlock(onFail map[string]any) map[string]any {
	out := map[string]any{}
	if v, _ := onFail["action"].(string); v != "" {
		out["action"] = v
	}
	if v, _ := onFail["nack_outcome"].(string); v != "" {
		out["nackOutcome"] = v
	}
	return out
}

func copyRuleArtifactProcessing(eh, rule map[string]any) {
	ap, ok := eh["artifact_processing"].(map[string]any)
	if !ok {
		return
	}
	apBlock := map[string]any{}
	if v, _ := ap["extract_artifacts_expression"].(string); v != "" {
		apBlock["extractExpression"] = v
	}
	if def, ok := ap["artifact_definition"].(map[string]any); ok {
		if defBlock := buildArtifactDefinitionBlock(def); len(defBlock) > 0 {
			apBlock["artifactDefinition"] = defBlock
		}
	}
	if len(apBlock) > 0 {
		rule["artifactProcessing"] = apBlock
	}
}

func buildArtifactDefinitionBlock(def map[string]any) map[string]any {
	out := map[string]any{}
	if v, _ := def["filename"].(string); v != "" {
		out["filename"] = v
	}
	if v, _ := def["content"].(string); v != "" {
		out["content"] = v
	}
	if v, _ := def["mime_type"].(string); v != "" {
		out["mimeType"] = v
	}
	if v, _ := def["content_encoding"].(string); v != "" {
		out["contentEncoding"] = v
	}
	return out
}

func attachOutputs(eh map[string]any, outputByName map[string]map[string]any, rule map[string]any) {
	if name, _ := eh["on_success"].(string); name != "" {
		if oh, ok := outputByName[name]; ok {
			rule["successOutput"] = buildOutputFromHandler(oh)
		}
	}
	if name, _ := eh["on_error"].(string); name != "" {
		if oh, ok := outputByName[name]; ok {
			rule["errorOutput"] = buildOutputFromHandler(oh)
		}
	}
}

func buildOutputFromHandler(oh map[string]any) map[string]any {
	out := map[string]any{
		"enabled": true,
	}

	if topicExpr, _ := oh["topic_expression"].(string); topicExpr != "" {
		switch {
		case strings.HasPrefix(topicExpr, staticPrefix):
			out["topic"] = strings.TrimPrefix(topicExpr, staticPrefix)
			out["topicType"] = "static"
		case strings.HasPrefix(topicExpr, templatePrefix):
			out["topic"] = strings.TrimPrefix(topicExpr, templatePrefix)
			out["topicType"] = "dynamic"
		default:
			out["topic"] = topicExpr
			out["topicType"] = "static"
		}
	}

	if payloadExpr, _ := oh["payload_expression"].(string); payloadExpr != "" {
		switch payloadExpr {
		case "task_response:text":
			out["responseType"] = "text"
		case "task_response:a2a_task_response":
			out["responseType"] = "full"
		case "task_response:structured_output":
			out["responseType"] = "structured"
		case "task_response:a2a_task_response.error":
			out["responseType"] = "error"
		default:
			out["responseType"] = "custom"
			out["customExpression"] = payloadExpr
		}
	}

	if v, _ := oh["payload_format"].(string); v != "" {
		out["format"] = v
	}
	if v, _ := oh["payload_encoding"].(string); v != "" {
		out["payloadEncoding"] = v
	}
	if v, ok := oh["output_schema"]; ok {
		out["outputSchema"] = v
	}
	if v, _ := oh["on_validation_error"].(string); v != "" {
		out["onValidationError"] = v
	}
	if v, ok := oh["max_file_size_for_base64_bytes"]; ok {
		out["maxFileSizeForBase64Bytes"] = v
	}
	if v, ok := oh["output_transforms"].([]any); ok && len(v) > 0 {
		out["outputTransforms"] = v
	}
	if v, _ := oh["delivery_mode"].(string); v != "" {
		out["deliveryMode"] = v
	}
	if v := authoredValueMap(oh["headers"]); v != nil {
		out["headers"] = v
	}
	if v := authoredValueMap(oh["user_properties"]); v != nil {
		out["userProperties"] = v
	}

	return out
}

func literalTemplateWarnings(outputByName map[string]map[string]any) []string {
	var warnings []string
	for _, handlerName := range slices.Sorted(maps.Keys(outputByName)) {
		oh := outputByName[handlerName]
		for _, field := range []string{"headers", "user_properties"} {
			values, _ := oh[field].(map[string]any)
			for _, key := range slices.Sorted(maps.Keys(values)) {
				s, _ := values[key].(string)
				if literal, isStatic := strings.CutPrefix(s, staticPrefix); isStatic && strings.Contains(literal, "{{") {
					warnings = append(warnings, fmt.Sprintf(
						"event_mesh: output handler %q: %s.%s is a literal containing \"{{\", which the authored form reads as a template; rewrite the value without \"{{\"",
						handlerName, field, key))
				}
			}
		}
	}
	return warnings
}

func authoredValueMap(raw any) map[string]any {
	runtime, _ := raw.(map[string]any)
	if len(runtime) == 0 {
		return nil
	}
	out := make(map[string]any, len(runtime))
	for k, v := range runtime {
		out[k] = solaceheaders.AuthoredValue(v)
	}
	return out
}

func recoverPromptTemplatePlaceholders(s string) string {
	s = strings.ReplaceAll(s, "{{json://input.payload}}", "{payload}")
	s = strings.ReplaceAll(s, "{{text://input.payload}}", "{payload}")
	s = strings.ReplaceAll(s, "{{text://input.topic}}", "{topic}")
	const prefix = "{{text://input.payload:"
	for {
		idx := strings.Index(s, prefix)
		if idx < 0 {
			break
		}
		end := strings.Index(s[idx:], "}}")
		if end < 0 {
			break
		}
		inner := s[idx+len(prefix) : idx+end]
		s = s[:idx] + "{payload." + inner + "}" + s[idx+end+2:]
	}
	return s
}

func copyStringField(src map[string]any, srcKey string, dst map[string]any, dstKey string) {
	if v, _ := src[srcKey].(string); v != "" {
		dst[dstKey] = v
	}
}
