// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"gopkg.in/yaml.v3"
)

func TestMergeStructuredData_DescriptionNamesTheToolThatWritesANull(t *testing.T) {
	description := (&MergeStructuredDataTool{}).Description()
	if !strings.Contains(description, "null is deleted") {
		t.Error("description no longer says a null in the patch deletes the key")
	}
	head := string([]rune(description)[:300])
	if replace := (&SearchAndReplaceInArtifactTool{}).Name(); !strings.Contains(head, replace) {
		t.Errorf("the first 300 runes of the description do not name %s for writing a literal null", replace)
	}
	if !strings.Contains(description, "components not in the patch are kept") {
		t.Error("description no longer says a manifest patch keeps the components it does not list")
	}
}

func TestMergeStructuredData_JSONUpdateKeys(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	original := `{"server": {"host": "localhost", "port": 8080, "debug": true}}`
	saveDataToolArtifact(t, ctx, facade, "config.json", []byte(original), "application/json")

	mergeTool := &MergeStructuredDataTool{}
	result, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "config.json",
		"patch": map[string]any{
			"server": map[string]any{
				"port": float64(9090),
				"tls":  true,
			},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["status"] != "success" {
		t.Fatalf("expected success, got: %v", result.Data)
	}

	content, _, _ := facade.LoadArtifact(ctx, "config.json", nil)
	var parsed map[string]any
	_ = json.Unmarshal(content, &parsed)

	server := parsed["server"].(map[string]any)
	if server["host"] != "localhost" {
		t.Error("host should be preserved")
	}
	if server["port"] != float64(9090) {
		t.Errorf("port should be 9090, got %v", server["port"])
	}
	if server["debug"] != true {
		t.Error("debug should be preserved")
	}
	if server["tls"] != true {
		t.Error("tls should be added")
	}
}

func TestMergeStructuredData_JSONDeleteWithNull(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	original := `{"server": {"host": "localhost", "port": 8080, "debug": true, "verbose": false}}`
	saveDataToolArtifact(t, ctx, facade, "config.json", []byte(original), "application/json")

	mergeTool := &MergeStructuredDataTool{}
	result, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "config.json",
		"patch": map[string]any{
			"server": map[string]any{
				"debug":   nil,
				"verbose": nil,
			},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["status"] != "success" {
		t.Fatalf("expected success, got: %v", result.Data)
	}

	content, _, _ := facade.LoadArtifact(ctx, "config.json", nil)
	var parsed map[string]any
	_ = json.Unmarshal(content, &parsed)

	server := parsed["server"].(map[string]any)
	if _, exists := server["debug"]; exists {
		t.Error("debug should be deleted")
	}
	if _, exists := server["verbose"]; exists {
		t.Error("verbose should be deleted")
	}
	if server["host"] != "localhost" {
		t.Error("host should be preserved")
	}
	if server["port"] != float64(8080) {
		t.Error("port should be preserved")
	}
}

func TestMergeStructuredData_YAMLMerge(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	original := "database:\n  host: db.local\n  port: 5432\n  pool_size: 10\n"
	saveDataToolArtifact(t, ctx, facade, "db.yaml", []byte(original), "application/yaml")

	mergeTool := &MergeStructuredDataTool{}
	result, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "db.yaml",
		"patch": map[string]any{
			"database": map[string]any{
				"pool_size": nil,
				"timeout":   30,
			},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["status"] != "success" {
		t.Fatalf("expected success, got: %v", result.Data)
	}
	if result.Data["format"] != "YAML" {
		t.Errorf("expected YAML format, got %v", result.Data["format"])
	}

	content, mimeType, _ := facade.LoadArtifact(ctx, "db.yaml", nil)
	if mimeType != "application/yaml" {
		t.Errorf("expected application/yaml, got %s", mimeType)
	}

	var parsed map[string]any
	_ = yaml.Unmarshal(content, &parsed)

	db := parsed["database"].(map[string]any)
	if db["host"] != "db.local" {
		t.Error("host should be preserved")
	}
	if _, exists := db["pool_size"]; exists {
		t.Error("pool_size should be deleted")
	}
	if db["timeout"] != 30 {
		t.Errorf("timeout should be 30, got %v", db["timeout"])
	}
}

func TestMergeStructuredData_VendorYAMLMIME(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	manifest := "name: vehicle-detector\ncomponents:\n  - filename: wf.yaml\n    name: Detector\n    status: deployed\n"
	saveDataToolArtifact(t, ctx, facade, "detector.yaml", []byte(manifest), SAMAgentConfigMIMEType)

	mergeTool := &MergeStructuredDataTool{}
	result, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "detector.yaml",
		"path":           "components.0",
		"patch":          map[string]any{"status": "validated"},
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["status"] != "success" {
		t.Fatalf("expected success, got: %v", result.Data)
	}
	if result.Data["format"] != "YAML" {
		t.Errorf("expected YAML format, got %v", result.Data["format"])
	}

	content, mimeType, _ := facade.LoadArtifact(ctx, "detector.yaml", nil)
	if mimeType != SAMAgentConfigMIMEType {
		t.Errorf("vendor MIME type must be preserved on write-back, got %s", mimeType)
	}

	var parsed map[string]any
	_ = yaml.Unmarshal(content, &parsed)
	components, ok := parsed["components"].([]any)
	if !ok || len(components) != 1 {
		t.Fatalf("components not preserved: %v", parsed["components"])
	}
	component := components[0].(map[string]any)
	if component["status"] != "validated" {
		t.Errorf("status should be patched to validated, got %v", component["status"])
	}
	if component["name"] != "Detector" {
		t.Error("sibling keys should be preserved")
	}
}

func TestMergeStructuredData_StillRejectsUnstructured(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "notes.txt", []byte("just prose"), "text/plain")

	mergeTool := &MergeStructuredDataTool{}
	result, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "notes.txt",
		"patch":          map[string]any{"status": "validated"},
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("text/plain must still be refused, got status %v (%s)", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "unsupported file type") {
		t.Errorf("refusal should name the unsupported type, got: %s", result.Message)
	}
}

func TestMergeStructuredData_WithPath(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	original := `{"app": {"server": {"host": "localhost", "port": 8080}, "logging": {"level": "info"}}}`
	saveDataToolArtifact(t, ctx, facade, "app.json", []byte(original), "application/json")

	mergeTool := &MergeStructuredDataTool{}
	result, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "app.json",
		"patch": map[string]any{
			"level":  "debug",
			"output": "stdout",
		},
		"path": "app.logging",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Data["status"] != "success" {
		t.Fatalf("expected success, got: %v", result.Data)
	}

	content, _, _ := facade.LoadArtifact(ctx, "app.json", nil)
	var parsed map[string]any
	_ = json.Unmarshal(content, &parsed)

	app := parsed["app"].(map[string]any)
	logging := app["logging"].(map[string]any)
	if logging["level"] != "debug" {
		t.Errorf("level should be debug, got %v", logging["level"])
	}
	if logging["output"] != "stdout" {
		t.Error("output should be added")
	}
	server := app["server"].(map[string]any)
	if server["host"] != "localhost" {
		t.Error("server.host should be preserved")
	}
}

func TestMergeStructuredData_ArrayReplaced(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	original := `{"tags": ["a", "b", "c"], "name": "test"}`
	saveDataToolArtifact(t, ctx, facade, "data.json", []byte(original), "application/json")

	mergeTool := &MergeStructuredDataTool{}
	_, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "data.json",
		"patch": map[string]any{
			"tags": []any{"x", "y"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _, _ := facade.LoadArtifact(ctx, "data.json", nil)
	var parsed map[string]any
	_ = json.Unmarshal(content, &parsed)

	tags := parsed["tags"].([]any)
	if len(tags) != 2 || tags[0] != "x" || tags[1] != "y" {
		t.Errorf("tags should be [x, y], got %v", tags)
	}
	if parsed["name"] != "test" {
		t.Error("name should be preserved")
	}
}

func TestMergeStructuredData_DeleteTopLevelKey(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	original := `{"keep": "yes", "remove_me": "gone", "also_remove": {"nested": true}}`
	saveDataToolArtifact(t, ctx, facade, "data.json", []byte(original), "application/json")

	mergeTool := &MergeStructuredDataTool{}
	_, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "data.json",
		"patch": map[string]any{
			"remove_me":   nil,
			"also_remove": nil,
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	content, _, _ := facade.LoadArtifact(ctx, "data.json", nil)
	var parsed map[string]any
	_ = json.Unmarshal(content, &parsed)

	if parsed["keep"] != "yes" {
		t.Error("keep should be preserved")
	}
	if _, exists := parsed["remove_me"]; exists {
		t.Error("remove_me should be deleted")
	}
	if _, exists := parsed["also_remove"]; exists {
		t.Error("also_remove should be deleted")
	}
}

func TestMergeStructuredData_UnsupportedFormat(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "data.csv", []byte("a,b\n1,2"), "text/csv")

	mergeTool := &MergeStructuredDataTool{}
	result, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "data.csv",
		"patch":          map[string]any{"a": "1"},
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("expected error for CSV, got status: %s", result.Status)
	}
}

func TestMergeStructuredData_InvalidPath(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "data.json", []byte(`{"a": 1}`), "application/json")

	mergeTool := &MergeStructuredDataTool{}
	result, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "data.json",
		"patch":          map[string]any{"x": "y"},
		"path":           "nonexistent.path",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("expected error for invalid path, got status: %s", result.Status)
	}
	if !strings.Contains(result.Message, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", result.Message)
	}
}

func TestMergeStructuredData_NotFound(t *testing.T) {
	toolCtx, _ := newDataToolContext(t)
	ctx := context.Background()

	mergeTool := &MergeStructuredDataTool{}
	result, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "missing.json",
		"patch":          map[string]any{"a": 1},
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Errorf("expected error for missing file, got status: %s", result.Status)
	}
}

func TestMergePatch_DeepNested(t *testing.T) {
	target := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "original",
				"d": "keep",
			},
			"e": "keep",
		},
	}
	patch := map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "updated",
				"d": nil,
			},
		},
	}
	mergePatch(target, patch)

	a := target["a"].(map[string]any)
	b := a["b"].(map[string]any)
	if b["c"] != "updated" {
		t.Errorf("c should be updated, got %v", b["c"])
	}
	if _, exists := b["d"]; exists {
		t.Error("d should be deleted")
	}
	if a["e"] != "keep" {
		t.Error("e should be preserved")
	}
}

func TestMergePatch_CreateNestedFromNull(t *testing.T) {
	target := map[string]any{
		"existing": "value",
	}
	patch := map[string]any{
		"new_section": map[string]any{
			"key1": "val1",
			"key2": "val2",
		},
	}
	mergePatch(target, patch)

	if target["existing"] != "value" {
		t.Error("existing should be preserved")
	}
	newSection := target["new_section"].(map[string]any)
	if newSection["key1"] != "val1" || newSection["key2"] != "val2" {
		t.Errorf("new section not created properly: %v", newSection)
	}
}
