// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func newDataToolContext(t *testing.T) (*tool.Context, *tool.Facade) {
	t.Helper()
	svc := &artifact.ScopedService{
		Inner: memory.New(),
		Scope: "agent",
	}
	ctx := &tool.Context{
		ArtifactService: svc,
		AgentName:       "test-agent",
		UserID:          "test-user",
		SessionID:       "test-session",
	}
	facade := ctx.Facade()
	return ctx, facade
}

func saveDataToolArtifact(t *testing.T, ctx context.Context, facade *tool.Facade, filename string, content []byte, mime string) { //nolint:revive // t before ctx for test helper convention
	t.Helper()
	_, err := facade.SaveArtifact(ctx, filename, content, mime)
	if err != nil {
		t.Fatalf("failed to save test artifact: %v", err)
	}
}

func TestQueryDataWithSQL_BasicCSV(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	csvData := "name,age,city\nAlice,30,NYC\nBob,25,LA\nCharlie,35,NYC"
	saveDataToolArtifact(t, ctx, facade, "people.csv", []byte(csvData), "text/csv")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"people": "people.csv"},
		"sql_query":   "SELECT name, age FROM people WHERE city = 'NYC' ORDER BY name",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 2 {
		t.Errorf("expected 2 rows, got %v", result.Data["result_rows"])
	}
}

func TestQueryDataWithSQL_BlankCSVCellIsNull(t *testing.T) {
	ctx := context.Background()

	csvData := "name,score\nAlice,95\nBob,\nCarol,80"
	rows, err := parseCSVToRows([]byte(csvData))
	if err != nil {
		t.Fatalf("parseCSVToRows: %v", err)
	}
	if v := rows[1]["score"]; v != nil {
		t.Fatalf("blank cell parsed as %#v, want nil (SQL NULL)", v)
	}

	db, err := openInMemoryQueryDB()
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if createErr := createTableFromRows(ctx, db, "scores", rows, nil); createErr != nil {
		t.Fatalf("create table: %v", createErr)
	}

	got, _, err := executeQuery(ctx, db, "SELECT name FROM scores WHERE score > 50 ORDER BY name")
	if err != nil {
		t.Fatalf("WHERE query: %v", err)
	}
	gotNames := make([]string, len(got))
	for i, r := range got {
		gotNames[i] = fmt.Sprintf("%v", r["name"])
	}
	if want := []string{"Alice", "Carol"}; !slices.Equal(gotNames, want) {
		t.Errorf("WHERE score > 50 = %v, want %v (Bob's blank score must be excluded)", gotNames, want)
	}

	got, _, err = executeQuery(ctx, db, "SELECT AVG(score) AS avg FROM scores")
	if err != nil {
		t.Fatalf("AVG query: %v", err)
	}
	if avg := toFloat(t, got[0]["avg"]); avg != 87.5 {
		t.Errorf("AVG(score) = %v, want 87.5 (blank must be NULL, not 0)", avg)
	}

	got, _, err = executeQuery(ctx, db, "SELECT COUNT(*) AS c FROM scores WHERE score IS NULL")
	if err != nil {
		t.Fatalf("IS NULL query: %v", err)
	}
	if c := toFloat(t, got[0]["c"]); c != 1 {
		t.Errorf("COUNT(score IS NULL) = %v, want 1", c)
	}
	got, _, err = executeQuery(ctx, db, "SELECT COUNT(*) AS c FROM scores WHERE score = ''")
	if err != nil {
		t.Fatalf("= '' query: %v", err)
	}
	if c := toFloat(t, got[0]["c"]); c != 0 {
		t.Errorf("COUNT(score = '') = %v, want 0 (blank is NULL, not empty string)", c)
	}
}

func toFloat(t *testing.T, v any) float64 {
	t.Helper()
	switch n := v.(type) {
	case int64:
		return float64(n)
	case float64:
		return n
	default:
		t.Fatalf("unexpected numeric type %T (%v)", v, v)
		return 0
	}
}

func TestQueryDataWithSQL_QuotedTableName(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	jsonData := `[{"name":"Support Desk","key":"SUP"},{"name":"Billing","key":"BIL"}]`
	saveDataToolArtifact(t, ctx, facade, "project_list.json", []byte(jsonData), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{`"projects"`: "project_list.json"},
		"sql_query":   "SELECT name FROM projects WHERE key = 'SUP'",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 1 {
		t.Errorf("expected 1 row, got %v", result.Data["result_rows"])
	}
}

func TestNormalizeTableName(t *testing.T) {
	cases := map[string]string{
		`"projects"`: "projects",
		"`projects`": "projects",
		"'projects'": "projects",
		"[projects]": "projects",
		"projects":   "projects",
		`"projects`:  `"projects`,
		`projects"`:  `projects"`,
		`"`:          `"`,
		"":           "",
		`""`:         "",
		`"my table"`: "my table",
		`"a"b"`:      `a"b`,
		"[brackets'": "[brackets'",
	}
	for in, want := range cases {
		if got := normalizeTableName(in); got != want {
			t.Errorf("normalizeTableName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQueryDataWithSQL_JSON(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	jsonData := `[{"name":"Alice","score":90},{"name":"Bob","score":85},{"name":"Charlie","score":95}]`
	saveDataToolArtifact(t, ctx, facade, "scores.json", []byte(jsonData), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files":   map[string]any{"scores": "scores.json"},
		"sql_query":     "SELECT name, score FROM scores WHERE CAST(score AS INTEGER) > 88 ORDER BY name",
		"output_format": "json",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 2 {
		t.Errorf("expected 2 rows, got %v", result.Data["result_rows"])
	}
}

func TestQueryDataWithSQL_MultiTable(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	customers := "id,name\n1,Alice\n2,Bob"
	orders := `[{"customer_id":"1","amount":"100"},{"customer_id":"1","amount":"200"},{"customer_id":"2","amount":"50"}]`
	saveDataToolArtifact(t, ctx, facade, "customers.csv", []byte(customers), "text/csv")
	saveDataToolArtifact(t, ctx, facade, "orders.json", []byte(orders), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{
			"customers": "customers.csv",
			"orders":    "orders.json",
		},
		"sql_query": `
			WITH order_totals AS (
				SELECT customer_id, SUM(CAST(amount AS INTEGER)) as total
				FROM orders GROUP BY customer_id
			)
			SELECT c.name, COALESCE(o.total, 0) as total
			FROM customers c LEFT JOIN order_totals o ON c.id = o.customer_id
			ORDER BY c.name`,
		"output_format": "json",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 2 {
		t.Errorf("expected 2 rows, got %v", result.Data["result_rows"])
	}
}

func TestQueryDataWithSQL_JoinFanoutRejected(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "a.csv", []byte("id,val\n1,10"), "text/csv")
	saveDataToolArtifact(t, ctx, facade, "b.csv", []byte("id,amt\n1,20"), "text/csv")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"a": "a.csv", "b": "b.csv"},
		"sql_query":   "SELECT a.id, SUM(a.val) FROM a JOIN b ON a.id = b.id GROUP BY a.id",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error for fan-out risk, got %s", result.Status)
	}
	if !strings.Contains(result.Message, "JOIN") {
		t.Errorf("expected JOIN fan-out message, got: %s", result.Message)
	}
	if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
	}
}

func TestQueryDataWithSQL_FailedQueryListsColumns(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := t.Context()

	saveDataToolArtifact(t, ctx, facade, "orders.csv", []byte("order_id,amount,region\n1,10,N"), "text/csv")
	saveDataToolArtifact(t, ctx, facade, "targets.csv", []byte("region,target\nN,5"), "text/csv")
	saveDataToolArtifact(t, ctx, facade, "people.json", []byte(`[{"id":1,"address":{"city":"Ottawa"},"prénom":"Ada"}]`), "application/json")

	tests := []struct {
		name     string
		sqlQuery string
		want     []string
		wantNot  []string
	}{
		{
			name:     "misspelled column",
			sqlQuery: "SELECT amnt FROM orders",
			want:     []string{`Did you mean "amount" (table orders)?`, "orders(amount, order_id, region)", "targets(region, target)"},
			wantNot:  []string{"LIMIT 5"},
		},
		{
			name:     "qualified wrong case",
			sqlQuery: "SELECT t.Targt FROM targets t",
			want:     []string{`Did you mean "target" (table targets)?`},
		},
		{
			name:     "dotted loaded column",
			sqlQuery: "SELECT address.cty FROM people",
			want:     []string{`Did you mean "address.city" (table people)?`},
		},
		{
			name:     "non-ASCII column",
			sqlQuery: "SELECT prénm FROM people",
			want:     []string{`Did you mean "prénom" (table people)?`},
		},
		{
			name:     "name too short to suggest",
			sqlQuery: "SELECT idx FROM people",
			want:     []string{"people("},
			wantNot:  []string{"Did you mean"},
		},
		{
			name:     "table in the query wins",
			sqlQuery: "SELECT regon FROM targets",
			want:     []string{`Did you mean "region" (table targets)?`},
		},
		{
			name:     "unrelated column",
			sqlQuery: "SELECT zzzzzzzz FROM orders",
			want:     []string{"orders(amount, order_id, region)"},
			wantNot:  []string{"Did you mean"},
		},
		{
			name:     "unknown table",
			sqlQuery: "SELECT * FROM missing",
			want:     []string{"no such table", "orders(amount, order_id, region)"},
			wantNot:  []string{"Did you mean"},
		},
	}
	sqlTool := &QueryDataWithSQLTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := sqlTool.Execute(ctx, map[string]any{
				"input_files": map[string]any{"orders": "orders.csv", "targets": "targets.csv", "people": "people.json"},
				"sql_query":   tt.sqlQuery,
			}, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != "error" {
				t.Fatalf("status = %s: %s, want error", result.Status, result.Message)
			}
			if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
			}
			for _, want := range tt.want {
				if !strings.Contains(result.Message, want) {
					t.Errorf("message missing %q: %s", want, result.Message)
				}
			}
			for _, not := range tt.wantNot {
				if strings.Contains(result.Message, not) {
					t.Errorf("message contains %q: %s", not, result.Message)
				}
			}
		})
	}
}

func TestQueryDataWithSQL_FailedQueryHintIsBounded(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := t.Context()

	const columnCount = 90
	header := make([]string, columnCount)
	row := make([]string, columnCount)
	for i := range header {
		header[i] = fmt.Sprintf("col_%03d", i)
		row[i] = "1"
	}
	saveDataToolArtifact(t, ctx, facade, "wide.csv", []byte(strings.Join(header, ",")+"\n"+strings.Join(row, ",")), "text/csv")

	result, err := (&QueryDataWithSQLTool{}).Execute(ctx, map[string]any{
		"input_files": map[string]any{"wide": "wide.csv"},
		"sql_query":   "SELECT nothing_like_it FROM wide",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Message, "col_039, and 50 more)") {
		t.Errorf("message does not cap the columns at 40: %s", result.Message)
	}
	if strings.Contains(result.Message, "col_040") {
		t.Errorf("message lists a column past the cap: %s", result.Message)
	}
}

func TestQueryDataWithSQL_ShapeRefusalErrorCode(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := t.Context()

	saveDataToolArtifact(t, ctx, facade, "nested.json", []byte(`[{"id":1,"owner":{"address":{"city":"Ottawa"}}}]`), "application/json")
	saveDataToolArtifact(t, ctx, facade, "tagged.json", []byte(`[{"id":1,"tags":["a","b"]}]`), "application/json")
	saveDataToolArtifact(t, ctx, facade, "broken.json", []byte(`[{"id":1,`), "application/json")

	tests := []struct {
		name        string
		file        string
		wantMessage string
		wantCode    string
	}{
		{"nested object column", "nested.json", "contains a nested object", taxonomy.ToolFailureInvalidQuery},
		{"array column", "tagged.json", "contains an array", taxonomy.ToolFailureInvalidQuery},
		{"malformed JSON", "broken.json", "invalid JSON", ""},
	}
	sqlTool := &QueryDataWithSQLTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := sqlTool.Execute(ctx, map[string]any{
				"input_files": map[string]any{"data": tt.file},
				"sql_query":   "SELECT * FROM data",
			}, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != "error" {
				t.Fatalf("expected error, got %s: %s", result.Status, result.Message)
			}
			if !strings.Contains(result.Message, tt.wantMessage) {
				t.Errorf("expected %q in message, got: %s", tt.wantMessage, result.Message)
			}
			if result.ErrorCode != tt.wantCode {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, tt.wantCode)
			}
		})
	}
}

func TestQueryDataWithSQL_FailedQueryIsAnInvalidQuery(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := t.Context()
	saveDataToolArtifact(t, ctx, facade, "people.csv", []byte("name,age\nAda,36\n"), "text/csv")

	result, err := (&QueryDataWithSQLTool{}).Execute(ctx, map[string]any{
		"input_files": map[string]any{"people": "people.csv"},
		"sql_query":   "SELECT missing_column FROM people",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" || !strings.Contains(result.Message, "SQL query failed") {
		t.Fatalf("result = %s: %s, want the SQL failure", result.Status, result.Message)
	}
	if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
	}
}

func TestQueryDataWithSQL_InterruptedQueryIsNotAnInvalidQuery(t *testing.T) {
	const interruptAfter = 300 * time.Millisecond
	withDeadline := func(t *testing.T) context.Context {
		ctx, cancel := context.WithTimeout(t.Context(), interruptAfter)
		t.Cleanup(cancel)
		return ctx
	}
	canceledLater := func(t *testing.T) context.Context {
		ctx, cancel := context.WithCancel(t.Context())
		timer := time.AfterFunc(interruptAfter, cancel)
		t.Cleanup(func() {
			timer.Stop()
			cancel()
		})
		return ctx
	}

	cases := []struct {
		name     string
		ctx      func(*testing.T) context.Context
		wantCode string
	}{
		{"deadline", withDeadline, tool.DispatchErrorCode(taxonomy.DispatchClassTimeout)},
		{"canceled", canceledLater, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			saveDataToolArtifact(t, t.Context(), facade, "people.csv", []byte("name,age\nAda,36\n"), "text/csv")

			result, err := (&QueryDataWithSQLTool{}).Execute(tc.ctx(t), map[string]any{
				"input_files": map[string]any{"people": "people.csv"},
				"sql_query":   "WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM n) SELECT count(*) FROM n",
			}, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status != "error" || !strings.Contains(result.Message, "SQL query failed") {
				t.Fatalf("result = %s: %s, want the interrupted query", result.Status, result.Message)
			}
			if result.ErrorCode != tc.wantCode {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, tc.wantCode)
			}
		})
	}
}

func TestQueryDataWithSQL_YAML(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	yamlData := `- name: Alice
  age: 30
- name: Bob
  age: 25`
	saveDataToolArtifact(t, ctx, facade, "people.yaml", []byte(yamlData), "application/yaml")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"people": "people.yaml"},
		"sql_query":   "SELECT name FROM people ORDER BY name",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 2 {
		t.Errorf("expected 2 rows, got %v", result.Data["result_rows"])
	}
}

func TestQueryDataWithSQL_ColumnarJSON(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	columnar := `{"name":["Alice","Bob"],"age":[30,25]}`
	saveDataToolArtifact(t, ctx, facade, "data.json", []byte(columnar), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"data": "data.json"},
		"sql_query":   "SELECT * FROM data",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 2 {
		t.Errorf("expected 2 rows, got %v", result.Data["result_rows"])
	}
}

func TestDetectJoinAggregationRisk(t *testing.T) {
	tests := []struct {
		name  string
		query string
		risky bool
	}{
		{"no join", "SELECT SUM(x) FROM t GROUP BY y", false},
		{"no group by", "SELECT * FROM t1 JOIN t2 ON t1.id = t2.id", false},
		{"no aggregate", "SELECT * FROM t1 JOIN t2 ON t1.id = t2.id GROUP BY t1.id", false},
		{"flat risky", "SELECT t1.id, SUM(t1.val) FROM t1 JOIN t2 ON t1.id = t2.id GROUP BY t1.id", true},
		{"CTE safe", `WITH agg AS (SELECT id, SUM(val) as total FROM t1 GROUP BY id) SELECT * FROM agg JOIN t2 ON agg.id = t2.id`, false},
		{"DISTINCT safe", "SELECT t1.id, COUNT(DISTINCT t1.val) FROM t1 JOIN t2 ON t1.id = t2.id GROUP BY t1.id", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := detectJoinAggregationRisk(tt.query)
			if tt.risky && msg == "" {
				t.Error("expected risk detected, got none")
			}
			if !tt.risky && msg != "" {
				t.Errorf("expected no risk, got: %s", msg)
			}
		})
	}
}

func TestParseFilenameVersion(t *testing.T) {
	tests := []struct {
		input   string
		name    string
		version *int
	}{
		{"data.csv", "data.csv", nil},
		{"data.csv:2", "data.csv", intPtr(2)},
		{"data.csv:latest", "data.csv", nil},
		{"data.csv:notanum", "data.csv:notanum", nil},
		{"meeting 10:30.txt", "meeting 10:30.txt", nil},
		{"data.csv:3abc", "data.csv:3abc", nil},
		{"data.csv:-1", "data.csv:-1", nil},
	}

	for _, tt := range tests {
		name, ver := parseFilenameVersion(tt.input)
		if name != tt.name {
			t.Errorf("parseFilenameVersion(%q) name = %q, want %q", tt.input, name, tt.name)
		}
		if (ver == nil) != (tt.version == nil) {
			t.Errorf("parseFilenameVersion(%q) version nil mismatch", tt.input)
		} else if ver != nil && *ver != *tt.version {
			t.Errorf("parseFilenameVersion(%q) version = %d, want %d", tt.input, *ver, *tt.version)
		}
	}
}

func intPtr(i int) *int { return &i }

func TestParseCSVToRows(t *testing.T) {
	csvData := "a,b,c\n1,2,3\n4,5,6"
	rows, err := parseCSVToRows([]byte(csvData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0]["a"] != "1" {
		t.Errorf("expected '1', got %v", rows[0]["a"])
	}
}

func TestParseJSONToRows_List(t *testing.T) {
	data := `[{"x":1,"y":"a"},{"x":2,"y":"b"}]`
	rows, err := parseJSONToRows([]byte(data), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestParseJSONToRows_Columnar(t *testing.T) {
	data := `{"x":[1,2],"y":["a","b"]}`
	rows, err := parseJSONToRows([]byte(data), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestFlattenMap(t *testing.T) {
	m := map[string]any{
		"name":    "Alice",
		"address": map[string]any{"city": "NYC", "zip": "10001"},
	}
	flat := flattenMap(m)
	if flat["address.city"] != "NYC" {
		t.Errorf("expected NYC, got %v", flat["address.city"])
	}
	if flat["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", flat["name"])
	}
}

func TestConvertListToRows_InconsistentData(t *testing.T) {
	items := make([]any, 12)
	for i := range 12 {
		items[i] = map[string]any{
			"id":                                 float64(i + 1),
			"common":                             "value",
			fmt.Sprintf("field_%c", 'a'+rune(i)): float64((i + 1) * 100),
		}
	}
	_, err := convertListToRows(items, 100)
	if err == nil {
		t.Fatal("expected error for inconsistent data structure")
	}
	if !strings.Contains(err.Error(), "too inconsistent") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConvertListToRows_ConsistentData(t *testing.T) {
	items := []any{
		map[string]any{"name": "Alice", "age": float64(30)},
		map[string]any{"name": "Bob", "age": float64(40)},
	}
	rows, err := convertListToRows(items, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("expected 2 rows, got %d", len(rows))
	}
}

func TestQueryDataWithSQL_OutputFilename(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "t.csv", []byte("x\n1"), "text/csv")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files":     map[string]any{"t": "t.csv"},
		"sql_query":       "SELECT * FROM t",
		"output_filename": "my_result",
		"output_format":   "csv",
	}, toolCtx)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outFile, _ := result.Data["output_filename"].(string)
	if !strings.HasPrefix(outFile, "my_result") || !strings.HasSuffix(outFile, ".csv") {
		t.Errorf("unexpected output filename: %s", outFile)
	}
}

func TestQueryDataWithSQL_JSONOutput(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "t.csv", []byte("x,y\n1,a\n2,b"), "text/csv")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files":   map[string]any{"t": "t.csv"},
		"sql_query":     "SELECT * FROM t",
		"output_format": "json",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outFile := result.Data["output_filename"].(string)
	content, _, loadErr := facade.LoadArtifact(ctx, outFile, nil)
	if loadErr != nil {
		t.Fatalf("failed to load output: %v", loadErr)
	}
	var parsed []map[string]any
	if jsonErr := json.Unmarshal(content, &parsed); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v", jsonErr)
	}
	if len(parsed) != 2 {
		t.Errorf("expected 2 rows in JSON output, got %d", len(parsed))
	}
}

func buildTestSQLiteBytes(t *testing.T, tableName string, columns []string, rows [][]any) []byte {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "seed.sqlite")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed sqlite: %v", err)
	}
	colDefs := make([]string, len(columns))
	for i, c := range columns {
		colDefs[i] = fmt.Sprintf("%q NUMERIC", c)
	}
	create := fmt.Sprintf("CREATE TABLE %q (%s)", tableName, strings.Join(colDefs, ", "))
	if _, err := db.Exec(create); err != nil {
		_ = db.Close()
		t.Fatalf("create seed table: %v", err)
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(columns)), ",")
	insert := fmt.Sprintf("INSERT INTO %q VALUES (%s)", tableName, placeholders)
	for _, row := range rows {
		if _, err := db.Exec(insert, row...); err != nil {
			_ = db.Close()
			t.Fatalf("insert seed row: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
	b, readErr := os.ReadFile(dbPath)
	if readErr != nil {
		t.Fatalf("read seed db: %v", readErr)
	}
	return b
}

func TestQueryDataWithSQL_SQLiteInput_ByMIME(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	dbBytes := buildTestSQLiteBytes(t, "people", []string{"name", "age"}, [][]any{
		{"Alice", 30},
		{"Bob", 25},
		{"Charlie", 35},
	})
	saveDataToolArtifact(t, ctx, facade, "people.sqlite", dbBytes, sqliteDBMIMEType)

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files":   map[string]any{"users": "people.sqlite"},
		"sql_query":     "SELECT name FROM users WHERE CAST(age AS INTEGER) >= 30 ORDER BY name",
		"output_format": "json",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 2 {
		t.Errorf("expected 2 rows, got %v", result.Data["result_rows"])
	}

	outFile, _ := result.Data["output_filename"].(string)
	content, _, loadErr := facade.LoadArtifact(ctx, outFile, nil)
	if loadErr != nil {
		t.Fatalf("load output: %v", loadErr)
	}
	var parsed []map[string]any
	if jsonErr := json.Unmarshal(content, &parsed); jsonErr != nil {
		t.Fatalf("output not valid JSON: %v", jsonErr)
	}
	names := make([]string, 0, len(parsed))
	for _, row := range parsed {
		if n, ok := row["name"].(string); ok {
			names = append(names, n)
		}
	}
	want := []string{"Alice", "Charlie"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("expected names %v, got %v", want, names)
	}
}

func TestQueryDataWithSQL_SQLiteInput_ByHeaderFallback(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	dbBytes := buildTestSQLiteBytes(t, "events", []string{"id", "label"}, [][]any{
		{1, "alpha"},
		{2, "beta"},
	})
	saveDataToolArtifact(t, ctx, facade, "events.bin", dbBytes, "application/octet-stream")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"e": "events.bin"},
		"sql_query":   "SELECT COUNT(*) AS n FROM e",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 1 {
		t.Errorf("expected 1 row, got %v", result.Data["result_rows"])
	}
}

func TestQueryDataWithSQL_SQLiteInput_MixedWithCSV(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	dbBytes := buildTestSQLiteBytes(t, "customers", []string{"id", "name"}, [][]any{
		{1, "Alice"},
		{2, "Bob"},
	})
	saveDataToolArtifact(t, ctx, facade, "customers.sqlite", dbBytes, sqliteDBMIMEType)
	saveDataToolArtifact(t, ctx, facade, "orders.csv", []byte("customer_id,amount\n1,100\n1,50\n2,20"), "text/csv")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{
			"customers": "customers.sqlite",
			"orders":    "orders.csv",
		},
		"sql_query": `
			WITH order_totals AS (
				SELECT customer_id, SUM(CAST(amount AS INTEGER)) AS total
				FROM orders GROUP BY customer_id
			)
			SELECT c.name, COALESCE(o.total, 0) AS total
			FROM customers c LEFT JOIN order_totals o ON CAST(c.id AS INTEGER) = CAST(o.customer_id AS INTEGER)
			ORDER BY c.name`,
		"output_format": "json",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 2 {
		t.Errorf("expected 2 rows, got %v", result.Data["result_rows"])
	}
}

func TestQueryDataWithSQL_RoundTripFromCreateSQLiteDB(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	csvData := "name,score\nAlice,90\nBob,85\nCharlie,95"
	saveDataToolArtifact(t, ctx, facade, "scores.csv", []byte(csvData), "text/csv")
	useUnwritableTempDir(t)

	create := &CreateSQLiteDBTool{}
	createResult, err := create.Execute(ctx, map[string]any{
		"input_filename":     "scores.csv",
		"output_db_filename": "scores_db",
	}, toolCtx)
	if err != nil {
		t.Fatalf("create_sqlite_db unexpected error: %v", err)
	}
	if createResult.Status != "success" {
		t.Fatalf("create_sqlite_db failed: %s", createResult.Message)
	}
	dbFilename, _ := createResult.Data["output_filename"].(string)
	if dbFilename != "scores_db.sqlite" {
		t.Fatalf("unexpected db filename: %s", dbFilename)
	}

	sqlTool := &QueryDataWithSQLTool{}
	queryResult, err := sqlTool.Execute(ctx, map[string]any{
		"input_files":   map[string]any{"scores": dbFilename},
		"sql_query":     "SELECT name FROM scores WHERE CAST(score AS INTEGER) > 88 ORDER BY name",
		"output_format": "json",
	}, toolCtx)
	if err != nil {
		t.Fatalf("query_data_with_sql unexpected error: %v", err)
	}
	if queryResult.Status != "success" {
		t.Fatalf("query_data_with_sql failed: %s", queryResult.Message)
	}
	if queryResult.Data["result_rows"] != 2 {
		t.Errorf("expected 2 rows, got %v", queryResult.Data["result_rows"])
	}
}

func TestQueryDataWithSQL_SQLiteInput_NoTables(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "empty.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open empty db: %v", err)
	}
	if _, execErr := db.Exec("PRAGMA user_version = 1"); execErr != nil {
		_ = db.Close()
		t.Fatalf("pragma: %v", execErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close empty db: %v", closeErr)
	}
	dbBytes, readErr := os.ReadFile(dbPath)
	if readErr != nil {
		t.Fatalf("read empty db: %v", readErr)
	}
	saveDataToolArtifact(t, ctx, facade, "empty.sqlite", dbBytes, sqliteDBMIMEType)

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"x": "empty.sqlite"},
		"sql_query":   "SELECT * FROM x",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "no tables") {
		t.Errorf("expected 'no tables' error, got: %s", result.Message)
	}
}

func TestLoadRowsFromSQLiteArtifact_WithoutWritableTempDir(t *testing.T) {
	dbBytes := buildTestSQLiteBytes(t, "events", []string{"id", "label"}, [][]any{
		{1, "alpha"},
		{2, "beta"},
	})
	useUnwritableTempDir(t)

	rows, srcTable, err := loadRowsFromSQLiteArtifact(t.Context(), dbBytes)
	if err != nil {
		t.Fatalf("loadRowsFromSQLiteArtifact: %v", err)
	}
	if srcTable != "events" {
		t.Errorf("source table = %q, want %q", srcTable, "events")
	}
	if len(rows) != 2 || rows[0]["label"] != "alpha" || rows[1]["label"] != "beta" {
		t.Errorf("unexpected rows: %v", rows)
	}
}

func TestLoadRowsFromSQLiteArtifact_WALModeDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wal.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open wal db: %v", err)
	}
	for _, stmt := range []string{
		"PRAGMA journal_mode=WAL",
		"CREATE TABLE readings (sensor NUMERIC, value NUMERIC)",
		"INSERT INTO readings VALUES ('a', 1), ('b', 2)",
	} {
		if _, execErr := db.Exec(stmt); execErr != nil {
			_ = db.Close()
			t.Fatalf("%s: %v", stmt, execErr)
		}
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatalf("close wal db: %v", closeErr)
	}
	dbBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read wal db: %v", err)
	}
	if dbBytes[sqliteWriteVersionOffset] != sqliteWALFmt {
		t.Fatalf("fixture is not in WAL mode (write version %d)", dbBytes[sqliteWriteVersionOffset])
	}
	original := slices.Clone(dbBytes)

	rows, srcTable, err := loadRowsFromSQLiteArtifact(t.Context(), dbBytes)
	if err != nil {
		t.Fatalf("loadRowsFromSQLiteArtifact: %v", err)
	}
	if srcTable != "readings" || len(rows) != 2 {
		t.Errorf("got table %q with rows %v, want readings with 2 rows", srcTable, rows)
	}
	if !bytes.Equal(dbBytes, original) {
		t.Error("loadRowsFromSQLiteArtifact mutated the caller's artifact bytes")
	}
}

func TestLoadRowsFromSQLiteArtifact_RejectsNonSQLiteBytes(t *testing.T) {
	cases := map[string][]byte{
		"empty":              {},
		"garbage":            []byte("this is definitely not a sqlite database, just some text"),
		"truncated header":   []byte("SQLite format 3\x00"),
		"header then random": append([]byte("SQLite format 3\x00"), bytes.Repeat([]byte{0xAB}, 4096)...),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			rows, _, err := loadRowsFromSQLiteArtifact(t.Context(), content)
			if err == nil {
				t.Fatalf("expected an error for %s content, got rows %v", name, rows)
			}
			if len(content) == 0 && !strings.Contains(err.Error(), "SQLite artifact is empty") {
				t.Errorf("empty content error = %q, want the clear empty-artifact message", err)
			}
		})
	}
}

func TestWithRollbackJournalHeader_RewritesOnlyWALHeaders(t *testing.T) {
	header := func(write, read byte) []byte {
		h := make([]byte, 100)
		copy(h, "SQLite format 3\x00")
		h[sqliteWriteVersionOffset], h[sqliteReadVersionOffset] = write, read
		return h
	}
	cases := []struct {
		name       string
		content    []byte
		wantWrite  byte
		wantRead   byte
		wantCopied bool
	}{
		{"wal", header(2, 2), 1, 1, true},
		{"rollback journal", header(1, 1), 1, 1, false},
		{"newer read format", header(2, 3), 2, 3, false},
		{"wal read, rollback write", header(1, 2), 1, 1, true},
		{"newer write format, wal read", header(3, 2), 3, 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.content)
			got := withRollbackJournalHeader(tc.content)
			if got[sqliteWriteVersionOffset] != tc.wantWrite || got[sqliteReadVersionOffset] != tc.wantRead {
				t.Errorf("versions = %d/%d, want %d/%d", got[sqliteWriteVersionOffset], got[sqliteReadVersionOffset], tc.wantWrite, tc.wantRead)
			}
			if copied := &got[0] != &tc.content[0]; copied != tc.wantCopied {
				t.Errorf("returned a copy = %v, want %v", copied, tc.wantCopied)
			}
			if !bytes.Equal(tc.content, original) {
				t.Error("the caller's bytes were mutated")
			}
		})
	}
	if short := []byte("SQLite"); &withRollbackJournalHeader(short)[0] != &short[0] {
		t.Error("content shorter than the header must be returned unchanged")
	}
}

func TestLoadRowsFromSQLiteArtifact_HeaderVersions(t *testing.T) {
	image := buildTestSQLiteBytes(t, "events", []string{"id"}, [][]any{{1}, {2}})
	withVersions := func(write, read byte) []byte {
		b := slices.Clone(image)
		b[sqliteWriteVersionOffset], b[sqliteReadVersionOffset] = write, read
		return b
	}

	for name, content := range map[string][]byte{
		"wal read, rollback write":     withVersions(1, 2),
		"newer write format, wal read": withVersions(3, 2),
	} {
		t.Run(name, func(t *testing.T) {
			rows, _, err := loadRowsFromSQLiteArtifact(t.Context(), content)
			if err != nil {
				t.Fatalf("loadRowsFromSQLiteArtifact: %v", err)
			}
			if len(rows) != 2 {
				t.Errorf("got %d rows, want 2", len(rows))
			}
		})
	}

	t.Run("newer read format is refused", func(t *testing.T) {
		if rows, _, err := loadRowsFromSQLiteArtifact(t.Context(), withVersions(2, 3)); err == nil {
			t.Fatalf("a read format newer than SQLite supports was accepted: %v", rows)
		}
	})
}

func TestWithSQLiteSerializer_HonorsCanceledContext(t *testing.T) {
	db, err := openInMemoryQueryDB()
	if err != nil {
		t.Fatalf("openInMemoryQueryDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	ran := false
	err = withSQLiteSerializer(ctx, db, func(sqliteSerializer) error {
		ran = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if ran {
		t.Error("the serializer callback ran despite a canceled context")
	}
}

func TestIsSQLiteArtifact(t *testing.T) {
	header := append([]byte("SQLite format 3\x00"), 0x00, 0x10)
	cases := []struct {
		name     string
		mime     string
		filename string
		content  []byte
		want     bool
	}{
		{"mime sqlite3", sqliteDBMIMEType, "foo.bin", nil, true},
		{"mime x-sqlite3", "application/x-sqlite3", "foo.bin", nil, true},
		{"header", "", "foo.bin", header, true},
		{"extension .sqlite", "", "foo.sqlite", nil, true},
		{"extension .db", "", "foo.DB", nil, true},
		{"plain csv", "text/csv", "foo.csv", []byte("a,b\n1,2"), false},
		{"short content", "", "foo.bin", []byte("SQLite"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isSQLiteArtifact(c.mime, c.filename, c.content)
			if got != c.want {
				t.Errorf("isSQLiteArtifact(%q, %q) = %v, want %v", c.mime, c.filename, got, c.want)
			}
		})
	}
}

func TestQueryDataWithSQL_DropRejectedByQueryOnly(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "people.csv", []byte("name,age\nAlice,30"), "text/csv")

	cases := []string{
		"DROP TABLE people",
		"INSERT INTO people (name, age) VALUES ('Mallory', 99)",
		"UPDATE people SET age = 0",
		"DELETE FROM people",
	}
	sqlTool := &QueryDataWithSQLTool{}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			result, err := sqlTool.Execute(ctx, map[string]any{
				"input_files": map[string]any{"people": "people.csv"},
				"sql_query":   q,
			}, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Status == "success" {
				t.Fatalf("expected error for %q, got success", q)
			}
		})
	}
}

func TestQuoteSQLiteIdent(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "users", want: `"users"`},
		{in: "my-table", want: `"my-table"`},
		{in: "with space", want: `"with space"`},
		{in: `my"table`, want: `"my""table"`},
		{in: `"quoted"`, want: `"""quoted"""`},
		{in: "", wantErr: true},
		{in: "null\x00byte", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, err := quoteSQLiteIdent(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("quoteSQLiteIdent(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestQueryDataWithSQL_IdentifierWithQuoteInTable(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "data.csv", []byte("x\n1"), "text/csv")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{`quo"ted`: "data.csv"},
		"sql_query":   `SELECT * FROM "quo""ted"`,
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success with properly-quoted identifier, got %s: %s", result.Status, result.Message)
	}
}

func TestFormatResultAsJSON_NullEmitsJSONNull(t *testing.T) {
	columns := []string{"id", "name", "value"}
	rows := []map[string]any{
		{"id": int64(1), "name": "ok", "value": int64(42)},
		{"id": int64(2), "name": nil, "value": nil},
	}

	out, err := formatResultAsJSON(rows, columns)
	if err != nil {
		t.Fatalf("formatResultAsJSON error: %v", err)
	}

	var decoded []map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw=%s", err, out)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(decoded))
	}
	if v, ok := decoded[1]["name"]; !ok || v != nil {
		t.Errorf("row[1].name = %v (present=%v), want JSON null decoded to Go nil", v, ok)
	}
	if v, ok := decoded[1]["value"]; !ok || v != nil {
		t.Errorf("row[1].value = %v (present=%v), want JSON null decoded to Go nil", v, ok)
	}
	if strings.Contains(string(out), "NaN") {
		t.Errorf("output contains NaN (non-JSON): %s", out)
	}
	if strings.Contains(string(out), `"null"`) {
		t.Errorf("output encodes NULL as string %q instead of bare null: %s", `"null"`, out)
	}
}

func TestQueryDataWithSQL_MemoryConnectionPoolShared(t *testing.T) {
	ctx := context.Background()

	db, err := openInMemoryQueryDB()
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows := []map[string]any{{"id": float64(1)}, {"id": float64(2)}, {"id": float64(3)}}
	if createErr := createTableFromRows(ctx, db, "people", rows, nil); createErr != nil {
		t.Fatalf("create table: %v", createErr)
	}

	const concurrency = 100
	var wg sync.WaitGroup
	errs := make([]error, concurrency)
	for i := range concurrency {
		wg.Go(func() {
			got, _, queryErr := executeQuery(ctx, db, "SELECT id FROM people")
			if queryErr != nil {
				errs[i] = queryErr
				return
			}
			if len(got) != len(rows) {
				errs[i] = fmt.Errorf("got %d rows, want %d", len(got), len(rows))
			}
		})
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("concurrent query %d failed: %v (a fresh pooled connection saw an empty in-memory DB)", i, e)
		}
	}
	if open := db.Stats().MaxOpenConnections; open > 1 {
		t.Fatalf("expected the pool capped at 1 connection, saw %d", open)
	}
}

func TestQueryDataWithSQL_TransformEnvelopeExtraction(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	envelope := `{"isLast":true,"nextPageToken":null,"issues":[{"key":"AAA-1","summary":"first"},{"key":"AAA-2","summary":"second"}]}`
	saveDataToolArtifact(t, ctx, facade, "page1.json", []byte(envelope), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files":         map[string]any{"issues": "page1.json"},
		"jmespath_transforms": map[string]any{"issues": "issues"},
		"sql_query":           "SELECT key, summary FROM issues ORDER BY key",
		"output_format":       "json",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 2 {
		t.Fatalf("expected 2 rows, got %v", result.Data["result_rows"])
	}

	keys := queryResultColumn(t, ctx, facade, result, "key")
	if strings.Join(keys, ",") != "AAA-1,AAA-2" {
		t.Errorf("expected keys [AAA-1 AAA-2], got %v", keys)
	}
}

func TestQueryDataWithSQL_TransformSelectRenameFlatten(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	envelope := `{"issues":[
		{"key":"AAA-1","fields":{"status":{"name":"Open"}}},
		{"key":"AAA-2","fields":{"status":{"name":"Done"}}}
	]}`
	saveDataToolArtifact(t, ctx, facade, "jira.json", []byte(envelope), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files":         map[string]any{"issues": "jira.json"},
		"jmespath_transforms": map[string]any{"issues": "issues[].{key: key, status: fields.status.name}"},
		"sql_query":           "SELECT key, status FROM issues WHERE status = 'Done'",
		"output_format":       "json",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 1 {
		t.Fatalf("expected 1 row, got %v", result.Data["result_rows"])
	}
	statuses := queryResultColumn(t, ctx, facade, result, "status")
	if strings.Join(statuses, ",") != "Done" {
		t.Errorf("expected status [Done], got %v", statuses)
	}
}

func TestQueryDataWithSQL_TransformMultiTableUnion(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	envelope := `{"issues":[{"key":"X-1","points":3},{"key":"X-2","points":5}]}`
	saveDataToolArtifact(t, ctx, facade, "page.json", []byte(envelope), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{
			"p1": "page.json",
			"p2": "page.json",
			"p3": "page.json",
		},
		"jmespath_transforms": map[string]any{
			"p1": "issues",
			"p2": "issues",
			"p3": "issues",
		},
		"sql_query": `
			WITH merged AS (
				SELECT key, points FROM p1
				UNION ALL SELECT key, points FROM p2
				UNION ALL SELECT key, points FROM p3
			)
			SELECT key, SUM(points) AS total FROM merged GROUP BY key ORDER BY key`,
		"output_format": "json",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if result.Data["result_rows"] != 2 {
		t.Fatalf("expected 2 rows, got %v (message: %s)", result.Data["result_rows"], result.Message)
	}
	parsed := loadJSONResult(t, ctx, facade, result)
	for _, row := range parsed {
		key, _ := row["key"].(string)
		total, _ := row["total"].(float64)
		want := map[string]float64{"X-1": 9, "X-2": 15}
		if total != want[key] {
			t.Errorf("key %s total = %v, want %v", key, total, want[key])
		}
	}
}

func TestQueryDataWithSQL_TransformGuardsStillFire(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	items := make([]string, 0, 12)
	for i := range 12 {
		items = append(items, fmt.Sprintf(`{"id":%d,"common":"v","field_%c":%d}`, i, 'a'+rune(i), i*100))
	}
	envelope := `{"records":[` + strings.Join(items, ",") + `]}`
	saveDataToolArtifact(t, ctx, facade, "messy.json", []byte(envelope), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files":         map[string]any{"messy": "messy.json"},
		"jmespath_transforms": map[string]any{"messy": "records"},
		"sql_query":           "SELECT * FROM messy",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected guard error, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "too inconsistent") {
		t.Errorf("expected inconsistency guard error, got: %s", result.Message)
	}
	if !strings.Contains(result.Message, `table "messy"`) {
		t.Errorf("expected error attributed to table \"messy\", got: %s", result.Message)
	}
	if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
		t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
	}
}

func TestQueryDataWithSQL_TransformErrorAttribution(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "page1.json", []byte(`{"issues":[{"key":"A"}]}`), "application/json")
	dbBytes := buildTestSQLiteBytes(t, "people", []string{"name"}, [][]any{{"Alice"}})
	saveDataToolArtifact(t, ctx, facade, "people.sqlite", dbBytes, sqliteDBMIMEType)

	sqlTool := &QueryDataWithSQLTool{}

	t.Run("invalid jmespath", func(t *testing.T) {
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"page1": "page1.json"},
			"jmespath_transforms": map[string]any{"page1": "issues[.{"},
			"sql_query":           "SELECT * FROM page1",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "error" {
			t.Fatalf("expected error, got %s", result.Status)
		}
		if !strings.Contains(result.Message, `jmespath_transforms for table "page1" is invalid`) {
			t.Errorf("expected table-attributed invalid-expression error, got: %s", result.Message)
		}
		if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
			t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
		}
	})

	t.Run("key not in input_files", func(t *testing.T) {
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"page1": "page1.json"},
			"jmespath_transforms": map[string]any{"nope": "issues"},
			"sql_query":           "SELECT * FROM page1",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "error" {
			t.Fatalf("expected error, got %s", result.Status)
		}
		if !strings.Contains(result.Message, `references table "nope"`) {
			t.Errorf("expected validation error naming the unknown table, got: %s", result.Message)
		}
	})

	t.Run("transform on sqlite artifact", func(t *testing.T) {
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"people": "people.sqlite"},
			"jmespath_transforms": map[string]any{"people": "[]"},
			"sql_query":           "SELECT * FROM people",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "error" {
			t.Fatalf("expected error, got %s", result.Status)
		}
		if !strings.Contains(result.Message, "SQLite database") {
			t.Errorf("expected clear SQLite-rejection error, got: %s", result.Message)
		}
		if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
			t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
		}
	})

	t.Run("jmespath_transforms not an object", func(t *testing.T) {
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"page1": "page1.json"},
			"jmespath_transforms": "issues",
			"sql_query":           "SELECT * FROM page1",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "error" || !strings.Contains(result.Message, "must be an object") {
			t.Errorf("expected object-type error, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("transform value not a string", func(t *testing.T) {
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"page1": "page1.json"},
			"jmespath_transforms": map[string]any{"page1": 123},
			"sql_query":           "SELECT * FROM page1",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "error" || !strings.Contains(result.Message, `table "page1" must be a JMESPath expression string`) {
			t.Errorf("expected non-string-value error, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("expression too long", func(t *testing.T) {
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"page1": "page1.json"},
			"jmespath_transforms": map[string]any{"page1": strings.Repeat("a", maxJMESPathExpressionLen+1)},
			"sql_query":           "SELECT * FROM page1",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "error" || !strings.Contains(result.Message, "too long") {
			t.Errorf("expected too-long error, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("expression too deeply nested", func(t *testing.T) {
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"page1": "page1.json"},
			"jmespath_transforms": map[string]any{"page1": strings.Repeat("[", maxJMESPathExpressionDepth+1)},
			"sql_query":           "SELECT * FROM page1",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "error" || !strings.Contains(result.Message, "too deeply nested") {
			t.Errorf("expected too-deep error, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("malformed json before transform", func(t *testing.T) {
		saveDataToolArtifact(t, ctx, facade, "broken.json", []byte(`{"issues": [`), "application/json")
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"broken": "broken.json"},
			"jmespath_transforms": map[string]any{"broken": "issues"},
			"sql_query":           "SELECT * FROM broken",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "error" || !strings.Contains(result.Message, `parse table "broken" before applying jmespath_transforms`) {
			t.Errorf("expected parse-attributed error, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("malformed yaml before transform", func(t *testing.T) {
		saveDataToolArtifact(t, ctx, facade, "broken.yaml", []byte("rows: [1, 2"), "application/yaml")
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"broken": "broken.yaml"},
			"jmespath_transforms": map[string]any{"broken": "rows"},
			"sql_query":           "SELECT * FROM broken",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "error" || !strings.Contains(result.Message, `parse table "broken"`) {
			t.Errorf("expected parse-attributed error, got %s: %s", result.Status, result.Message)
		}
	})

	t.Run("artifact not found", func(t *testing.T) {
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files": map[string]any{"missing": "does_not_exist.json"},
			"sql_query":   "SELECT * FROM missing",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "error" {
			t.Errorf("expected error for missing artifact, got %s: %s", result.Status, result.Message)
		}
	})
}

func TestQueryDataWithSQL_TransformCSVAndYAML(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "people.csv", []byte("name,age\nAlice,30\nBob,25\nCarol,40"), "text/csv")
	yamlEnvelope := "result:\n  rows:\n    - id: 1\n      city: NYC\n    - id: 2\n      city: LA\n"
	saveDataToolArtifact(t, ctx, facade, "data.yaml", []byte(yamlEnvelope), "application/yaml")

	sqlTool := &QueryDataWithSQLTool{}

	t.Run("csv filter+project transform", func(t *testing.T) {
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"people": "people.csv"},
			"jmespath_transforms": map[string]any{"people": "[?name != 'Bob'].{name: name}"},
			"sql_query":           "SELECT name FROM people ORDER BY name",
			"output_format":       "json",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "success" {
			t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
		}
		names := queryResultColumn(t, ctx, facade, result, "name")
		if strings.Join(names, ",") != "Alice,Carol" {
			t.Errorf("expected [Alice Carol], got %v", names)
		}
	})

	t.Run("yaml envelope extraction", func(t *testing.T) {
		result, err := sqlTool.Execute(ctx, map[string]any{
			"input_files":         map[string]any{"rows": "data.yaml"},
			"jmespath_transforms": map[string]any{"rows": "result.rows"},
			"sql_query":           "SELECT id, city FROM rows ORDER BY id",
			"output_format":       "json",
		}, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "success" {
			t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
		}
		if result.Data["result_rows"] != 2 {
			t.Errorf("expected 2 rows, got %v", result.Data["result_rows"])
		}
	})
}

func TestQueryDataWithSQL_TransformBackwardCompatible(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	jsonData := `[{"name":"Alice","score":90},{"name":"Bob","score":85}]`
	saveDataToolArtifact(t, ctx, facade, "scores.json", []byte(jsonData), "application/json")

	sqlTool := &QueryDataWithSQLTool{}
	for _, args := range []map[string]any{
		{
			"input_files": map[string]any{"scores": "scores.json"},
			"sql_query":   "SELECT name FROM scores ORDER BY name",
		},
		{
			"input_files":         map[string]any{"scores": "scores.json"},
			"jmespath_transforms": map[string]any{},
			"sql_query":           "SELECT name FROM scores ORDER BY name",
		},
	} {
		result, err := sqlTool.Execute(ctx, args, toolCtx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "success" {
			t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
		}
		if result.Data["result_rows"] != 2 {
			t.Errorf("expected 2 rows, got %v", result.Data["result_rows"])
		}
	}
}

func loadJSONResult(t *testing.T, ctx context.Context, facade *tool.Facade, result *tool.Result) []map[string]any { //nolint:revive // t before ctx for test helper convention
	t.Helper()
	outFile, _ := result.Data["output_filename"].(string)
	content, _, loadErr := facade.LoadArtifact(ctx, outFile, nil)
	if loadErr != nil {
		t.Fatalf("load output: %v", loadErr)
	}
	var parsed []map[string]any
	if jsonErr := json.Unmarshal(content, &parsed); jsonErr != nil {
		t.Fatalf("output not valid JSON: %v", jsonErr)
	}
	return parsed
}

func queryResultColumn(t *testing.T, ctx context.Context, facade *tool.Facade, result *tool.Result, column string) []string { //nolint:revive // t before ctx for test helper convention
	t.Helper()
	parsed := loadJSONResult(t, ctx, facade, result)
	out := make([]string, 0, len(parsed))
	for _, row := range parsed {
		out = append(out, fmt.Sprintf("%v", row[column]))
	}
	return out
}

func TestQueryDataWithSQL_ManyTablesUnion(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	const numTables = 8
	const rowsPerTable = 3
	inputFiles := make(map[string]any, numTables)
	selects := make([]string, 0, numTables)
	for i := range numTables {
		table := fmt.Sprintf("p%d", i)
		filename := fmt.Sprintf("page_%d.json", i)
		rows := make([]string, 0, rowsPerTable)
		for r := range rowsPerTable {
			rows = append(rows, fmt.Sprintf(`{"id":%d,"page":%d}`, i*rowsPerTable+r, i))
		}
		saveDataToolArtifact(t, ctx, facade, filename, []byte("["+strings.Join(rows, ",")+"]"), "application/json")
		inputFiles[table] = filename
		selects = append(selects, fmt.Sprintf("SELECT id, page FROM %s", table))
	}
	query := strings.Join(selects, "\nUNION ALL\n")

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files":   inputFiles,
		"sql_query":     query,
		"output_format": "json",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if got := result.Data["result_rows"]; got != numTables*rowsPerTable {
		t.Fatalf("expected %d rows, got %v (message: %s)", numTables*rowsPerTable, got, result.Message)
	}
}

func TestStreamQueryResult_StreamsAndCaps(t *testing.T) {
	ctx := t.Context()
	db, err := openInMemoryQueryDB()
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows := make([]map[string]any, 0, 500)
	for i := range 500 {
		rows = append(rows, map[string]any{"id": int64(i), "name": fmt.Sprintf("name-%d", i)})
	}
	if createErr := createTableFromRows(ctx, db, "t", rows, nil); createErr != nil {
		t.Fatalf("create table: %v", createErr)
	}
	const crossJoin = "SELECT a.id, b.name FROM t a CROSS JOIN t b"

	out, preview, rowCount, truncated, _, err := streamQueryResult(ctx, db, crossJoin, "csv", 256<<20, 5, 2048)
	if err != nil {
		t.Fatalf("streamQueryResult: %v", err)
	}
	if rowCount != 250000 {
		t.Errorf("rowCount = %d, want 250000", rowCount)
	}
	if !truncated {
		t.Errorf("truncated = false, want true (250k rows exceed the 5-row preview)")
	}
	if len(out) == 0 {
		t.Errorf("output is empty")
	}
	if pv, ok := preview.([]map[string]any); !ok || len(pv) != 5 {
		t.Errorf("preview = %T len mismatch, want []map[string]any of len 5", preview)
	}

	if _, _, _, _, _, capErr := streamQueryResult(ctx, db, crossJoin, "csv", 1024, 5, 2048); !errors.Is(capErr, errResultTooLarge) {
		t.Fatalf("cap error = %v, want errResultTooLarge", capErr)
	}

	jsonOut, _, jsonRows, _, _, jsonErr := streamQueryResult(ctx, db, "SELECT id, name FROM t ORDER BY id LIMIT 3", "json", 256<<20, 5, 2048)
	if jsonErr != nil {
		t.Fatalf("streamQueryResult json: %v", jsonErr)
	}
	if jsonRows != 3 {
		t.Errorf("json rowCount = %d, want 3", jsonRows)
	}
	var parsed []map[string]any
	if unmarshalErr := json.Unmarshal(jsonOut, &parsed); unmarshalErr != nil {
		t.Fatalf("streamed JSON is not valid: %v\n%s", unmarshalErr, jsonOut)
	}
	if len(parsed) != 3 {
		t.Errorf("parsed JSON objects = %d, want 3", len(parsed))
	}

	mixedOut, _, _, _, _, mixedErr := streamQueryResult(ctx, db, "SELECT NULL AS n, x'4869' AS b", "csv", 256<<20, 5, 2048)
	if mixedErr != nil {
		t.Fatalf("streamQueryResult null/blob: %v", mixedErr)
	}
	if got := string(mixedOut); !strings.Contains(got, "n,b\n") || !strings.Contains(got, ",Hi") {
		t.Errorf("null/blob CSV = %q, want header n,b and a row ,Hi", got)
	}
}

func TestQueryStreaming_CancelledContext(t *testing.T) {
	db, err := openInMemoryQueryDB()
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if createErr := createTableFromRows(t.Context(), db, "t", []map[string]any{{"id": int64(1)}}, nil); createErr != nil {
		t.Fatalf("create table: %v", createErr)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, _, _, _, _, streamErr := streamQueryResult(ctx, db, "SELECT id FROM t", "csv", 256<<20, 5, 2048); streamErr == nil {
		t.Error("streamQueryResult with cancelled context: want error, got nil")
	}
	if _, _, execErr := executeQuery(ctx, db, "SELECT id FROM t"); execErr == nil {
		t.Error("executeQuery with cancelled context: want error, got nil")
	}
}

func TestQueryDataWithSQL_ResultTooLarge(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := t.Context()

	csvData := "name,age\nAlice,30\nBob,25\nCarol,40\nDave,35"
	saveDataToolArtifact(t, ctx, facade, "people.csv", []byte(csvData), "text/csv")
	toolCtx.ToolConfig = map[string]any{"max_result_bytes": 8}

	sqlTool := &QueryDataWithSQLTool{}
	result, err := sqlTool.Execute(ctx, map[string]any{
		"input_files": map[string]any{"people": "people.csv"},
		"sql_query":   "SELECT name, age FROM people",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("status = %q, want error (result over the byte ceiling)", result.Status)
	}
	if !strings.Contains(result.Message, "LIMIT") {
		t.Errorf("error message = %q, want actionable guidance mentioning LIMIT", result.Message)
	}
	if result.ErrorCode != taxonomy.ToolFailureInvalidQuery {
		t.Errorf("error code = %q, want %q", result.ErrorCode, taxonomy.ToolFailureInvalidQuery)
	}
}

func TestOpenInMemoryQueryDB_KeepsTempStorageInMemory(t *testing.T) {
	db, err := openInMemoryQueryDB()
	if err != nil {
		t.Fatalf("openInMemoryQueryDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const tempStoreMemory = 2
	var tempStore int
	if err := db.QueryRowContext(t.Context(), "PRAGMA temp_store").Scan(&tempStore); err != nil {
		t.Fatalf("read temp_store: %v", err)
	}
	if tempStore != tempStoreMemory {
		t.Errorf("temp_store = %d, want %d (MEMORY)", tempStore, tempStoreMemory)
	}
}

func TestQueryDataWithSQL_RefusedCallCodes(t *testing.T) {
	invalidArgs := tool.DispatchErrorCode(taxonomy.DispatchClassValidation)
	tests := []struct {
		name        string
		args        map[string]any
		wantMessage string
		wantCode    string
	}{
		{"input_files missing", map[string]any{"sql_query": "SELECT 1"}, "input_files is required", invalidArgs},
		{"sql_query missing", map[string]any{"input_files": map[string]any{"people": "people.csv"}}, "sql_query is required", invalidArgs},
		{"unsupported output_format", map[string]any{"input_files": map[string]any{"people": "people.csv"}, "sql_query": "SELECT 1", "output_format": "xml"}, "output_format must be", invalidArgs},
		{"transform for a table not in input_files", map[string]any{"input_files": map[string]any{"people": "people.csv"}, "sql_query": "SELECT 1", "jmespath_transforms": map[string]any{"other": "a"}}, "not present in input_files", invalidArgs},
		{"empty filename for a table", map[string]any{"input_files": map[string]any{"people": ""}, "sql_query": "SELECT 1"}, "Empty filename", invalidArgs},
		{"input artifact does not exist", map[string]any{"input_files": map[string]any{"people": "missing.csv"}, "sql_query": "SELECT 1"}, "not found", taxonomy.ErrorCodeArtifactNotFound},
		{"input has a header and no rows", map[string]any{"input_files": map[string]any{"people": "header_only.csv"}, "sql_query": "SELECT 1"}, "No data rows found", ""},
		{"transform leaves no rows", map[string]any{"input_files": map[string]any{"people": "people.json"}, "sql_query": "SELECT 1", "jmespath_transforms": map[string]any{"people": "[?age > `100`]"}}, "No data rows found", taxonomy.ToolFailureInvalidQuery},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			saveDataToolArtifact(t, t.Context(), facade, "people.csv", []byte("name,age\nAda,36\n"), "text/csv")
			saveDataToolArtifact(t, t.Context(), facade, "header_only.csv", []byte("name,age\n"), "text/csv")
			saveDataToolArtifact(t, t.Context(), facade, "people.json", []byte(`[{"name":"Ada","age":36}]`), "application/json")

			result, err := (&QueryDataWithSQLTool{}).Execute(t.Context(), tc.args, toolCtx)
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
