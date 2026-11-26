// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/eventmesh/solaceheaders"
)

var payloadFieldRegex = regexp.MustCompile(`\{payload\.([a-zA-Z0-9_.]+)\}`)

func translateEventRules(rules []any) (eventHandlers []map[string]any, outputHandlers []map[string]any) {
	for _, raw := range rules {
		rule, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		handler := buildEventHandler(rule)

		if successOutput, ok := rule["successOutput"].(map[string]any); ok {
			if enabled, _ := successOutput["enabled"].(bool); enabled {
				name := fmt.Sprintf("%s_success", rule["name"])
				outputHandlers = append(outputHandlers, buildOutputHandler(name, successOutput, successDefaultResponseType(handler)))
				handler["on_success"] = name
			}
		}

		if errorOutput, ok := rule["errorOutput"].(map[string]any); ok {
			if enabled, _ := errorOutput["enabled"].(bool); enabled {
				name := fmt.Sprintf("%s_error", rule["name"])
				outputHandlers = append(outputHandlers, buildOutputHandler(name, errorOutput, "error"))
				handler["on_error"] = name
			}
		}

		eventHandlers = append(eventHandlers, handler)
	}
	return eventHandlers, outputHandlers
}

func successDefaultResponseType(handler map[string]any) string {
	_, structured := handler["structured_invocation"]
	_, toAgent := handler["target_agent_name"]
	if structured && toAgent {
		return "structured"
	}
	return "text"
}

func buildEventHandler(rule map[string]any) map[string]any {
	messageFormat, _ := rule["messageFormat"].(string)
	if messageFormat == "" {
		messageFormat = "json"
	}

	handler := map[string]any{
		"name":           rule["name"],
		"payload_format": messageFormat,
	}
	if subs, ok := rule["subscriptions"]; ok {
		handler["subscriptions"] = subs
	}
	if q, _ := rule["existingQueueName"].(string); q != "" {
		handler["existing_queue_name"] = q
	}
	if enc, _ := rule["payloadEncoding"].(string); enc != "" {
		handler["payload_encoding"] = enc
	}
	if filter := rule["eventFilter"]; filter != nil {
		handler["event_filter"] = filter
	}

	emitHandlerTarget(rule, handler, messageFormat)
	emitHandlerStructuredInvocation(rule, handler)
	emitHandlerAckPolicy(rule, handler)
	emitHandlerIdentity(rule, handler)
	emitHandlerArtifactProcessing(rule, handler)

	return handler
}

func emitHandlerTarget(rule, handler map[string]any, messageFormat string) {
	if targetWorkflow, _ := rule["targetWorkflowName"].(string); targetWorkflow != "" {
		handler["target_workflow_name"] = targetWorkflow

		if inputExpr, _ := rule["inputExpression"].(string); inputExpr != "" {
			handler["input_expression"] = inputExpr
		} else if promptTemplate, _ := rule["promptTemplate"].(string); promptTemplate != "" {
			handler["input_expression"] = translatePromptTemplate(promptTemplate, messageFormat)
		}
		return
	}

	targetAgent, _ := rule["targetAgent"].(string)
	if targetAgent == "" {
		targetAgent = builtinOrchestratorName
	}
	handler["target_agent_name"] = targetAgent

	promptTemplate, _ := rule["promptTemplate"].(string)
	inputExpr := translatePromptTemplate(promptTemplate, messageFormat)
	if responseFormat, _ := rule["responseFormat"].(string); responseFormat != "" {
		inputExpr += "\n\nResponse Format:\n" + responseFormat
	}
	handler["input_expression"] = inputExpr
}

func emitHandlerStructuredInvocation(rule, handler map[string]any) {
	si, ok := rule["structuredInvocation"].(map[string]any)
	if !ok {
		return
	}
	siBlock := make(map[string]any)
	if is, ok := si["inputSchema"].(map[string]any); ok {
		siBlock["input_schema"] = is
	}
	if os, ok := si["outputSchema"].(map[string]any); ok {
		siBlock["output_schema"] = os
	}
	if len(siBlock) > 0 {
		handler["structured_invocation"] = siBlock
	}
}

func emitHandlerAckPolicy(rule, handler map[string]any) {
	ackPolicy, ok := rule["acknowledgmentPolicy"].(map[string]any)
	if !ok {
		return
	}
	mode, _ := ackPolicy["mode"].(string)
	if mode != "on_completion" {
		return
	}
	ackBlock := map[string]any{"mode": "on_completion"}
	if ts, ok := ackPolicy["timeoutSeconds"]; ok {
		ackBlock["timeout_seconds"] = ts
	}
	if onFailure, ok := ackPolicy["onFailure"].(map[string]any); ok {
		if failureBlock := emitOnFailureBlock(onFailure); len(failureBlock) > 0 {
			ackBlock["on_failure"] = failureBlock
		}
	}
	handler["acknowledgment_policy"] = ackBlock
}

func emitOnFailureBlock(onFailure map[string]any) map[string]any {
	out := make(map[string]any)
	if action, _ := onFailure["action"].(string); action != "" {
		out["action"] = action
	}
	if outcome, _ := onFailure["nackOutcome"].(string); outcome != "" {
		out["nack_outcome"] = outcome
	}
	return out
}

func emitHandlerIdentity(rule, handler map[string]any) {
	if id := getNonEmpty(rule, "defaultUserIdentity"); id != "" {
		handler["default_user_identity"] = id
	}
	handler["run_as"] = getNonEmpty(rule, "runAs")
	if expr, _ := rule["userIdentityExpression"].(string); expr != "" {
		handler["user_identity_expression"] = expr
	}
	if fc := rule["forwardContext"]; fc != nil {
		handler["forward_context"] = fc
	}
}

func emitHandlerArtifactProcessing(rule, handler map[string]any) {
	ap, ok := rule["artifactProcessing"].(map[string]any)
	if !ok {
		return
	}
	apBlock := make(map[string]any)
	if expr, _ := ap["extractExpression"].(string); expr != "" {
		apBlock["extract_artifacts_expression"] = expr
	}
	if def, ok := ap["artifactDefinition"].(map[string]any); ok {
		if defBlock := emitArtifactDefinitionBlock(def); len(defBlock) > 0 {
			apBlock["artifact_definition"] = defBlock
		}
	}
	if len(apBlock) > 0 {
		handler["artifact_processing"] = apBlock
	}
}

func emitArtifactDefinitionBlock(def map[string]any) map[string]any {
	out := make(map[string]any)
	if v, _ := def["filename"].(string); v != "" {
		out["filename"] = v
	}
	if v, _ := def["content"].(string); v != "" {
		out["content"] = v
	}
	if v, _ := def["mimeType"].(string); v != "" {
		out["mime_type"] = v
	}
	if v, _ := def["contentEncoding"].(string); v != "" {
		out["content_encoding"] = v
	}
	return out
}

func getNonEmpty(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func translatePromptTemplate(template, messageFormat string) string {
	if template == "" {
		return "template:{{json://input.payload}}"
	}

	result := payloadFieldRegex.ReplaceAllString(template, "{{text://input.payload:$1}}")

	payloadAccessor := "json"
	if messageFormat != "json" {
		payloadAccessor = "text"
	}
	result = strings.ReplaceAll(result, "{payload}", fmt.Sprintf("{{%s://input.payload}}", payloadAccessor))
	result = strings.ReplaceAll(result, "{topic}", "{{text://input.topic}}")

	return "template:" + result
}

func translateResponseType(responseType, customExpression string) string {
	if responseType == "custom" && customExpression != "" {
		return customExpression
	}
	switch responseType {
	case "text":
		return "task_response:text"
	case "full", "full_json":
		return "task_response:a2a_task_response"
	case "structured":
		return "task_response:structured_output"
	case "error", "error_json":
		return "task_response:a2a_task_response.error"
	default:
		return "task_response:text"
	}
}

func translateTopic(topic, topicType string) string {
	if topicType == "static" {
		return "static:" + topic
	}
	return "template:" + topic
}

func buildOutputHandler(name string, config map[string]any, defaultResponseType string) map[string]any {
	topicType, _ := config["topicType"].(string)
	if topicType == "" {
		topicType = "static"
	}
	topic, _ := config["topic"].(string)

	responseType, _ := config["responseType"].(string)
	if responseType == "" {
		responseType = defaultResponseType
	}
	customExpr, _ := config["customExpression"].(string)

	handler := map[string]any{
		"name":               name,
		"topic_expression":   translateTopic(topic, topicType),
		"payload_expression": translateResponseType(responseType, customExpr),
	}

	if format, _ := config["format"].(string); format != "" {
		handler["payload_format"] = format
	}
	if enc, _ := config["payloadEncoding"].(string); enc != "" {
		handler["payload_encoding"] = enc
	}
	if outSchema := config["outputSchema"]; outSchema != nil {
		handler["output_schema"] = outSchema
	}
	if v, _ := config["onValidationError"].(string); v != "" {
		handler["on_validation_error"] = v
	}
	if v, ok := config["maxFileSizeForBase64Bytes"]; ok {
		handler["max_file_size_for_base64_bytes"] = v
	}
	if transforms, ok := config["outputTransforms"].([]any); ok && len(transforms) > 0 {
		handler["output_transforms"] = transforms
	}
	if mode, _ := config["deliveryMode"].(string); mode != "" {
		handler["delivery_mode"] = mode
	}
	if headers := runtimeValueMap(config["headers"]); headers != nil {
		handler["headers"] = headers
	}
	if props := runtimeValueMap(config["userProperties"]); props != nil {
		handler["user_properties"] = props
	}

	return handler
}

func runtimeValueMap(raw any) map[string]any {
	authored, _ := raw.(map[string]any)
	if len(authored) == 0 {
		return nil
	}
	out := make(map[string]any, len(authored))
	for k, v := range authored {
		out[k] = solaceheaders.RuntimeValue(v)
	}
	return out
}
