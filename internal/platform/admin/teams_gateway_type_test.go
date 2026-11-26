// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"
)

func teamsSchema(t *testing.T) GatewaySchemaResponse {
	t.Helper()
	for _, s := range getAllGatewaySchemas() {
		if s.Type == "teams" {
			return s
		}
	}
	t.Fatal("teams gateway schema not registered in getAllGatewaySchemas()")
	return GatewaySchemaResponse{}
}

func teamsFieldMap(t *testing.T) map[string]map[string]any {
	t.Helper()
	schema := teamsSchema(t)
	raw, ok := schema.Schema.(map[string]any)
	if !ok {
		t.Fatalf("teams schema is %T, want map[string]any", schema.Schema)
	}
	out := make(map[string]map[string]any, len(raw))
	for name, def := range raw {
		m, ok := def.(map[string]any)
		if !ok {
			t.Fatalf("field %q is %T, want map[string]any", name, def)
		}
		out[name] = m
	}
	return out
}

func TestTeamsGatewayType_Properties(t *testing.T) {
	t.Parallel()
	schema := teamsSchema(t)

	if schema.Type != "teams" {
		t.Errorf("Type = %q, want %q", schema.Type, "teams")
	}
	if schema.Name != "Microsoft Teams" {
		t.Errorf("Name = %q, want %q", schema.Name, "Microsoft Teams")
	}
	if !strings.Contains(schema.Description, "Microsoft Teams") {
		t.Errorf("Description %q missing 'Microsoft Teams'", schema.Description)
	}
	if !strings.Contains(schema.Warning, "Azure Bot Service") {
		t.Errorf("Warning %q missing 'Azure Bot Service'", schema.Warning)
	}
}

func TestTeamsGatewayType_FieldSchema_Structure(t *testing.T) {
	t.Parallel()
	fields := teamsFieldMap(t)

	expected := []string{
		"default_agent_name",
		"microsoft_app_id",
		"microsoft_app_password",
		"microsoft_app_tenant_id",
		"initial_status_message",
		"enable_typing_indicator",
	}
	for _, name := range expected {
		if _, ok := fields[name]; !ok {
			t.Errorf("field %q missing from schema", name)
		}
	}
}

func TestTeamsGatewayType_DefaultAgentField(t *testing.T) {
	t.Parallel()
	fields := teamsFieldMap(t)
	field, ok := fields["default_agent_name"]
	if !ok {
		t.Fatal("default_agent_name field missing")
	}

	if got, _ := field["type"].(string); got != "agent_select" {
		t.Errorf("type = %q, want %q", got, "agent_select")
	}
	if req, _ := field["required"].(bool); req {
		t.Error("required = true, want false")
	}
	if def, _ := field["default"].(string); def != "Orchestrator" {
		t.Errorf("default = %q, want %q", def, "Orchestrator")
	}
}

func TestTeamsGatewayType_AppIDField(t *testing.T) {
	t.Parallel()
	fields := teamsFieldMap(t)
	field, ok := fields["microsoft_app_id"]
	if !ok {
		t.Fatal("microsoft_app_id field missing")
	}

	if got, _ := field["type"].(string); got != "string" {
		t.Errorf("type = %q, want %q", got, "string")
	}
	if req, _ := field["required"].(bool); !req {
		t.Error("required = false, want true")
	}
	if secret, _ := field["secret"].(bool); secret {
		t.Error("secret = true, want false")
	}

	validation, ok := field["validation"].(map[string]any)
	if !ok {
		t.Fatalf("validation is %T, want map[string]any", field["validation"])
	}
	pattern, _ := validation["pattern"].(string)
	if pattern != guidPattern {
		t.Errorf("validation.pattern = %q, want guidPattern", pattern)
	}
}

func TestTeamsGatewayType_AppPasswordField(t *testing.T) {
	t.Parallel()
	fields := teamsFieldMap(t)
	field, ok := fields["microsoft_app_password"]
	if !ok {
		t.Fatal("microsoft_app_password field missing")
	}

	if got, _ := field["type"].(string); got != "password" {
		t.Errorf("type = %q, want %q", got, "password")
	}
	if req, _ := field["required"].(bool); !req {
		t.Error("required = false, want true")
	}
	if secret, _ := field["secret"].(bool); !secret {
		t.Error("secret = false, want true")
	}

	validation, ok := field["validation"].(map[string]any)
	if !ok {
		t.Fatalf("validation is %T, want map[string]any", field["validation"])
	}
	switch v := validation["minLength"].(type) {
	case int:
		if v != 1 {
			t.Errorf("minLength = %d, want 1", v)
		}
	case float64:
		if v != 1 {
			t.Errorf("minLength = %v, want 1", v)
		}
	default:
		t.Errorf("minLength is %T (%v), want numeric 1", validation["minLength"], validation["minLength"])
	}
}

func TestTeamsGatewayType_TenantIDField(t *testing.T) {
	t.Parallel()
	fields := teamsFieldMap(t)
	field, ok := fields["microsoft_app_tenant_id"]
	if !ok {
		t.Fatal("microsoft_app_tenant_id field missing")
	}

	if got, _ := field["type"].(string); got != "string" {
		t.Errorf("type = %q, want %q", got, "string")
	}
	if req, _ := field["required"].(bool); req {
		t.Error("required = true, want false")
	}

	validation, ok := field["validation"].(map[string]any)
	if !ok {
		t.Fatalf("validation is %T, want map[string]any", field["validation"])
	}
	pattern, _ := validation["pattern"].(string)
	if pattern != guidPattern {
		t.Errorf("validation.pattern = %q, want guidPattern", pattern)
	}
}

func TestTeamsGatewayType_InitialStatusField(t *testing.T) {
	t.Parallel()
	fields := teamsFieldMap(t)
	field, ok := fields["initial_status_message"]
	if !ok {
		t.Fatal("initial_status_message field missing")
	}

	if got, _ := field["type"].(string); got != "string" {
		t.Errorf("type = %q, want %q", got, "string")
	}
	if req, _ := field["required"].(bool); req {
		t.Error("required = true, want false")
	}
	if def, _ := field["default"].(string); def != "Processing your request..." {
		t.Errorf("default = %q, want %q", def, "Processing your request...")
	}
}

func TestTeamsGatewayType_TypingIndicatorField(t *testing.T) {
	t.Parallel()
	fields := teamsFieldMap(t)
	field, ok := fields["enable_typing_indicator"]
	if !ok {
		t.Fatal("enable_typing_indicator field missing")
	}

	if got, _ := field["type"].(string); got != "select" {
		t.Errorf("type = %q, want %q", got, "select")
	}
	if req, _ := field["required"].(bool); req {
		t.Error("required = true, want false")
	}
	if def, _ := field["default"].(string); def != "true" {
		t.Errorf("default = %q, want %q", def, "true")
	}
	opts, ok := field["options"].([]map[string]any)
	if !ok {
		t.Fatalf("options is %T, want []map[string]any", field["options"])
	}
	if len(opts) != 2 {
		t.Errorf("len(options) = %d, want 2", len(opts))
	}
}

func TestTeamsGatewayType_YAMLTemplatePath(t *testing.T) {
	t.Parallel()
	schema := teamsSchema(t)

	if schema.YAMLTemplatePath != "teams_gateway.yaml" {
		t.Errorf("YAMLTemplatePath = %q, want %q", schema.YAMLTemplatePath, "teams_gateway.yaml")
	}
}

func TestTeamsGatewayType_RequiredEnvVars(t *testing.T) {
	t.Parallel()
	schema := teamsSchema(t)

	if len(schema.RequiredEnvVars) == 0 {
		t.Fatal("RequiredEnvVars is empty, want [TEAMS_APP_PASSWORD]")
	}

	hasPassword := false
	for _, v := range schema.RequiredEnvVars {
		if v == "TEAMS_APP_PASSWORD" {
			hasPassword = true
		}
		if strings.Contains(v, "APP_ID") {
			t.Errorf("RequiredEnvVars contains app-id entry %q; app_id is a non-secret identifier and must not be exposed via env vars", v)
		}
	}
	if !hasPassword {
		t.Errorf("RequiredEnvVars = %v, missing TEAMS_APP_PASSWORD", schema.RequiredEnvVars)
	}
}
