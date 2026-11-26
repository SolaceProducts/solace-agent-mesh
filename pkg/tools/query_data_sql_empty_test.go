// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"strings"
	"testing"
)

func TestJMESPathProjectionColumns(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want []string
	}{
		{"array projection", "data[].{name: agent.name, hits: count}", []string{"name", "hits"}},
		{"feedback projection", "recentComments[].{rating: rating, comment: comment, created_at: createdAt, task_id: taskId}", []string{"rating", "comment", "created_at", "task_id"}},
		{"single object", "{up: byRating.up, down: byRating.down}", []string{"up", "down"}},
		{"nested hash value", "x[].{a: {b: c}, d: e}", []string{"a", "d"}},
		{"quoted key", `data[].{"my key": v, other: w}`, []string{"my key", "other"}},
		{"backtick literal with brace/comma", "s[].{status: `\"x,y\"`, n: count}", []string{"status", "n"}},
		{"not a hash (bare projection)", "data[].name", nil},
		{"pipe after hash (unsupported)", "data[].{a: b} | [0]", nil},
		{"list of hashes (not a trailing hash)", "[{a: b}, {c: d}]", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jmespathProjectionColumns(tc.expr)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("jmespathProjectionColumns(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func TestQueryDataWithSQL_EmptyTransformResult_HeaderOnly(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "feedback.json",
		[]byte(`{"byRating":{},"recentComments":[]}`), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files":         map[string]any{"fb": "feedback.json"},
		"jmespath_transforms": map[string]any{"fb": "recentComments[].{rating: rating, comment: comment, created_at: createdAt, task_id: taskId}"},
		"sql_query":           "SELECT rating, comment, created_at, task_id FROM fb ORDER BY created_at DESC",
		"output_filename":     "tpl_feedback.csv",
		"output_format":       "csv",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success on empty transform result, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 0 {
		t.Fatalf("expected 0 result rows, got %v", result.Data["result_rows"])
	}

	content, _, err := facade.LoadArtifact(ctx, "tpl_feedback.csv", nil)
	if err != nil {
		t.Fatalf("load output: %v", err)
	}
	got := strings.TrimSpace(string(content))
	if got != "rating,comment,created_at,task_id" {
		t.Fatalf("expected header-only CSV, got:\n%s", got)
	}
}

func TestQueryDataWithSQL_EmptyNoTransform_StillErrors(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()
	saveDataToolArtifact(t, ctx, facade, "empty.json", []byte(`[]`), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"t": "empty.json"},
		"sql_query":   "SELECT * FROM t",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status == "success" {
		t.Fatal("expected an error for an empty input with no derivable schema")
	}
}
