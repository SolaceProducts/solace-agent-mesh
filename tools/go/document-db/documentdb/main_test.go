// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package documentdb

import (
	"testing"
)

func TestDetectBackend_MongoDB(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{
			name:   "mongodb URI",
			config: map[string]any{"connection_string": "mongodb://localhost:27017/testdb"},
			want:   "mongodb",
		},
		{
			name:   "mongodb+srv URI",
			config: map[string]any{"connection_string": "mongodb+srv://user:pass@cluster.mongodb.net/db"},
			want:   "mongodb",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectBackend(tt.config)
			if got != tt.want {
				t.Errorf("detectBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectBackend_DynamoDB(t *testing.T) {
	config := map[string]any{
		"region":     "us-east-1",
		"table_name": "my-table",
	}
	got := detectBackend(config)
	if got != "dynamodb" {
		t.Errorf("detectBackend() = %q, want %q", got, "dynamodb")
	}
}

func TestDetectBackend_Empty(t *testing.T) {
	got := detectBackend(map[string]any{})
	if got != "" {
		t.Errorf("detectBackend() = %q, want empty", got)
	}
}

func TestDetectBackend_ExplicitOverridesAutoDetect(t *testing.T) {
	config := map[string]any{
		"backend":           "dynamodb",
		"connection_string": "mongodb://localhost:27017/db",
	}
	backend, err := newBackend(config)
	if err != nil {
		t.Fatalf("newBackend() error: %v", err)
	}
	if _, ok := backend.(*dynamoBackend); !ok {
		t.Errorf("expected *dynamoBackend, got %T", backend)
	}
}

func TestEffectiveBackend(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		want   backendKind
	}{
		{"explicit mongodb", map[string]any{"backend": "mongodb"}, backendMongoDB},
		{"explicit dynamodb", map[string]any{"backend": "dynamodb"}, backendDynamoDB},
		{"explicit generic", map[string]any{"backend": "generic"}, backendGeneric},
		{"detected mongodb", map[string]any{"connection_string": "mongodb://x/y"}, backendMongoDB},
		{"detected dynamodb", map[string]any{"region": "us-west-2"}, backendDynamoDB},
		{"undetected defaults to generic", map[string]any{}, backendGeneric},
		{"mixed case explicit", map[string]any{"backend": "MongoDB"}, backendMongoDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveBackend(tt.config)
			if got != tt.want {
				t.Errorf("effectiveBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewBackend_Unsupported(t *testing.T) {
	config := map[string]any{"backend": "couchdb"}
	_, err := newBackend(config)
	if err == nil {
		t.Fatal("expected error for unsupported backend")
	}
}

func TestNewBackend_NoConfig(t *testing.T) {
	_, err := newBackend(map[string]any{})
	if err == nil {
		t.Fatal("expected error for empty config (generic is not a real backend)")
	}
}

func TestConfigString(t *testing.T) {
	config := map[string]any{"key": "value"}
	if got := configString(config, "key", "default"); got != "value" {
		t.Errorf("configString() = %q, want %q", got, "value")
	}
	if got := configString(config, "missing", "default"); got != "default" {
		t.Errorf("configString() = %q, want %q", got, "default")
	}
	config["wrong_type"] = 42
	if got := configString(config, "wrong_type", "default"); got != "default" {
		t.Errorf("configString() = %q, want %q", got, "default")
	}
}

func TestConfigBool(t *testing.T) {
	config := map[string]any{"flag": true}
	if got := configBool(config, "flag", false); got != true {
		t.Errorf("configBool() = %v, want true", got)
	}
	if got := configBool(config, "missing", true); got != true {
		t.Errorf("configBool() = %v, want true", got)
	}
}

func TestConfigInt(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want int
	}{
		{"float64", float64(42), 42},
		{"int", int(42), 42},
		{"int64", int64(42), 42},
		{"string_returns_default", "not a number", 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := map[string]any{"key": tt.val}
			if got := configInt(config, "key", 10); got != tt.want {
				t.Errorf("configInt() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDynamicSchema_EmptyConfigYieldsGeneric(t *testing.T) {
	result := dynamicSchema(map[string]any{})
	if result == nil {
		t.Fatal("expected non-nil override for empty config (generic default)")
	}
	if result.Name != "" {
		t.Errorf("Name = %q, want empty (keep default)", result.Name)
	}
	if result.Parameters == nil {
		t.Fatal("expected non-nil Parameters for generic backend")
	}
	if _, ok := result.Parameters.Properties["collection"]; !ok {
		t.Errorf("generic schema missing 'collection' property")
	}
	if _, ok := result.Parameters.Properties["query"]; !ok {
		t.Errorf("generic schema missing 'query' property")
	}
}

func TestDynamicSchema_MongoDefaultsToolName(t *testing.T) {
	config := map[string]any{
		"backend":            "mongodb",
		"connection_string":  "mongodb://localhost:27017/testdb",
		"auto_detect_schema": false,
	}
	result := dynamicSchema(config)
	if result == nil {
		t.Fatal("expected non-nil override")
	}
	if result.Name != defaultMongoToolName {
		t.Errorf("Name = %q, want %q", result.Name, defaultMongoToolName)
	}
}

func TestDynamicSchema_DynamoDefaultsToolName(t *testing.T) {
	config := map[string]any{
		"backend":            "dynamodb",
		"region":             "us-east-1",
		"auto_detect_schema": false,
	}
	result := dynamicSchema(config)
	if result == nil {
		t.Fatal("expected non-nil override")
	}
	if result.Name != defaultDynamoToolName {
		t.Errorf("Name = %q, want %q", result.Name, defaultDynamoToolName)
	}
}

func TestDynamicSchema_GenericKeepsDefaultName(t *testing.T) {
	config := map[string]any{
		"backend":            "generic",
		"auto_detect_schema": false,
	}
	result := dynamicSchema(config)
	if result == nil {
		t.Fatal("expected non-nil override")
	}
	if result.Name != "" {
		t.Errorf("Name = %q, want empty (keep default %q)", result.Name, defaultGenericName)
	}
}

func TestDynamicSchema_ExplicitToolNameWins(t *testing.T) {
	config := map[string]any{
		"backend":            "mongodb",
		"connection_string":  "mongodb://localhost:27017/testdb",
		"tool_name":          "my_custom_tool",
		"tool_description":   "Custom description",
		"auto_detect_schema": false,
	}
	result := dynamicSchema(config)
	if result == nil {
		t.Fatal("expected non-nil SchemaOverride")
	}
	if result.Name != "my_custom_tool" {
		t.Errorf("Name = %q, want %q", result.Name, "my_custom_tool")
	}
	if result.Description != "Custom description" {
		t.Errorf("Description = %q, want %q", result.Description, "Custom description")
	}
}

func TestDynamicSchema_MongoSchemaShape(t *testing.T) {
	config := map[string]any{
		"backend":            "mongodb",
		"connection_string":  "mongodb://localhost:27017/testdb",
		"auto_detect_schema": false,
	}
	result := dynamicSchema(config)
	if result == nil || result.Parameters == nil {
		t.Fatalf("expected non-nil Parameters, got override=%+v", result)
	}
	props := result.Parameters.Properties
	for _, key := range []string{"operation", "pipeline_str", "documents", "filter", "update", "response_format", "result_description"} {
		if _, ok := props[key]; !ok {
			t.Errorf("mongo schema missing property %q", key)
		}
	}
	if _, ok := props["collection"]; ok {
		t.Errorf("mongo schema MUST NOT expose 'collection' (comes from tool_config)")
	}
	if required := result.Parameters.Required; len(required) != 0 {
		t.Errorf("mongo required = %v, want []", required)
	}
}

func TestDynamicSchema_DynamoSchemaShape(t *testing.T) {
	config := map[string]any{
		"backend":            "dynamodb",
		"region":             "us-east-1",
		"auto_detect_schema": false,
	}
	result := dynamicSchema(config)
	if result == nil || result.Parameters == nil {
		t.Fatalf("expected non-nil Parameters, got override=%+v", result)
	}
	props := result.Parameters.Properties
	for _, key := range []string{"statement", "response_format", "result_description"} {
		if _, ok := props[key]; !ok {
			t.Errorf("dynamodb schema missing property %q", key)
		}
	}
	if _, ok := props["query"]; ok {
		t.Errorf("dynamodb schema should not expose 'query' (use 'statement' per PartiQL)")
	}
	required := result.Parameters.Required
	if len(required) != 1 || required[0] != "statement" {
		t.Errorf("dynamodb required = %v, want [\"statement\"]", required)
	}
}

func TestDynamicSchema_SchemaSummaryOverride(t *testing.T) {
	config := map[string]any{
		"backend":                 "mongodb",
		"connection_string":       "mongodb://localhost:27017/testdb",
		"auto_detect_schema":      false,
		"schema_summary_override": "users collection with name and email fields",
	}
	result := dynamicSchema(config)
	if result == nil {
		t.Fatal("expected non-nil SchemaOverride")
	}
	if result.Instructions == "" {
		t.Fatal("expected instructions to be set")
	}
}

func TestBuildResult_ReadQuery(t *testing.T) {
	config := map[string]any{
		"backend":    "mongodb",
		"collection": "users",
	}
	qr := &QueryResult{
		Documents: []map[string]any{
			{"name": "alice", "age": 30},
			{"name": "bob", "age": 25},
		},
		ResponseFormat: "json",
		QueryText:      "[{\"$match\":{}}]",
	}

	res, err := buildResult(config, qr)
	if err != nil {
		t.Fatalf("buildResult error: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("Status = %q, want success", res.Status)
	}
	for _, key := range []string{"artifact_filename", "artifact_version", "row_count", "content"} {
		if _, ok := res.Data[key]; !ok {
			t.Errorf("Data missing key %q (got keys %v)", key, keysOf(res.Data))
		}
	}
	if res.Data["row_count"] != 2 {
		t.Errorf("row_count = %v, want 2", res.Data["row_count"])
	}
	if _, hasAffected := res.Data["affected_count"]; hasAffected {
		t.Errorf("read query response should not include affected_count")
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("expected 1 DataObject (artifact), got %d", len(res.DataObjects))
	}
	filename, _ := res.Data["artifact_filename"].(string)
	if filename == "" {
		t.Errorf("artifact_filename is empty")
	}
	if res.DataObjects[0].Name != filename {
		t.Errorf("DataObject.Name (%q) != artifact_filename (%q)", res.DataObjects[0].Name, filename)
	}
}

func TestBuildResult_WriteQuery(t *testing.T) {
	config := map[string]any{
		"backend": "dynamodb",
		"region":  "us-east-1",
	}
	qr := &QueryResult{
		IsWrite:       true,
		AffectedCount: 3,
		Message:       "Statement executed successfully",
	}
	res, err := buildResult(config, qr)
	if err != nil {
		t.Fatalf("buildResult error: %v", err)
	}
	if res.Data["affected_count"] != int64(3) {
		t.Errorf("affected_count = %v, want 3", res.Data["affected_count"])
	}
	if _, hasArtifact := res.Data["artifact_filename"]; hasArtifact {
		t.Errorf("write response should not include artifact_filename")
	}
	if len(res.DataObjects) != 0 {
		t.Errorf("write response should not save an artifact (got %d)", len(res.DataObjects))
	}
}

func TestBuildResult_InlinePreviewCap(t *testing.T) {
	config := map[string]any{
		"backend":            "mongodb",
		"collection":         "users",
		"max_inline_results": 1,
	}
	qr := &QueryResult{
		Documents: []map[string]any{
			{"id": 1}, {"id": 2}, {"id": 3},
		},
		ResponseFormat: "json",
		QueryText:      "[]",
	}
	res, err := buildResult(config, qr)
	if err != nil {
		t.Fatalf("buildResult error: %v", err)
	}
	if res.Data["content"] != "" {
		t.Errorf("content should be empty when row_count > max_inline_results, got %q", res.Data["content"])
	}
	if !containsSubstr(res.Message, "exceeds the inline limit") {
		t.Errorf("message missing preview-skip note: %q", res.Message)
	}
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func containsSubstr(haystack, needle string) bool {
	return indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
