// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/configbuilder"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"gopkg.in/yaml.v3"
)

func TestGenerateAgentYAML_Basic(t *testing.T) {
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "test-model")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://test.example.com")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")
	svc := NewYAMLService("test-ns", slog.Default())

	skills := `[{"name":"Research","description":"Research topics"}]`
	agent := &store.Agent{
		ID:           "550e8400-e29b-41d4-a716-446655440000",
		Name:         "My Test Agent",
		Description:  strPtr("A helpful test agent"),
		SystemPrompt: strPtr("You are a helpful assistant that answers questions clearly and concisely. You should always be polite and thorough in your responses to help users effectively."),
		Skills:       &skills,
		CreatedBy:    "test-user",
		UpdatedBy:    "test-user",
	}

	yamlStr, err := svc.GenerateAgentYAML(agent, []string{"builtin_artifact_tools", "builtin_web_request_tools"}, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("GenerateAgentYAML() error: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("generated YAML is not valid: %v\n%s", err, yamlStr)
	}

	apps, ok := parsed["apps"].([]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("expected apps array, got: %v", parsed["apps"])
	}

	app, ok := apps[0].(map[string]any)
	if !ok {
		t.Fatalf("expected app map, got: %T", apps[0])
	}

	expectedName := "agent_550e8400_e29b_41d4_a716_446655440000"
	if app["name"] != expectedName {
		t.Errorf("expected name %q, got %q", expectedName, app["name"])
	}

	appConfig, ok := app["app_config"].(map[string]any)
	if !ok {
		t.Fatalf("expected app_config map")
	}

	if appConfig["display_name"] != "My Test Agent" {
		t.Errorf("expected display_name 'My Test Agent', got %q", appConfig["display_name"])
	}

	if appConfig["agent_name"] != expectedName {
		t.Errorf("expected agent_name %q, got %q", expectedName, appConfig["agent_name"])
	}

	if _, present := appConfig["model"]; present {
		t.Errorf("literal model: block must not be emitted — bindings own model config. Got: %v", appConfig["model"])
	}
	mp, ok := appConfig["model_provider"].([]any)
	if !ok {
		t.Fatalf("expected model_provider list, got %T", appConfig["model_provider"])
	}
	if len(mp) != 1 || mp[0] != "general" {
		t.Errorf("expected model_provider=[general], got %v", mp)
	}

	tools, ok := appConfig["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array")
	}
	if len(tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(tools))
	}

	agentCard, ok := appConfig["agent_card"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent_card map")
	}
	cardSkills, ok := agentCard["skills"].([]any)
	if !ok || len(cardSkills) != 1 {
		t.Errorf("expected 1 skill in agent_card, got %v", agentCard["skills"])
	}
}

func TestGenerateAgentYAML_NilAgent(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	_, err := svc.GenerateAgentYAML(nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil agent")
	}
}

func TestGenerateAgentYAML_EmptyToolsets(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	agent := &store.Agent{
		ID:          "abc-123",
		Name:        "Empty Agent",
		Description: strPtr("An agent with no tools"),
		CreatedBy:   "test",
		UpdatedBy:   "test",
	}

	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}

	apps := parsed["apps"].([]any)
	appConfig := apps[0].(map[string]any)["app_config"].(map[string]any)
	agentCard := appConfig["agent_card"].(map[string]any)

	inputModes, ok := agentCard["defaultInputModes"].([]any)
	if !ok || len(inputModes) != 1 || inputModes[0] != "text" {
		t.Errorf("expected default input modes [text], got %v", agentCard["defaultInputModes"])
	}
}

func TestGenerateAgentYAML_WithConnectors(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	agent := &store.Agent{
		ID:          "abc-123-def-456",
		Name:        "SQL Agent",
		Description: strPtr("Agent with SQL connector"),
		CreatedBy:   "test",
		UpdatedBy:   "test",
	}

	connectors := []ConnectorConfig{
		{
			ID:   "2b673ec2-14b9-40e4-bc85-e511f379ff8b",
			Type: "sql",
			Values: map[string]any{
				"tool_name":        "my_db",
				"tool_description": "Query my database",
			},
		},
	}

	yamlStr, err := svc.GenerateAgentYAML(agent, []string{"builtin_artifact_tools"}, nil, nil, connectors)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	expectedUUID := "2B673EC2_14B9_40E4_BC85_E511F379FF8B"
	if !strings.Contains(yamlStr, "${USERNAME_"+expectedUUID+"}") {
		t.Errorf("expected connector env var reference with UUID %s in:\n%s", expectedUUID, yamlStr)
	}
}

func TestNameToSnakeCase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected string
	}{
		{"Problem Solving", "problem_solving"},
		{"web-search", "web_search"},
		{"  Spaces  ", "spaces"},
		{"CamelCase", "camelcase"},
		{"multi   space", "multi_space"},
	}
	for _, tc := range tests {
		got := nameToSnakeCase(tc.input)
		if got != tc.expected {
			t.Errorf("nameToSnakeCase(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestGenerateAgentYAML_SkillAutoID(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	skills := `[{"name":"Problem Solving","description":"Solve problems"}]`
	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "test-id",
		Name:        "Skill Agent",
		Skills:      &skills,
		CreatedBy:   "test",
		UpdatedBy:   "test",
	}

	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	appConfig := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)
	agentCard := appConfig["agent_card"].(map[string]any)
	cardSkills := agentCard["skills"].([]any)
	skill := cardSkills[0].(map[string]any)
	if skill["id"] != "problem_solving" {
		t.Errorf("expected skill id 'problem_solving', got %q", skill["id"])
	}
}

func TestSkillsA2ARoundTrip(t *testing.T) {
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "test-model")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://test.example.com")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")

	svc := NewYAMLService("test-ns", slog.Default())

	skillsJSON := `[
		{
			"id": "research_topics",
			"name": "Research",
			"description": "Research topics in depth",
			"tags": ["research", "knowledge"],
			"examples": ["Tell me about quantum computing", "Find recent ML papers"],
			"inputModes": ["text"],
			"outputModes": ["text", "application/json"],
			"required": ["query"],
			"optional": ["depth"]
		}
	]`
	agent := &store.Agent{
		Description:  strPtr("test agent for fixture purposes"),
		ID:           "550e8400-e29b-41d4-a716-446655440000",
		Name:         "Skills Agent",
		SystemPrompt: strPtr("You are a helpful research assistant. " + strings.Repeat("x", 80)),
		Skills:       &skillsJSON,
		CreatedBy:    "test",
		UpdatedBy:    "test",
	}

	yamlStr, err := svc.GenerateAgentYAML(agent, nil, []string{"text"}, []string{"text"}, nil)
	if err != nil {
		t.Fatalf("GenerateAgentYAML: %v", err)
	}

	req, err := ParseAgentYAML([]byte(yamlStr))
	if err != nil {
		t.Fatalf("ParseAgentYAML: %v", err)
	}

	if len(req.Skills) != 1 {
		t.Fatalf("expected 1 skill after round-trip, got %d", len(req.Skills))
	}
	got := req.Skills[0]
	if got.ID != "research_topics" {
		t.Errorf("ID: got %q, want %q", got.ID, "research_topics")
	}
	if got.Name != "Research" {
		t.Errorf("Name: got %q, want %q", got.Name, "Research")
	}
	if got.Description != "Research topics in depth" {
		t.Errorf("Description: got %q", got.Description)
	}
	if want := []string{"research", "knowledge"}; !equalStrings(got.Tags, want) {
		t.Errorf("Tags: got %v, want %v", got.Tags, want)
	}
	if want := []string{"Tell me about quantum computing", "Find recent ML papers"}; !equalStrings(got.Examples, want) {
		t.Errorf("Examples: got %v, want %v", got.Examples, want)
	}
	if want := []string{"text"}; !equalStrings(got.InputModes, want) {
		t.Errorf("InputModes: got %v, want %v", got.InputModes, want)
	}
	if want := []string{"text", "application/json"}; !equalStrings(got.OutputModes, want) {
		t.Errorf("OutputModes: got %v, want %v", got.OutputModes, want)
	}

	if len(got.Required) != 0 {
		t.Errorf("Required should be dropped on YAML round-trip, got %v", got.Required)
	}
	if len(got.Optional) != 0 {
		t.Errorf("Optional should be dropped on YAML round-trip, got %v", got.Optional)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestGenerateAgentYAML_TestMode(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	prompt := "You are a test agent"
	agent := &store.Agent{
		Description:  strPtr("test agent for fixture purposes"),
		ID:           "abc-123",
		Name:         "Test Agent",
		SystemPrompt: &prompt,
		CreatedBy:    "test",
		UpdatedBy:    "test",
	}

	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil, DeployOptions{
		TestMode: true,
		UserID:   "alice",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	appConfig := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)
	scopes, ok := appConfig["required_scopes"].([]any)
	if !ok || len(scopes) == 0 {
		t.Fatalf("expected required_scopes in test mode, got %v", appConfig["required_scopes"])
	}
	expectedScope := "agent:alice__agent_abc_123:invoke"
	if scopes[0] != expectedScope {
		t.Errorf("expected scope %q, got %q", expectedScope, scopes[0])
	}
}

func TestGenerateAgentYAML_EphemeralDisplayName(t *testing.T) {
	t.Parallel()
	const wireName = "__test__sess1234__user5678"
	src := "Weather Reporter"
	empty := ""

	tests := []struct {
		name            string
		isEphemeral     bool
		sourceComponent *string
		wantDisplayName string
		wantEphemeral   bool
	}{
		{"ephemeral with source component → human-readable", true, &src, src, true},
		{"ephemeral with nil source → falls back to wire name", true, nil, wireName, true},
		{"ephemeral with empty source → falls back to wire name", true, &empty, wireName, true},
		{"non-ephemeral ignores source component", false, &src, wireName, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := NewYAMLService("test-ns", slog.Default())
			agent := &store.Agent{
				ID:                  "abc-123",
				Name:                wireName,
				IsEphemeral:         tt.isEphemeral,
				SourceComponentName: tt.sourceComponent,
				CreatedBy:           "test",
				UpdatedBy:           "test",
			}

			yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			var parsed map[string]any
			if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
				t.Fatalf("invalid YAML: %v", err)
			}
			appConfig := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)

			gotEphemeral := appConfig["ephemeral"] == true
			if gotEphemeral != tt.wantEphemeral {
				t.Errorf("ephemeral = %v, want %v", appConfig["ephemeral"], tt.wantEphemeral)
			}
			if appConfig["display_name"] != tt.wantDisplayName {
				t.Errorf("display_name = %q, want %q", appConfig["display_name"], tt.wantDisplayName)
			}
		})
	}
}

func TestGenerateAgentYAML_NoTestMode(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	prompt := "You are a normal agent"
	agent := &store.Agent{
		Description:  strPtr("test agent for fixture purposes"),
		ID:           "abc-123",
		Name:         "Normal Agent",
		SystemPrompt: &prompt,
		CreatedBy:    "test",
		UpdatedBy:    "test",
	}

	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if strings.Contains(yamlStr, "required_scopes:") {
		t.Errorf("unexpected required_scopes in non-test-mode YAML:\n%s", yamlStr)
	}
}

func TestGenerateAgentYAML_DeploymentID(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	agent := &store.Agent{
		ID:        "abc-123",
		Name:      "Deploy Agent",
		CreatedBy: "test",
		UpdatedBy: "test",
	}

	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil, DeployOptions{
		DeploymentID: "deploy-row-xyz",
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var parsed map[string]any
	if uerr := yaml.Unmarshal([]byte(yamlStr), &parsed); uerr != nil {
		t.Fatalf("invalid YAML: %v", uerr)
	}
	appConfig := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)
	deploy, ok := appConfig["deployment"].(map[string]any)
	if !ok {
		t.Fatalf("expected deployment block, got %v", appConfig["deployment"])
	}
	if deploy["id"] != "deploy-row-xyz" {
		t.Errorf("deployment.id = %v, want deploy-row-xyz", deploy["id"])
	}

	yamlStr, err = svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(yamlStr, "deployment:") {
		t.Errorf("unexpected deployment block when DeploymentID empty:\n%s", yamlStr)
	}
}

func TestDetectObjectStorageType(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected string
	}{
		{"explicit_s3", map[string]string{"OBJECT_STORAGE_TYPE": "s3"}, "s3"},
		{"explicit_azure", map[string]string{"OBJECT_STORAGE_TYPE": "azure"}, "azure"},
		{"explicit_gcs", map[string]string{"OBJECT_STORAGE_TYPE": "gcs"}, "gcs"},
		{"explicit_gcs_uppercase", map[string]string{"OBJECT_STORAGE_TYPE": "GCS"}, "gcs"},
		{"unknown_type", map[string]string{"OBJECT_STORAGE_TYPE": "minio"}, "none"},
		{"no_env_vars", map[string]string{}, "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OBJECT_STORAGE_TYPE", "")
			t.Setenv("S3_BUCKET_NAME", "")
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}
			if got := configbuilder.DetectObjectStorageType(); got != tt.expected {
				t.Errorf("configbuilder.DetectObjectStorageType() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestGenerateAgentYAML_S3Artifacts(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "s3")
	t.Setenv("OBJECT_STORAGE_BUCKET_NAME", "my-test-bucket")
	t.Setenv("S3_REGION", "us-west-2")

	svc := NewYAMLService("test-ns", slog.Default())
	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "test-id", Name: "Test", CreatedBy: "test", UpdatedBy: "test",
	}
	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlStr, "type: s3") {
		t.Errorf("expected S3 artifact type in:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "bucket_name: my-test-bucket") {
		t.Errorf("expected resolved S3 bucket name in:\n%s", yamlStr)
	}
}

func TestGenerateAgentYAML_AzureArtifacts(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "azure")
	t.Setenv("OBJECT_STORAGE_BUCKET_NAME", "my-container")
	t.Setenv("AZURE_STORAGE_ACCOUNT_NAME", "myaccount")
	t.Setenv("AZURE_STORAGE_ACCOUNT_KEY", "mykey")

	svc := NewYAMLService("test-ns", slog.Default())
	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "test-id", Name: "Test", CreatedBy: "test", UpdatedBy: "test",
	}
	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlStr, "type: azure") {
		t.Errorf("expected Azure artifact type in:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "container_name: my-container") {
		t.Errorf("expected resolved Azure container name in:\n%s", yamlStr)
	}
}

func TestGenerateAgentYAML_GCSArtifacts(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "gcs")
	t.Setenv("OBJECT_STORAGE_BUCKET_NAME", "my-gcs-bucket")
	t.Setenv("GCS_PROJECT", "my-project")

	svc := NewYAMLService("test-ns", slog.Default())
	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "test-id", Name: "Test", CreatedBy: "test", UpdatedBy: "test",
	}
	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlStr, "type: gcs") {
		t.Errorf("expected GCS artifact type in:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "bucket_name: my-gcs-bucket") {
		t.Errorf("expected resolved GCS bucket name in:\n%s", yamlStr)
	}
}

func TestGenerateGatewayYAML_Teams(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{"microsoft_app_id":"app-123","microsoft_app_password":"secret","microsoft_app_tenant_id":"tenant-1","default_agent_name":"MyAgent","initial_status_message":"Working...","enable_typing_indicator":true,"max_download_file_size_mb":25}`
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "abc-123-def-456",
		Name:        "My Teams GW",
		Type:        "teams",
		Values:      &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}

	apps := parsed["apps"].([]any)
	appConfig := apps[0].(map[string]any)["app_config"].(map[string]any)
	gwAdapter := appConfig["gateway_adapter"].(map[string]any)
	if gwAdapter["type"] != "teams" {
		t.Errorf("expected gateway_adapter type 'teams', got %q", gwAdapter["type"])
	}
	adapterCfg := gwAdapter["adapter_config"].(map[string]any)
	if adapterCfg["microsoft_app_id"] != "app-123" {
		t.Errorf("expected microsoft_app_id 'app-123', got %q", adapterCfg["microsoft_app_id"])
	}
	if adapterCfg["microsoft_app_tenant_id"] != "tenant-1" {
		t.Errorf("expected microsoft_app_tenant_id 'tenant-1', got %q", adapterCfg["microsoft_app_tenant_id"])
	}
	if adapterCfg["default_agent_name"] != "MyAgent" {
		t.Errorf("expected default_agent_name 'MyAgent', got %q", adapterCfg["default_agent_name"])
	}
	if adapterCfg["initial_status_message"] != "Working..." {
		t.Errorf("expected initial_status_message 'Working...', got %q", adapterCfg["initial_status_message"])
	}
	if adapterCfg["enable_typing_indicator"] != true {
		t.Errorf("expected enable_typing_indicator true, got %v", adapterCfg["enable_typing_indicator"])
	}
	if adapterCfg["max_download_file_size_mb"] != 25 {
		t.Errorf("expected max_download_file_size_mb 25, got %v", adapterCfg["max_download_file_size_mb"])
	}
}

func TestGenerateGatewayYAML_Email_DefaultAgentNameNesting(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{"mailbox":"inbox@example.com","imap_host":"imap.gmail.com","imap_port":993,"auth_mode":"basic","imap_password":"app-pw","trusted_authserv_ids":"mx.google.com","policy_mode":"log_only","default_agent_name":"Manager"}`
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "abc-123-def-456",
		Name:        "My Email GW",
		Type:        "email",
		Values:      &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}

	apps := parsed["apps"].([]any)
	appConfig := apps[0].(map[string]any)["app_config"].(map[string]any)
	gwAdapter := appConfig["gateway_adapter"].(map[string]any)
	if gwAdapter["type"] != "email" {
		t.Errorf("gateway_adapter.type = %q, want email", gwAdapter["type"])
	}
	adapterCfg := gwAdapter["adapter_config"].(map[string]any)

	if got := adapterCfg["default_agent_name"]; got != "Manager" {
		t.Errorf("adapter_config.default_agent_name = %v, want %q", got, "Manager")
	}
	if _, leaked := appConfig["default_agent_name"]; leaked {
		t.Error("default_agent_name leaked to app_config root; the adapter parser would not see it")
	}
}

func TestGenerateGatewayYAML_Email_OAuth2Path(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{
		"mailbox":"sam@example.org",
		"imap_host":"outlook.office365.com",
		"imap_port":993,
		"smtp_host":"smtp.office365.com",
		"smtp_port":587,
		"auth_mode":"oauth2",
		"oauth2_tenant_id":"tenant-123",
		"oauth2_client_id":"client-456",
		"oauth2_client_secret":"shh",
		"trusted_authserv_ids":"protection.outlook.com, mx.google.com",
		"policy_mode":"strict",
		"require_dmarc_for_identity":true,
		"trusted_internal_tenant_id":"tenant-123",
		"allowed_senders":"alice@example.org, bob@example.org",
		"mime_allowlist":"text/plain, application/pdf",
		"mime_denylist":"application/x-msdownload",
		"max_message_size_mb":50,
		"max_attachment_size_mb":15,
		"poll_interval_seconds":60,
		"delivery_mode":"idle",
		"default_agent_name":"Manager"
	}`
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "oauth-gw-1",
		Name:        "Office 365 GW",
		Type:        "email",
		Values:      &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}
	apps := parsed["apps"].([]any)
	adapterCfg := apps[0].(map[string]any)["app_config"].(map[string]any)["gateway_adapter"].(map[string]any)["adapter_config"].(map[string]any)

	if got := adapterCfg["oauth2_tenant_id"]; got != "tenant-123" {
		t.Errorf("oauth2_tenant_id = %v, want tenant-123", got)
	}
	if got := adapterCfg["oauth2_client_id"]; got != "client-456" {
		t.Errorf("oauth2_client_id = %v, want client-456", got)
	}
	if got := adapterCfg["oauth2_client_secret"]; got != "shh" {
		t.Errorf("oauth2_client_secret = %v, want shh", got)
	}
	if _, leaked := adapterCfg["imap_password"]; leaked {
		t.Error("imap_password should not be emitted on the oauth2 path")
	}

	if got := adapterCfg["smtp_host"]; got != "smtp.office365.com" {
		t.Errorf("smtp_host = %v, want smtp.office365.com", got)
	}
	if got := adapterCfg["smtp_port"]; got != 587 {
		t.Errorf("smtp_port = %v, want 587", got)
	}

	ids, ok := adapterCfg["trusted_authserv_ids"].([]any)
	if !ok {
		t.Fatalf("trusted_authserv_ids type = %T, want []any", adapterCfg["trusted_authserv_ids"])
	}
	if len(ids) != 2 || ids[0] != "protection.outlook.com" || ids[1] != "mx.google.com" {
		t.Errorf("trusted_authserv_ids = %v, want [protection.outlook.com, mx.google.com]", ids)
	}

	if got, _ := adapterCfg["allowed_senders"].([]any); len(got) != 2 {
		t.Errorf("allowed_senders = %v, want 2 entries", got)
	}
	if got, _ := adapterCfg["mime_allowlist"].([]any); len(got) != 2 {
		t.Errorf("mime_allowlist = %v, want 2 entries", got)
	}
	if got, _ := adapterCfg["mime_denylist"].([]any); len(got) != 1 {
		t.Errorf("mime_denylist = %v, want 1 entry", got)
	}

	if got := adapterCfg["max_message_size_bytes"]; got != 50*1024*1024 {
		t.Errorf("max_message_size_bytes = %v, want %d (50 MB)", got, 50*1024*1024)
	}
	if got := adapterCfg["max_attachment_size_bytes"]; got != 15*1024*1024 {
		t.Errorf("max_attachment_size_bytes = %v, want %d (15 MB)", got, 15*1024*1024)
	}

	if got := adapterCfg["require_dmarc_for_identity"]; got != true {
		t.Errorf("require_dmarc_for_identity = %v, want true", got)
	}

	if got := adapterCfg["trusted_internal_tenant_id"]; got != "tenant-123" {
		t.Errorf("trusted_internal_tenant_id = %v, want tenant-123", got)
	}

	if got := adapterCfg["policy_mode"]; got != "strict" {
		t.Errorf("policy_mode = %v, want strict", got)
	}
	if got := adapterCfg["delivery_mode"]; got != "idle" {
		t.Errorf("delivery_mode = %v, want idle", got)
	}
	if got := adapterCfg["default_agent_name"]; got != "Manager" {
		t.Errorf("default_agent_name = %v, want Manager", got)
	}
	if got := adapterCfg["poll_interval_seconds"]; got != 60 {
		t.Errorf("poll_interval_seconds = %v, want 60", got)
	}
}

func TestGenerateGatewayYAML_WhatsApp(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{
		"phone_number_id":"1234567890",
		"access_token":"EAAtoken",
		"app_secret":"appsecret",
		"verify_token":"verifytoken",
		"default_agent_name":"Manager"
	}`
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "wa-gw-1",
		Name:        "My WhatsApp GW",
		Type:        "whatsapp",
		Values:      &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}

	apps := parsed["apps"].([]any)
	appConfig := apps[0].(map[string]any)["app_config"].(map[string]any)
	gwAdapter := appConfig["gateway_adapter"].(map[string]any)
	if gwAdapter["type"] != "whatsapp" {
		t.Errorf("gateway_adapter.type = %q, want whatsapp", gwAdapter["type"])
	}
	adapterCfg := gwAdapter["adapter_config"].(map[string]any)

	for key, want := range map[string]string{
		"phone_number_id":    "1234567890",
		"access_token":       "EAAtoken",
		"app_secret":         "appsecret",
		"verify_token":       "verifytoken",
		"default_agent_name": "Manager",
	} {
		if got := adapterCfg[key]; got != want {
			t.Errorf("adapter_config.%s = %v, want %q", key, got, want)
		}
	}
	for _, key := range []string{"phone_number_id", "access_token", "app_secret", "verify_token", "default_agent_name"} {
		if _, leaked := appConfig[key]; leaked {
			t.Errorf("%s leaked to app_config root; the adapter parser would not see it", key)
		}
	}
}

func TestGenerateGatewayYAML_WhatsApp_RunAs(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	for _, tt := range []struct {
		name    string
		stored  string
		want    string
		present bool
	}{
		{
			name:   "unstored leaves senders authorizing as themselves",
			stored: "",
		},
		{
			name:    "a named system user is threaded",
			stored:  `,"run_as":"ops"`,
			want:    "ops",
			present: true,
		},
		{
			name:    "an explicit clear still reaches the adapter",
			stored:  `,"run_as":""`,
			want:    "",
			present: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewYAMLService("test-ns", slog.Default())
			values := `{
				"phone_number_id":"1234567890",
				"access_token":"EAAtoken",
				"app_secret":"appsecret",
				"verify_token":"verifytoken"` + tt.stored + `}`
			gw := &store.Gateway{
				Description: strPtr("test gateway for fixture purposes"),
				ID:          "wa-gw-runas",
				Name:        "Run-as WhatsApp GW",
				Type:        "whatsapp",
				Values:      &values,
			}

			yamlStr, err := svc.GenerateGatewayYAML(gw)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			var parsed map[string]any
			if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
				t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
			}
			apps := parsed["apps"].([]any)
			appConfig := apps[0].(map[string]any)["app_config"].(map[string]any)
			adapterCfg := appConfig["gateway_adapter"].(map[string]any)["adapter_config"].(map[string]any)

			got, present := adapterCfg["run_as"]
			if present != tt.present {
				t.Fatalf("adapter_config.run_as present = %v, want %v (config = %v)", present, tt.present, adapterCfg)
			}
			if tt.present && got != tt.want {
				t.Errorf("adapter_config.run_as = %v, want %q", got, tt.want)
			}
			if _, leaked := appConfig["run_as"]; leaked {
				t.Error("run_as leaked to app_config root; the adapter parser would not see it")
			}
		})
	}
}

func TestGenerateGatewayYAML_WhatsApp_SecretEnvFallback(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("WHATSAPP_ACCESS_TOKEN_WA_GW_2", "env-token")
	t.Setenv("WHATSAPP_APP_SECRET_WA_GW_2", "env-secret")
	t.Setenv("WHATSAPP_VERIFY_TOKEN_WA_GW_2", "env-verify")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{"phone_number_id":"1234567890"}`
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "wa-gw-2",
		Name:        "Env WhatsApp GW",
		Type:        "whatsapp",
		Values:      &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}
	adapterCfg := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)["gateway_adapter"].(map[string]any)["adapter_config"].(map[string]any)

	for key, want := range map[string]string{
		"access_token": "env-token",
		"app_secret":   "env-secret",
		"verify_token": "env-verify",
	} {
		if got := adapterCfg[key]; got != want {
			t.Errorf("adapter_config.%s = %v, want %q (env-var fallback)", key, got, want)
		}
	}

}

func TestGenerateGatewayYAML_EventMesh_CustomBroker(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{"broker_connection_type":"custom","broker_url":"tcp://broker:55555","broker_vpn":"default","broker_username":"admin","broker_password":"admin","event_rules":[]}`
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-001",
		Name:        "My Event Mesh GW",
		Type:        "event_mesh",
		Values:      &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if !strings.Contains(yamlStr, "event_mesh_broker_config:") {
		t.Errorf("expected event_mesh_broker_config block in:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "tcp://broker:55555") {
		t.Errorf("expected concrete custom broker URL in:\n%s", yamlStr)
	}
	if strings.Contains(yamlStr, "${BROKER_URL_") {
		t.Errorf("unresolved env var placeholder left in config:\n%s", yamlStr)
	}
}

func TestGenerateGatewayYAML_EventMesh_CustomBroker_TLSSkipVerify(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	cases := []struct {
		name       string
		values     string
		wantEmit   bool
		wantInYAML string
	}{
		{
			name:       "string true",
			values:     `{"broker_connection_type":"custom","broker_url":"tcps://broker:55443","broker_vpn":"default","broker_username":"admin","broker_password":"admin","tls_skip_verify":"true","event_rules":[]}`,
			wantEmit:   true,
			wantInYAML: "tls_skip_verify: true",
		},
		{
			name:     "string false",
			values:   `{"broker_connection_type":"custom","broker_url":"tcps://broker:55443","broker_vpn":"default","broker_username":"admin","broker_password":"admin","tls_skip_verify":"false","event_rules":[]}`,
			wantEmit: false,
		},
		{
			name:       "bool true",
			values:     `{"broker_connection_type":"custom","broker_url":"tcps://broker:55443","broker_vpn":"default","broker_username":"admin","broker_password":"admin","tls_skip_verify":true,"event_rules":[]}`,
			wantEmit:   true,
			wantInYAML: "tls_skip_verify: true",
		},
		{
			name:     "omitted",
			values:   `{"broker_connection_type":"custom","broker_url":"tcps://broker:55443","broker_vpn":"default","broker_username":"admin","broker_password":"admin","event_rules":[]}`,
			wantEmit: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewYAMLService("test-ns", slog.Default())
			values := tc.values
			gw := &store.Gateway{
				Description: strPtr("test"),
				ID:          "gw-tls",
				Name:        "TLS GW",
				Type:        "event_mesh",
				Values:      &values,
			}
			yamlStr, err := svc.GenerateGatewayYAML(gw)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			has := strings.Contains(yamlStr, "tls_skip_verify:")
			if tc.wantEmit && !has {
				t.Errorf("expected tls_skip_verify in:\n%s", yamlStr)
			}
			if !tc.wantEmit && has {
				t.Errorf("did not expect tls_skip_verify in:\n%s", yamlStr)
			}
			if tc.wantInYAML != "" && !strings.Contains(yamlStr, tc.wantInYAML) {
				t.Errorf("expected %q in:\n%s", tc.wantInYAML, yamlStr)
			}
		})
	}
}

func TestGenerateGatewayYAML_EventMesh_DefaultBroker_OmitsTLSSkipVerify(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("SOLACE_BROKER_URL", "tcps://platform-broker.example.com:55443")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{"broker_connection_type":"default","tls_skip_verify":"true","event_rules":[]}`
	gw := &store.Gateway{
		Description: strPtr("test"),
		ID:          "gw-def",
		Name:        "Default Broker",
		Type:        "event_mesh",
		Values:      &values,
	}
	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if strings.Contains(yamlStr, "tls_skip_verify:") {
		t.Errorf("did not expect tls_skip_verify under default broker:\n%s", yamlStr)
	}
}

func TestGenerateGatewayYAML_EventMesh_DefaultBroker_NeverInheritsSolaceBrokerPassword(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("SOLACE_BROKER_URL", "tcps://platform-broker.example.com:55443")
	t.Setenv("SOLACE_BROKER_VPN", "platform-vpn")
	t.Setenv("SOLACE_BROKER_USERNAME", "platform-user")
	t.Setenv("SOLACE_BROKER_PASSWORD", "platform-pass")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{"broker_connection_type":"default","broker_password":"gw-pass","event_rules":[]}`
	gw := &store.Gateway{
		Description: strPtr("test gateway for fixture purposes"),
		ID:          "gw-002",
		Name:        "Default Broker EM",
		Type:        "event_mesh",
		Values:      &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if !strings.Contains(yamlStr, "tcps://platform-broker.example.com:55443") {
		t.Errorf("expected the system broker URL (from SOLACE_BROKER_URL) in:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "gw-pass") {
		t.Errorf("expected the explicit per-gateway password in:\n%s", yamlStr)
	}
	if strings.Contains(yamlStr, "platform-pass") {
		t.Errorf("default mode must not inherit SOLACE_BROKER_PASSWORD:\n%s", yamlStr)
	}
	if strings.Contains(yamlStr, "${SOLACE_BROKER_URL}") || strings.Contains(yamlStr, "${BROKER_URL_") {
		t.Errorf("unresolved env var placeholder left in config:\n%s", yamlStr)
	}
}

func TestGenerateGatewayYAML_UnsupportedType(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	gw := &store.Gateway{ID: "test", Name: "Test", Type: "unknown_type"}
	_, err := svc.GenerateGatewayYAML(gw)
	if err == nil {
		t.Error("expected error for unsupported gateway type")
	}
}

func TestGenerateGatewayYAML_NilGateway(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	_, err := svc.GenerateGatewayYAML(nil)
	if err == nil {
		t.Error("expected error for nil gateway")
	}
}

func TestClassifyConnectionError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		contains string
	}{
		{"authentication_failure", fmt.Errorf("authentication failed for user"), "Authentication failed"},
		{"unauthorized", fmt.Errorf("Unauthorized access"), "Authentication failed"},
		{"login_failure", fmt.Errorf("login failure"), "Authentication failed"},
		{"unknown_vpn", fmt.Errorf("message vpn not found"), "Message VPN not found"},
		{"timeout", fmt.Errorf("connection timeout"), "timed out"},
		{"deadline_exceeded", fmt.Errorf("context deadline exceeded"), "timed out"},
		{"connection_refused", fmt.Errorf("connect: connection refused"), "Connection refused"},
		{"dns_failure", fmt.Errorf("no such host"), "Could not resolve the broker host"},
		{"solace_host_unresolved", fmt.Errorf("requested service is unreachable: host names in the hostlist 'tcps://x' could not be resolved from session '(c1,s1)' configuration property 'SESSION_HOST'"), "Could not resolve the broker host"},
		{"solace_unreachable", fmt.Errorf("requested service is unreachable"), "could not be reached"},
		{"tls_error", fmt.Errorf("tls: handshake failure"), "TLS/certificate error"},
		{"certificate_error", fmt.Errorf("x509: certificate signed by unknown authority"), "TLS/certificate error"},
		{"not_available", fmt.Errorf("broker connection testing not available in this build"), "not available in this build"},
		{"generic_error", fmt.Errorf("something unexpected"), "Connection failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := classifyConnectionError(tc.err)
			if !strings.Contains(result, tc.contains) {
				t.Errorf("classifyConnectionError(%q) = %q, want it to contain %q", tc.err, result, tc.contains)
			}
		})
	}
}

func TestGenerateAgentYAML_ModelProviderDefault(t *testing.T) {
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "general-model")
	svc := NewYAMLService("test-ns", slog.Default())
	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "mp-default", Name: "DefaultModel", CreatedBy: "test", UpdatedBy: "test",
	}
	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlStr, "model_provider:\n            - general") {
		t.Errorf("expected model_provider: [general] list form, got:\n%s", yamlStr)
	}
	assertNoLiteralModelBlock(t, yamlStr)
}

func TestGenerateAgentYAML_ModelProviderCustom(t *testing.T) {
	t.Setenv("LLM_SERVICE_PLANNING_MODEL_NAME", "planning-model")
	svc := NewYAMLService("test-ns", slog.Default())
	mp := "planning"
	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "mp-custom", Name: "PlanAgent", ModelProvider: &mp,
		CreatedBy: "test", UpdatedBy: "test",
	}
	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlStr, "model_provider:\n            - planning") {
		t.Errorf("expected model_provider: [planning] list form, got:\n%s", yamlStr)
	}
	if strings.Contains(yamlStr, "model_id: planning") {
		t.Errorf("legacy model_id map form should not be emitted, got:\n%s", yamlStr)
	}
	assertNoLiteralModelBlock(t, yamlStr)
}

func TestGenerateAgentYAML_ModelProviderGeneral(t *testing.T) {
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "general-model")
	svc := NewYAMLService("test-ns", slog.Default())
	mp := "general"
	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "mp-general", Name: "GeneralAgent", ModelProvider: &mp,
		CreatedBy: "test", UpdatedBy: "test",
	}
	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlStr, "model_provider:\n            - general") {
		t.Errorf("expected model_provider: [general] list form, got:\n%s", yamlStr)
	}
	assertNoLiteralModelBlock(t, yamlStr)
}

func TestGenerateAgentYAML_ModelProviderUUIDTranslated(t *testing.T) {
	t.Parallel()
	_, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	mc := &store.ModelConfiguration{
		Alias:           "general",
		Provider:        "openai",
		ModelName:       "gpt-4",
		ModelAuthType:   "apikey",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-test"}`,
		ModelParams:     "{}",
		CreatedBy:       "tester",
		UpdatedBy:       "tester",
	}
	apiBase := "https://llm.example.com/v1"
	mc.APIBase = &apiBase
	if err := repo.Create(ctx, mc); err != nil {
		t.Fatalf("seed model config: %v", err)
	}

	svc := NewYAMLService("test-ns", slog.Default())
	svc.SetModelConfigRepo(repo)
	agent := &store.Agent{
		ID: "mp-uuid", Name: "UUIDAgent", ModelProvider: &mc.ID,
		CreatedBy: "test", UpdatedBy: "test",
	}
	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlStr, "model_provider:\n            - general") {
		t.Errorf("expected model_provider translated to alias [general], got:\n%s", yamlStr)
	}
	if strings.Contains(yamlStr, mc.ID) {
		t.Errorf("UUID %q must not appear in emitted YAML, got:\n%s", mc.ID, yamlStr)
	}
}

func TestGenerateAgentYAML_ModelProviderNilWithRepo(t *testing.T) {
	t.Parallel()
	_, repo := setupModelConfigSvc(t)
	ctx := context.Background()

	apiBase := "https://llm.example.com/v1"
	if err := repo.Create(ctx, &store.ModelConfiguration{
		Alias: "general", Provider: "openai", ModelName: "gpt-4o",
		ModelAuthType: "apikey", ModelAuthConfig: `{"type":"apikey","api_key":"sk-test"}`,
		ModelParams: "{}", APIBase: &apiBase,
		CreatedBy: "tester", UpdatedBy: "tester",
	}); err != nil {
		t.Fatalf("seed general: %v", err)
	}

	svc := NewYAMLService("test-ns", slog.Default())
	svc.SetModelConfigRepo(repo)
	agent := &store.Agent{
		ID: "mp-nil", Name: "DefaultAgent",
		CreatedBy: "test", UpdatedBy: "test",
	}
	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlStr, "model_provider:\n            - general") {
		t.Errorf("expected default model_provider [general], got:\n%s", yamlStr)
	}
}

func TestGenerateAgentYAML_ModelProviderUnknownPassthrough(t *testing.T) {
	t.Parallel()
	_, repo := setupModelConfigSvc(t)

	svc := NewYAMLService("test-ns", slog.Default())
	svc.SetModelConfigRepo(repo)
	stale := "019e0000-0000-7000-8000-000000000000"
	agent := &store.Agent{
		ID: "mp-stale", Name: "StaleAgent", ModelProvider: &stale,
		CreatedBy: "test", UpdatedBy: "test",
	}
	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yamlStr, "model_provider:\n            - "+stale) {
		t.Errorf("expected unknown value %q emitted verbatim, got:\n%s", stale, yamlStr)
	}
}

func assertNoLiteralModelBlock(t *testing.T, yamlStr string) {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	apps, _ := parsed["apps"].([]any)
	if len(apps) == 0 {
		t.Fatalf("no apps parsed")
	}
	appConfig, _ := apps[0].(map[string]any)["app_config"].(map[string]any)
	if v, present := appConfig["model"]; present {
		t.Errorf("literal model: block must not appear in app_config, got: %#v", v)
	}
}

func TestGenerateAgentYAML_AgentNameIsUUIDDerived(t *testing.T) {
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://test.example.com")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")
	svc := NewYAMLService("test-ns", slog.Default())

	tests := []struct {
		name             string
		agentID          string
		agentDisplayName string
	}{
		{
			name:             "kebab name",
			agentID:          "019dac01-fe75-7591-81c9-b81706f03961",
			agentDisplayName: "sweep-multi-agent",
		},
		{
			name:             "simple alnum",
			agentID:          "aa111111-e29b-41d4-a716-446655440000",
			agentDisplayName: "MyAgent",
		},
		{
			name:             "spaces and specials",
			agentID:          "bb222222-e29b-41d4-a716-446655440000",
			agentDisplayName: "My Helpful Agent!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &store.Agent{
				Description: strPtr("test agent for fixture purposes"),
				ID:          tt.agentID,
				Name:        tt.agentDisplayName,
				CreatedBy:   "u", UpdatedBy: "u",
			}
			yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			var parsed map[string]any
			if unErr := yaml.Unmarshal([]byte(yamlStr), &parsed); unErr != nil {
				t.Fatalf("invalid YAML: %v", unErr)
			}
			apps := parsed["apps"].([]any)
			app := apps[0].(map[string]any)
			appCfg := app["app_config"].(map[string]any)

			wantAgentName := "agent_" + strings.ReplaceAll(tt.agentID, "-", "_")
			if app["name"] != wantAgentName {
				t.Errorf("app name = %q, want %q", app["name"], wantAgentName)
			}
			if appCfg["agent_name"] != wantAgentName {
				t.Errorf("agent_name = %q, want %q", appCfg["agent_name"], wantAgentName)
			}
			if appCfg["display_name"] != tt.agentDisplayName {
				t.Errorf("display_name = %q, want %q (original, unsanitized)",
					appCfg["display_name"], tt.agentDisplayName)
			}
		})
	}
}

func TestGenerateAgentYAML_DisplayNameSourcedFromName(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	cases := []struct {
		name      string
		agentName string
	}{
		{name: "slug-like name", agentName: "my-agent"},
		{name: "human label with space", agentName: "My Pretty Agent"},
		{name: "broker-unsafe characters", agentName: "agent/with spaces and ✨"},
	}
	const agentID = "11111111-2222-3333-4444-555555555555"
	const wantInstance = "agent_11111111_2222_3333_4444_555555555555"
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agent := &store.Agent{
				Description: strPtr("test agent for fixture purposes"),
				ID:          agentID,
				Name:        tt.agentName,
				CreatedBy:   "u", UpdatedBy: "u",
			}
			yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, nil)
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			var parsed map[string]any
			if unErr := yaml.Unmarshal([]byte(yamlStr), &parsed); unErr != nil {
				t.Fatalf("invalid YAML: %v", unErr)
			}
			appCfg := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)
			if got := appCfg["display_name"]; got != tt.agentName {
				t.Errorf("display_name = %q, want %q", got, tt.agentName)
			}
			if got := appCfg["agent_name"]; got != wantInstance {
				t.Errorf("agent_name = %q, want %q (UUID-derived)", got, wantInstance)
			}
		})
	}
}

func TestAgentInstanceName_AlwaysUUIDDerived(t *testing.T) {
	t.Parallel()
	const id = "abcdef01-2345-6789-abcd-ef0123456789"
	const want = "agent_abcdef01_2345_6789_abcd_ef0123456789"
	for _, name := range []string{
		"plain-name",
		"with spaces",
		"slashy/name",
		"unicode ✨",
		"",
	} {
		agent := &store.Agent{ID: id, Name: name}
		if got := AgentInstanceName(agent); got != want {
			t.Errorf("AgentInstanceName(name=%q) = %q, want %q", name, got, want)
		}
	}
	if got := AgentInstanceName(nil); got != "" {
		t.Errorf("AgentInstanceName(nil) = %q, want empty", got)
	}
}

func TestGenerateAgentYAMLWithPackages_ExpansionAndMix(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "agent-1",
		Name:        "Mix Agent",
		CreatedBy:   "u", UpdatedBy: "u",
	}

	packages := map[string]ToolPackageInfo{
		"my_pkg": {
			ToolFiles: []store.ToolPackageFile{
				{ToolName: "alpha"},
				{ToolName: "beta"},
			},
		},
	}
	toolsets := []string{"builtin_artifact_tools", "my_pkg"}

	yamlStr, err := svc.GenerateAgentYAMLWithPackages(agent, toolsets, nil, nil, nil, packages)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLWithPackages: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}
	apps := parsed["apps"].([]any)
	app := apps[0].(map[string]any)
	cfg := app["app_config"].(map[string]any)
	tools := cfg["tools"].([]any)

	if len(tools) != 3 {
		t.Fatalf("expected 3 tool entries (1 group + 2 expanded), got %d:\n%v", len(tools), tools)
	}
	first := tools[0].(map[string]any)
	if first["tool_type"] != "builtin-group" || first["group_name"] != "builtin_artifact_tools" {
		t.Errorf("first tool: want builtin-group/builtin_artifact_tools, got %v", first)
	}
	wantNames := map[string]bool{"my_pkg__alpha": true, "my_pkg__beta": true}
	for _, tt := range tools[1:] {
		entry := tt.(map[string]any)
		if entry["tool_type"] != "builtin" {
			t.Errorf("expanded entry tool_type = %v, want builtin", entry["tool_type"])
		}
		name, _ := entry["tool_name"].(string)
		if !wantNames[name] {
			t.Errorf("unexpected tool_name %q (want one of %v)", name, wantNames)
		}
		delete(wantNames, name)
	}
	if len(wantNames) != 0 {
		t.Errorf("missing expanded tools: %v", wantNames)
	}
}

func TestBuildAgentConfigWithPackages_ToolConfigEmission(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	schema := `[
		{"key":"api_key","secret":true},
		{"key":"base_url","secret":false,"default":"https://api.example.com"},
		{"key":"timeout","secret":false}
	]`
	schemaPtr := schema

	packages := map[string]ToolPackageInfo{
		"weather_tools": {
			ToolFiles: []store.ToolPackageFile{
				{ToolName: "get_weather", ConfigSchema: &schemaPtr},
			},
			ConfigValues: map[string]map[string]any{
				"get_weather": {
					"api_key": "my-secret-key",
					"timeout": 30,
				},
			},
		},
	}

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "abc12345-1234-1234-1234-123456789012",
		Name:        "Weather Agent",
		CreatedBy:   "u", UpdatedBy: "u",
	}

	yamlStr, err := svc.GenerateAgentYAMLWithPackages(agent, []string{"weather_tools"}, nil, nil, nil, packages)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLWithPackages: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}
	apps := parsed["apps"].([]any)
	cfg := apps[0].(map[string]any)["app_config"].(map[string]any)
	tools := cfg["tools"].([]any)

	if len(tools) != 1 {
		t.Fatalf("expected 1 tool entry, got %d", len(tools))
	}
	entry := tools[0].(map[string]any)
	if entry["tool_name"] != "weather_tools__get_weather" {
		t.Errorf("tool_name = %v, want weather_tools__get_weather", entry["tool_name"])
	}

	toolCfg, ok := entry["tool_config"].(map[string]any)
	if !ok {
		t.Fatalf("expected tool_config map, got %T: %v", entry["tool_config"], entry["tool_config"])
	}

	apiKey, _ := toolCfg["api_key"].(string)
	if apiKey != "my-secret-key" {
		t.Errorf("api_key (secret with value) = %q, want plaintext inline value", apiKey)
	}
	if strings.HasPrefix(apiKey, "${TC_") {
		t.Errorf("api_key must not be a ${TC_*} placeholder; got %q", apiKey)
	}

	baseURL, _ := toolCfg["base_url"].(string)
	if baseURL != "https://api.example.com" {
		t.Errorf("base_url (non-secret with default) = %q, want https://api.example.com", baseURL)
	}

	timeout := toolCfg["timeout"]
	if timeout != 30 {
		t.Errorf("timeout (from config_values) = %v, want 30", timeout)
	}

	if _, present := toolCfg["undefined_key"]; present {
		t.Error("unexpected key in tool_config")
	}

	if strings.Contains(yamlStr, "${TC_") {
		t.Errorf("no ${TC_*} placeholder should appear in YAML:\n%s", yamlStr)
	}
}

func TestBuildAgentConfigWithPackages_AuthEmission(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	authJSON := `{"type":"oauth2","scheme":{"authorization_url":"https://idp.example.com/authorize","token_url":"https://idp.example.com/token","scopes":["calendar.read","calendar.write"],"token_endpoint_auth_method":"client_secret_post"}}`
	authPtr := authJSON

	packages := map[string]ToolPackageInfo{
		"cal_tools": {
			ToolFiles: []store.ToolPackageFile{
				{ToolName: "lookup_event", Auth: &authPtr},
				{ToolName: "list_events"},
			},
		},
	}

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "deadbeef-1234-1234-1234-123456789012",
		Name:        "Calendar Agent",
		CreatedBy:   "u", UpdatedBy: "u",
	}

	yamlStr, err := svc.GenerateAgentYAMLWithPackages(agent, []string{"cal_tools"}, nil, nil, nil, packages)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLWithPackages: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}
	apps := parsed["apps"].([]any)
	cfg := apps[0].(map[string]any)["app_config"].(map[string]any)
	tools := cfg["tools"].([]any)

	var lookupEntry, listEntry map[string]any
	for _, ti := range tools {
		entry := ti.(map[string]any)
		switch entry["tool_name"] {
		case "cal_tools__lookup_event":
			lookupEntry = entry
		case "cal_tools__list_events":
			listEntry = entry
		}
	}
	if lookupEntry == nil || listEntry == nil {
		t.Fatalf("missing tool entries; got tools = %+v", tools)
	}

	authMap, ok := lookupEntry["auth"].(map[string]any)
	if !ok {
		t.Fatalf("expected auth: block on lookup_event, got %T: %v", lookupEntry["auth"], lookupEntry["auth"])
	}
	if authMap["type"] != "oauth2" {
		t.Errorf("auth.type = %v", authMap["type"])
	}
	scheme, ok := authMap["scheme"].(map[string]any)
	if !ok {
		t.Fatalf("expected scheme: map, got %T", authMap["scheme"])
	}
	if scheme["authorization_url"] != "https://idp.example.com/authorize" {
		t.Errorf("authorization_url = %v", scheme["authorization_url"])
	}
	if scheme["token_url"] != "https://idp.example.com/token" {
		t.Errorf("token_url = %v", scheme["token_url"])
	}
	if scheme["token_endpoint_auth_method"] != "client_secret_post" {
		t.Errorf("token_endpoint_auth_method = %v", scheme["token_endpoint_auth_method"])
	}
	scopes := scheme["scopes"].([]any)
	if len(scopes) != 2 || scopes[0] != "calendar.read" {
		t.Errorf("scopes = %v", scopes)
	}

	if _, present := listEntry["auth"]; present {
		t.Errorf("list_events should not have auth block; got %+v", listEntry["auth"])
	}
}

func TestBuildAgentConfigWithPackages_AuthEmissionMalformed(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	for _, tc := range []struct {
		name string
		auth string
	}{
		{"invalid_json", `{not valid json}`},
		{"empty_type", `{"type":""}`},
		{"empty_string", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			authPtr := tc.auth
			packages := map[string]ToolPackageInfo{
				"pkg": {
					ToolFiles: []store.ToolPackageFile{
						{ToolName: "tool1", Auth: &authPtr},
					},
				},
			}
			agent := &store.Agent{
				Description: strPtr("test agent for fixture purposes"),
				ID:          "deadbeef-1111-2222-3333-123456789012",
				Name:        "Test",
				CreatedBy:   "u", UpdatedBy: "u",
			}
			yamlStr, err := svc.GenerateAgentYAMLWithPackages(agent, []string{"pkg"}, nil, nil, nil, packages)
			if err != nil {
				t.Fatalf("GenerateAgentYAMLWithPackages: %v", err)
			}
			var parsed map[string]any
			if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
				t.Fatalf("invalid YAML: %v", err)
			}
			tools := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)["tools"].([]any)
			entry := tools[0].(map[string]any)
			if _, present := entry["auth"]; present {
				t.Errorf("malformed auth column should not emit an auth: block; got %+v", entry["auth"])
			}
		})
	}
}

func TestBuildAgentConfig_SkillsToolConfigEmission(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "abc12345-1234-1234-1234-123456789012",
		Name:        "Skill Agent",
		CreatedBy:   "u", UpdatedBy: "u",
	}

	cfg, err := svc.BuildAgentConfigFull(agent, nil, nil, nil, nil, ToolEmission{}, DeployOptions{
		SkillNames: []string{"manim", "no_config_skill", "empty_skill"},
		SkillConfigsByName: map[string]map[string]map[string]any{
			"manim": {
				"render":  {"api_key": "sk-secret", "region": "us-east-1"},
				"cleanup": {"ttl_seconds": 3600},
			},
			"empty_skill": {},
		},
	})
	if err != nil {
		t.Fatalf("BuildAgentConfigFull: %v", err)
	}

	apps := cfg["apps"].([]map[string]any)
	appCfg := apps[0]["app_config"].(map[string]any)
	skills, ok := appCfg["skills"].([]map[string]any)
	if !ok {
		t.Fatalf("skills list type = %T, want []map[string]any", appCfg["skills"])
	}
	if len(skills) != 3 {
		t.Fatalf("expected 3 skill entries, got %d", len(skills))
	}

	bySkillName := map[string]map[string]any{}
	for _, sk := range skills {
		name, _ := sk["name"].(string)
		bySkillName[name] = sk
	}

	manim := bySkillName["manim"]
	tc, ok := manim["tool_config"].(map[string]any)
	if !ok {
		t.Fatalf("manim tool_config missing or wrong type: %T", manim["tool_config"])
	}
	render := tc["render"].(map[string]any)
	if render["api_key"] != "sk-secret" {
		t.Errorf("manim.render.api_key = %v, want sk-secret", render["api_key"])
	}
	if render["region"] != "us-east-1" {
		t.Errorf("manim.render.region = %v, want us-east-1", render["region"])
	}
	cleanup := tc["cleanup"].(map[string]any)
	if cleanup["ttl_seconds"] != 3600 {
		t.Errorf("manim.cleanup.ttl_seconds = %v, want 3600", cleanup["ttl_seconds"])
	}

	noCfg := bySkillName["no_config_skill"]
	if _, has := noCfg["tool_config"]; has {
		t.Errorf("no_config_skill should have no tool_config, got %v", noCfg)
	}

	empty := bySkillName["empty_skill"]
	if _, has := empty["tool_config"]; has {
		t.Errorf("empty_skill should have no tool_config (empty map), got %v", empty)
	}
}

func TestBuildAgentConfig_SkillsToolConfigDropsEmptyPerToolMaps(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "abc12345-1234-1234-1234-123456789012",
		Name:        "Sparse Agent",
		CreatedBy:   "u", UpdatedBy: "u",
	}

	cfg, err := svc.BuildAgentConfigFull(agent, nil, nil, nil, nil, ToolEmission{}, DeployOptions{
		SkillNames: []string{"sparse"},
		SkillConfigsByName: map[string]map[string]map[string]any{
			"sparse": {
				"render": {"api_key": "sk-set"},
				"unused": {},
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildAgentConfigFull: %v", err)
	}

	apps := cfg["apps"].([]map[string]any)
	appCfg := apps[0]["app_config"].(map[string]any)
	skills := appCfg["skills"].([]map[string]any)
	tc, ok := skills[0]["tool_config"].(map[string]any)
	if !ok {
		t.Fatalf("sparse tool_config missing: %T", skills[0]["tool_config"])
	}
	if _, has := tc["unused"]; has {
		t.Errorf("empty per-tool map should be suppressed, got %v", tc)
	}
	if _, has := tc["render"]; !has {
		t.Errorf("populated per-tool map should be present, got %v", tc)
	}
}

func TestBuildAgentConfigWithPackages_NoConfigSchema(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	packages := map[string]ToolPackageInfo{
		"simple_tools": {
			ToolFiles: []store.ToolPackageFile{
				{ToolName: "do_thing"},
			},
		},
	}

	agent := &store.Agent{ID: "agent-x", Name: "Simple", CreatedBy: "u", UpdatedBy: "u"}
	yamlStr, err := svc.GenerateAgentYAMLWithPackages(agent, []string{"simple_tools"}, nil, nil, nil, packages)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLWithPackages: %v", err)
	}

	if strings.Contains(yamlStr, "tool_config:") {
		t.Errorf("expected no tool_config block when config_schema is absent:\n%s", yamlStr)
	}
}

func TestGenerateAgentYAMLWithPackages_ExcludedTools(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "agent-excl",
		Name:        "Excl Agent",
		CreatedBy:   "u", UpdatedBy: "u",
	}

	packages := map[string]ToolPackageInfo{
		"multi_tools": {
			ToolFiles: []store.ToolPackageFile{
				{ToolName: "tool_a"},
				{ToolName: "tool_b"},
				{ToolName: "tool_c"},
			},
			ExcludedTools: []string{"tool_b"},
		},
	}
	toolsets := []string{"multi_tools"}

	yamlStr, err := svc.GenerateAgentYAMLWithPackages(agent, toolsets, nil, nil, nil, packages)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLWithPackages: %v", err)
	}

	if !strings.Contains(yamlStr, "multi_tools__tool_a") {
		t.Errorf("expected tool_a to appear in YAML:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "multi_tools__tool_c") {
		t.Errorf("expected tool_c to appear in YAML:\n%s", yamlStr)
	}
	if strings.Contains(yamlStr, "multi_tools__tool_b") {
		t.Errorf("excluded tool_b must not appear in YAML:\n%s", yamlStr)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}
	apps := parsed["apps"].([]any)
	cfg := apps[0].(map[string]any)["app_config"].(map[string]any)
	tools := cfg["tools"].([]any)
	if len(tools) != 2 {
		t.Errorf("expected 2 tool entries (tool_a + tool_c), got %d:\n%v", len(tools), tools)
	}
}

func TestStandardDeployer_ToolPackage_Expansion(t *testing.T) {
	db := testOpenDB(t)
	ctx := context.Background()
	repo := store.NewToolPackageRepo(db)

	pkg := &store.ToolPackage{
		Name:            "calc_tools",
		DiscoveryStatus: "pending",
		CreatedBy:       "tester",
		UpdatedBy:       "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	for _, name := range []string{"add", "multiply"} {
		if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{
			PackageID: pkg.ID,
			ToolName:  name,
		}); err != nil {
			t.Fatalf("upsert tool file %q: %v", name, err)
		}
		if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); err != nil {
			t.Fatalf("flip package to ready: %v", err)
		}
	}
	if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); err != nil {
		t.Fatalf("flip package to ready: %v", err)
	}

	deployer := NewStandardAgentDeployer(NewYAMLService("test-ns", slog.Default()), nil, repo, nil, slog.Default())
	agent := &store.Agent{
		ID:           idgen.New(),
		Description:  strPtr("test agent for fixture purposes"),
		Name:         "Pkg Agent",
		SystemPrompt: strPtr("You add and multiply numbers."),
		CreatedBy:    "u", UpdatedBy: "u",
	}
	res, err := deployer.BuildPayloads(ctx, DeployInput{
		Agent:    agent,
		Action:   DeployActionDeploy,
		Toolsets: []string{"calc_tools"},
	})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}
	if len(res.Payloads) == 0 {
		t.Fatal("no payloads produced")
	}
	if !strings.Contains(res.SnapshotYAML, "calc_tools__add") ||
		!strings.Contains(res.SnapshotYAML, "calc_tools__multiply") {
		t.Errorf("snapshot YAML missing expanded tool names:\n%s", res.SnapshotYAML)
	}
	if strings.Contains(res.SnapshotYAML, "group_name: calc_tools") {
		t.Errorf("snapshot YAML still emits builtin-group for known package:\n%s", res.SnapshotYAML)
	}
}

func TestStandardDeployer_ConfigValues_Injected(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := context.Background()
	repo := store.NewToolPackageRepo(db)
	agentRepo := store.NewAgentRepo(db)

	schema := `[{"key":"api_key","secret":true},{"key":"endpoint","secret":false}]`

	pkg := &store.ToolPackage{
		Name:            "weather_tools",
		DiscoveryStatus: "pending",
		CreatedBy:       "tester",
		UpdatedBy:       "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{
		PackageID:    pkg.ID,
		ToolName:     "get_weather",
		ConfigSchema: &schema,
	}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}
	if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); err != nil {
		t.Fatalf("flip package to ready: %v", err)
	}

	agent := &store.Agent{
		Description:  strPtr("test agent for fixture purposes"),
		Name:         "Weather Agent",
		SystemPrompt: strPtr("You are a weather agent."),
		CreatedBy:    "u", UpdatedBy: "u",
	}
	if err := agentRepo.Create(ctx, agent, []string{"weather_tools"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	configVals := `{"get_weather":{"api_key":"super-secret","endpoint":"https://weather.example.com"}}`
	if err := repo.SetToolsetConfigsForAgent(ctx, agent.ID, []store.AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: pkg.ID, ConfigValues: &configVals},
	}); err != nil {
		t.Fatalf("set toolset configs: %v", err)
	}

	deployer := NewStandardAgentDeployer(NewYAMLService("test-ns", slog.Default()), nil, repo, nil, slog.Default())
	res, err := deployer.BuildPayloads(ctx, DeployInput{
		Agent:    agent,
		Action:   DeployActionDeploy,
		Toolsets: []string{"weather_tools"},
	})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}

	if !strings.Contains(res.SnapshotYAML, "tool_config:") {
		t.Errorf("expected tool_config: block in snapshot YAML:\n%s", res.SnapshotYAML)
	}

	if !strings.Contains(res.SnapshotYAML, "super-secret") {
		t.Errorf("secret api_key value must be inlined plaintext in snapshot YAML:\n%s", res.SnapshotYAML)
	}

	if strings.Contains(res.SnapshotYAML, "${TC_") {
		t.Errorf("${TC_*} placeholder must not appear in snapshot YAML:\n%s", res.SnapshotYAML)
	}

	if !strings.Contains(res.SnapshotYAML, "https://weather.example.com") {
		t.Errorf("expected non-secret endpoint value inlined in snapshot YAML:\n%s", res.SnapshotYAML)
	}
}

func TestStandardDeployer_BuiltinToolsetConfig_Injected(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := context.Background()
	repo := store.NewToolPackageRepo(db)
	agentRepo := store.NewAgentRepo(db)

	agent := &store.Agent{
		Description:  strPtr("test agent for fixture purposes"),
		Name:         "Builtin Config Agent",
		SystemPrompt: strPtr("You are a builtin-tools agent."),
		CreatedBy:    "u", UpdatedBy: "u",
	}
	if err := agentRepo.Create(ctx, agent, []string{"builtin_web_request_tools", "builtin_artifact_tools"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	configVals := `{"web_search_google":{"max_iterations":3,"sources":["web"]}}`
	if err := repo.SetBuiltinToolsetConfigsForAgent(ctx, agent.ID, []store.AgentBuiltinToolsetConfig{
		{AgentID: agent.ID, ToolsetName: "builtin_web_request_tools", ConfigValues: &configVals},
	}); err != nil {
		t.Fatalf("set builtin toolset configs: %v", err)
	}

	deployer := NewStandardAgentDeployer(NewYAMLService("test-ns", slog.Default()), nil, repo, nil, slog.Default())
	res, err := deployer.BuildPayloads(ctx, DeployInput{
		Agent:    agent,
		Action:   DeployActionDeploy,
		Toolsets: []string{"builtin_web_request_tools", "builtin_artifact_tools"},
	})
	if err != nil {
		t.Fatalf("BuildPayloads: %v", err)
	}

	if !strings.Contains(res.SnapshotYAML, "tool_config:") {
		t.Errorf("expected tool_config: block in snapshot YAML:\n%s", res.SnapshotYAML)
	}
	if !strings.Contains(res.SnapshotYAML, "max_iterations: 3") {
		t.Errorf("expected max_iterations: 3 inlined in snapshot YAML:\n%s", res.SnapshotYAML)
	}
	if !strings.Contains(res.SnapshotYAML, "tool_name: web_search_google") {
		t.Errorf("expected per-tool entry for web_search_google in snapshot YAML:\n%s", res.SnapshotYAML)
	}
	if !strings.Contains(res.SnapshotYAML, "group_name: builtin_artifact_tools") {
		t.Errorf("expected legacy builtin-group entry for builtin_artifact_tools in snapshot YAML:\n%s", res.SnapshotYAML)
	}
}

func TestBuildToolConfigForFile_SecretWithNoValue_Omitted(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())

	schema := `[{"key":"api_key","secret":true}]`
	schemaPtr := schema

	packages := map[string]ToolPackageInfo{
		"my_pkg": {
			ToolFiles: []store.ToolPackageFile{
				{ToolName: "my_tool", ConfigSchema: &schemaPtr},
			},
		},
	}

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		ID:          "aabb1122-0000-0000-0000-000000000000",
		Name:        "Omit Agent",
		CreatedBy:   "u", UpdatedBy: "u",
	}

	yamlStr, err := svc.GenerateAgentYAMLWithPackages(agent, []string{"my_pkg"}, nil, nil, nil, packages)
	if err != nil {
		t.Fatalf("GenerateAgentYAMLWithPackages: %v", err)
	}

	if strings.Contains(yamlStr, "${TC_") {
		t.Errorf("no ${TC_*} placeholder should appear in YAML:\n%s", yamlStr)
	}
	if strings.Contains(yamlStr, "api_key") {
		t.Errorf("secret api_key with no config_value and no default must be omitted from YAML:\n%s", yamlStr)
	}
	if strings.Contains(yamlStr, "tool_config:") {
		t.Errorf("tool_config block must be absent when all fields are omitted:\n%s", yamlStr)
	}
}

func TestBuildToolConfigForFile_SecretWithValue_Inline(t *testing.T) {
	t.Parallel()
	schema := `[{"key":"token","secret":true}]`
	f := store.ToolPackageFile{ToolName: "my_tool", ConfigSchema: &schema}
	cfg := buildToolConfigForFile(f, map[string]any{"token": "s3cr3t"})
	if cfg == nil {
		t.Fatal("expected non-nil tool_config")
	}
	if cfg["token"] != "s3cr3t" {
		t.Errorf("token = %v, want s3cr3t", cfg["token"])
	}
}

func TestBuildToolConfigForFile_SecretMissingNoDefault_Omitted(t *testing.T) {
	t.Parallel()
	schema := `[{"key":"token","secret":true}]`
	f := store.ToolPackageFile{ToolName: "my_tool", ConfigSchema: &schema}
	cfg := buildToolConfigForFile(f, nil)
	if cfg != nil {
		t.Errorf("expected nil tool_config (all fields omitted), got %v", cfg)
	}
}

func TestBuildToolConfigForFile_SecretMissingWithDefault_UsesDefault(t *testing.T) {
	t.Parallel()
	schema := `[{"key":"token","secret":true,"default":"default-val"}]`
	f := store.ToolPackageFile{ToolName: "my_tool", ConfigSchema: &schema}
	cfg := buildToolConfigForFile(f, nil)
	if cfg == nil {
		t.Fatal("expected non-nil tool_config")
	}
	if cfg["token"] != "default-val" {
		t.Errorf("token = %v, want default-val", cfg["token"])
	}
}

func TestBuildToolConfigForFile_MixedSecretAndNonSecret(t *testing.T) {
	t.Parallel()
	schema := `[
		{"key":"api_key","secret":true},
		{"key":"endpoint","secret":false},
		{"key":"missing_secret","secret":true}
	]`
	f := store.ToolPackageFile{ToolName: "my_tool", ConfigSchema: &schema}
	cfg := buildToolConfigForFile(f, map[string]any{
		"api_key":  "sk-abc",
		"endpoint": "https://example.com",
	})
	if cfg == nil {
		t.Fatal("expected non-nil tool_config")
	}
	if cfg["api_key"] != "sk-abc" {
		t.Errorf("api_key = %v, want sk-abc", cfg["api_key"])
	}
	if cfg["endpoint"] != "https://example.com" {
		t.Errorf("endpoint = %v, want https://example.com", cfg["endpoint"])
	}
	if _, present := cfg["missing_secret"]; present {
		t.Error("missing_secret must be omitted (no value, no default)")
	}
}

func TestBuildToolConfigForFile_NilVsEmptyConfigValues(t *testing.T) {
	t.Parallel()
	schema := `[{"key":"api_key","secret":true},{"key":"base_url","default":"https://x.com"}]`
	f := store.ToolPackageFile{ToolName: "my_tool", ConfigSchema: &schema}
	cfgNil := buildToolConfigForFile(f, nil)
	cfgEmpty := buildToolConfigForFile(f, map[string]any{})

	if fmt.Sprintf("%v", cfgNil) != fmt.Sprintf("%v", cfgEmpty) {
		t.Errorf("nil and empty configValues produced different results: nil=%v empty=%v", cfgNil, cfgEmpty)
	}

	if cfgNil != nil {
		if _, present := cfgNil["api_key"]; present {
			t.Error("api_key (no value, no default) must be omitted")
		}
		if cfgNil["base_url"] != "https://x.com" {
			t.Errorf("base_url default = %v, want https://x.com", cfgNil["base_url"])
		}
	}
}

func TestStandardDeployer_Validate_MissingRequiredSecret(t *testing.T) {
	db := testOpenDB(t)
	ctx := context.Background()
	repo := store.NewToolPackageRepo(db)
	agentRepo := store.NewAgentRepo(db)

	schema := `[{"key":"api_key","secret":true}]`
	pkg := &store.ToolPackage{
		Name: "secret_tools", DiscoveryStatus: "pending",
		CreatedBy: "tester", UpdatedBy: "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{
		PackageID: pkg.ID, ToolName: "do_thing", ConfigSchema: &schema,
	}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}
	if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); err != nil {
		t.Fatalf("flip package to ready: %v", err)
	}

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		Name:        "Sec Agent", SystemPrompt: strPtr("You help."),
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := agentRepo.Create(ctx, agent, []string{"secret_tools"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	deployer := NewStandardAgentDeployer(NewYAMLService("test-ns", slog.Default()), nil, repo, nil, slog.Default())
	err := deployer.Validate(ctx, DeployInput{
		Agent:    agent,
		Action:   DeployActionDeploy,
		Toolsets: []string{"secret_tools"},
	})
	if err == nil {
		t.Fatal("expected ValidationError for missing required secret")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(valErr.Message, "api_key") {
		t.Errorf("error message should mention missing key 'api_key', got: %s", valErr.Message)
	}
}

func TestStandardDeployer_Validate_SecretWithDefault_OK(t *testing.T) {
	db := testOpenDB(t)
	ctx := context.Background()
	repo := store.NewToolPackageRepo(db)
	agentRepo := store.NewAgentRepo(db)

	schema := `[{"key":"api_key","secret":true,"default":"built-in-key"}]`
	pkg := &store.ToolPackage{
		Name: "default_tools", DiscoveryStatus: "pending",
		CreatedBy: "tester", UpdatedBy: "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{
		PackageID: pkg.ID, ToolName: "do_thing", ConfigSchema: &schema,
	}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}
	if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); err != nil {
		t.Fatalf("flip package to ready: %v", err)
	}

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		Name:        "Default Agent", SystemPrompt: strPtr("You help."),
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := agentRepo.Create(ctx, agent, []string{"default_tools"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	deployer := NewStandardAgentDeployer(NewYAMLService("test-ns", slog.Default()), nil, repo, nil, slog.Default())
	if err := deployer.Validate(ctx, DeployInput{
		Agent:    agent,
		Action:   DeployActionDeploy,
		Toolsets: []string{"default_tools"},
	}); err != nil {
		t.Errorf("expected no error when secret has default, got: %v", err)
	}
}

func TestStandardDeployer_Validate_SecretWithConfigValues_OK(t *testing.T) {
	db := testOpenDB(t)
	ctx := context.Background()
	repo := store.NewToolPackageRepo(db)
	agentRepo := store.NewAgentRepo(db)

	schema := `[{"key":"api_key","secret":true}]`
	pkg := &store.ToolPackage{
		Name: "cv_tools", DiscoveryStatus: "pending",
		CreatedBy: "tester", UpdatedBy: "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{
		PackageID: pkg.ID, ToolName: "do_thing", ConfigSchema: &schema,
	}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}
	if err := repo.UpdateDiscoveryStatus(ctx, pkg.ID, "ready", nil); err != nil {
		t.Fatalf("flip package to ready: %v", err)
	}

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		Name:        "CV Agent", SystemPrompt: strPtr("You help."),
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := agentRepo.Create(ctx, agent, []string{"cv_tools"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	configVals := `{"do_thing":{"api_key":"real-value"}}`
	if err := repo.SetToolsetConfigsForAgent(ctx, agent.ID, []store.AgentToolsetConfig{
		{AgentID: agent.ID, PackageID: pkg.ID, ConfigValues: &configVals},
	}); err != nil {
		t.Fatalf("set toolset configs: %v", err)
	}

	deployer := NewStandardAgentDeployer(NewYAMLService("test-ns", slog.Default()), nil, repo, nil, slog.Default())
	if err := deployer.Validate(ctx, DeployInput{
		Agent:    agent,
		Action:   DeployActionDeploy,
		Toolsets: []string{"cv_tools"},
	}); err != nil {
		t.Errorf("expected no error when secret is in config_values, got: %v", err)
	}
}

func TestBuildEventMeshConnectorTool_Basic(t *testing.T) {
	t.Parallel()

	conn := ConnectorConfig{
		ID:   "abc-123-def-456",
		Type: "event_mesh",
		Values: map[string]any{
			"broker_connection_type": "custom",
			"broker_url":             "tcps://broker.example.com:55443",
			"broker_vpn":             "production",
			"broker_username":        "agent-mesh",
			"broker_password":        "secret123",
			"tool_name":              "get_order_status",
			"tool_description":       "Get the status of a customer order",
			"topic":                  "orders/v1/{{ order_id }}/status",
			"wait_for_response":      "true",
			"request_expiry_ms":      float64(30000),
			"payload_format":         "json",
		},
	}

	result := buildEventMeshConnectorTool(conn, "ABC123DEF456")

	if result["tool_name"] == "" {
		t.Error("expected non-empty tool_name")
	}
	if result["tool_type"] != "builtin" {
		t.Errorf("expected tool_type 'builtin', got %v", result["tool_type"])
	}
	if result["timeout_seconds"] != eventMeshToolTimeoutSeconds {
		t.Errorf("expected timeout %d, got %v", eventMeshToolTimeoutSeconds, result["timeout_seconds"])
	}

	toolCfg := result["tool_config"].(map[string]any)
	if toolCfg["_str_binary"] != "event_mesh_request" {
		t.Errorf("expected _str_binary 'event_mesh_request', got %v", toolCfg["_str_binary"])
	}
	if toolCfg["topic"] != "orders/v1/{{ order_id }}/status" {
		t.Errorf("expected topic with template, got %v", toolCfg["topic"])
	}
	if toolCfg["description"] != "Get the status of a customer order" {
		t.Errorf("expected description, got %v", toolCfg["description"])
	}
	if toolCfg["wait_for_response"] != true {
		t.Errorf("expected wait_for_response true, got %v", toolCfg["wait_for_response"])
	}

	emCfg := toolCfg["event_mesh_config"].(map[string]any)
	if emCfg["request_expiry_ms"] != 30000 {
		t.Errorf("expected request_expiry_ms 30000, got %v", emCfg["request_expiry_ms"])
	}
	if emCfg["payload_format"] != "json" {
		t.Errorf("expected payload_format 'json', got %v", emCfg["payload_format"])
	}

	brokerCfg := emCfg["broker_config"].(map[string]any)
	if brokerCfg["broker_url"] != "tcps://broker.example.com:55443" {
		t.Errorf("expected broker_url, got %v", brokerCfg["broker_url"])
	}
}

func TestBuildEventMeshConnectorTool_WithParameters(t *testing.T) {
	t.Parallel()

	conn := ConnectorConfig{
		ID:   "abc-123",
		Type: "event_mesh",
		Values: map[string]any{
			"broker_connection_type": "custom",
			"broker_url":             "tcp://localhost:55555",
			"broker_vpn":             "default",
			"broker_username":        "user",
			"broker_password":        "pass",
			"tool_name":              "lookup_user",
			"topic":                  "users/{{ user_id }}",
			"parameters":             `[{"name": "user_id", "type": "string", "required": true, "description": "User ID", "payload_path": "request.user_id"}]`,
		},
	}

	result := buildEventMeshConnectorTool(conn, "ABC123")
	toolCfg := result["tool_config"].(map[string]any)

	params, ok := toolCfg["parameters"].([]map[string]any)
	if !ok {
		t.Fatal("expected parameters array")
	}
	if len(params) != 1 {
		t.Fatalf("expected 1 parameter, got %d", len(params))
	}
	if params[0]["name"] != "user_id" {
		t.Errorf("expected param name 'user_id', got %v", params[0]["name"])
	}
	if params[0]["payload_path"] != "request.user_id" {
		t.Errorf("expected payload_path, got %v", params[0]["payload_path"])
	}
}

func TestBuildEventMeshConnectorTool_Defaults(t *testing.T) {
	t.Parallel()

	conn := ConnectorConfig{
		ID:   "min-123",
		Type: "event_mesh",
		Values: map[string]any{
			"broker_connection_type": "custom",
			"broker_url":             "tcp://localhost:55555",
			"broker_vpn":             "default",
			"broker_username":        "user",
			"broker_password":        "pass",
			"tool_name":              "my_tool",
			"topic":                  "test/topic",
		},
	}

	result := buildEventMeshConnectorTool(conn, "MIN123")
	toolCfg := result["tool_config"].(map[string]any)
	emCfg := toolCfg["event_mesh_config"].(map[string]any)

	if toolCfg["wait_for_response"] != true {
		t.Errorf("expected default wait_for_response true, got %v", toolCfg["wait_for_response"])
	}
	if emCfg["request_expiry_ms"] != 60000 {
		t.Errorf("expected default request_expiry_ms 60000, got %v", emCfg["request_expiry_ms"])
	}
	if emCfg["payload_format"] != "json" {
		t.Errorf("expected default payload_format 'json', got %v", emCfg["payload_format"])
	}
}

func TestBuildEventMeshConnectorTool_FireAndForget(t *testing.T) {
	t.Parallel()

	conn := ConnectorConfig{
		ID:   "ff-123",
		Type: "event_mesh",
		Values: map[string]any{
			"broker_connection_type": "custom",
			"broker_url":             "tcp://localhost:55555",
			"broker_vpn":             "default",
			"broker_username":        "user",
			"broker_password":        "pass",
			"tool_name":              "emit_event",
			"topic":                  "events/notification",
			"wait_for_response":      "false",
		},
	}

	result := buildEventMeshConnectorTool(conn, "FF123")
	toolCfg := result["tool_config"].(map[string]any)

	if toolCfg["wait_for_response"] != false {
		t.Errorf("expected wait_for_response false, got %v", toolCfg["wait_for_response"])
	}
}

func TestBuildEventMeshConnectorTool_SAMBroker_NeverInheritsAgentMeshBrokerCredentials(t *testing.T) {
	t.Parallel()

	conn := ConnectorConfig{
		ID:   "sam-broker-123",
		Type: "event_mesh",
		Values: map[string]any{
			"broker_connection_type": "sam",
			"broker_url":             "tcps://platform-broker.example.com:55443",
			"broker_vpn":             "platform-vpn",
			"broker_username":        "platform-user",
			"tool_name":              "my_tool",
			"topic":                  "test/topic",
		},
	}

	result := buildEventMeshConnectorTool(conn, "SAMBROKER123")
	toolCfg := result["tool_config"].(map[string]any)
	emCfg := toolCfg["event_mesh_config"].(map[string]any)
	brokerCfg := emCfg["broker_config"].(map[string]any)

	if brokerCfg["broker_url"] != "tcps://platform-broker.example.com:55443" {
		t.Errorf("expected literal broker_url, got %v", brokerCfg["broker_url"])
	}
	if brokerCfg["broker_vpn"] != "platform-vpn" {
		t.Errorf("expected literal broker_vpn, got %v", brokerCfg["broker_vpn"])
	}
	if brokerCfg["broker_username"] != "platform-user" {
		t.Errorf("expected literal broker_username, got %v", brokerCfg["broker_username"])
	}
	if brokerCfg["broker_password"] != "${BROKER_PASSWORD_SAMBROKER123}" {
		t.Errorf("expected per-connector secret broker_password, got %v", brokerCfg["broker_password"])
	}

	for k, v := range brokerCfg {
		if s, ok := v.(string); ok && strings.Contains(s, "SOLACE_BROKER") {
			t.Errorf("broker_config[%s]=%q must not reference SOLACE_BROKER_*", k, s)
		}
	}

	if _, exists := brokerCfg["tls_skip_verify"]; exists {
		t.Error("expected tls_skip_verify to NOT be set by the connector")
	}
}

func TestBuildEventMeshConnectorTool_NeverEmitsTLSSkipVerify(t *testing.T) {
	t.Parallel()

	conn := ConnectorConfig{
		ID:   "tls-123",
		Type: "event_mesh",
		Values: map[string]any{
			"broker_connection_type": "custom",
			"broker_url":             "tcps://broker:55443",
			"broker_vpn":             "default",
			"broker_username":        "user",
			"broker_password":        "pass",
			"tls_skip_verify":        "true",
			"tool_name":              "t",
			"topic":                  "a/b",
		},
	}

	result := buildEventMeshConnectorTool(conn, "TLS123")
	brokerCfg := result["tool_config"].(map[string]any)["event_mesh_config"].(map[string]any)["broker_config"].(map[string]any)
	if _, exists := brokerCfg["tls_skip_verify"]; exists {
		t.Errorf("connector must never emit tls_skip_verify, got %v", brokerCfg["tls_skip_verify"])
	}
}

func TestBuildEventMeshConnectorTool_QoSMapping(t *testing.T) {
	t.Parallel()

	base := func(qos string) ConnectorConfig {
		v := map[string]any{
			"broker_connection_type": "custom",
			"broker_url":             "tcps://broker:55443",
			"broker_vpn":             "default",
			"broker_username":        "user",
			"broker_password":        "pass",
			"tool_name":              "q",
			"topic":                  "a/b",
		}
		if qos != "" {
			v["qos"] = qos
		}
		return ConnectorConfig{ID: "qos-123", Type: "event_mesh", Values: v}
	}

	emCfg := func(conn ConnectorConfig) map[string]any {
		return buildEventMeshConnectorTool(conn, "QOS123")["tool_config"].(map[string]any)["event_mesh_config"].(map[string]any)
	}

	if got := emCfg(base("0"))["qos"]; got != 0 {
		t.Errorf("qos=0 → emitted %v, want 0", got)
	}
	if got := emCfg(base("1"))["qos"]; got != 1 {
		t.Errorf("qos=1 → emitted %v, want 1", got)
	}
	if _, exists := emCfg(base(""))["qos"]; exists {
		t.Error("absent qos must not be emitted")
	}
}

func TestBuildEventMeshConnectorTool_ReplyModeMapping(t *testing.T) {
	t.Parallel()

	base := func(rm string) ConnectorConfig {
		v := map[string]any{
			"broker_connection_type": "custom",
			"broker_url":             "tcps://broker:55443",
			"broker_vpn":             "default",
			"broker_username":        "user",
			"broker_password":        "pass",
			"tool_name":              "r",
			"topic":                  "a/b",
		}
		if rm != "" {
			v["reply_mode"] = rm
		}
		return ConnectorConfig{ID: "rm-123", Type: "event_mesh", Values: v}
	}

	emCfg := func(conn ConnectorConfig) map[string]any {
		return buildEventMeshConnectorTool(conn, "RM123")["tool_config"].(map[string]any)["event_mesh_config"].(map[string]any)
	}

	if got := emCfg(base("temp_queue"))["reply_mode"]; got != "temp_queue" {
		t.Errorf("reply_mode=temp_queue → emitted %v", got)
	}
	if got := emCfg(base("p2p_inbox"))["reply_mode"]; got != "p2p_inbox" {
		t.Errorf("reply_mode=p2p_inbox → emitted %v", got)
	}
	if _, exists := emCfg(base(""))["reply_mode"]; exists {
		t.Error("absent reply_mode must not be emitted")
	}
}

func TestBuildEventMeshConnectorTools_MultipleOperations(t *testing.T) {
	t.Parallel()

	conn := ConnectorConfig{
		ID:   "multi-op-123",
		Type: "event_mesh",
		Values: map[string]any{
			"broker_connection_type": "custom",
			"broker_url":             "tcps://broker.example.com:55443",
			"broker_vpn":             "production",
			"broker_username":        "agent",
			"broker_password":        "secret",
			"operations": []any{
				map[string]any{
					"tool_name":         "get_order_status",
					"tool_description":  "Get the status of an order",
					"topic":             "acme/orders/v1/{order_id}/status",
					"wait_for_response": true,
					"payload_format":    "json",
					"request_expiry_ms": float64(30000),
					"input_schema": map[string]any{
						"type":     "object",
						"required": []any{"order_id"},
						"properties": map[string]any{
							"order_id": map[string]any{"type": "string", "x-solace-payload-path": "request.order_id"},
						},
					},
				},
				map[string]any{
					"tool_name":         "publish_inventory_update",
					"tool_description":  "Publish an inventory adjustment",
					"topic":             "acme/inventory/v1/update",
					"wait_for_response": false,
					"payload_format":    "yaml",
					"input_schema":      map[string]any{"type": "object", "properties": map[string]any{}},
				},
			},
		},
	}

	tools := buildEventMeshConnectorTools(conn, "MULTIOP123")
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools (one per operation), got %d", len(tools))
	}

	suffix := connectorSuffix(conn.ID)

	first := tools[0]
	if want := "get_order_status_" + suffix; first["tool_name"] != want {
		t.Errorf("tool[0] name = %v, want %v", first["tool_name"], want)
	}
	firstCfg := first["tool_config"].(map[string]any)
	if firstCfg["topic"] != "acme/orders/v1/{order_id}/status" {
		t.Errorf("tool[0] topic = %v", firstCfg["topic"])
	}
	if firstCfg["wait_for_response"] != true {
		t.Errorf("tool[0] wait_for_response = %v, want true", firstCfg["wait_for_response"])
	}
	params, ok := firstCfg["parameters"].([]map[string]any)
	if !ok || len(params) != 1 {
		t.Fatalf("tool[0] expected 1 derived parameter, got %v", firstCfg["parameters"])
	}
	if params[0]["name"] != "order_id" || params[0]["payload_path"] != "request.order_id" {
		t.Errorf("tool[0] derived parameter = %v", params[0])
	}

	second := tools[1]
	if want := "publish_inventory_update_" + suffix; second["tool_name"] != want {
		t.Errorf("tool[1] name = %v, want %v", second["tool_name"], want)
	}
	secondCfg := second["tool_config"].(map[string]any)
	if secondCfg["wait_for_response"] != false {
		t.Errorf("tool[1] wait_for_response = %v, want false", secondCfg["wait_for_response"])
	}
	if got := secondCfg["event_mesh_config"].(map[string]any)["payload_format"]; got != "yaml" {
		t.Errorf("tool[1] payload_format = %v, want yaml", got)
	}
}

func TestGenerateAgentYAML_WithEventMeshConnector(t *testing.T) {
	t.Parallel()
	svc := NewYAMLService("test-ns", slog.Default())
	agent := &store.Agent{
		ID:          "evt-mesh-agent",
		Name:        "Event Mesh Agent",
		Description: strPtr("Agent with Event Mesh connector"),
		CreatedBy:   "test",
		UpdatedBy:   "test",
	}

	connectors := []ConnectorConfig{
		{
			ID:      "2b673ec2-14b9-40e4-bc85-e511f379ff8b",
			Type:    "event_mesh",
			Subtype: "solace",
			Values: map[string]any{
				"broker_connection_type": "custom",
				"broker_url":             "tcps://broker.example.com:55443",
				"broker_vpn":             "production",
				"broker_username":        "agent",
				"broker_password":        "secret",
				"tool_name":              "get_order",
				"tool_description":       "Get order details",
				"topic":                  "orders/v1/{{ order_id }}",
			},
		},
	}

	yamlStr, err := svc.GenerateAgentYAML(agent, nil, nil, nil, connectors)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if !strings.Contains(yamlStr, "_str_binary: event_mesh_request") {
		t.Errorf("expected '_str_binary: event_mesh_request' in YAML:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "tool_type: builtin") {
		t.Errorf("expected 'tool_type: builtin' in YAML:\n%s", yamlStr)
	}
	if !strings.Contains(yamlStr, "orders/v1/{{ order_id }}") {
		t.Errorf("expected topic template in YAML:\n%s", yamlStr)
	}
}

func TestGenerateGatewayYAML_MCP_AuthOn(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("EXTERNAL_AUTH_PROVIDER", "azure")
	t.Setenv("AUTHORIZATION_TYPE", "default_rbac")
	t.Setenv("ROLE_DEFINITIONS_PATH", "/app/data/config/auth/role-to-scope-definitions.yaml")
	t.Setenv("USER_ROLES_PATH", "/app/data/config/auth/user-to-role-assignments.yaml")
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.test")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{
		"enableAuth": "true",
		"allowedRedirectUris": [
			{"uri": "https://claude.ai/api/mcp/auth_callback"},
			{"uri": "http://127.0.0.1"}
		],
		"includeTools": [{"pattern": "fetch_.*"}],
		"excludeTools": [{"pattern": ".*_debug"}],
		"corsAllowedOrigins": [
			{"origin": "http://127.0.0.1:3010"},
			{"origin": "http://localhost:6274"}
		],
		"corsAllowedOriginRegex": "^http://localhost:\\d+$",
		"serverName": "Solace Agent Mesh",
		"taskTimeoutSeconds": 600
	}`
	gw := &store.Gateway{
		Description: strPtr("test mcp gateway for fixture purposes"),
		ID:          "mcp-gw-1",
		Slug:        "mcp-gw-1",
		Name:        "My MCP GW",
		Type:        "mcp",
		Values:      &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}

	apps := parsed["apps"].([]any)
	appConfig := apps[0].(map[string]any)["app_config"].(map[string]any)

	if appConfig["frontend_use_authorization"] != true {
		t.Errorf("expected frontend_use_authorization true, got %v", appConfig["frontend_use_authorization"])
	}
	if appConfig["external_auth_provider"] != "azure" {
		t.Errorf("expected external_auth_provider 'azure' (resolved from EXTERNAL_AUTH_PROVIDER env), got %v", appConfig["external_auth_provider"])
	}
	authSvc, ok := appConfig["authorization_service"].(map[string]any)
	if !ok {
		t.Fatalf("expected authorization_service block, got %v", appConfig["authorization_service"])
	}
	if authSvc["type"] != "default_rbac" {
		t.Errorf("expected authorization_service.type 'default_rbac' (resolved from AUTHORIZATION_TYPE env), got %v", authSvc["type"])
	}
	if authSvc["role_to_scope_definitions_path"] != "/app/data/config/auth/role-to-scope-definitions.yaml" {
		t.Errorf("unexpected role_to_scope_definitions_path: %v", authSvc["role_to_scope_definitions_path"])
	}

	gwAdapter := appConfig["gateway_adapter"].(map[string]any)
	if gwAdapter["type"] != "mcp" {
		t.Errorf("expected gateway_adapter type 'mcp', got %q", gwAdapter["type"])
	}
	adapterCfg := gwAdapter["adapter_config"].(map[string]any)
	if adapterCfg["enable_auth"] != true {
		t.Errorf("expected enable_auth true, got %v", adapterCfg["enable_auth"])
	}
	if _, present := adapterCfg["endpoint_path"]; present {
		t.Errorf("endpoint_path must not be emitted (MCP mounts at the gateway root), got %q", adapterCfg["endpoint_path"])
	}
	if adapterCfg["issuer"] != "https://sam.example.test/gw/mcp-gw-1" {
		t.Errorf("unexpected issuer: %v", adapterCfg["issuer"])
	}
	if adapterCfg["external_auth_provider"] != "azure" {
		t.Errorf("expected adapter_config.external_auth_provider 'azure', got %v", adapterCfg["external_auth_provider"])
	}
	redirects, ok := adapterCfg["allowed_redirect_uris"].([]any)
	if !ok || len(redirects) != 2 {
		t.Fatalf("expected 2 flattened redirect URIs, got %v", adapterCfg["allowed_redirect_uris"])
	}
	if redirects[0] != "https://claude.ai/api/mcp/auth_callback" || redirects[1] != "http://127.0.0.1" {
		t.Errorf("unexpected redirect URIs: %v", redirects)
	}
	if got, _ := adapterCfg["include_tools"].([]any); len(got) != 1 || got[0] != "fetch_.*" {
		t.Errorf("unexpected include_tools: %v", adapterCfg["include_tools"])
	}
	if got, _ := adapterCfg["exclude_tools"].([]any); len(got) != 1 || got[0] != ".*_debug" {
		t.Errorf("unexpected exclude_tools: %v", adapterCfg["exclude_tools"])
	}
	origins, ok := adapterCfg["cors_allowed_origins"].([]any)
	if !ok || len(origins) != 2 {
		t.Fatalf("expected 2 flattened CORS origins, got %v", adapterCfg["cors_allowed_origins"])
	}
	if origins[0] != "http://127.0.0.1:3010" || origins[1] != "http://localhost:6274" {
		t.Errorf("unexpected CORS origins: %v", origins)
	}
	if adapterCfg["cors_allowed_origin_regex"] != `^http://localhost:\d+$` {
		t.Errorf("unexpected cors_allowed_origin_regex: %v", adapterCfg["cors_allowed_origin_regex"])
	}
	if adapterCfg["server_name"] != "Solace Agent Mesh" {
		t.Errorf("unexpected server_name: %v", adapterCfg["server_name"])
	}
	tts, _ := adapterCfg["task_timeout_seconds"].(int)
	if tts != 600 {
		t.Errorf("unexpected task_timeout_seconds: %v", adapterCfg["task_timeout_seconds"])
	}
	if _, present := adapterCfg["default_user_identity"]; present {
		t.Errorf("default_user_identity must be absent when enableAuth=true: %v", adapterCfg["default_user_identity"])
	}
}

func TestGenerateGatewayYAML_MCP_RBACPathsDefaultMatchPlatform(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("EXTERNAL_AUTH_PROVIDER", "azure")
	t.Setenv("AUTHORIZATION_TYPE", "default_rbac")
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	unsetEnv(t, "ROLE_DEFINITIONS_PATH")
	unsetEnv(t, "USER_ROLES_PATH")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{"enableAuth": "true"}`
	gw := &store.Gateway{
		ID:     "mcp-gw-1",
		Slug:   "mcp-gw-1",
		Name:   "My MCP GW",
		Type:   "mcp",
		Values: &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}
	appConfig := parsed["apps"].([]any)[0].(map[string]any)["app_config"].(map[string]any)
	authSvc := appConfig["authorization_service"].(map[string]any)

	if got := authSvc["role_to_scope_definitions_path"]; got != configbuilder.DefaultRoleDefinitionsPath {
		t.Errorf("MCP builder role path fallback = %v, want %q (shared default)", got, configbuilder.DefaultRoleDefinitionsPath)
	}
	if got := authSvc["user_to_role_assignments_path"]; got != configbuilder.DefaultUserRolesPath {
		t.Errorf("MCP builder user path fallback = %v, want %q (shared default)", got, configbuilder.DefaultUserRolesPath)
	}

	platformAuthz := loadPlatformAuthzBlock(t)
	if got := platformAuthz["role_to_scope_definitions_path"]; got != configbuilder.DefaultRoleDefinitionsPath {
		t.Errorf("platform.yaml role default = %v, want %q", got, configbuilder.DefaultRoleDefinitionsPath)
	}
	if got := platformAuthz["user_to_role_assignments_path"]; got != configbuilder.DefaultUserRolesPath {
		t.Errorf("platform.yaml user default = %v, want %q", got, configbuilder.DefaultUserRolesPath)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	_ = os.Unsetenv(key)
}

func loadPlatformAuthzBlock(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("../../../configs/gwe/platform.yaml")
	if err != nil {
		t.Fatalf("read platform.yaml: %v", err)
	}
	loaded, err := (&config.YAMLLoader{}).LoadBytes(context.Background(), data)
	if err != nil {
		t.Fatalf("load platform.yaml: %v", err)
	}
	apps, ok := loaded.GetRaw("apps").([]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("apps = %v, want at least one app", loaded.GetRaw("apps"))
	}
	app, _ := apps[0].(map[string]any)
	appCfg, _ := app["app_config"].(map[string]any)
	authz, ok := appCfg["authorization_service"].(map[string]any)
	if !ok {
		t.Fatalf("authorization_service missing or wrong type: %T", appCfg["authorization_service"])
	}
	return authz
}

func TestGenerateGatewayYAML_MCP_AuthOff(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{"enableAuth": "false", "defaultUserIdentity": "mcp_dev_user"}`
	gw := &store.Gateway{
		Description: strPtr("test mcp gateway for fixture purposes"),
		ID:          "mcp-dev",
		Name:        "Dev MCP",
		Type:        "mcp",
		Values:      &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}

	apps := parsed["apps"].([]any)
	appConfig := apps[0].(map[string]any)["app_config"].(map[string]any)

	if _, present := appConfig["frontend_use_authorization"]; present {
		t.Errorf("frontend_use_authorization must be absent when enableAuth=false")
	}
	if _, present := appConfig["authorization_service"]; present {
		t.Errorf("authorization_service must be absent when enableAuth=false")
	}

	gwAdapter := appConfig["gateway_adapter"].(map[string]any)
	adapterCfg := gwAdapter["adapter_config"].(map[string]any)
	if adapterCfg["enable_auth"] != false {
		t.Errorf("expected enable_auth false, got %v", adapterCfg["enable_auth"])
	}
	if adapterCfg["default_user_identity"] != "mcp_dev_user" {
		t.Errorf("expected default_user_identity 'mcp_dev_user', got %v", adapterCfg["default_user_identity"])
	}
	if _, present := adapterCfg["allowed_redirect_uris"]; present {
		t.Errorf("allowed_redirect_uris must be absent when enableAuth=false")
	}
	if _, present := adapterCfg["issuer"]; present {
		t.Errorf("issuer must be absent when WEBUI_FRONTEND_SERVER_URL is unset (local-dev fallback): got %v", adapterCfg["issuer"])
	}
}

func TestGenerateGatewayYAML_MCP_FlattenMixedShapeAllowedURIs(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")

	svc := NewYAMLService("test-ns", slog.Default())
	values := `{
		"enableAuth": "true",
		"allowedRedirectUris": [
			{"uri": "https://claude.ai/api/mcp/auth_callback"},
			"http://127.0.0.1",
			{"uri": ""},
			""
		]
	}`
	gw := &store.Gateway{
		Description: strPtr("test mcp gateway for fixture purposes"),
		ID:          "mcp-mixed",
		Name:        "Mixed Shape MCP",
		Type:        "mcp",
		Values:      &values,
	}

	yamlStr, err := svc.GenerateGatewayYAML(gw)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, yamlStr)
	}

	apps := parsed["apps"].([]any)
	appConfig := apps[0].(map[string]any)["app_config"].(map[string]any)
	gwAdapter := appConfig["gateway_adapter"].(map[string]any)
	adapterCfg := gwAdapter["adapter_config"].(map[string]any)

	redirects, ok := adapterCfg["allowed_redirect_uris"].([]any)
	if !ok {
		t.Fatalf("expected []any for allowed_redirect_uris, got %T", adapterCfg["allowed_redirect_uris"])
	}
	if len(redirects) != 2 {
		t.Fatalf("expected 2 valid redirect URIs (empty entries filtered out), got %v", redirects)
	}
	if redirects[0] != "https://claude.ai/api/mcp/auth_callback" {
		t.Errorf("first redirect should be the {uri} object value, got %v", redirects[0])
	}
	if redirects[1] != "http://127.0.0.1" {
		t.Errorf("second redirect should be the bare-string value, got %v", redirects[1])
	}
}
