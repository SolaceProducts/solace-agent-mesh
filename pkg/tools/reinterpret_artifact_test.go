// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const stimYAML = `invocation_details:
    task_id: 019ec173-cb45-7cca-86e2-7b3c5ccfa90f
    status: completed
invocation_flow:
    - id: evt-000
      direction: request
      topic: default/a2a/v1/agent/request/agent_x
    - id: evt-001
      direction: status
      topic: default/a2a/v1/gateway/status/agent_x
`

func TestTransformJMESPath_StimReinterpretedAsYAML(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "task.stim", []byte(stimYAML), "text/plain; charset=utf-8")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "task.stim",
		"jmespath_expression": "invocation_flow[].direction",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if rows, ok := result.Data["result_rows"].(int); !ok || rows != 2 {
		t.Errorf("expected 2 directions extracted, got %v", result.Data["result_rows"])
	}
}

func TestQueryDataWithSQL_StimReinterpretedAsYAML(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	listYAML := "- name: Alice\n  age: 30\n- name: Bob\n  age: 25\n"
	saveDataToolArtifact(t, ctx, facade, "people.stim", []byte(listYAML), "text/plain; charset=utf-8")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"people": "people.stim"},
		"sql_query":   "SELECT name FROM people WHERE age > 26 ORDER BY name",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
}

func TestMergeStructuredData_StimReinterpretedAsYAML(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "task.stim", []byte(stimYAML), "text/plain")

	mergeTool := &MergeStructuredDataTool{}
	result, err := mergeTool.Execute(ctx, map[string]any{
		"input_filename": "task.stim",
		"patch":          map[string]any{"invocation_details": map[string]any{"status": "cancelled"}},
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
}

func TestQueryDataWithSQL_PlainTextStaysUnsupported(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "notes.txt", []byte("just some prose"), "text/plain")

	sqlTool := &QueryDataWithSQLTool{}
	result, _ := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"notes": "notes.txt"},
		"sql_query":   "SELECT * FROM notes",
	}, toolCtx)
	if result.Status == "success" {
		t.Fatalf("expected failure for plain-text artifact, got success")
	}
	if !strings.Contains(strings.ToLower(result.Message), "unsupported") {
		t.Errorf("expected an unsupported-type error, got: %s", result.Message)
	}
}

func TestReinterpretArtifact_RejectsInternalName(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()
	saveDataToolArtifact(t, ctx, facade, "task.stim", []byte(stimYAML), "text/plain")

	tl := &ReinterpretArtifactTool{}
	result, _ := tl.Execute(ctx, map[string]any{
		"input_filename":  "task.stim",
		"output_filename": "report.metadata.json",
	}, toolCtx)
	if result.Status == "success" {
		t.Fatalf("expected rejection of an internal-artifact output name")
	}
}

func TestReinterpretArtifact_RejectsGenericMIME(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()
	saveDataToolArtifact(t, ctx, facade, "task.stim", []byte(stimYAML), "text/plain")

	tl := &ReinterpretArtifactTool{}
	result, _ := tl.Execute(ctx, map[string]any{
		"input_filename":  "task.stim",
		"output_filename": "task.yaml",
		"mime_type":       "text/plain",
	}, toolCtx)
	if result.Status == "success" {
		t.Fatalf("expected rejection of a generic mime_type")
	}
}

func TestReinterpretArtifact_Metadata(t *testing.T) {
	tl := &ReinterpretArtifactTool{}
	if tl.Name() != "reinterpret_artifact" {
		t.Errorf("Name = %q", tl.Name())
	}
	if tl.Description() == "" {
		t.Error("Description is empty")
	}
	params := tl.Parameters()
	if params == nil || params.Type != "object" {
		t.Fatalf("unexpected Parameters: %#v", params)
	}
	if _, ok := params.Properties["input_filename"]; !ok {
		t.Error("Parameters missing input_filename")
	}
	if len(params.Required) == 0 || params.Required[0] != "input_filename" {
		t.Errorf("Required = %v, want [input_filename]", params.Required)
	}
}

func TestReinterpretArtifact_MissingInput(t *testing.T) {
	toolCtx, _ := newDataToolContext(t)
	tl := &ReinterpretArtifactTool{}
	result, _ := tl.Execute(context.Background(), map[string]any{"output_filename": "x.yaml"}, toolCtx)
	if result.Status == "success" {
		t.Fatal("expected error when input_filename is missing")
	}
}

func TestReinterpretArtifact_NotFound(t *testing.T) {
	toolCtx, _ := newDataToolContext(t)
	tl := &ReinterpretArtifactTool{}
	result, _ := tl.Execute(context.Background(), map[string]any{
		"input_filename":  "nope.stim",
		"output_filename": "nope.yaml",
	}, toolCtx)
	if result.Status == "success" {
		t.Fatal("expected error for a nonexistent input artifact")
	}
}

func TestReinterpretArtifact_DefaultExtensionForMIME(t *testing.T) {
	cases := []struct{ mime, wantExt string }{
		{"application/json", ".json"},
		{"text/csv", ".csv"},
		{"application/xml", ".xml"},
		{"text/yaml", ".yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.mime, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			ctx := context.Background()
			saveDataToolArtifact(t, ctx, facade, "blob.stim", []byte("a: 1\n"), "text/plain")

			tl := &ReinterpretArtifactTool{}
			result, err := tl.Execute(ctx, map[string]any{
				"input_filename": "blob.stim",
				"mime_type":      tc.mime,
			}, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != "success" {
				t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
			}
			if got := result.Data["output_filename"].(string); !strings.HasSuffix(got, tc.wantExt) {
				t.Errorf("output_filename %q, want suffix %q", got, tc.wantExt)
			}
		})
	}
}

func TestReinterpretArtifact_NoDefaultExtension(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()
	saveDataToolArtifact(t, ctx, facade, "task.stim", []byte(stimYAML), "text/plain")

	tl := &ReinterpretArtifactTool{}
	result, _ := tl.Execute(ctx, map[string]any{
		"input_filename": "task.stim",
		"mime_type":      "image/png",
	}, toolCtx)
	if result.Status == "success" {
		t.Fatalf("expected failure when no default extension exists for the mime_type")
	}
}

func TestReinterpretArtifact_StimToYAML(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "task.stim", []byte(stimYAML), "text/plain")

	tl := &ReinterpretArtifactTool{}
	result, err := tl.Execute(ctx, map[string]any{
		"input_filename":  "task.stim",
		"output_filename": "task.yaml",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if got := result.Data["mime_type"]; got != "application/x-yaml" {
		t.Errorf("expected application/x-yaml, got %v", got)
	}

	content, mime, loadErr := facade.LoadArtifact(ctx, "task.yaml", nil)
	if loadErr != nil {
		t.Fatalf("failed to load reinterpreted artifact: %v", loadErr)
	}
	if mime != "application/x-yaml" {
		t.Errorf("stored MIME = %q, want application/x-yaml", mime)
	}
	if string(content) != stimYAML {
		t.Errorf("content was modified during reinterpretation")
	}

	if _, _, origErr := facade.LoadArtifact(ctx, "task.stim", nil); origErr != nil {
		t.Errorf("original artifact should be preserved: %v", origErr)
	}

	schema, ok := result.Data["schema"].(map[string]any)
	if !ok {
		t.Fatalf("expected schema in result, got %#v", result.Data["schema"])
	}
	if schema["type"] != "application/x-yaml" {
		t.Errorf("schema type = %v, want application/x-yaml", schema["type"])
	}
	if inferred, _ := schema["inferred"].(bool); !inferred {
		t.Errorf("expected schema.inferred = true, got %v", schema["inferred"])
	}
	if schema["structure"] == nil {
		t.Error("expected a structure in the inferred YAML schema")
	}
}

func TestReinterpretArtifact_DefaultFilenameFromMIME(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "task.stim", []byte(stimYAML), "text/plain")

	tl := &ReinterpretArtifactTool{}
	result, err := tl.Execute(ctx, map[string]any{
		"input_filename": "task.stim",
		"mime_type":      "application/x-yaml",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if got := result.Data["output_filename"]; got != "task.yaml" {
		t.Errorf("expected derived filename task.yaml, got %v", got)
	}
}

func TestReinterpretArtifact_RequiresTargetType(t *testing.T) {
	toolCtx, _ := newDataToolContext(t)
	ctx := context.Background()

	tl := &ReinterpretArtifactTool{}
	result, _ := tl.Execute(ctx, map[string]any{"input_filename": "task.stim"}, toolCtx)
	if result.Status == "success" {
		t.Fatalf("expected error when neither output_filename nor mime_type given")
	}
}

func TestReinterpretArtifact_RejectsSameFilename(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()
	saveDataToolArtifact(t, ctx, facade, "data.yaml", []byte(stimYAML), "application/x-yaml")

	tl := &ReinterpretArtifactTool{}
	result, _ := tl.Execute(ctx, map[string]any{
		"input_filename":  "data.yaml",
		"output_filename": "data.yaml",
	}, toolCtx)
	if result.Status == "success" {
		t.Fatalf("expected error when output equals input filename")
	}
}

func TestReinterpretArtifact_FailureCodes(t *testing.T) {
	invalidArgs := tool.DispatchErrorCode(taxonomy.DispatchClassValidation)
	tests := []struct {
		name        string
		args        map[string]any
		wantMessage string
		wantCode    string
	}{
		{"input_filename missing", map[string]any{"output_filename": "x.yaml"}, "input_filename is required", invalidArgs},
		{"no target type", map[string]any{"input_filename": "task.stim"}, "provide output_filename", invalidArgs},
		{"input artifact does not exist", map[string]any{"input_filename": "nope.stim", "output_filename": "nope.yaml"}, "not found", taxonomy.ErrorCodeArtifactNotFound},
		{"generic mime_type", map[string]any{"input_filename": "task.stim", "mime_type": "text/plain"}, "could not resolve a specific MIME type", invalidArgs},
		{"mime_type with no default extension", map[string]any{"input_filename": "task.stim", "mime_type": "application/pdf"}, "no default extension", invalidArgs},
		{"output_filename empty after sanitizing", map[string]any{"input_filename": "task.stim", "output_filename": "___", "mime_type": "application/x-yaml"}, "is not a valid artifact name", invalidArgs},
		{"output_filename reserved for metadata", map[string]any{"input_filename": "task.stim", "output_filename": "report.metadata.json"}, "reserved for internal use", invalidArgs},
		{"output_filename same as input", map[string]any{"input_filename": "task.stim", "output_filename": "task.stim", "mime_type": "application/x-yaml"}, "must differ from input_filename", invalidArgs},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			saveDataToolArtifact(t, t.Context(), facade, "task.stim", []byte(stimYAML), "text/plain")

			result, err := (&ReinterpretArtifactTool{}).Execute(t.Context(), tc.args, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError || !strings.Contains(result.Message, tc.wantMessage) {
				t.Fatalf("result = %s: %q, want an error containing %q", result.Status, result.Message, tc.wantMessage)
			}
			if result.ErrorCode != tc.wantCode {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, tc.wantCode)
			}
		})
	}
}
