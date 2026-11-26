// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestDetectComponentType(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			name:   "agent detected by agent_name",
			config: map[string]any{"agent_name": "MyAgent", "instruction": "do stuff"},
			want:   "agent",
		},
		{
			name:   "workflow detected by agent_type workflow",
			config: map[string]any{"agent_name": "WF", "agent_type": "workflow"},
			want:   "workflow",
		},
		{
			name:   "workflow detection is case-insensitive",
			config: map[string]any{"agent_name": "WF", "agent_type": "Workflow"},
			want:   "workflow",
		},
		{
			name:   "gateway detected by event_handlers",
			config: map[string]any{"event_handlers": []any{}},
			want:   "gateway",
		},
		{
			name:   "gateway detected by fastapi_port without agent_name",
			config: map[string]any{"fastapi_port": 8080},
			want:   "gateway",
		},
		{
			name:   "agent with fastapi_port and agent_name is still agent",
			config: map[string]any{"agent_name": "A", "fastapi_port": 8080},
			want:   "agent",
		},
		{
			name:   "gateway fallback when no agent_name",
			config: map[string]any{"namespace": "ns"},
			want:   "gateway",
		},
		{
			name:   "event mesh gateway with event_mesh_broker_config",
			config: map[string]any{"event_mesh_broker_config": map[string]any{"broker_url": "tcp://x"}, "event_handlers": []any{}},
			want:   "gateway",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectComponentType(tt.config)
			if got != tt.want {
				t.Errorf("DetectComponentType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSchemaContent_AllNames(t *testing.T) {
	for _, name := range SchemaNames {
		t.Run(name, func(t *testing.T) {
			data, err := SchemaContent(name)
			if err != nil {
				t.Fatalf("SchemaContent(%q) error: %v", name, err)
			}
			if len(data) == 0 {
				t.Fatalf("SchemaContent(%q) returned empty", name)
			}
			content := string(data)
			if !strings.Contains(content, "type:") && !strings.Contains(content, "\"type\"") {
				t.Errorf("schema %q missing type declaration", name)
			}
		})
	}
}

func TestSchemaContent_Unknown(t *testing.T) {
	_, err := SchemaContent("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown schema")
	}
}

func TestSchemaFiles_AllPresent(t *testing.T) {
	files := SchemaFiles()
	for _, name := range SchemaNames {
		key := name + ".schema.yaml"
		if _, ok := files[key]; !ok {
			t.Errorf("SchemaFiles() missing %s", key)
		}
	}
}

func TestSchemaFiles_NoTrigger(t *testing.T) {
	files := SchemaFiles()
	if _, ok := files["trigger.schema.yaml"]; ok {
		t.Error("SchemaFiles() should not contain trigger.schema.yaml (triggers replaced by EM gateways)")
	}
}

func TestSchemaContentFiltered_AllNames(t *testing.T) {
	for _, name := range SchemaNames {
		t.Run(name, func(t *testing.T) {
			data, err := SchemaContentFiltered(name)
			if err != nil {
				t.Fatalf("SchemaContentFiltered(%q) error: %v", name, err)
			}
			if len(data) == 0 {
				t.Fatalf("SchemaContentFiltered(%q) returned empty", name)
			}
		})
	}
}

func TestSchemaContentFiltered_AgentNoSystemFields(t *testing.T) {
	data, err := SchemaContentFiltered("agent")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	systemFields := []string{"namespace:", "display_name:", "session_service:", "artifact_service:",
		"agent_card_publishing:", "agent_discovery:", "inject_system_purpose:", "inject_response_format:",
		"inject_user_profile:", "global_instruction:"}
	for _, field := range systemFields {
		if strings.Contains(content, field) {
			t.Errorf("filtered agent schema should not contain %q", field)
		}
	}
	if strings.Contains(content, "x-sam-system") {
		t.Error("filtered schema should not contain x-sam-system annotations")
	}
}

func TestSchemaContentFiltered_AgentRequiredUpdated(t *testing.T) {
	data, err := SchemaContentFiltered("agent")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "agent_name") {
		t.Error("agent_name should still be present")
	}
	if !strings.Contains(content, "agent_card") {
		t.Error("agent_card should still be present")
	}
}

func TestSchemaContentFiltered_GatewayOnlyEMFields(t *testing.T) {
	data, err := SchemaContentFiltered("gateway")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	emFields := []string{"event_mesh_broker_config:", "event_handlers:", "output_handlers:", "acknowledgment_policy:"}
	for _, field := range emFields {
		if !strings.Contains(content, field) {
			t.Errorf("filtered gateway schema should contain %q", field)
		}
	}

	systemFields := []string{"namespace:", "fastapi_port:", "session_service:", "artifact_service:",
		"platform_service:", "frontend_welcome_message:"}
	for _, field := range systemFields {
		if strings.Contains(content, field) {
			t.Errorf("filtered gateway schema should not contain %q", field)
		}
	}
}

func TestSchemaFilesFiltered_AllPresent(t *testing.T) {
	files := SchemaFilesFiltered()
	for _, name := range SchemaNames {
		key := name + ".schema.yaml"
		if _, ok := files[key]; !ok {
			t.Errorf("SchemaFilesFiltered() missing %s", key)
		}
	}
}

func TestValidateConfig_ValidEventMeshGateway(t *testing.T) {
	yaml := `apps:
  - name: test_em_gw
    app_config:
      event_mesh_broker_config:
        broker_url: "tcps://broker.example.com"
        broker_vpn: "default"
        broker_username: "user"
        broker_password: "pass"
      event_handlers:
        - name: order_handler
          subscriptions:
            - topic: "orders/created/>"
              qos: 1
          input_expression: "input.payload:"
          target_agent_name: OrderProcessor
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
	if r.ComponentType != "gateway" {
		t.Errorf("component type = %q, want %q", r.ComponentType, "gateway")
	}
}

func TestValidateConfig_EventMeshGatewayWithOutputHandlers(t *testing.T) {
	yaml := `apps:
  - name: full_em_gw
    app_config:
      event_mesh_broker_config:
        broker_url: "tcps://broker.example.com"
      event_handlers:
        - name: orders
          subscriptions:
            - topic: "orders/created/>"
              qos: 1
          input_expression: "input.payload:"
          target_agent_name: OrderProcessor
          on_success: order_result
          on_error: order_error
          default_user_identity: "system"
          forward_context:
            order_id: "input.payload:order_id"
      output_handlers:
        - name: order_result
          topic_expression: "template:orders/{{text://user_data.forward_context:order_id}}/processed"
          payload_expression: "input.payload:text"
          payload_format: json
          delivery_mode: guaranteed
          headers:
            solace_dmqEligible: true
            solace_timeToLive: 30000
            solace_correlationId: "template:{{text://user_data.forward_context:order_id}}"
          user_properties:
            source: "static:sam"
            attempt: 1
        - name: order_error
          topic_expression: "static:orders/errors"
          payload_expression: "input.payload:text"
      acknowledgment_policy:
        mode: on_completion
        timeout_seconds: 300
        on_failure:
          action: nack
          nack_outcome: rejected
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
}

func TestValidateConfig_EventMeshOutputDeliveryModeEnum(t *testing.T) {
	yaml := `apps:
  - name: em_gw
    app_config:
      event_mesh_broker_config:
        broker_url: "tcps://broker.example.com"
      output_handlers:
        - name: out
          topic_expression: "static:out"
          delivery_mode: persistent
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected delivery_mode persistent to be rejected")
	}
	if got := fmt.Sprint(r.Errors); !strings.Contains(got, "delivery_mode") {
		t.Errorf("errors = %s, want one about delivery_mode", got)
	}
}

func TestValidateConfig_InvalidYAML(t *testing.T) {
	r, err := ValidateConfig([]byte("not: [valid: yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid for malformed YAML")
	}
}

func TestValidateConfig_MissingApps(t *testing.T) {
	r, err := ValidateConfig([]byte("something: else\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid for missing apps key")
	}
}

func TestValidateConfig_EmptyApps(t *testing.T) {
	r, err := ValidateConfig([]byte("apps: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid for empty apps array")
	}
}

func TestValidateConfig_ValidAgent(t *testing.T) {
	yaml := `apps:
  - name: test_agent
    app_config:
      agent_name: "TestAgent"
      description: "A test agent that does testing things for the test suite"
      instruction: "You are a test agent. You help with testing the validation schema. You should respond to all requests with helpful and accurate information about testing procedures and best practices."
      agent_card:
        description: "A test agent"
        skills:
          - id: "test"
            name: "Test"
            description: "A test skill"
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid agent, got errors: %v", r.Errors)
	}
	if r.ComponentType != "agent" {
		t.Errorf("component type = %q, want %q", r.ComponentType, "agent")
	}
}

func TestValidateConfig_WorkflowToolNode(t *testing.T) {
	yaml := `apps:
  - name: time_report
    app_config:
      agent_name: "TimeReportToolNode"
      agent_type: workflow
      description: "Workflow that calls a tool node directly for schema validation testing"
      tools:
        - tool_type: builtin
          tool_name: get_current_time
      connectors:
        - name: ops_email
          type: email
          subtype: smtp
      nodes:
        - id: fetch_time
          type: tool
          tool_name: get_current_time
          input:
            timezone: "UTC"
      output_mapping:
        time: "{{fetch_time.output.message}}"
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid workflow with a tool node, got errors: %v", r.Errors)
	}
	if r.ComponentType != "workflow" {
		t.Errorf("component type = %q, want %q", r.ComponentType, "workflow")
	}
}

func TestValidateConfig_WorkflowRejectsConnectorMissingRequired(t *testing.T) {
	yaml := `apps:
  - name: bad_conn_wf
    app_config:
      agent_name: "BadConnectorWorkflow"
      agent_type: workflow
      description: "Workflow whose connector omits required fields for negative testing"
      connectors:
        - subtype: smtp
      nodes:
        - id: n1
          type: agent
          agent_name: Helper
      output_mapping:
        x: "{{n1.output.message}}"
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid workflow for a connector missing required name/type, got valid")
	}
}

func TestValidateConfig_WorkflowRejectsUnknownNodeType(t *testing.T) {
	yaml := `apps:
  - name: bad_wf
    app_config:
      agent_name: "BadWorkflow"
      agent_type: workflow
      description: "Workflow with an unknown node type for negative schema testing"
      nodes:
        - id: n1
          type: banana
      output_mapping:
        x: "{{n1.output.message}}"
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid workflow for unknown node type 'banana', got valid")
	}
	pinsNodeType := false
	for _, e := range r.Errors {
		if strings.Contains(e.Path, "nodes") && strings.Contains(e.Path, "type") {
			pinsNodeType = true
		}
	}
	if !pinsNodeType {
		t.Errorf("expected a validation error on a node 'type' field (the enum), got: %v", r.Errors)
	}
}

func TestValidateConfig_AgentWithoutSystemFields(t *testing.T) {
	yaml := `apps:
  - name: test_agent
    app_config:
      agent_name: "TestAgent"
      description: "A test agent that does testing things for the test suite"
      instruction: "You are a test agent. You help with testing the validation schema. You should respond to all requests with helpful and accurate information about testing procedures and best practices."
      agent_card:
        description: "A test agent"
        skills:
          - id: "test"
            name: "Test"
            description: "A test skill"
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid agent without system fields, got errors: %v", r.Errors)
	}
	if r.ComponentType != "agent" {
		t.Errorf("component type = %q, want %q", r.ComponentType, "agent")
	}
}

func TestValidateConfig_EMGatewayWithoutSystemFields(t *testing.T) {
	yaml := `apps:
  - name: em_gw
    app_config:
      event_mesh_broker_config:
        broker_url: "tcps://broker.example.com"
      event_handlers:
        - name: orders
          subscriptions:
            - topic: "orders/>"
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid EM gateway without system fields, got errors: %v", r.Errors)
	}
	if r.ComponentType != "gateway" {
		t.Errorf("component type = %q, want %q", r.ComponentType, "gateway")
	}
}

func TestValidateConfig_EMGatewayRejectsSystemFields(t *testing.T) {
	yaml := `apps:
  - name: em_gw
    app_config:
      namespace: "solace-agent-mesh"
      session_service: { type: memory }
      artifact_service: { type: memory }
      event_mesh_broker_config:
        broker_url: "tcps://broker.example.com"
      event_handlers:
        - name: orders
          subscriptions:
            - topic: "orders/>"
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid — system fields should be rejected by additionalProperties: false")
	}
}

func TestValidateConfig_AgentRejectsSystemFields(t *testing.T) {
	yaml := `apps:
  - name: test_agent
    app_config:
      agent_name: "TestAgent"
      description: "A test agent"
      instruction: "You are a test agent."
      model: "gpt-4o"
      namespace: "solace-agent-mesh"
      agent_card:
        description: "A test agent"
        skills:
          - id: "test"
            name: "Test"
            description: "A test skill"
`
	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid — system fields (model, namespace) should be rejected")
	}
}

func TestValidateConnectorConfig_ValidPostgres(t *testing.T) {
	json := `{
		"name": "My DB",
		"type": "sql",
		"subtype": "postgres",
		"values": {
			"hostname": "localhost",
			"port": 5432,
			"database": "mydb",
			"username": "user",
			"password": "<<__SAM_REQUIRED__>>"
		}
	}`
	r, err := ValidateConnectorConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
	if r.ComponentType != "connector" {
		t.Errorf("component type = %q, want %q", r.ComponentType, "connector")
	}
}

func TestValidateConnectorConfig_ValidMCP(t *testing.T) {
	json := `{
		"name": "My MCP",
		"type": "mcp",
		"subtype": "remote",
		"values": {
			"server_url": "https://example.com/mcp",
			"connection_type": "streamable-http",
			"auth_type": "none"
		}
	}`
	r, err := ValidateConnectorConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
}

func TestValidateConnectorConfig_ValidKnowledgeBase(t *testing.T) {
	json := `{
		"name": "My KB",
		"type": "knowledge_base",
		"subtype": "bedrock",
		"values": {
			"kb_id": "kb-123",
			"region": "us-east-1",
			"auth_type": "access_key",
			"aws_access_key_id": "AKIAIOSFODNN7EXAMPLE",
			"aws_secret_access_key": "<<__SAM_REQUIRED__>>"
		}
	}`
	r, err := ValidateConnectorConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
}

func TestValidateConnectorConfig_ValidOpenAPI(t *testing.T) {
	json := `{
		"name": "My API",
		"type": "api",
		"subtype": "openapi",
		"values": {
			"base_url": "https://example.com",
			"specification_url": "https://example.com/openapi.json",
			"auth_type": "none"
		}
	}`
	r, err := ValidateConnectorConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
}

func TestValidateConnectorConfig_MissingRequiredFields(t *testing.T) {
	json := `{}`
	r, err := ValidateConnectorConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for empty object")
	}
	if len(r.Errors) == 0 {
		t.Fatal("expected errors for missing required fields")
	}
}

func TestValidateConnectorConfig_MissingSubtypeFields(t *testing.T) {
	json := `{
		"name": "Bad DB",
		"type": "sql",
		"subtype": "postgres",
		"values": {}
	}`
	r, err := ValidateConnectorConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for missing per-subtype fields")
	}
	found := false
	for _, e := range r.Errors {
		if strings.Contains(e.Path, "values") || strings.Contains(e.Message, "host") || strings.Contains(e.Message, "required") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about missing values fields, got: %v", r.Errors)
	}
}

func TestValidateConnectorConfig_InvalidType(t *testing.T) {
	json := `{
		"name": "Bad",
		"type": "nosql",
		"subtype": "mongo",
		"values": {}
	}`
	r, err := ValidateConnectorConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for unrecognized type")
	}
}

func TestValidateConnectorConfig_InvalidJSON(t *testing.T) {
	r, err := ValidateConnectorConfig([]byte("not json"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for malformed JSON")
	}
}

func TestValidateConnectorConfig_UndeclaredValuesKeyIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name     string
		values   string
		wantKey  string
		wantPass bool
	}{
		{"host instead of hostname", `"host":"db.example.com","hostname":"db.example.com","port":5432,"database":"d","username":"u","password":"p"`, "host", false},
		{"every key declared", `"hostname":"db.example.com","port":5432,"database":"d","username":"u","password":"p"`, "", true},
		{"runtime override keys are accepted", `"hostname":"db.example.com","database":"d","username":"u","password":"p","tool_name":"orders","tool_description":"Query orders"`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := ValidateConnectorConfig([]byte(`{"name":"My DB","description":"A PostgreSQL database","type":"sql","subtype":"postgres","values":{` + tc.values + `}}`))
			if err != nil {
				t.Fatal(err)
			}
			if r.Valid != tc.wantPass {
				t.Fatalf("Valid = %v, want %v (errors: %v)", r.Valid, tc.wantPass, r.Errors)
			}
			if tc.wantKey == "" {
				return
			}
			var messages []string
			for _, e := range r.Errors {
				messages = append(messages, e.Message)
			}
			if !strings.Contains(strings.Join(messages, " | "), tc.wantKey) {
				t.Errorf("errors = %v, want one naming %q", r.Errors, tc.wantKey)
			}
		})
	}
}

func TestValidateConnectorConfig_PlaceholderPasses(t *testing.T) {
	json := `{
		"name": "DB with placeholders",
		"type": "sql",
		"subtype": "postgres",
		"values": {
			"hostname": "<<__SAM_REQUIRED__>>",
			"port": 5432,
			"database": "<<__SAM_REQUIRED__>>",
			"username": "<<__SAM_REQUIRED__>>",
			"password": "<<__SAM_REQUIRED__>>"
		}
	}`
	r, err := ValidateConnectorConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid with placeholders, got errors: %v", r.Errors)
	}
}

func TestValidateSkillConfig_ValidMinimal(t *testing.T) {
	yml := `
name: my-skill
description: "A test skill for API reference documentation"
instruction_content: |
  # API Reference Skill
  This skill provides reference documentation for the REST API.
  Use it when you need to look up endpoint details.
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
	if r.ComponentType != "skill" {
		t.Errorf("expected component type 'skill', got %q", r.ComponentType)
	}
}

func TestValidateSkillConfig_ValidWithInlineReferences(t *testing.T) {
	yml := `
name: api-ref
description: "API reference with inline docs"
instruction_content: |
  # API Reference
  Load this skill for endpoint documentation.
  Use grep_skill_resources to search for specific endpoints.
references:
  - name: endpoints.md
    content: |
      # Endpoints
      ## GET /users
      Returns a list of users.
  - name: examples.md
    content: |
      # Examples
      curl -X GET /users
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
}

func TestValidateSkillConfig_ValidWithArtifactReferences(t *testing.T) {
	yml := `
name: docs-skill
description: "Skill with uploaded reference documents"
instruction_content: |
  # Documentation Skill
  This skill uses uploaded reference documents.
  Search references with grep_skill_resources.
references:
  - name: user_guide.md
    artifact: uploaded_user_guide
  - name: admin_guide.md
    artifact: uploaded_admin_guide
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
}

func TestValidateSkillConfig_ValidMixedReferences(t *testing.T) {
	yml := `
name: mixed-refs
description: "Skill with both inline and artifact references"
tags:
  - api
  - documentation
examples:
  - "How do I use the REST API?"
  - "Show me the authentication flow"
instruction_content: |
  # Mixed Reference Skill
  This skill has both inline and uploaded references.
references:
  - name: overview.md
    content: |
      # API Overview
      This API provides user management functionality.
  - name: full_spec.md
    artifact: uploaded_openapi_spec
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
}

func TestValidateSkillConfig_MissingName(t *testing.T) {
	yml := `
description: "A skill without a name"
instruction_content: |
  # Some instructions that are long enough to pass validation rules.
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for missing name")
	}
	hasNameErr := false
	for _, e := range r.Errors {
		if strings.Contains(e.Message, "name") {
			hasNameErr = true
		}
	}
	if !hasNameErr {
		t.Errorf("expected error about name, got: %v", r.Errors)
	}
}

func TestValidateSkillConfig_MissingInstructionContent(t *testing.T) {
	yml := `
name: no-content
description: "A skill without instruction content"
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for missing instruction_content")
	}
	hasErr := false
	for _, e := range r.Errors {
		if strings.Contains(e.Message, "instruction_content") {
			hasErr = true
		}
	}
	if !hasErr {
		t.Errorf("expected error about instruction_content, got: %v", r.Errors)
	}
}

func TestValidateSkillConfig_ReferenceBothContentAndArtifact(t *testing.T) {
	yml := `
name: bad-refs
description: "Skill with invalid reference"
instruction_content: |
  # Bad Skill
  This skill has a reference with both content and artifact.
references:
  - name: conflict.md
    content: "inline content"
    artifact: some_artifact
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for reference with both content and artifact")
	}
	hasErr := false
	for _, e := range r.Errors {
		if strings.Contains(e.Message, "not both") {
			hasErr = true
		}
	}
	if !hasErr {
		t.Errorf("expected error about mutually exclusive fields, got: %v", r.Errors)
	}
}

func TestValidateSkillConfig_ReferenceNeitherContentNorArtifact(t *testing.T) {
	yml := `
name: empty-ref
description: "Skill with empty reference"
instruction_content: |
  # Empty Ref Skill
  This skill has a reference with neither content nor artifact.
references:
  - name: nothing.md
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for reference with neither content nor artifact")
	}
	hasErr := false
	for _, e := range r.Errors {
		if strings.Contains(e.Message, "must have either") {
			hasErr = true
		}
	}
	if !hasErr {
		t.Errorf("expected error about missing content/artifact, got: %v", r.Errors)
	}
}

func TestValidateSkillConfig_ValidWithAssets(t *testing.T) {
	yml := `
name: report-skill
description: "Skill that packages a binary report template"
instruction_content: |
  # Report Skill
  Render the quarterly report with instantiate_template using the bundled asset.
assets:
  - name: quarterly_report.samt
    artifact: packaged_quarterly_report
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
}

func TestValidateSkillConfig_AssetMissingArtifact(t *testing.T) {
	yml := `
name: bad-asset
description: "Skill with an asset that has no artifact"
instruction_content: |
  # Bad Asset Skill
  This skill declares an asset without a backing artifact.
assets:
  - name: template.samt
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for asset without artifact")
	}
}

func TestValidateSkillConfig_AssetInlineContentRejected(t *testing.T) {
	yml := `
name: inline-asset
description: "Skill with an asset that tries to inline content"
instruction_content: |
  # Inline Asset Skill
  Assets are artifact-only; inline content must be rejected.
assets:
  - name: template.samt
    artifact: packaged_template
    content: "raw bytes"
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for asset with inline content")
	}
}

func TestValidateSkillConfig_UppercaseName(t *testing.T) {
	yml := `
name: MySkill
description: "Skill with uppercase name"
instruction_content: |
  # Bad Name Skill
  This skill has an uppercase name which violates the pattern.
`
	r, err := ValidateSkillConfig([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for uppercase name")
	}
}

func TestValidateSkillConfig_InvalidYAML(t *testing.T) {
	r, err := ValidateSkillConfig([]byte("{{not valid yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Fatal("expected invalid for malformed YAML")
	}
}

func TestValidateToolReferences_ValidBuiltinGroup(t *testing.T) {
	appConfig := map[string]any{
		"tools": []any{
			map[string]any{"tool_type": "builtin-group", "group_name": "artifact_management"},
			map[string]any{"tool_type": "builtin-group", "group_name": "web_tools"},
		},
	}
	errs, warns := ValidateToolReferences(appConfig)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %v", warns)
	}
}

func TestValidateToolReferences_UnknownBuiltinGroup(t *testing.T) {
	appConfig := map[string]any{
		"tools": []any{
			map[string]any{"tool_type": "builtin-group", "group_name": "nonexistent_group"},
		},
	}
	errs, warns := ValidateToolReferences(appConfig)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(errs), errs)
	}
	if !strings.Contains(errs[0].Message, "unknown builtin group") {
		t.Errorf("expected unknown group error, got: %s", errs[0].Message)
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %v", warns)
	}
}

func TestValidateToolReferences_ValidBuiltinTool(t *testing.T) {
	appConfig := map[string]any{
		"tools": []any{
			map[string]any{"tool_type": "builtin", "tool_name": "web_request"},
			map[string]any{"tool_type": "builtin", "tool_name": "list_artifacts"},
		},
	}
	errs, warns := ValidateToolReferences(appConfig)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %v", warns)
	}
}

func TestValidateToolReferences_UnknownBuiltinToolWarnsForSTR(t *testing.T) {
	appConfig := map[string]any{
		"tools": []any{
			map[string]any{"tool_type": "builtin", "tool_name": "FakeTool"},
		},
	}
	errs, warns := ValidateToolReferences(appConfig)
	if len(errs) != 0 {
		t.Errorf("expected no errors for unknown builtin, got %v", errs)
	}
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0].Message, "STR worker") || !strings.Contains(warns[0].Message, "snake_case") {
		t.Errorf("expected actionable STR-routing warning, got: %s", warns[0].Message)
	}
}

func TestValidateToolReferences_UnknownBuiltinFallsThroughToSTR(t *testing.T) {
	appConfig := map[string]any{
		"tools": []any{
			map[string]any{"tool_type": "builtin", "tool_name": "transform_artifact"},
			map[string]any{"tool_type": "builtin", "tool_name": "extract_content_from_artifact"},
		},
	}
	errs, warns := ValidateToolReferences(appConfig)
	if len(errs) != 0 {
		t.Errorf("expected no errors for unknown builtin, got %v", errs)
	}
	if len(warns) != 2 {
		t.Fatalf("expected 2 warnings, got %d: %v", len(warns), warns)
	}
	for _, w := range warns {
		if !strings.Contains(w.Message, "not a recognized in-process built-in") {
			t.Errorf("expected STR-routing warning, got: %s", w.Message)
		}
	}
}

func TestValidateToolReferences_BuiltinWithSTRShapeValid(t *testing.T) {
	appConfig := map[string]any{
		"tools": []any{
			map[string]any{
				"tool_type":        "builtin",
				"tool_name":        "mermaid_diagram_generator",
				"tool_description": "desc",
				"timeout_seconds":  120,
			},
		},
	}
	errs, warns := ValidateToolReferences(appConfig)
	if len(errs) != 0 {
		t.Errorf("expected no errors for STR-shaped builtin, got %v", errs)
	}
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning (not in in-process registry), got %d: %v", len(warns), warns)
	}
}

func TestValidateToolReferences_MCPNoStaticError(t *testing.T) {
	appConfig := map[string]any{
		"tools": []any{
			map[string]any{"tool_type": "mcp", "connection_params": map[string]any{"type": "stdio"}},
		},
	}
	errs, warns := ValidateToolReferences(appConfig)
	if len(errs) != 0 {
		t.Errorf("expected no errors for mcp, got %v", errs)
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings for mcp, got %v", warns)
	}
}

func TestValidateToolReferences_WarningsDontAffectValid(t *testing.T) {
	yaml := `
apps:
  - name: test
    app_config:
      agent_name: test_agent
      instruction: "You are a test agent. You help with testing the validation schema. You should respond to all requests with helpful and accurate information about testing procedures and best practices."
      description: "A test agent that does testing things for the test suite"
      tools:
        - tool_type: builtin
          tool_name: transform_artifact
        - tool_type: builtin-group
          group_name: general_agent_tools
      agent_card:
        description: "test"
        skills:
          - id: test
            name: Test
            description: "test skill"
`
	result, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid {
		t.Errorf("expected valid config, got errors: %v", result.Errors)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected warnings for deprecated tool, got none")
	}
}

func TestSchemaContentFiltered_ToolIncludesAllGroups(t *testing.T) {
	data, err := SchemaContentFiltered("tool")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for group := range ValidBuiltinGroups {
		if !strings.Contains(content, group) {
			t.Errorf("SchemaContentFiltered(tool) missing group %q", group)
		}
	}
}

func TestSchemaContentFiltered_ToolIncludesAllToolNames(t *testing.T) {
	data, err := SchemaContentFiltered("tool")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for toolName := range ValidBuiltinToolNames {
		if UngroupedBuiltinToolNames[toolName] {
			continue
		}
		if !strings.Contains(content, toolName) {
			t.Errorf("SchemaContentFiltered(tool) missing tool name %q", toolName)
		}
	}
}

func TestSchemaContentFiltered_ToolOmitsNamesNoGroupRegisters(t *testing.T) {
	data, err := SchemaContentFiltered("tool")
	if err != nil {
		t.Fatal(err)
	}
	for toolName := range UngroupedBuiltinToolNames {
		if !ValidBuiltinToolNames[toolName] {
			t.Errorf("%q is listed as ungrouped but is not a valid builtin tool name", toolName)
		}
		if mentionsToolName(string(data), toolName) {
			t.Errorf("SchemaContentFiltered(tool) offers %q, which no built-in group registers", toolName)
		}
	}
}

func mentionsToolName(text, toolName string) bool {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(toolName) + `\b`).MatchString(text)
}

func TestSchemaContentFiltered_ToolNameDescriptionGuidesSnakeCase(t *testing.T) {
	data, err := SchemaContentFiltered("tool")
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "PascalCase") {
		t.Error("tool schema must not mention PascalCase — registry names are snake_case only")
	}
	for _, want := range []string{"snake_case", "do not change the casing", "Secure Tool Runtime"} {
		if !strings.Contains(content, want) {
			t.Errorf("tool_name description missing guidance %q", want)
		}
	}
}

func TestValidateToolReferences_PascalCaseToolNameWarns(t *testing.T) {
	pascal := map[string]any{
		"tools": []any{map[string]any{"tool_type": "builtin", "tool_name": "WebRequest"}},
	}
	errs, warns := ValidateToolReferences(pascal)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %v", errs)
	}
	if len(warns) != 1 {
		t.Fatalf("expected 1 warning for PascalCase tool name, got %d: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0].Message, "snake_case") {
		t.Errorf("warning should advise snake_case spelling, got: %s", warns[0].Message)
	}

	snake := map[string]any{
		"tools": []any{map[string]any{"tool_type": "builtin", "tool_name": "web_request"}},
	}
	if _, warns := ValidateToolReferences(snake); len(warns) != 0 {
		t.Errorf("snake_case web_request should produce no warnings, got %v", warns)
	}
}

func validAgentYAML(overrides map[string]string) string {
	fields := map[string]string{
		"agent_name":  "TestAgent",
		"description": "A test agent that does testing things for the test suite",
		"instruction": "You are a test agent. You help with testing the validation schema. You should respond to all requests with helpful and accurate information about testing procedures and best practices.",
	}
	for k, v := range overrides {
		fields[k] = v
	}
	return fmt.Sprintf(`apps:
  - name: test_agent
    app_config:
      agent_name: "%s"
      description: "%s"
      instruction: "%s"
      agent_card:
        description: "A test agent"
        skills:
          - id: "test"
            name: "Test"
            description: "A test skill"
`, fields["agent_name"], fields["description"], fields["instruction"])
}

func TestValidateConfig_AgentFieldConstraints(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]string
		extraYAML string
		wantValid bool
		wantPath  string
		wantMsg   string
	}{
		{
			name:      "valid agent at constraint boundaries",
			overrides: map[string]string{},
			wantValid: true,
		},
		{
			name:      "agent_name too short",
			overrides: map[string]string{"agent_name": "AB"},
			wantValid: false,
			wantPath:  "agent_name",
			wantMsg:   "minLength",
		},
		{
			name:      "agent_name at minimum (3 chars)",
			overrides: map[string]string{"agent_name": "Bot"},
			wantValid: true,
		},
		{
			name:      "agent_name too long (41 chars)",
			overrides: map[string]string{"agent_name": strings.Repeat("A", 41)},
			wantValid: false,
			wantPath:  "agent_name",
			wantMsg:   "maxLength",
		},
		{
			name:      "agent_name at maximum (40 chars)",
			overrides: map[string]string{"agent_name": strings.Repeat("A", 40)},
			wantValid: true,
		},
		{
			name:      "description too short",
			overrides: map[string]string{"description": "Too short"},
			wantValid: false,
			wantPath:  "description",
			wantMsg:   "minLength",
		},
		{
			name:      "description at minimum (10 chars)",
			overrides: map[string]string{"description": "Exactly 10"},
			wantValid: true,
		},
		{
			name:      "description too long (1001 chars)",
			overrides: map[string]string{"description": strings.Repeat("D", 1001)},
			wantValid: false,
			wantPath:  "description",
			wantMsg:   "maxLength",
		},
		{
			name:      "terse instruction",
			overrides: map[string]string{"instruction": "You are a helpful SQL assistant."},
			wantValid: true,
		},
		{
			name:      "empty instruction",
			overrides: map[string]string{"instruction": ""},
			wantValid: false,
			wantPath:  "instruction",
			wantMsg:   "minLength",
		},
		{
			name:      "instruction too long (32001 chars)",
			overrides: map[string]string{"instruction": strings.Repeat("I", 32001)},
			wantValid: false,
			wantPath:  "instruction",
			wantMsg:   "maxLength",
		},
		{
			name:      "instruction at maximum (32000 chars)",
			overrides: map[string]string{"instruction": strings.Repeat("I", 32000)},
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := validAgentYAML(tt.overrides)
			r, err := ValidateConfig([]byte(yaml))
			if err != nil {
				t.Fatal(err)
			}
			if r.Valid != tt.wantValid {
				if tt.wantValid {
					t.Errorf("expected valid, got errors: %v", r.Errors)
				} else {
					t.Errorf("expected invalid, but got valid")
				}
				return
			}
			if !tt.wantValid {
				found := false
				for _, e := range r.Errors {
					if strings.Contains(e.Path, tt.wantPath) && strings.Contains(e.Message, tt.wantMsg) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error with path containing %q and message containing %q, got: %v",
						tt.wantPath, tt.wantMsg, r.Errors)
				}
			}
		})
	}
}

func TestValidateConfig_AgentSkillsMaxItems(t *testing.T) {
	skillsYAML := ""
	for i := 0; i < 21; i++ {
		skillsYAML += fmt.Sprintf("        - name: skill_%d\n", i)
	}
	yaml := fmt.Sprintf(`apps:
  - name: test_agent
    app_config:
      agent_name: "TestAgent"
      description: "A test agent that does testing things for the test suite"
      instruction: "%s"
      skills:
%s      agent_card:
        description: "A test agent"
        skills:
          - id: "test"
            name: "Test"
            description: "A test skill"
`, strings.Repeat("I", 100), skillsYAML)

	r, err := ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid for 21 skills, but got valid")
	}
	found := false
	for _, e := range r.Errors {
		if strings.Contains(e.Path, "skills") && strings.Contains(e.Message, "maxItems") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected maxItems error on skills, got: %v", r.Errors)
	}

	skillsYAML = ""
	for i := 0; i < 20; i++ {
		skillsYAML += fmt.Sprintf("        - name: skill_%d\n", i)
	}
	yaml = fmt.Sprintf(`apps:
  - name: test_agent
    app_config:
      agent_name: "TestAgent"
      description: "A test agent that does testing things for the test suite"
      instruction: "%s"
      skills:
%s      agent_card:
        description: "A test agent"
        skills:
          - id: "test"
            name: "Test"
            description: "A test skill"
`, strings.Repeat("I", 100), skillsYAML)

	r, err = ValidateConfig([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid for 20 skills, got errors: %v", r.Errors)
	}
}

func TestValidateConfig_WorkflowFieldConstraints(t *testing.T) {
	makeWorkflowYAML := func(agentName, description string) string {
		return fmt.Sprintf(`apps:
  - name: test_workflow
    app_config:
      agent_name: "%s"
      agent_type: workflow
      description: "%s"
      nodes:
        - id: step1
          type: agent
          agent_name: SomeAgent
          input:
            data: "{{workflow.input.text}}"
      output_mapping:
        result: "{{step1.output}}"
`, agentName, description)
	}

	tests := []struct {
		name        string
		agentName   string
		description string
		wantValid   bool
		wantPath    string
		wantMsg     string
	}{
		{
			name:        "valid workflow",
			agentName:   "MyWorkflow",
			description: "A workflow that processes data through multiple steps",
			wantValid:   true,
		},
		{
			name:        "workflow name too short",
			agentName:   "WF",
			description: "A workflow that processes data through multiple steps",
			wantValid:   false,
			wantPath:    "agent_name",
			wantMsg:     "minLength",
		},
		{
			name:        "workflow name at minimum (3 chars)",
			agentName:   "Bot",
			description: "A workflow that processes data through multiple steps",
			wantValid:   true,
		},
		{
			name:        "workflow name too long (81 chars)",
			agentName:   strings.Repeat("W", 81),
			description: "A workflow that processes data through multiple steps",
			wantValid:   false,
			wantPath:    "agent_name",
			wantMsg:     "maxLength",
		},
		{
			name:        "workflow name at maximum (80 chars)",
			agentName:   strings.Repeat("W", 80),
			description: "A workflow that processes data through multiple steps",
			wantValid:   true,
		},
		{
			name:        "workflow description too short",
			agentName:   "MyWorkflow",
			description: "Too short",
			wantValid:   false,
			wantPath:    "description",
			wantMsg:     "minLength",
		},
		{
			name:        "workflow description too long (2001 chars)",
			agentName:   "MyWorkflow",
			description: strings.Repeat("D", 2001),
			wantValid:   false,
			wantPath:    "description",
			wantMsg:     "maxLength",
		},
		{
			name:        "workflow description at maximum (2000 chars)",
			agentName:   "MyWorkflow",
			description: strings.Repeat("D", 2000),
			wantValid:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := makeWorkflowYAML(tt.agentName, tt.description)
			r, err := ValidateConfig([]byte(yaml))
			if err != nil {
				t.Fatal(err)
			}
			if r.Valid != tt.wantValid {
				if tt.wantValid {
					t.Errorf("expected valid, got errors: %v", r.Errors)
				} else {
					t.Errorf("expected invalid, but got valid")
				}
				return
			}
			if !tt.wantValid {
				found := false
				for _, e := range r.Errors {
					if strings.Contains(e.Path, tt.wantPath) && strings.Contains(e.Message, tt.wantMsg) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error with path containing %q and message containing %q, got: %v",
						tt.wantPath, tt.wantMsg, r.Errors)
				}
			}
		})
	}
}

func TestValidateConnectorConfig_FieldConstraints(t *testing.T) {
	makeConnectorJSON := func(name, description string) string {
		return fmt.Sprintf(`{
			"name": %q,
			"description": %q,
			"type": "api",
			"subtype": "openapi",
			"values": {
				"base_url": "https://example.com",
				"auth_type": "none"
			}
		}`, name, description)
	}

	tests := []struct {
		name        string
		connName    string
		description string
		wantValid   bool
		wantPath    string
		wantMsg     string
	}{
		{
			name:        "valid connector",
			connName:    "MyConnector",
			description: "A connector that connects to an external API service",
			wantValid:   true,
		},
		{
			name:        "connector name too short (2 chars)",
			connName:    "AB",
			description: "A connector that connects to an external API service",
			wantValid:   false,
			wantPath:    "name",
			wantMsg:     "minLength",
		},
		{
			name:        "connector name at minimum (3 chars)",
			connName:    "API",
			description: "A connector that connects to an external API service",
			wantValid:   true,
		},
		{
			name:        "connector description too short",
			connName:    "MyConnector",
			description: "Too short",
			wantValid:   false,
			wantPath:    "description",
			wantMsg:     "minLength",
		},
		{
			name:        "connector description at minimum (10 chars)",
			connName:    "MyConnector",
			description: "Exactly 10",
			wantValid:   true,
		},
		{
			name:        "connector description too long (1001 chars)",
			connName:    "MyConnector",
			description: strings.Repeat("D", 1001),
			wantValid:   false,
			wantPath:    "description",
			wantMsg:     "maxLength",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			json := makeConnectorJSON(tt.connName, tt.description)
			r, err := ValidateConnectorConfig([]byte(json))
			if err != nil {
				t.Fatal(err)
			}
			if r.Valid != tt.wantValid {
				if tt.wantValid {
					t.Errorf("expected valid, got errors: %v", r.Errors)
				} else {
					t.Errorf("expected invalid, but got valid")
				}
				return
			}
			if !tt.wantValid {
				found := false
				for _, e := range r.Errors {
					if strings.Contains(e.Path, tt.wantPath) && strings.Contains(e.Message, tt.wantMsg) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error with path containing %q and message containing %q, got: %v",
						tt.wantPath, tt.wantMsg, r.Errors)
				}
			}
		})
	}
}

func TestGenerateToolGroupsTable(t *testing.T) {
	table := GenerateToolGroupsTable()

	if !strings.Contains(table, "| Group | Tools Included | Use When |") {
		t.Error("table missing header row")
	}

	for name := range ValidBuiltinGroups {
		if !strings.Contains(table, "`"+name+"`") {
			t.Errorf("table missing group %q", name)
		}
	}

	artIdx := strings.Index(table, "`artifact_management`")
	aliasIdx := strings.Index(table, "`builtin_artifact_tools`")
	if artIdx < 0 || aliasIdx < 0 {
		t.Fatal("missing expected groups in table")
	}
	if artIdx > aliasIdx {
		t.Error("primary group artifact_management should appear before alias builtin_artifact_tools")
	}
}

func TestValidateConnectorConfig_SlackBotTokenPattern(t *testing.T) {
	cfg := func(token string) string {
		return `{"name":"Slack Sales","type":"slack","subtype":"bot","values":{"slack_bot_token":"` + token + `"}}`
	}
	cases := []struct {
		name  string
		token string
		valid bool
	}{
		{"valid xoxb- token", "xoxb-abcdefghij", true},
		{"placeholder is accepted (filled later)", "<<__SAM_REQUIRED__>>", true},
		{"concrete non-xoxb- token is rejected", "not-a-real-token", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := ValidateConnectorConfig([]byte(cfg(tc.token)))
			if err != nil {
				t.Fatal(err)
			}
			if r.Valid != tc.valid {
				t.Errorf("Valid = %v, want %v (errors: %v)", r.Valid, tc.valid, r.Errors)
			}
		})
	}
}

func TestAgentSchema_NoAgentLevelHIL(t *testing.T) {
	data, err := SchemaContent("agent")
	if err != nil {
		t.Fatalf("SchemaContent: %v", err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.HasPrefix(line, "  hil:") {
			t.Error("agent.schema.yaml declares an agent-level 'hil' property: nothing reads one, so it would validate clean and gate nothing — configure HIL per tool under tools[]")
		}
	}
}
