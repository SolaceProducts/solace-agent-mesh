// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package configbuilder

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAppConfig_WrapsInAppsArray(t *testing.T) {
	t.Parallel()
	cfg := AppConfig("my-agent", map[string]any{"instruction": "be helpful"})
	apps, ok := cfg["apps"].([]map[string]any)
	if !ok || len(apps) != 1 {
		t.Fatalf("expected apps array with 1 entry, got %v", cfg["apps"])
	}
	if apps[0]["name"] != "my-agent" {
		t.Errorf("name = %v", apps[0]["name"])
	}
	ac, ok := apps[0]["app_config"].(map[string]any)
	if !ok {
		t.Fatal("app_config missing")
	}
	if ac["instruction"] != "be helpful" {
		t.Errorf("instruction = %v", ac["instruction"])
	}
}

func TestModelBlock_Structure(t *testing.T) {
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "test-model")
	t.Setenv("LLM_SERVICE_ENDPOINT", "https://test.example.com")
	t.Setenv("LLM_SERVICE_API_KEY", "sk-test")
	m := ModelBlock()
	if m["model"] != "test-model" {
		t.Errorf("model = %v, want test-model", m["model"])
	}
	if m["api_base"] != "https://test.example.com" {
		t.Errorf("api_base = %v", m["api_base"])
	}
	if m["api_key"] != "sk-test" {
		t.Errorf("api_key = %v", m["api_key"])
	}
}

func TestModelBlock_EmptyEnv(t *testing.T) {
	t.Setenv("LLM_SERVICE_GENERAL_MODEL_NAME", "")
	t.Setenv("LLM_SERVICE_ENDPOINT", "")
	t.Setenv("LLM_SERVICE_API_KEY", "")
	m := ModelBlock()
	if m["model"] != "" {
		t.Errorf("model = %v, want empty", m["model"])
	}
}

func TestSessionBlock_PerResourceSqliteDefault(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SAM_DATA_DIR", "/var/lib/sam")
	s := SessionBlock("my_agent_123")
	dbURL, ok := s["database_url"].(string)
	if !ok {
		t.Fatal("database_url missing")
	}
	if want := "sqlite:////var/lib/sam/agent_my_agent_123.db"; dbURL != want {
		t.Errorf("database_url = %q, want %q", dbURL, want)
	}
}

func TestSessionBlock_DatabaseURLWins(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pw@host:5432/sam_sessions")
	s := SessionBlock("my_agent_123")
	if got := s["database_url"]; got != "postgres://user:pw@host:5432/sam_sessions" {
		t.Errorf("database_url = %v, want the shared Postgres URL", got)
	}
}

func TestGatewaySessionBlock_UsesGatewayDBNotAgentStore(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:pw@host:5432/rc_orchestrator")
	t.Setenv("WEB_UI_GATEWAY_DATABASE_URL", "postgres://u:pw@host:5432/rc_webui")
	s := GatewaySessionBlock("slack_bot")
	got, _ := s["database_url"].(string)
	if got != "postgres://u:pw@host:5432/rc_webui" {
		t.Errorf("gateway database_url = %q, want the gateway DB (rc_webui); MUST NOT use the agent DATABASE_URL", got)
	}
}

func TestGatewaySessionBlock_PerGatewaySqliteFallback(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:pw@host:5432/rc_orchestrator")
	t.Setenv("WEB_UI_GATEWAY_DATABASE_URL", "")
	t.Setenv("SAM_DATA_DIR", "/var/lib/sam")
	s := GatewaySessionBlock("slack_bot")
	if want := "sqlite:////var/lib/sam/gateway_slack_bot.db"; s["database_url"] != want {
		t.Errorf("gateway database_url = %v, want per-gateway sqlite %q (not the agent DATABASE_URL)", s["database_url"], want)
	}
}

func TestArtifactBlock_DefaultFilesystem(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	t.Setenv("S3_BUCKET_NAME", "")
	b := ArtifactBlock()
	if !containsStr(b["type"].(string), "filesystem") {
		t.Errorf("type = %v, want filesystem reference", b["type"])
	}
}

func TestDetectObjectStorageType_EnvVar(t *testing.T) {
	tests := []struct {
		name   string
		envVal string
		want   string
	}{
		{"explicit s3", "s3", "s3"},
		{"explicit azure", "azure", "azure"},
		{"explicit gcs", "gcs", "gcs"},
		{"unknown type", "unknown", "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OBJECT_STORAGE_TYPE", tt.envVal)
			got := DetectObjectStorageType()
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectObjectStorageType_None(t *testing.T) {
	t.Setenv("OBJECT_STORAGE_TYPE", "")
	if got := DetectObjectStorageType(); got != "none" {
		t.Errorf("got %q, want none", got)
	}
}

func TestToControlPayloads_SingleApp(t *testing.T) {
	t.Parallel()
	cfg := AppConfig("agent-1", map[string]any{
		"agent_name":  "agent-1",
		"instruction": "test",
	})
	payloads, err := ToControlPayloads(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(payloads))
	}
	if payloads[0]["name"] != "agent-1" {
		t.Errorf("name = %v", payloads[0]["name"])
	}
	if payloads[0]["app_config"] == nil {
		t.Error("app_config missing")
	}
}

func TestToControlPayloads_MultipleApps(t *testing.T) {
	t.Parallel()
	cfg := map[string]any{
		"apps": []map[string]any{
			{"name": "agent-1", "app_config": map[string]any{"agent_name": "a1"}},
			{"name": "agent-2", "app_config": map[string]any{"agent_name": "a2"}},
		},
	}
	payloads, err := ToControlPayloads(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(payloads) != 2 {
		t.Fatalf("expected 2 payloads, got %d", len(payloads))
	}
}

func TestToControlPayloads_NoAppsKey(t *testing.T) {
	t.Parallel()
	_, err := ToControlPayloads(map[string]any{"other": "value"})
	if err == nil {
		t.Error("expected error for missing apps key")
	}
}

func TestParseYAMLToControlPayloads(t *testing.T) {
	t.Parallel()
	yamlStr := `
apps:
  - name: test-agent
    app_config:
      agent_name: test-agent
      instruction: "be helpful"
`
	payloads, err := ParseYAMLToControlPayloads([]byte(yamlStr))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(payloads) != 1 {
		t.Fatalf("expected 1 payload, got %d", len(payloads))
	}
	if payloads[0]["name"] != "test-agent" {
		t.Errorf("name = %v", payloads[0]["name"])
	}
}

func TestParseYAMLToControlPayloads_Invalid(t *testing.T) {
	t.Parallel()
	_, err := ParseYAMLToControlPayloads([]byte("not: valid: yaml: ["))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestMarshalYAML_RoundTrip(t *testing.T) {
	t.Parallel()
	input := map[string]any{
		"apps": []any{
			map[string]any{
				"name": "my-agent",
				"app_config": map[string]any{
					"instruction": "be helpful",
				},
			},
		},
	}
	yamlStr, err := MarshalYAML(input)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlStr), &parsed); err != nil {
		t.Fatalf("unmarshal roundtrip: %v", err)
	}
	apps, ok := parsed["apps"].([]any)
	if !ok || len(apps) != 1 {
		t.Fatalf("roundtrip apps = %v", parsed["apps"])
	}
}

func TestSetOrEnvVar_ValuePresent(t *testing.T) {
	t.Parallel()
	m := map[string]any{}
	SetOrEnvVar(m, "host", map[string]any{"host": "db.example.com"}, "DB_HOST")
	if m["host"] != "db.example.com" {
		t.Errorf("host = %v", m["host"])
	}
}

func TestSetOrEnvVar_ValueMissing_EnvSet(t *testing.T) {
	t.Setenv("DB_HOST", "resolved-host.example.com")
	m := map[string]any{}
	SetOrEnvVar(m, "host", map[string]any{}, "DB_HOST")
	if m["host"] != "resolved-host.example.com" {
		t.Errorf("host = %v, want resolved-host.example.com", m["host"])
	}
}

func TestSetOrEnvVar_ValueMissing_EnvUnset(t *testing.T) {
	t.Setenv("DB_HOST", "")
	m := map[string]any{}
	SetOrEnvVar(m, "host", map[string]any{}, "DB_HOST")
	if m["host"] != "" {
		t.Errorf("host = %v, want empty string", m["host"])
	}
}

func TestSetOrEnvVar_EmptyString(t *testing.T) {
	t.Setenv("DB_HOST", "fallback-host")
	m := map[string]any{}
	SetOrEnvVar(m, "host", map[string]any{"host": ""}, "DB_HOST")
	if m["host"] != "fallback-host" {
		t.Errorf("empty string should fall back to env var, got %v", m["host"])
	}
}

func TestSetOrEnvVarFrom_KeyRemapping(t *testing.T) {
	t.Parallel()
	m := map[string]any{}
	SetOrEnvVarFrom(m, "db_host", map[string]any{"hostname": "db.example.com"}, "hostname", "DB_HOST")
	if m["db_host"] != "db.example.com" {
		t.Errorf("db_host = %v", m["db_host"])
	}
}

func TestSetOrEnvVarFrom_Missing(t *testing.T) {
	t.Setenv("DB_HOST", "resolved-host")
	m := map[string]any{}
	SetOrEnvVarFrom(m, "db_host", map[string]any{}, "hostname", "DB_HOST")
	if m["db_host"] != "resolved-host" {
		t.Errorf("db_host = %v, want resolved-host", m["db_host"])
	}
}

func TestSetIfPresent_String(t *testing.T) {
	t.Parallel()
	m := map[string]any{}
	SetIfPresent(m, "name", map[string]any{"name": "test"})
	if m["name"] != "test" {
		t.Errorf("name = %v", m["name"])
	}
}

func TestSetIfPresent_EmptyString(t *testing.T) {
	t.Parallel()
	m := map[string]any{}
	SetIfPresent(m, "name", map[string]any{"name": ""})
	if _, ok := m["name"]; ok {
		t.Error("empty string should not be set")
	}
}

func TestSetIfPresent_Bool(t *testing.T) {
	t.Parallel()
	m := map[string]any{}
	SetIfPresent(m, "enabled", map[string]any{"enabled": true})
	if m["enabled"] != true {
		t.Errorf("enabled = %v", m["enabled"])
	}
}

func TestSetIfPresent_Absent(t *testing.T) {
	t.Parallel()
	m := map[string]any{}
	SetIfPresent(m, "name", map[string]any{})
	if _, ok := m["name"]; ok {
		t.Error("absent field should not be set")
	}
}

func TestSetIfPresent_Float(t *testing.T) {
	t.Parallel()
	m := map[string]any{}
	SetIfPresent(m, "count", map[string]any{"count": float64(42)})
	if m["count"] != float64(42) {
		t.Errorf("count = %v", m["count"])
	}
}

func TestGetIntOrDefault_Present(t *testing.T) {
	t.Parallel()
	got := GetIntOrDefault(map[string]any{"port": float64(8080)}, "port", 3000)
	if got != 8080 {
		t.Errorf("got %d, want 8080", got)
	}
}

func TestGetIntOrDefault_Missing(t *testing.T) {
	t.Parallel()
	got := GetIntOrDefault(map[string]any{}, "port", 3000)
	if got != 3000 {
		t.Errorf("got %d, want 3000", got)
	}
}

func TestGetIntOrDefault_WrongType(t *testing.T) {
	t.Parallel()
	got := GetIntOrDefault(map[string]any{"port": "not a number"}, "port", 3000)
	if got != 3000 {
		t.Errorf("wrong type should return default, got %d", got)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstr(s, substr))
}

func findSubstr(s, sub string) bool {
	for i := range len(s) - len(sub) + 1 {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
