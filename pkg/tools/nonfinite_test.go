// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func runSQLTool(t *testing.T, filename, mimeType, input, transform, query, format string) (*tool.Result, *tool.Facade) {
	t.Helper()
	toolCtx, facade := newDataToolContext(t)
	saveDataToolArtifact(t, t.Context(), facade, filename, []byte(input), mimeType)
	args := map[string]any{
		"input_files":   map[string]any{"t": filename},
		"sql_query":     query,
		"output_format": format,
	}
	if transform != "" {
		args["jmespath_transforms"] = map[string]any{"t": transform}
	}
	result, err := (&QueryDataWithSQLTool{}).Execute(t.Context(), args, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	return result, facade
}

func assertNonFiniteNote(t *testing.T, result *tool.Result, count int) {
	t.Helper()
	messages := []string{result.Message, result.Data["message"].(string)}
	for _, msg := range messages {
		if count == 0 {
			if strings.Contains(msg, "NaN/Infinity") {
				t.Errorf("message = %q, want no non-finite note", msg)
			}
			continue
		}
		if note := fmt.Sprintf("%d NaN/Infinity value(s)", count); !strings.Contains(msg, note) {
			t.Errorf("message = %q, want it to report %q", msg, note)
		}
	}
}

func TestQueryDataWithSQL_NonFiniteOutputBecomesNull(t *testing.T) {
	tests := []struct {
		name      string
		filename  string
		mimeType  string
		input     string
		transform string
		query     string
		want      []string
		nonFinite int
	}{
		{
			name:      "YAML non-finite input values",
			filename:  "data.yaml",
			mimeType:  "application/yaml",
			input:     "- v: .nan\n- v: .inf\n- v: -.inf\n- v: 2\n",
			query:     "SELECT v FROM t",
			want:      []string{"<nil>", "<nil>", "<nil>", "2"},
			nonFinite: 2,
		},
		{
			name:      "to_number of Infinity in a transform",
			filename:  "data.json",
			mimeType:  "application/json",
			input:     `[{"v": "Inf"}, {"v": "2"}]`,
			transform: "[].{v: to_number(v)}",
			query:     "SELECT v FROM t",
			want:      []string{"<nil>", "2"},
			nonFinite: 1,
		},
		{
			name:      "Infinity computed by the query",
			filename:  "data.json",
			mimeType:  "application/json",
			input:     `[{"v": 1}]`,
			query:     "SELECT v * 1e308 * 10 AS v FROM t",
			want:      []string{"<nil>"},
			nonFinite: 1,
		},
		{
			name:     "Infinity input stays Infinity inside the query",
			filename: "data.yaml",
			mimeType: "application/yaml",
			input:    "- v: .inf\n- v: -.inf\n- v: 2\n",
			query:    "SELECT COUNT(v) AS v FROM t WHERE v > 100 OR v < -100",
			want:     []string{"2"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, facade := runSQLTool(t, tt.filename, tt.mimeType, tt.input, tt.transform, tt.query, "json")
			if got := queryResultColumn(t, t.Context(), facade, result, "v"); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("v = %v, want %v", got, tt.want)
			}
			assertNonFiniteNote(t, result, tt.nonFinite)
		})
	}
}

func TestQueryDataWithSQL_NonFiniteCSVKeepsText(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		mimeType string
		input    string
		query    string
	}{
		{"YAML Infinity input", "data.yaml", "application/yaml", "- v: .inf\n- v: 2\n", "SELECT v FROM t"},
		{"Infinity computed by the query", "data.json", "application/json", `[{"v": 1}, {"v": 2e-308}]`, "SELECT v * 1e308 * 10 AS v FROM t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, facade := runSQLTool(t, tt.filename, tt.mimeType, tt.input, "", tt.query, "csv")
			content, _, loadErr := facade.LoadArtifact(t.Context(), result.Data["output_filename"].(string), nil)
			if loadErr != nil {
				t.Fatalf("failed to load output: %v", loadErr)
			}
			if got := strings.Split(strings.TrimSpace(string(content)), "\n"); len(got) != 3 || got[1] != "+Inf" {
				t.Errorf("csv lines = %q, want the Infinity row kept as +Inf", got)
			}
			preview, _ := result.Data["result_preview"].([]map[string]any)
			if len(preview) == 0 || preview[0]["v"] != nil {
				t.Errorf("result_preview = %v, want the Infinity row as null", result.Data["result_preview"])
			}
			assertNonFiniteNote(t, result, 1)
			for _, msg := range []string{result.Message, result.Data["message"].(string)} {
				if !strings.Contains(msg, "CSV artifact keeps them as text") || strings.Contains(msg, "written as null") {
					t.Errorf("message = %q, want it to say the CSV keeps the values as text", msg)
				}
			}
		})
	}
}

func TestNormalizeYAMLForJMESPath(t *testing.T) {
	in := []any{
		int(-(1 << 53)), int(1<<53 + 1),
		int64(5), int64(1 << 60),
		uint64(7), uint64(1 << 63),
		map[any]any{1: int(3)},
	}
	want := []any{
		float64(-(1 << 53)), int(1<<53 + 1),
		float64(5), int64(1 << 60),
		float64(7), uint64(1 << 63),
		map[string]any{"1": float64(3)},
	}
	if got := NormalizeYAMLForJMESPath(in); !reflect.DeepEqual(got, want) {
		t.Errorf("NormalizeYAMLForJMESPath = %#v, want %#v", got, want)
	}
}

func TestYAMLLargeIntegersStayExact(t *testing.T) {
	yamlIDs := "- id: 9007199254740993\n- id: 18446744073709551615\n"

	t.Run("transform_data_with_jmespath", func(t *testing.T) {
		toolCtx, facade := newDataToolContext(t)
		saveDataToolArtifact(t, t.Context(), facade, "ids.yaml", []byte(yamlIDs), "application/yaml")
		result, err := (&TransformDataWithJMESPathTool{}).Execute(t.Context(), map[string]any{
			"input_filename":      "ids.yaml",
			"jmespath_expression": "[].{id: id}",
		}, toolCtx)
		if err != nil || result.Status != "success" {
			t.Fatalf("expected success, got %v %v", err, result)
		}
		content, _, _ := facade.LoadArtifact(t.Context(), result.Data["output_filename"].(string), nil)
		for _, id := range []string{"9007199254740993", "18446744073709551615"} {
			if !strings.Contains(string(content), id) {
				t.Errorf("artifact %s lost precision for %s", content, id)
			}
		}
	})

	t.Run("query_data_with_sql jmespath_transforms", func(t *testing.T) {
		result, facade := runSQLTool(t, "ids.yaml", "application/yaml", "- id: 9007199254740993\n- id: 2\n",
			"[].{id: id}", "SELECT COUNT(*) AS v FROM t WHERE id = 9007199254740993", "json")
		if got := queryResultColumn(t, t.Context(), facade, result, "v"); !reflect.DeepEqual(got, []string{"1"}) {
			t.Errorf("rows matching the exact id = %v, want [1]", got)
		}
	})
}

func TestYAMLIntegersAreJMESPathNumbers(t *testing.T) {
	t.Run("transform_data_with_jmespath", func(t *testing.T) {
		toolCtx, facade := newDataToolContext(t)
		ctx := t.Context()
		saveDataToolArtifact(t, ctx, facade, "data.yaml", []byte("xs:\n  - 2\n  - 4\n"), "application/yaml")

		result, err := (&TransformDataWithJMESPathTool{}).Execute(ctx, map[string]any{
			"input_filename":      "data.yaml",
			"jmespath_expression": "{a: avg(xs), m: max(xs), s: sort(xs)}",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "success" {
			t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
		}
		want := map[string]any{"a": float64(3), "m": float64(4), "s": []any{float64(2), float64(4)}}
		if got := result.Data["result_preview"]; !reflect.DeepEqual(got, want) {
			t.Errorf("result_preview = %v, want %v", got, want)
		}
		if strings.Contains(result.Message, "NaN") {
			t.Errorf("message = %q, want no non-finite note for a finite result", result.Message)
		}
	})

	t.Run("query_data_with_sql jmespath_transforms", func(t *testing.T) {
		toolCtx, facade := newDataToolContext(t)
		ctx := t.Context()
		saveDataToolArtifact(t, ctx, facade, "data.yaml", []byte("- xs: [2, 4]\n"), "application/yaml")

		result, err := (&QueryDataWithSQLTool{}).Execute(ctx, map[string]any{
			"input_files":         map[string]any{"t": "data.yaml"},
			"jmespath_transforms": map[string]any{"t": "[].{a: avg(xs)}"},
			"sql_query":           "SELECT a FROM t",
			"output_format":       "json",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "success" {
			t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
		}
		if got := queryResultColumn(t, ctx, facade, result, "a"); !reflect.DeepEqual(got, []string{"3"}) {
			t.Errorf("a = %v, want [3]", got)
		}
	})
}
