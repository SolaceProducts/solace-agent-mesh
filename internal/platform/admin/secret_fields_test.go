// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"strings"
	"testing"
)

func TestMergeSecretFields_NullPreserves(t *testing.T) {
	t.Parallel()

	existing := map[string]any{
		"broker_password":        "real_secret",
		"broker_connection_type": "custom",
	}
	newVals := map[string]any{
		"broker_password":        nil,
		"broker_connection_type": "custom",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	result, err := mergeSecretFields(newVals, existing, secretFields)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["broker_password"] != "real_secret" {
		t.Errorf("broker_password = %v, want %q (null must preserve existing — BUG-023)",
			result["broker_password"], "real_secret")
	}
}

func TestMergeSecretFields_NullWithoutExisting_Removes(t *testing.T) {
	t.Parallel()

	existing := map[string]any{
		"broker_connection_type": "custom",
	}
	newVals := map[string]any{
		"broker_password":        nil,
		"broker_connection_type": "custom",
	}

	secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
	result, err := mergeSecretFields(newVals, existing, secretFields)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := result["broker_password"]; present {
		t.Errorf("broker_password should not be present when null + no existing; got %v", result["broker_password"])
	}
}

func TestMergeSecretFields_RejectsNonStringTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  any
	}{
		{"object", map[string]any{"hidden": "sk"}},
		{"array", []any{"sk-1", "sk-2"}},
		{"number", 42},
		{"float", 3.14},
		{"bool true", true},
		{"bool false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			existing := map[string]any{
				"broker_password":        "real_secret",
				"broker_connection_type": "custom",
			}
			newVals := map[string]any{
				"broker_password":        tt.val,
				"broker_connection_type": "custom",
			}
			secretFields := getSecretRegistry().GatewaySecretFields("event_mesh")
			_, err := mergeSecretFields(newVals, existing, secretFields)
			if err == nil {
				t.Fatalf("expected validation error for %T value, got nil", tt.val)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("expected *ValidationError, got %T: %v", err, err)
			}
			if !strings.Contains(ve.Message, "broker_password") {
				t.Errorf("error %q should mention the field name", ve.Message)
			}
			if !strings.Contains(ve.Message, "string") {
				t.Errorf("error %q should mention the expected type", ve.Message)
			}
		})
	}
}

func TestMergeHiddenFields_OmittedPreservesExisting(t *testing.T) {
	t.Parallel()

	existing := map[string]any{
		"tool_name":  "my_tool",
		"server_url": "https://example.com",
	}
	newVals := map[string]any{
		"server_url": "https://example.com",
	}
	hidden := map[string]bool{"tool_name": true}

	mergeHiddenFields(newVals, existing, hidden)

	if newVals["tool_name"] != "my_tool" {
		t.Errorf("tool_name = %v, want %q (omitted hidden field must be preserved)", newVals["tool_name"], "my_tool")
	}
}

func TestMergeHiddenFields_ExplicitValuePassesThrough(t *testing.T) {
	t.Parallel()

	existing := map[string]any{"tool_name": "old_tool"}
	newVals := map[string]any{"tool_name": "new_tool"}
	hidden := map[string]bool{"tool_name": true}

	mergeHiddenFields(newVals, existing, hidden)

	if newVals["tool_name"] != "new_tool" {
		t.Errorf("tool_name = %v, want %q (explicit value must not be overwritten)", newVals["tool_name"], "new_tool")
	}
}

func TestMergeHiddenFields_NoExistingValue_OmittedStaysMissing(t *testing.T) {
	t.Parallel()

	existing := map[string]any{"server_url": "https://example.com"}
	newVals := map[string]any{"server_url": "https://example.com"}
	hidden := map[string]bool{"tool_name": true}

	mergeHiddenFields(newVals, existing, hidden)

	if _, present := newVals["tool_name"]; present {
		t.Errorf("tool_name should be absent when neither request nor existing has a value; got %v", newVals["tool_name"])
	}
}

func TestMergeHiddenFields_EmptyRegistry_NoOp(t *testing.T) {
	t.Parallel()

	existing := map[string]any{"tool_name": "my_tool"}
	newVals := map[string]any{"server_url": "https://example.com"}
	hidden := map[string]bool{}

	mergeHiddenFields(newVals, existing, hidden)

	if _, present := newVals["tool_name"]; present {
		t.Errorf("tool_name should not be injected when not in hidden registry")
	}
	if newVals["server_url"] != "https://example.com" {
		t.Errorf("server_url unexpectedly modified")
	}
}

func TestHiddenRegistry_MCPRemoteHasExpectedFields(t *testing.T) {
	t.Parallel()

	hidden := getHiddenRegistry().ConnectorHiddenFields("mcp", "remote")

	for _, field := range []string{"tool_name", "tool_name_prefix", "deny_list", "manifest", "hil"} {
		if !hidden[field] {
			t.Errorf("field %q should be registered as hidden for mcp/remote", field)
		}
	}

	for _, field := range []string{"server_url", "connection_type", "custom_headers", "allow_list", "auth_type"} {
		if hidden[field] {
			t.Errorf("field %q should NOT be registered as hidden for mcp/remote", field)
		}
	}
}

func TestMergeHiddenFields_MultipleFields(t *testing.T) {
	t.Parallel()

	existing := map[string]any{
		"tool_name":        "my_tool",
		"tool_name_prefix": "prefix_",
		"deny_list":        "tool_x,tool_y",
		"server_url":       "https://example.com",
	}
	newVals := map[string]any{
		"server_url": "https://example.com",
	}
	hidden := map[string]bool{
		"tool_name":        true,
		"tool_name_prefix": true,
		"deny_list":        true,
	}

	mergeHiddenFields(newVals, existing, hidden)

	for field, want := range map[string]string{
		"tool_name":        "my_tool",
		"tool_name_prefix": "prefix_",
		"deny_list":        "tool_x,tool_y",
	} {
		if newVals[field] != want {
			t.Errorf("%s = %v, want %q", field, newVals[field], want)
		}
	}
}

func TestMergeHiddenFields_NullPreservesExisting(t *testing.T) {
	t.Parallel()

	existing := map[string]any{"tool_name": "my_tool", "server_url": "https://example.com"}
	newVals := map[string]any{
		"tool_name":  nil,
		"server_url": "https://example.com",
	}
	hidden := map[string]bool{"tool_name": true}

	mergeHiddenFields(newVals, existing, hidden)

	if newVals["tool_name"] != "my_tool" {
		t.Errorf("tool_name = %v, want %q (null must preserve existing — mirrors BUG-023)", newVals["tool_name"], "my_tool")
	}
}

func TestMergeHiddenFields_NullWithoutExisting_Removes(t *testing.T) {
	t.Parallel()

	existing := map[string]any{"server_url": "https://example.com"}
	newVals := map[string]any{
		"tool_name":  nil,
		"server_url": "https://example.com",
	}
	hidden := map[string]bool{"tool_name": true}

	mergeHiddenFields(newVals, existing, hidden)

	if _, present := newVals["tool_name"]; present {
		t.Errorf("tool_name should be absent when null + no existing value; got %v", newVals["tool_name"])
	}
}

func TestMergeHiddenFields_MutexConflictRejectedAfterMerge(t *testing.T) {
	t.Parallel()

	existing := map[string]any{
		"tool_name":       "my_tool",
		"server_url":      "https://example.com",
		"connection_type": "streamable-http",
		"auth_type":       "none",
	}
	newVals := map[string]any{
		"allow_list":      "tool_x",
		"server_url":      "https://example.com",
		"connection_type": "streamable-http",
		"auth_type":       "none",
	}

	if err := validateConnectorValues("mcp", "remote", newVals); err != nil {
		t.Fatalf("pre-merge validation should pass: %v", err)
	}

	hidden := getHiddenRegistry().ConnectorHiddenFields("mcp", "remote")
	mergeHiddenFields(newVals, existing, hidden)

	if newVals["tool_name"] != "my_tool" {
		t.Fatalf("tool_name should be restored by merge, got %v", newVals["tool_name"])
	}

	err := validateConnectorValues("mcp", "remote", newVals)
	if err == nil {
		t.Fatal("expected validation error after merge reveals mutex conflict, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(ve.Message, "tool_name") || !strings.Contains(ve.Message, "allow_list") {
		t.Errorf("error %q should mention both conflicting fields", ve.Message)
	}
}

func TestRedactSecretFields_BedrockKnowledgeBase(t *testing.T) {
	t.Parallel()

	values := map[string]any{
		"kb_id":                 "ABC123",
		"region":                "us-east-1",
		"auth_type":             "access_key",
		"aws_access_key_id":     "AKIA-PLAINTEXT-MUST-NOT-LEAK",
		"aws_secret_access_key": "secret-MUST-NOT-LEAK",
		"external_id":           "external-MUST-NOT-LEAK",
		"role_name":             "BedrockKBAccessRole",
	}

	secretFields := getSecretRegistry().ConnectorSecretFields("knowledge_base", "bedrock")

	for _, field := range []string{"aws_access_key_id", "aws_secret_access_key", "external_id"} {
		if _, isSecret := secretFields[field]; !isSecret {
			t.Errorf("field %q must be registered as secret", field)
		}
	}

	redacted := redactSecretFields(values, secretFields)

	for _, field := range []string{"aws_access_key_id", "aws_secret_access_key", "external_id"} {
		if got := redacted[field]; got != redactedSentinel {
			t.Errorf("%s = %v, want %q (BUG-017/033 — must be redacted)", field, got, redactedSentinel)
		}
	}

	if redacted["kb_id"] != "ABC123" {
		t.Errorf("non-secret kb_id was redacted: %v", redacted["kb_id"])
	}
	if redacted["region"] != "us-east-1" {
		t.Errorf("non-secret region was redacted: %v", redacted["region"])
	}
}

func TestHasConnectorType_TellsNoSecretsFromAnUnknownType(t *testing.T) {
	t.Parallel()
	reg := &secretFieldRegistry{
		connectorFields: map[string]map[string]secretFieldInfo{},
		connectorTypes:  map[string]bool{"plain/basic": true},
	}
	if len(reg.ConnectorSecretFields("plain", "basic")) != 0 {
		t.Fatal("the fixture must declare no secret field, or it tests nothing")
	}
	if !reg.HasConnectorType("plain", "basic") {
		t.Error("a declared type with no secret field must still be known")
	}
	if reg.HasConnectorType("plain", "other") {
		t.Error("a subtype no definition declares must not be known")
	}
	if reg.HasConnectorType("unknown", "basic") {
		t.Error("a type no definition declares must not be known")
	}
}

func TestConnectorSecretFieldsKnown_ReadsTheEmbeddedDefinitions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		connType, subtype string
		want              bool
	}{
		{"api", "openapi", true},
		{"api", "not_a_declared_subtype", false},
		{"not_a_registered_type", "generic", false},
	} {
		if got := ConnectorSecretFieldsKnown(tc.connType, tc.subtype); got != tc.want {
			t.Errorf("ConnectorSecretFieldsKnown(%q, %q) = %v, want %v", tc.connType, tc.subtype, got, tc.want)
		}
	}
}
