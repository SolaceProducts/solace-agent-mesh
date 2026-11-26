// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/jmespath/go-jmespath"
)

func TestTransformJMESPath_JSONFiltering(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	jsonData := `{"people":[{"name":"Alice","active":true},{"name":"Bob","active":false},{"name":"Charlie","active":true}]}`
	saveDataToolArtifact(t, ctx, facade, "data.json", []byte(jsonData), "application/json")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "data.json",
		"jmespath_expression": "people[?active].name",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}

	outFile := result.Data["output_filename"].(string)
	content, _, loadErr := facade.LoadArtifact(ctx, outFile, nil)
	if loadErr != nil {
		t.Fatalf("failed to load output: %v", loadErr)
	}
	var names []string
	if jsonErr := json.Unmarshal(content, &names); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v", jsonErr)
	}
	if len(names) != 2 || names[0] != "Alice" || names[1] != "Charlie" {
		t.Errorf("unexpected result: %v", names)
	}
}

func TestTransformJMESPath_TagsWorkingOnlyWhenAutoNamed(t *testing.T) {
	tests := []struct {
		name           string
		outputFilename string
		wantWorking    bool
	}{
		{"auto-generated filename", "", true},
		{"explicit filename", "names", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			ctx := t.Context()
			saveDataToolArtifact(t, ctx, facade, "data.json", []byte(`{"people":[{"name":"Alice"}]}`), "application/json")

			args := map[string]any{
				"input_filename":      "data.json",
				"jmespath_expression": "people[].name",
			}
			if tt.outputFilename != "" {
				args["output_filename"] = tt.outputFilename
			}
			result, err := (&TransformDataWithJMESPathTool{}).Execute(ctx, args, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != "success" {
				t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
			}

			outFile := result.Data["output_filename"].(string)
			meta := artifact.LoadCompanionMetadata(ctx, toolCtx.ArtifactService,
				toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, outFile, nil)
			if meta == nil {
				t.Fatalf("no companion metadata for %q", outFile)
			}
			if got := slices.Contains(companionTagStrings(meta), a2a.ArtifactTagWorking); got != tt.wantWorking {
				t.Fatalf("jmespath output %q tags = %v, __working = %v, want %v", outFile, meta["tags"], got, tt.wantWorking)
			}
		})
	}
}

func TestTransformJMESPath_NonFiniteBecomesNull(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		mimeType   string
		input      string
		expression string
		want       string
		nullified  int
	}{
		{
			name:       "avg of empty filter inside multi-select",
			input:      `{"records": []}`,
			expression: `{ satisfaction_avg: avg(map(&to_number(@), records[?contains(['1','2','3','4','5'], CustomerSatisfactionScore__c)].CustomerSatisfactionScore__c)) }`,
			want:       `{"satisfaction_avg": null}`,
			nullified:  1,
		},
		{
			name:       "to_number of non-finite strings",
			input:      `{"records": [{"v": "NaN"}, {"v": "Inf"}, {"v": "-Inf"}, {"v": "3"}]}`,
			expression: `records[].to_number(v)`,
			want:       `[null, null, null, 3]`,
			nullified:  3,
		},
		{
			name:       "per-group avg nested two containers deep",
			input:      `{"groups": [{"n": "a", "s": []}, {"n": "b", "s": [1, 3]}]}`,
			expression: `groups[].{n: n, a: avg(s)}`,
			want:       `[{"n": "a", "a": null}, {"n": "b", "a": 2}]`,
			nullified:  1,
		},
		{
			name:       "advised empty-aggregate expression keeps the count visible",
			input:      `{"xs": []}`,
			expression: emptyAggregateExample,
			want:       `{"count": 0, "avg": null}`,
			nullified:  1,
		},
		{
			name:       "YAML non-finite input values",
			filename:   "data.yaml",
			mimeType:   "application/yaml",
			input:      "records:\n  - v: .nan\n  - v: .inf\n  - v: -.inf\n  - v: 2\n",
			expression: `records`,
			want:       `[{"v": null}, {"v": null}, {"v": null}, {"v": 2}]`,
			nullified:  3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filename := cmp.Or(tt.filename, "data.json")
			toolCtx, facade := newDataToolContext(t)
			ctx := t.Context()
			saveDataToolArtifact(t, ctx, facade, filename, []byte(tt.input), cmp.Or(tt.mimeType, "application/json"))

			var want any
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatalf("bad want fixture %q: %v", tt.want, err)
			}

			result, err := (&TransformDataWithJMESPathTool{}).Execute(ctx, map[string]any{
				"input_filename":      filename,
				"jmespath_expression": tt.expression,
			}, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != "success" {
				t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
			}
			assertNonFiniteNote(t, result, tt.nullified)

			content, _, loadErr := facade.LoadArtifact(ctx, result.Data["output_filename"].(string), nil)
			if loadErr != nil {
				t.Fatalf("failed to load output: %v", loadErr)
			}
			var got any
			if jsonErr := json.Unmarshal(content, &got); jsonErr != nil {
				t.Fatalf("output is not valid JSON: %v", jsonErr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("artifact = %s, want %s", content, tt.want)
			}

			previewJSON, marshalErr := json.Marshal(result.Data["result_preview"])
			if marshalErr != nil {
				t.Fatalf("result_preview is not serializable: %v", marshalErr)
			}
			var preview any
			if jsonErr := json.Unmarshal(previewJSON, &preview); jsonErr != nil {
				t.Fatalf("result_preview is not valid JSON: %v", jsonErr)
			}
			if !reflect.DeepEqual(preview, want) {
				t.Errorf("result_preview = %s, want %s", previewJSON, tt.want)
			}
		})
	}
}

func TestTransformJMESPath_TopLevelNonFiniteIsError(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		expression string
	}{
		{"NaN from avg of empty list", `{"records": []}`, "avg(records[].score)"},
		{"Inf from to_number", `{"v": "Inf"}`, "to_number(v)"},
		{"-Inf from to_number", `{"v": "-Inf"}`, "to_number(v)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			ctx := t.Context()
			saveDataToolArtifact(t, ctx, facade, "data.json", []byte(tt.input), "application/json")

			result, err := (&TransformDataWithJMESPathTool{}).Execute(ctx, map[string]any{
				"input_filename":      "data.json",
				"jmespath_expression": tt.expression,
			}, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != "error" {
				t.Fatalf("expected error, got %s: %s", result.Status, result.Message)
			}
			if !strings.Contains(result.Message, "non-finite number") {
				t.Errorf("message = %q, want it to name the non-finite result", result.Message)
			}
			if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
			}
			if !strings.Contains(result.Message, emptyAggregateExample) {
				t.Errorf("message = %q, want it to advise %s", result.Message, emptyAggregateExample)
			}
			if !strings.Contains(result.Message, "check the field names") {
				t.Errorf("message = %q, want it to point at misspelled field names", result.Message)
			}
			if names, listErr := facade.ListArtifacts(ctx); listErr != nil || !slices.Equal(names, []string{"data.json"}) {
				t.Errorf("artifacts = %v (err %v), want only the input; an error must not save a result", names, listErr)
			}
		})
	}
}

func companionTagStrings(meta map[string]any) []string {
	raw, _ := meta["tags"].([]any)
	tags := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			tags = append(tags, s)
		}
	}
	return tags
}

func TestTransformJMESPath_Projection(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	jsonData := `[{"name":"Alice","age":30,"city":"NYC"},{"name":"Bob","age":25,"city":"LA"}]`
	saveDataToolArtifact(t, ctx, facade, "people.json", []byte(jsonData), "application/json")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "people.json",
		"jmespath_expression": "[].{person_name: name, location: city}",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}

	outFile := result.Data["output_filename"].(string)
	content, _, _ := facade.LoadArtifact(ctx, outFile, nil)
	var people []map[string]any
	_ = json.Unmarshal(content, &people)
	if len(people) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(people))
	}
	if people[0]["person_name"] != "Alice" {
		t.Errorf("expected Alice, got %v", people[0]["person_name"])
	}
}

func TestTransformJMESPath_CSV(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	csvData := "name,score\nAlice,90\nBob,85\nCharlie,95"
	saveDataToolArtifact(t, ctx, facade, "scores.csv", []byte(csvData), "text/csv")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "scores.csv",
		"jmespath_expression": "[].name",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}

	outFile := result.Data["output_filename"].(string)
	content, _, _ := facade.LoadArtifact(ctx, outFile, nil)
	var names []string
	_ = json.Unmarshal(content, &names)
	if len(names) != 3 || names[0] != "Alice" {
		t.Errorf("unexpected CSV result: %v", names)
	}
}

func TestTransformJMESPath_BlankCSVCellIsNull(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	csvData := "name,score\nAlice,95\nBob,\nCarol,80"
	saveDataToolArtifact(t, ctx, facade, "scores.csv", []byte(csvData), "text/csv")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "scores.csv",
		"jmespath_expression": "[]",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}

	outFile := result.Data["output_filename"].(string)
	content, _, _ := facade.LoadArtifact(ctx, outFile, nil)
	if strings.Contains(string(content), `"score":""`) {
		t.Errorf("blank cell encoded as empty string, want JSON null: %s", content)
	}
	var rows []map[string]any
	if err := json.Unmarshal(content, &rows); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw=%s", err, content)
	}
	if v, ok := rows[1]["score"]; !ok || v != nil {
		t.Errorf("Bob's blank score = %v (present=%v), want JSON null", v, ok)
	}
}

func TestTransformJMESPath_YAML(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	yamlData := `store:
  book:
    - title: "Book A"
      price: 5
    - title: "Book B"
      price: 15`
	saveDataToolArtifact(t, ctx, facade, "store.yaml", []byte(yamlData), "application/yaml")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "store.yaml",
		"jmespath_expression": "store.book[].title",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}

	outFile := result.Data["output_filename"].(string)
	content, _, _ := facade.LoadArtifact(ctx, outFile, nil)
	var titles []string
	_ = json.Unmarshal(content, &titles)
	if len(titles) != 2 || titles[0] != "Book A" {
		t.Errorf("unexpected YAML result: %v", titles)
	}
}

func TestTransformJMESPath_InvalidExpression(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "data.json", []byte(`{"a":1}`), "application/json")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "data.json",
		"jmespath_expression": "[???invalid",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error for invalid expression, got %s", result.Status)
	}
	if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
	}
}

func TestTransformJMESPath_NullMatchIsInvalidQuery(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "data.json", []byte(`{"a":1}`), "application/json")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "data.json",
		"jmespath_expression": "missing.field",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error for a null match, got %s", result.Status)
	}
	if !strings.Contains(result.Message, "returned null (no match)") {
		t.Errorf("expected no-match message, got: %s", result.Message)
	}
	if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
	}
}

func TestTransformJMESPath_CustomOutputFilename(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "data.json", []byte(`[1,2,3]`), "application/json")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "data.json",
		"jmespath_expression": "length(@)",
		"output_filename":     "count_result",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outFile := result.Data["output_filename"].(string)
	if !strings.HasPrefix(outFile, "count_result") || !strings.HasSuffix(outFile, ".json") {
		t.Errorf("unexpected output filename: %s", outFile)
	}
}

func TestTransformJMESPath_FileWithVersion(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "versioned.json", []byte(`{"v":1}`), "application/json")
	saveDataToolArtifact(t, ctx, facade, "versioned.json", []byte(`{"v":2}`), "application/json")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "versioned.json:0",
		"jmespath_expression": "v",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}

	outFile := result.Data["output_filename"].(string)
	content, _, _ := facade.LoadArtifact(ctx, outFile, nil)
	if strings.TrimSpace(string(content)) != "1" {
		t.Errorf("expected version 0 value (1), got: %s", string(content))
	}
}

func TestTransformJMESPath_UnsupportedMIME(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "data.bin", []byte{0x00, 0x01}, "application/octet-stream")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "data.bin",
		"jmespath_expression": "@",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error for unsupported MIME, got %s", result.Status)
	}
}

func TestTransformJMESPath_ExpressionTooLong(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()
	saveDataToolArtifact(t, ctx, facade, "data.json", []byte(`{"a":1}`), "application/json")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, _ := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "data.json",
		"jmespath_expression": strings.Repeat("a", maxJMESPathExpressionLen+1),
	}, toolCtx)

	if result.Status != "error" {
		t.Fatalf("expected error for over-length expression, got %s", result.Status)
	}
	if !strings.Contains(result.Message, "too long") {
		t.Errorf("expected 'too long' in error, got: %s", result.Message)
	}
	if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
	}
}

func TestTransformJMESPath_ExpressionTooDeep(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()
	saveDataToolArtifact(t, ctx, facade, "data.json", []byte(`{"a":1}`), "application/json")

	depth := maxJMESPathExpressionDepth + 1
	expr := strings.Repeat("(", depth) + "a" + strings.Repeat(")", depth)

	jmesTool := &TransformDataWithJMESPathTool{}
	result, _ := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "data.json",
		"jmespath_expression": expr,
	}, toolCtx)

	if result.Status != "error" {
		t.Fatalf("expected error for over-deep expression, got %s", result.Status)
	}
	if !strings.Contains(result.Message, "deeply nested") {
		t.Errorf("expected 'deeply nested' in error, got: %s", result.Message)
	}
	if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
	}
}

func TestTransformJMESPath_PanicRecovered(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()
	saveDataToolArtifact(t, ctx, facade, "nested.json", []byte(`{"a":[[1,2],[3,4]]}`), "application/json")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, err := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "nested.json",
		"jmespath_expression": "contains(a, [1,2])",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error result (recovered panic), got %s: %s", result.Status, result.Message)
	}
}

func TestTransformJMESPath_RuntimeErrorDoesNotEchoData(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	const secret = "TOPSECRET-API-KEY-abc123"
	jsonData := fmt.Sprintf(`{"creds":{"api_key":%q}}`, secret)
	saveDataToolArtifact(t, ctx, facade, "creds.json", []byte(jsonData), "application/json")

	jmesTool := &TransformDataWithJMESPathTool{}
	result, _ := jmesTool.Execute(ctx, map[string]any{
		"input_filename":      "creds.json",
		"jmespath_expression": "abs(creds.api_key)",
	}, toolCtx)

	if result.Status != "error" {
		t.Fatalf("expected error result, got %s", result.Status)
	}
	if strings.Contains(result.Message, secret) {
		t.Errorf("error message must not echo data values verbatim; leaked %q in: %s", secret, result.Message)
	}
	if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
	}
}

func TestMaxJMESPathDepth(t *testing.T) {
	cases := []struct {
		expr string
		want int
	}{
		{"a", 0},
		{"a.b.c", 0},
		{"((a))", 2},
		{"(((((a)))))", 5},
		{"!a", 1},
		{"!!!a", 3},
		{"&a", 1},
		{"a[*][*]", 1},
		{"{x: a}", 1},
		{"((a)) + ((b))", 2},
	}
	for _, tc := range cases {
		if got := maxJMESPathDepth(tc.expr); got != tc.want {
			t.Errorf("maxJMESPathDepth(%q) = %d; want %d", tc.expr, got, tc.want)
		}
	}
}

func TestTransformJMESPath_DescriptionLiteralsEvaluate(t *testing.T) {
	csvRows := []any{map[string]any{"price": "5"}, map[string]any{"price": "50"}}
	if !strings.Contains((&TransformDataWithJMESPathTool{}).Description(), "to_number(price)") {
		t.Fatal("description no longer says CSV values need to_number")
	}
	if got, err := jmespath.Search("[?to_number(price) < `10`]", csvRows); err != nil {
		t.Fatal(err)
	} else if matches, _ := got.([]any); len(matches) != 1 {
		t.Errorf("to_number filter on CSV strings matched %v, want the one row under 10", got)
	}

	data := []any{
		map[string]any{"price": 5.0, "status": "open"},
		map[string]any{"price": 50.0, "status": "closed"},
	}
	desc := (&TransformDataWithJMESPathTool{}).Description()
	for _, expr := range []string{"[?price < `10`]", "[?status == 'open']"} {
		if !strings.Contains(desc, expr) {
			t.Fatalf("description no longer shows %s", expr)
		}
		got, err := jmespath.Search(expr, data)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if matches, _ := got.([]any); len(matches) != 1 {
			t.Errorf("%s matched %v, want the one open item under 10", expr, got)
		}
	}

	if !strings.Contains(desc, "double quotes name a field") {
		t.Fatal("description no longer warns that double quotes name a field")
	}
	got, err := jmespath.Search(`[?status == "open"]`, data)
	if err != nil {
		t.Fatal(err)
	}
	if matches, _ := got.([]any); len(matches) != 0 {
		t.Errorf(`[?status == "open"] matched %v; the warning that double quotes name a field no longer holds`, got)
	}
	got, err = jmespath.Search(`[?"status" == 'open']`, data)
	if err != nil {
		t.Fatal(err)
	}
	if matches, _ := got.([]any); len(matches) != 1 {
		t.Errorf(`[?"status" == 'open'] matched %v, want the open item: double quotes name a field`, got)
	}
}

func TestTransformJMESPath_RefusedCallCodes(t *testing.T) {
	invalidArgs := tool.DispatchErrorCode(taxonomy.DispatchClassValidation)
	tests := []struct {
		name        string
		args        map[string]any
		wantMessage string
		wantCode    string
	}{
		{"input_filename missing", map[string]any{"jmespath_expression": "a"}, "input_filename is required", invalidArgs},
		{"jmespath_expression missing", map[string]any{"input_filename": "data.json"}, "jmespath_expression is required", invalidArgs},
		{"input artifact does not exist", map[string]any{"input_filename": "missing.json", "jmespath_expression": "a"}, "not found", taxonomy.ErrorCodeArtifactNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			saveDataToolArtifact(t, t.Context(), facade, "data.json", []byte(`{"a":1}`), "application/json")

			result, err := (&TransformDataWithJMESPathTool{}).Execute(t.Context(), tc.args, toolCtx)
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
