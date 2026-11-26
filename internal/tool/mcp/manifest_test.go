// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadManifestFile_OK(t *testing.T) {
	tools, err := loadManifestFile(filepath.Join("testdata", "manifest_ok.yaml"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("len(tools) = %d, want 2", len(tools))
	}

	spaces := tools[0]
	if spaces.Name != "getConfluenceSpaces" {
		t.Errorf("tools[0].Name = %q, want getConfluenceSpaces", spaces.Name)
	}
	if !strings.Contains(spaces.Description, "spaces from Confluence") {
		t.Errorf("tools[0].Description = %q, want substring 'spaces from Confluence'", spaces.Description)
	}
	if spaces.InputSchema == nil {
		t.Fatal("tools[0].InputSchema is nil")
	}
	if spaces.InputSchema["type"] != "object" {
		t.Errorf("tools[0].InputSchema[type] = %v, want object", spaces.InputSchema["type"])
	}

	if got := spaces.InputSchema["$schema"]; got != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("$schema = %v, want draft-07 URI", got)
	}
	if got := spaces.InputSchema["additionalProperties"]; got != false {
		t.Errorf("additionalProperties = %v, want false", got)
	}
	props, ok := spaces.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %T, want map[string]any", spaces.InputSchema["properties"])
	}
	ids, ok := props["ids"].(map[string]any)
	if !ok {
		t.Fatalf("properties.ids = %T, want map[string]any", props["ids"])
	}
	if _, ok := ids["anyOf"].([]any); !ok {
		t.Errorf("properties.ids.anyOf = %T, want []any", ids["anyOf"])
	}

	if tools[1].Name != "atlassianUserInfo" {
		t.Errorf("tools[1].Name = %q, want atlassianUserInfo (id fallback)", tools[1].Name)
	}
}

func TestLoadManifestFile_NotAList(t *testing.T) {
	_, err := loadManifestFile(filepath.Join("testdata", "manifest_not_a_list.yaml"))
	if err == nil {
		t.Fatal("expected error for non-list root, got nil")
	}
	if !strings.Contains(err.Error(), "list") {
		t.Errorf("error = %q, want mention of 'list'", err.Error())
	}
}

func TestLoadManifestFile_MissingFile(t *testing.T) {
	_, err := loadManifestFile(filepath.Join("testdata", "does_not_exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLoadManifestFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("this: is: not: valid: yaml: [\n"), 0o644); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	_, err := loadManifestFile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestParseManifestEntries_IDFallback(t *testing.T) {
	raw := []any{
		map[string]any{
			"id":          "onlyIDSet",
			"description": "No name, just an id.",
			"inputSchema": map[string]any{"type": "object"},
		},
	}
	tools, err := parseManifestEntries(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tools[0].Name != "onlyIDSet" {
		t.Errorf("Name = %q, want onlyIDSet", tools[0].Name)
	}
}

func TestParseManifestEntries_NameWins(t *testing.T) {
	raw := []any{
		map[string]any{
			"id":          "oldAlias",
			"name":        "newName",
			"inputSchema": map[string]any{"type": "object"},
		},
	}
	tools, err := parseManifestEntries(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tools[0].Name != "newName" {
		t.Errorf("Name = %q, want newName (name wins over id)", tools[0].Name)
	}
}

func TestParseManifestEntries_MissingBoth(t *testing.T) {
	raw := []any{
		map[string]any{
			"description": "No id, no name.",
			"inputSchema": map[string]any{"type": "object"},
		},
	}
	_, err := parseManifestEntries(raw)
	if err == nil {
		t.Fatal("expected error when both id and name are missing, got nil")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error = %q, want mention of 'name'", err.Error())
	}
}

func TestParseManifestEntries_NonMapSchema(t *testing.T) {
	raw := []any{
		map[string]any{
			"name":        "weird",
			"inputSchema": "this should be an object",
		},
	}
	_, err := parseManifestEntries(raw)
	if err == nil {
		t.Fatal("expected error for non-map inputSchema, got nil")
	}
}

func TestParseManifestEntries_NonMapEntry(t *testing.T) {
	raw := []any{"just a string"}
	_, err := parseManifestEntries(raw)
	if err == nil {
		t.Fatal("expected error for non-map entry, got nil")
	}
}

func TestParseManifestEntries_OmittedInputSchema(t *testing.T) {
	raw := []any{
		map[string]any{"name": "noArgs"},
	}
	tools, err := parseManifestEntries(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tools[0].InputSchema == nil {
		t.Fatal("InputSchema is nil, want empty object schema")
	}
	if tools[0].InputSchema["type"] != "object" {
		t.Errorf("default InputSchema[type] = %v, want object", tools[0].InputSchema["type"])
	}
}

func TestParseManifestEntries_OutputSchema(t *testing.T) {
	raw := []any{
		map[string]any{
			"name":         "detect_objects",
			"inputSchema":  map[string]any{"type": "object"},
			"outputSchema": map[string]any{"type": "object", "properties": map[string]any{"objects": map[string]any{"type": "array"}}},
		},
	}
	tools, err := parseManifestEntries(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tools[0].OutputSchema == nil {
		t.Fatal("OutputSchema is nil, want the declared schema")
	}
	if tools[0].OutputSchema["type"] != "object" {
		t.Errorf("OutputSchema[type] = %v, want object", tools[0].OutputSchema["type"])
	}
}

func TestParseManifestEntries_OmittedOutputSchemaStaysNil(t *testing.T) {
	raw := []any{
		map[string]any{"name": "noOut", "inputSchema": map[string]any{"type": "object"}},
	}
	tools, err := parseManifestEntries(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tools[0].OutputSchema != nil {
		t.Errorf("OutputSchema = %v, want nil when the manifest omits it", tools[0].OutputSchema)
	}
}

func TestParseManifestEntries_NonMapOutputSchema(t *testing.T) {
	raw := []any{
		map[string]any{
			"name":         "weird",
			"outputSchema": "this should be an object",
		},
	}
	_, err := parseManifestEntries(raw)
	if err == nil {
		t.Fatal("expected error for non-map outputSchema, got nil")
	}
}
