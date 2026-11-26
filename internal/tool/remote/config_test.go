// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"slices"
	"testing"
)

func TestParseToolConfigBasic(t *testing.T) {
	raw := map[string]any{
		"tool_type":        "builtin",
		"tool_name":        "process_file",
		"tool_description": "Process a file and produce a summary",
		"timeout_seconds":  30,
		"sandbox_profile":  "standard",
		"parameters": map[string]any{
			"input_file": map[string]any{
				"type":        "artifact",
				"description": "The file to process",
			},
			"format": map[string]any{
				"type":        "string",
				"description": "Output format",
				"required":    false,
				"default":     "json",
			},
		},
	}

	cfg, err := ParseToolConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ToolName != "process_file" {
		t.Errorf("ToolName = %q", cfg.ToolName)
	}
	if cfg.Description != "Process a file and produce a summary" {
		t.Errorf("Description = %q", cfg.Description)
	}
	if cfg.TimeoutSeconds != 30 {
		t.Errorf("TimeoutSeconds = %d", cfg.TimeoutSeconds)
	}
	if cfg.SandboxProfile != "standard" {
		t.Errorf("SandboxProfile = %q", cfg.SandboxProfile)
	}

	if len(cfg.Parameters) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(cfg.Parameters))
	}
	inputFile := cfg.Parameters["input_file"]
	if inputFile.Type != "artifact" {
		t.Errorf("input_file type = %q", inputFile.Type)
	}
	if !inputFile.Required {
		t.Error("input_file should be required by default")
	}

	format := cfg.Parameters["format"]
	if format.Type != "string" {
		t.Errorf("format type = %q", format.Type)
	}
	if format.Required {
		t.Error("format should not be required (has default)")
	}
	if format.Default != "json" {
		t.Errorf("format default = %v", format.Default)
	}
}

func TestParseToolConfigDefaults(t *testing.T) {
	raw := map[string]any{
		"tool_name": "simple_tool",
	}

	cfg, err := ParseToolConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TimeoutSeconds != 0 {
		t.Errorf("default TimeoutSeconds = %d, want 0", cfg.TimeoutSeconds)
	}
	if cfg.SandboxProfile != "standard" {
		t.Errorf("default SandboxProfile = %q, want standard", cfg.SandboxProfile)
	}
}

func TestParseToolConfigMissingName(t *testing.T) {
	raw := map[string]any{
		"tool_description": "no name",
	}

	_, err := ParseToolConfig(raw)
	if err == nil {
		t.Fatal("expected error for missing tool_name")
	}
}

func TestParseToolConfigWithToolConfig(t *testing.T) {
	raw := map[string]any{
		"tool_name": "db_tool",
		"tool_config": map[string]any{
			"db_host":     "localhost",
			"db_password": "secret",
		},
	}

	cfg, err := ParseToolConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ToolPassthroughConfig["db_host"] != "localhost" {
		t.Errorf("tool_config.db_host = %v", cfg.ToolPassthroughConfig["db_host"])
	}
}

func TestParseToolConfigRequiredScopes(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want []string
	}{
		{"yaml []any", []any{"schedule:task:create"}, []string{"schedule:task:create"}},
		{"direct []string", []string{"a", "b"}, []string{"a", "b"}},
		{"absent", nil, nil},
		{"empty list", []any{}, nil},
		{"drops non-strings and blanks", []any{"keep", "", 7}, []string{"keep"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"tool_name": "code_tool"}
			if tc.val != nil {
				raw["required_scopes"] = tc.val
			}
			cfg, err := ParseToolConfig(raw)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(cfg.RequiredScopes, tc.want) {
				t.Errorf("RequiredScopes = %v, want %v", cfg.RequiredScopes, tc.want)
			}
			tool := NewTool(cfg, nil)
			if !slices.Equal(tool.RequiredScopes(), tc.want) {
				t.Errorf("Tool.RequiredScopes() = %v, want %v", tool.RequiredScopes(), tc.want)
			}
		})
	}
}

func TestParseToolConfigFloatTimeout(t *testing.T) {
	raw := map[string]any{
		"tool_name":       "float_tool",
		"timeout_seconds": float64(60),
	}

	cfg, err := ParseToolConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TimeoutSeconds != 60 {
		t.Errorf("TimeoutSeconds = %d, want 60", cfg.TimeoutSeconds)
	}
}

func TestBuildJSONSchema(t *testing.T) {
	cfg := &ToolConfig{
		ToolName: "test_tool",
		Parameters: map[string]ParamConfig{
			"input_file": {Type: "artifact", Description: "File input", Required: true},
			"format":     {Type: "string", Description: "Output format", Required: false, Default: "json"},
		},
	}

	schema := cfg.BuildJSONSchema()
	if schema.Type != "object" {
		t.Errorf("type = %q", schema.Type)
	}

	inputFile := schema.Properties["input_file"].(map[string]any)
	if inputFile["type"] != "string" {
		t.Errorf("input_file type = %v (artifact should map to string)", inputFile["type"])
	}

	format := schema.Properties["format"].(map[string]any)
	if format["type"] != "string" {
		t.Errorf("format type = %v", format["type"])
	}
	if format["default"] != "json" {
		t.Errorf("format default = %v", format["default"])
	}

	if len(schema.Required) != 1 || schema.Required[0] != "input_file" {
		t.Errorf("required = %v, want [input_file]", schema.Required)
	}
}

func TestBuildJSONSchemaEmpty(t *testing.T) {
	cfg := &ToolConfig{ToolName: "no_params"}
	schema := cfg.BuildJSONSchema()
	if schema.Type != "object" {
		t.Errorf("type = %q", schema.Type)
	}
	if len(schema.Properties) != 0 {
		t.Errorf("properties should be empty, got %v", schema.Properties)
	}
}

func TestArtifactParamNames(t *testing.T) {
	cfg := &ToolConfig{
		Parameters: map[string]ParamConfig{
			"input_file": {Type: "artifact", Required: true},
			"format":     {Type: "string", Required: false},
			"output":     {Type: "artifact", Required: false},
		},
	}

	names := cfg.ArtifactParamNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 artifact params, got %d", len(names))
	}
	if _, ok := names["input_file"]; !ok {
		t.Error("missing input_file")
	}
	if _, ok := names["output"]; !ok {
		t.Error("missing output")
	}
	if names["input_file"].IsOptional {
		t.Error("input_file should not be optional")
	}
	if !names["output"].IsOptional {
		t.Error("output should be optional")
	}
}

func TestArtifactParamNamesNone(t *testing.T) {
	cfg := &ToolConfig{
		Parameters: map[string]ParamConfig{
			"query": {Type: "string"},
		},
	}
	if cfg.ArtifactParamNames() != nil {
		t.Error("expected nil for no artifact params")
	}
}

func TestParseToolConfigVolumeParams(t *testing.T) {
	raw := map[string]any{
		"tool_name": "code-review",
		"volume_params": []any{
			map[string]any{
				"name":        "source_code",
				"description": "Source code directory",
				"mode":        "readwrite",
				"mount_path":  "/workspace",
			},
			map[string]any{
				"name":        "config",
				"description": "Config files",
				"mode":        "readonly",
				"mount_path":  "/config",
			},
		},
	}

	cfg, err := ParseToolConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.VolumeParams) != 2 {
		t.Fatalf("expected 2 volume params, got %d", len(cfg.VolumeParams))
	}

	vp := cfg.VolumeParams[0]
	if vp.Name != "source_code" {
		t.Errorf("volume_params[0].Name = %q", vp.Name)
	}
	if vp.Mode != "readwrite" {
		t.Errorf("volume_params[0].Mode = %q", vp.Mode)
	}
	if vp.MountPath != "/workspace" {
		t.Errorf("volume_params[0].MountPath = %q", vp.MountPath)
	}

	vp1 := cfg.VolumeParams[1]
	if vp1.Name != "config" {
		t.Errorf("volume_params[1].Name = %q", vp1.Name)
	}
	if vp1.Mode != "readonly" {
		t.Errorf("volume_params[1].Mode = %q", vp1.Mode)
	}
}

func TestParseToolConfigNoVolumeParams(t *testing.T) {
	raw := map[string]any{
		"tool_name": "simple",
	}

	cfg, err := ParseToolConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.VolumeParams != nil {
		t.Errorf("expected nil VolumeParams, got %v", cfg.VolumeParams)
	}
}
