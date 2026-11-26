// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package migrate

import (
	"strings"
	"testing"
)

const fullFeatureLegacyEventMeshYAML = `
apps:
  - name: sfdc-case-events__app
    app_module: sam_event_mesh_gateway.app
    app_config:
      namespace: prod
      gateway_id: sfdc-case-event-gw-01
      default_user_identity: sfdc_event_user
      event_mesh_broker_config:
        broker_url: tcps://dataplane.example.com:55443
        broker_vpn: msgvpn-247gk4n25g8
        broker_username: sam-enterprise-gateway
        broker_password: "${DATAPLANE_SOLACE_BROKER_PASSWORD}"
        tls_skip_verify: false

      event_handlers:
        - name: sfdc_case_creation_handler
          subscriptions:
            - topic: salesforce/case/created/v1
              qos: 1
          payload_format: json
          payload_encoding: utf-8
          target_workflow_name: SupportCaseAdvisorWorkflow
          input_expression: input.payload
          user_identity_expression: input.user_properties.requester
          forward_context:
            requested_by: input.user_properties.requester
          structured_invocation:
            output_schema:
              type: object
              required: ["CaseId__c"]
              properties:
                CaseId__c:
                  type: string
          acknowledgment_policy:
            mode: on_completion
            timeout_seconds: 600
            on_failure:
              action: nack
              nack_outcome: failed
          artifact_processing:
            extract_artifacts_expression: input.payload.attachments
            artifact_definition:
              filename: input.iteration.name
              content: input.iteration.bytes
              mime_type: input.iteration.contentType
              content_encoding: base64
          on_success: enriched_handler
          on_error: error_handler

      output_handlers:
        - name: enriched_handler
          topic_expression: "static:samdataplane/case/enriched/v1"
          payload_expression: "task_response:structured_output"
          payload_format: json
          payload_encoding: utf-8
          output_schema:
            type: object
            required: ["AgenticContent__c"]
          on_validation_error: log
          max_file_size_for_base64_bytes: 10485760
          output_transforms:
            - type: copy
              source_expression: input.payload.foo
              dest_expression: input.payload.bar

        - name: error_handler
          topic_expression: "static:samdataplane/case/enriched/v1/errors"
          payload_expression: "task_response:a2a_task_response.error"
          payload_format: json
`

func TestMigrateGateway_EventMesh_FullFeatureRoundtrip(t *testing.T) {
	clean, warnings, err := MigrateGateway(parseDoc(t, fullFeatureLegacyEventMeshYAML))
	if err != nil {
		t.Fatalf("MigrateGateway: %v", err)
	}
	if clean.Kind != "entrypoint" || clean.Name != "sfdc-case-events" {
		t.Errorf("identity = %s/%s; want entrypoint/sfdc-case-events", clean.Kind, clean.Name)
	}

	if got := clean.Spec["type"]; got != "event_mesh" {
		t.Errorf("type = %v; want event_mesh", got)
	}

	values := clean.Spec["values"].(map[string]any)

	if _, ok := values["broker_connection_type"]; ok {
		t.Errorf("broker_connection_type must no longer be emitted, got %v", values["broker_connection_type"])
	}
	if values["broker_url"] != "tcps://dataplane.example.com:55443" {
		t.Errorf("broker_url = %v", values["broker_url"])
	}
	if values["broker_vpn"] != "msgvpn-247gk4n25g8" {
		t.Errorf("broker_vpn = %v", values["broker_vpn"])
	}
	if values["broker_username"] != "sam-enterprise-gateway" {
		t.Errorf("broker_username = %v", values["broker_username"])
	}
	if values["broker_password"] != "${DATAPLANE_SOLACE_BROKER_PASSWORD}" {
		t.Errorf("broker_password = %v", values["broker_password"])
	}
	if v, ok := values["tls_skip_verify"]; !ok || v == nil {
		t.Errorf("tls_skip_verify lost; got %v", values["tls_skip_verify"])
	}

	rules := values["event_rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("event_rules length = %d; want 1", len(rules))
	}
	rule := rules[0].(map[string]any)

	if rule["name"] != "sfdc_case_creation_handler" {
		t.Errorf("rule name = %v", rule["name"])
	}
	if rule["messageFormat"] != "json" {
		t.Errorf("messageFormat = %v; want json (from payload_format)", rule["messageFormat"])
	}
	if rule["payloadEncoding"] != "utf-8" {
		t.Errorf("payloadEncoding = %v", rule["payloadEncoding"])
	}

	if rule["targetWorkflowName"] != "SupportCaseAdvisorWorkflow" {
		t.Errorf("targetWorkflowName = %v", rule["targetWorkflowName"])
	}

	if rule["inputExpression"] != "input.payload" {
		t.Errorf("inputExpression = %v", rule["inputExpression"])
	}
	if _, has := rule["promptTemplate"]; has {
		t.Errorf("promptTemplate should not be set for raw inputExpression path")
	}

	if rule["defaultUserIdentity"] != "sfdc_event_user" {
		t.Errorf("defaultUserIdentity = %v; want sfdc_event_user (backfilled from gateway-level)", rule["defaultUserIdentity"])
	}
	if rule["userIdentityExpression"] != "input.user_properties.requester" {
		t.Errorf("userIdentityExpression = %v", rule["userIdentityExpression"])
	}

	if fc, ok := rule["forwardContext"].(map[string]any); !ok || fc["requested_by"] != "input.user_properties.requester" {
		t.Errorf("forwardContext lost or wrong shape: %v", rule["forwardContext"])
	}

	si := rule["structuredInvocation"].(map[string]any)
	if _, ok := si["outputSchema"]; !ok {
		t.Errorf("structuredInvocation.outputSchema lost")
	}

	ack := rule["acknowledgmentPolicy"].(map[string]any)
	if ack["mode"] != "on_completion" {
		t.Errorf("acknowledgmentPolicy.mode = %v", ack["mode"])
	}
	if ack["timeoutSeconds"] != 600 {
		t.Errorf("acknowledgmentPolicy.timeoutSeconds = %v (%T)", ack["timeoutSeconds"], ack["timeoutSeconds"])
	}
	of := ack["onFailure"].(map[string]any)
	if of["action"] != "nack" || of["nackOutcome"] != "failed" {
		t.Errorf("acknowledgmentPolicy.onFailure = %v", of)
	}

	ap := rule["artifactProcessing"].(map[string]any)
	if ap["extractExpression"] != "input.payload.attachments" {
		t.Errorf("artifactProcessing.extractExpression = %v", ap["extractExpression"])
	}
	def := ap["artifactDefinition"].(map[string]any)
	if def["filename"] != "input.iteration.name" || def["mimeType"] != "input.iteration.contentType" || def["contentEncoding"] != "base64" {
		t.Errorf("artifactProcessing.artifactDefinition = %v", def)
	}

	so := rule["successOutput"].(map[string]any)
	if so["enabled"] != true {
		t.Errorf("successOutput.enabled = %v", so["enabled"])
	}
	if so["topic"] != "samdataplane/case/enriched/v1" || so["topicType"] != "static" {
		t.Errorf("successOutput topic mapping wrong: %v / %v", so["topic"], so["topicType"])
	}
	if so["responseType"] != "structured" {
		t.Errorf("successOutput.responseType = %v; want structured", so["responseType"])
	}
	if _, has := so["customExpression"]; has {
		t.Errorf("structured responseType should not carry customExpression")
	}
	if so["format"] != "json" || so["payloadEncoding"] != "utf-8" {
		t.Errorf("successOutput format/encoding lost: %v / %v", so["format"], so["payloadEncoding"])
	}
	if _, ok := so["outputSchema"]; !ok {
		t.Errorf("successOutput.outputSchema lost")
	}
	if so["onValidationError"] != "log" {
		t.Errorf("successOutput.onValidationError = %v", so["onValidationError"])
	}
	if so["maxFileSizeForBase64Bytes"] != 10485760 {
		t.Errorf("successOutput.maxFileSizeForBase64Bytes = %v", so["maxFileSizeForBase64Bytes"])
	}
	transforms, ok := so["outputTransforms"].([]any)
	if !ok || len(transforms) != 1 {
		t.Errorf("successOutput.outputTransforms wrong: %v", so["outputTransforms"])
	}

	eo := rule["errorOutput"].(map[string]any)
	if eo["responseType"] != "error" {
		t.Errorf("errorOutput.responseType = %v; want error", eo["responseType"])
	}

	var hasGatewayIDWarning bool
	for _, w := range warnings {
		if strings.Contains(w, "gateway_id") {
			hasGatewayIDWarning = true
		}
	}
	if !hasGatewayIDWarning {
		t.Errorf("expected gateway_id warning; got %v", warnings)
	}
}

func TestMigrateGateway_EventMesh_PromptTemplateRecovery(t *testing.T) {
	const src = `
apps:
  - name: webhook__app
    app_module: sam_event_mesh_gateway.app
    app_config:
      gateway_adapter: sam_event_mesh_gateway_adapter.adapter.EventMeshAdapter
      event_handlers:
        - name: hook
          subscriptions:
            - topic: events/orders/>
          target_agent_name: OrderAgent
          input_expression: "template:Process this order: {{json://input.payload}} from topic {{text://input.topic}}, id {{text://input.payload:id}}"
`
	clean, _, err := MigrateGateway(parseDoc(t, src))
	if err != nil {
		t.Fatalf("MigrateGateway: %v", err)
	}
	values := clean.Spec["values"].(map[string]any)
	rules := values["event_rules"].([]any)
	rule := rules[0].(map[string]any)

	if rule["targetAgent"] != "OrderAgent" {
		t.Errorf("targetAgent = %v", rule["targetAgent"])
	}
	want := "Process this order: {payload} from topic {topic}, id {payload.id}"
	if got, _ := rule["promptTemplate"].(string); got != want {
		t.Errorf("promptTemplate recovery wrong:\n got: %q\nwant: %q", got, want)
	}
	if _, has := rule["inputExpression"]; has {
		t.Errorf("inputExpression should NOT be set when promptTemplate recovered from template:-prefixed expression")
	}
}
