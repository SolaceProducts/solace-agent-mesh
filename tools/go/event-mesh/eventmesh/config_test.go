// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"strings"
	"testing"
)

func minimalConfigMap() map[string]any {
	return map[string]any{
		"tool_name":   "GetWeather",
		"description": "Gets the current weather for a city.",
		"topic":       "acme/weather/{request_id}",
		"parameters": []any{
			map[string]any{
				"name":         "city",
				"type":         "string",
				"required":     true,
				"description":  "The city to get the weather for.",
				"payload_path": "location.city",
			},
			map[string]any{
				"name":         "unit",
				"type":         "string",
				"default":      "celsius",
				"payload_path": "unit",
			},
		},
		"event_mesh_config": map[string]any{
			"broker_config": map[string]any{
				"broker_url":      "tcp://localhost:55555",
				"broker_username": "u",
				"broker_password": "p",
				"broker_vpn":      "default",
			},
			"request_expiry_ms": 5000,
			"payload_format":    "json",
		},
	}
}

func TestLoadConfig_HappyPath(t *testing.T) {
	cfg, err := LoadConfig(minimalConfigMap())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ToolName != "GetWeather" {
		t.Fatalf("unexpected tool name: %q", cfg.ToolName)
	}
	if len(cfg.Parameters) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(cfg.Parameters))
	}
	if cfg.EventMeshConfig.RequestExpiryMs != 5000 {
		t.Fatalf("request_expiry_ms not preserved: %d", cfg.EventMeshConfig.RequestExpiryMs)
	}
	if !cfg.ShouldWaitForResponse() {
		t.Fatal("default wait_for_response should be true")
	}
}

func TestLoadConfig_DefaultsApplied(t *testing.T) {
	raw := minimalConfigMap()
	em := raw["event_mesh_config"].(map[string]any)
	delete(em, "request_expiry_ms")
	delete(em, "payload_format")

	cfg, err := LoadConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.EventMeshConfig.RequestExpiryMs != 60000 {
		t.Fatalf("default request_expiry_ms not applied: %d", cfg.EventMeshConfig.RequestExpiryMs)
	}
	if cfg.EventMeshConfig.PayloadFormat != "json" {
		t.Fatalf("default payload_format not applied: %q", cfg.EventMeshConfig.PayloadFormat)
	}
	if cfg.EventMeshConfig.PayloadEncoding != "utf-8" {
		t.Fatalf("default payload_encoding not applied: %q", cfg.EventMeshConfig.PayloadEncoding)
	}
}

func TestLoadConfig_QoSDefaultAndValidation(t *testing.T) {
	raw := minimalConfigMap()
	cfg, err := LoadConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.EffectiveQoS() != 1 {
		t.Fatalf("default EffectiveQoS = %d, want 1", cfg.EffectiveQoS())
	}

	raw["event_mesh_config"].(map[string]any)["qos"] = 0
	cfg, err = LoadConfig(raw)
	if err != nil {
		t.Fatalf("qos=0 should be valid: %v", err)
	}
	if cfg.EffectiveQoS() != 0 {
		t.Fatalf("EffectiveQoS = %d, want 0", cfg.EffectiveQoS())
	}

	raw["event_mesh_config"].(map[string]any)["qos"] = 2
	if _, err := LoadConfig(raw); err == nil || !strings.Contains(err.Error(), "qos") {
		t.Fatalf("expected qos validation error, got: %v", err)
	}
}

func TestLoadConfig_ReplyModeDefaultAndValidation(t *testing.T) {
	raw := minimalConfigMap()
	cfg, err := LoadConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.EffectiveReplyMode() != replyModeTempQueue {
		t.Fatalf("default EffectiveReplyMode = %q, want %q", cfg.EffectiveReplyMode(), replyModeTempQueue)
	}

	raw["event_mesh_config"].(map[string]any)["reply_mode"] = replyModeP2PInbox
	if cfg, err = LoadConfig(raw); err != nil || cfg.EffectiveReplyMode() != replyModeP2PInbox {
		t.Fatalf("p2p_inbox should be valid: err=%v mode=%q", err, cfg.EffectiveReplyMode())
	}

	raw["event_mesh_config"].(map[string]any)["reply_mode"] = "carrier_pigeon"
	if _, err := LoadConfig(raw); err == nil || !strings.Contains(err.Error(), "reply_mode") {
		t.Fatalf("expected reply_mode validation error, got: %v", err)
	}
}

func TestLoadConfig_ParameterTypeDefaultsToString(t *testing.T) {
	raw := minimalConfigMap()
	raw["parameters"] = []any{
		map[string]any{"name": "x"},
	}
	cfg, err := LoadConfig(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Parameters[0].Type != "string" {
		t.Fatalf("expected default type=string, got %q", cfg.Parameters[0].Type)
	}
}

func TestLoadConfig_RejectsMissingToolName(t *testing.T) {
	raw := minimalConfigMap()
	delete(raw, "tool_name")
	if _, err := LoadConfig(raw); err == nil {
		t.Fatal("expected validation error for missing tool_name")
	}
}

func TestLoadConfig_RejectsMissingTopic(t *testing.T) {
	raw := minimalConfigMap()
	delete(raw, "topic")
	if _, err := LoadConfig(raw); err == nil {
		t.Fatal("expected validation error for missing topic")
	}
}

func TestLoadConfig_RejectsBadPayloadFormat(t *testing.T) {
	raw := minimalConfigMap()
	raw["event_mesh_config"].(map[string]any)["payload_format"] = "xml"
	_, err := LoadConfig(raw)
	if err == nil || !strings.Contains(err.Error(), "payload_format") {
		t.Fatalf("expected payload_format error, got: %v", err)
	}
}

func TestLoadConfig_RejectsBadParamType(t *testing.T) {
	raw := minimalConfigMap()
	raw["parameters"] = []any{
		map[string]any{"name": "x", "type": "bigint"},
	}
	if _, err := LoadConfig(raw); err == nil {
		t.Fatal("expected validation error for bad param type")
	}
}

func TestLoadConfig_RejectsMissingBrokerURL(t *testing.T) {
	raw := minimalConfigMap()
	bc := raw["event_mesh_config"].(map[string]any)["broker_config"].(map[string]any)
	delete(bc, "broker_url")
	if _, err := LoadConfig(raw); err == nil {
		t.Fatal("expected error for missing broker_url")
	}
}

func TestLoadConfig_RejectsDuplicateParamName(t *testing.T) {
	raw := minimalConfigMap()
	raw["parameters"] = []any{
		map[string]any{"name": "x", "type": "string"},
		map[string]any{"name": "x", "type": "string"},
	}
	if _, err := LoadConfig(raw); err == nil {
		t.Fatal("expected error for duplicate param name")
	}
}

func TestLoadConfig_NilRaw(t *testing.T) {
	if _, err := LoadConfig(nil); err == nil {
		t.Fatal("expected error for nil tool_config")
	}
}

func TestDynamicSchema_HidesContextParams(t *testing.T) {
	raw := minimalConfigMap()
	raw["parameters"] = []any{
		map[string]any{"name": "city", "type": "string", "required": true},
		map[string]any{"name": "user_id", "context_expression": "user_id"},
	}
	override := dynamicSchema(raw)
	if override == nil || override.Parameters == nil {
		t.Fatal("expected schema override")
	}
	if _, has := override.Parameters.Properties["user_id"]; has {
		t.Fatal("context-expression param leaked into LLM schema")
	}
	if _, has := override.Parameters.Properties["city"]; !has {
		t.Fatal("expected city in schema")
	}
	if len(override.Parameters.Required) != 1 || override.Parameters.Required[0] != "city" {
		t.Fatalf("unexpected required list: %v", override.Parameters.Required)
	}
}

func TestDynamicSchema_OverridesNameAndDescription(t *testing.T) {
	raw := minimalConfigMap()
	override := dynamicSchema(raw)
	if override == nil {
		t.Fatal("expected override")
	}
	if override.Name != "GetWeather" {
		t.Fatalf("expected tool name override, got %q", override.Name)
	}
	if !strings.Contains(override.Description, "weather") {
		t.Fatalf("description not propagated: %q", override.Description)
	}
}

func TestDynamicSchema_MergesInputSchemaPropertiesAndRequired(t *testing.T) {
	raw := minimalConfigMap()
	raw["input_schema"] = map[string]any{
		"type":       "object",
		"properties": map[string]any{"city": map[string]any{"type": "string"}},
		"required":   []any{"city"},
	}
	override := dynamicSchema(raw)
	if override == nil || override.Parameters == nil {
		t.Fatal("expected schema override")
	}
	if _, has := override.Parameters.Properties["city"]; !has {
		t.Fatal("expected city seeded from input_schema")
	}
	count := 0
	for _, name := range override.Parameters.Required {
		if name == "city" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("city required count = %d, want 1 (no duplication)", count)
	}
}

func TestDynamicSchema_CopiesParameterEnum(t *testing.T) {
	raw := minimalConfigMap()
	raw["parameters"] = []any{
		map[string]any{"name": "unit", "type": "string", "enum": []any{"celsius", "fahrenheit"}},
	}
	override := dynamicSchema(raw)
	prop, ok := override.Parameters.Properties["unit"].(map[string]any)
	if !ok {
		t.Fatalf("unit property missing: %v", override.Parameters.Properties)
	}
	enum, ok := prop["enum"].([]any)
	if !ok || len(enum) != 2 {
		t.Fatalf("enum = %v, want two values", prop["enum"])
	}
}

func TestTools_ReturnsEventMeshToolDef(t *testing.T) {
	tools := Tools()
	if len(tools) != 1 {
		t.Fatalf("Tools() returned %d defs, want 1", len(tools))
	}
	if tools[0] == nil {
		t.Fatal("Tools()[0] is nil")
	}
}

func TestDynamicSchema_TypeMapping(t *testing.T) {
	raw := minimalConfigMap()
	raw["parameters"] = []any{
		map[string]any{"name": "s", "type": "string"},
		map[string]any{"name": "i", "type": "integer"},
		map[string]any{"name": "n", "type": "number"},
		map[string]any{"name": "b", "type": "boolean"},
	}
	override := dynamicSchema(raw)
	props := override.Parameters.Properties
	cases := map[string]string{"s": "string", "i": "integer", "n": "number", "b": "boolean"}
	for name, want := range cases {
		got := props[name].(map[string]any)["type"]
		if got != want {
			t.Errorf("param %q: got type %q, want %q", name, got, want)
		}
	}
}
