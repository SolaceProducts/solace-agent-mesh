// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqltool

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func selectRows(t *testing.T, res *sdk.Result) []map[string]any {
	t.Helper()
	if len(res.DataObjects) != 1 {
		t.Fatalf("DataObjects len = %d, want 1", len(res.DataObjects))
	}
	do := res.DataObjects[0]
	if do.Disposition != sdk.DispositionInline || do.MIMEType != "application/json" {
		t.Fatalf("unexpected DataObject: disposition=%q mime=%q", do.Disposition, do.MIMEType)
	}
	var rows []map[string]any
	if err := json.Unmarshal(do.Content, &rows); err != nil {
		t.Fatalf("unmarshal DataObject content: %v", err)
	}
	return rows
}

func TestQueryReturnsRows(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"simple select", "SELECT * FROM users", true},
		{"select lower", "select id from users", true},
		{"with select", "WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"with insert", "WITH cte AS (SELECT 1) INSERT INTO t SELECT * FROM cte", false},
		{"with update", "WITH cte AS (SELECT id FROM t) UPDATE t SET x = 1", false},
		{"with delete", "WITH cte AS (SELECT id FROM t) DELETE FROM t WHERE id IN (SELECT id FROM cte)", false},
		{"with merge", "WITH src AS (SELECT 1 id) MERGE INTO t USING src ON t.id = src.id", false},
		{"multiple ctes select", "WITH a AS (SELECT 1), b AS (SELECT 2) SELECT * FROM a, b", true},
		{"multiple ctes insert", "WITH a AS (SELECT 1), b AS (SELECT 2) INSERT INTO t SELECT * FROM a", false},
		{"show", "SHOW TABLES", true},
		{"describe", "DESCRIBE users", true},
		{"explain", "EXPLAIN SELECT 1", true},
		{"pragma", "PRAGMA table_info(users)", true},
		{"insert", "INSERT INTO users (name) VALUES ('a')", false},
		{"update", "UPDATE users SET name = 'b'", false},
		{"delete", "DELETE FROM users WHERE id = 1", false},
		{"leading whitespace", "  SELECT 1", true},
		{"insert returning star", "INSERT INTO users (name) VALUES ('a') RETURNING *", true},
		{"insert returning cols", "INSERT INTO users (name) VALUES ('a') RETURNING id, name", true},
		{"update returning", "UPDATE users SET name = 'b' WHERE id = 1 RETURNING *", true},
		{"delete returning", "DELETE FROM users WHERE id = 1 RETURNING id", true},
		{"merge returning", "MERGE INTO t USING src ON t.id = src.id WHEN MATCHED THEN UPDATE SET x = 1 RETURNING *", true},
		{"with insert returning", "WITH cte AS (SELECT 1) INSERT INTO t SELECT * FROM cte RETURNING id", true},
		{"with update returning", "WITH cte AS (SELECT 1) UPDATE t SET x = 1 RETURNING *", true},
		{"column named returning_user", "SELECT returning_user FROM t", true},
		{"update without returning but with identifier containing returning",
			"UPDATE users SET returning_user = 1 WHERE id = 1", false},
		{"returning inside string literal is not a clause",
			"INSERT INTO log (msg) VALUES ('RETURNING to sender')", false},
		{"returning inside doubled-quote escaped literal",
			"INSERT INTO log (msg) VALUES ('it''s RETURNING now')", false},
		{"returning inside line comment",
			"INSERT INTO log (msg) VALUES ('ok') -- RETURNING *", false},
		{"returning inside block comment",
			"INSERT INTO log (msg) VALUES ('ok') /* RETURNING */", false},
		{"cte with unbalanced parens inside string literal",
			"WITH cte AS (SELECT '((') INSERT INTO t VALUES (1)", false},
		{"cte with unbalanced parens inside literal plus returning",
			"WITH cte AS (SELECT '((') INSERT INTO t VALUES (1) RETURNING id", true},
		{"leading block comment then select",
			"/* kickoff */ SELECT 1", true},
		{"leading line comment then select",
			"-- kickoff\nSELECT 1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := queryReturnsRows(tt.query)
			if got != tt.want {
				t.Errorf("queryReturnsRows(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

func TestRunQueryInTx_SelectReturnsResult(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	res, err := runQueryInTx(context.Background(), db, "SELECT id, name FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("status = %q, want success; message = %q", res.Status, res.Message)
	}
	rows := selectRows(t, res)
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}
}

func TestRunQueryInTx_SelectEmitsInlineJSONDataObject(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	res, err := runQueryInTx(context.Background(), db, "SELECT id, name FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	if len(res.Data) != 0 {
		t.Errorf("rows-returning result should not carry inline Data; got %v", res.Data)
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("DataObjects len = %d, want 1", len(res.DataObjects))
	}
	do := res.DataObjects[0]
	if !strings.HasPrefix(do.Name, "sql_query_result_") || !strings.HasSuffix(do.Name, ".json") {
		t.Errorf("DataObject name = %q, want sql_query_result_<suffix>.json", do.Name)
	}
	if !json.Valid(do.Content) {
		t.Errorf("DataObject content is not valid JSON: %s", do.Content)
	}
	rows := selectRows(t, res)
	wantDesc := fmt.Sprintf("SQL query result: %d rows", len(rows))
	if do.Description != wantDesc {
		t.Errorf("Description = %q, want %q", do.Description, wantDesc)
	}
	if want := fmt.Sprintf("Query returned %d rows", len(rows)); res.Message != want {
		t.Errorf("Message = %q, want %q", res.Message, want)
	}
	if got, _ := do.Metadata["row_count"].(int); got != len(rows) {
		t.Errorf("Metadata row_count = %v, want %d", do.Metadata["row_count"], len(rows))
	}
	if got, _ := do.Metadata["source_query"].(string); got != "SELECT id, name FROM users ORDER BY id" {
		t.Errorf("Metadata source_query = %q, want the executed query", got)
	}
}

func TestRunQueryInTx_EmptyResultIsEmptyArray(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	res, err := runQueryInTx(context.Background(), db, "SELECT id, name FROM users WHERE 1 = 0")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	rows := selectRows(t, res)
	if len(rows) != 0 {
		t.Fatalf("len(rows) = %d, want 0", len(rows))
	}
	if got := string(res.DataObjects[0].Content); got != "[]" {
		t.Errorf("empty-result content = %q, want %q", got, "[]")
	}
	if res.Message != "Query returned 0 rows" {
		t.Errorf("Message = %q, want %q", res.Message, "Query returned 0 rows")
	}
	if do := res.DataObjects[0]; do.Description != "SQL query result: 0 rows" {
		t.Errorf("Description = %q, want %q", do.Description, "SQL query result: 0 rows")
	}
}

func TestRunQueryInTx_InsertReturnsAffectedRows(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	res, err := runQueryInTx(context.Background(),
		db,
		"INSERT INTO users (name, email, status) VALUES ('Frank', 'frank@test.com', 'active')")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("status = %q, want success; message = %q", res.Status, res.Message)
	}
	rows, ok := res.Data["result"].([]map[string]any)
	if !ok {
		t.Fatalf("data[\"result\"] type = %T, want []map[string]any", res.Data["result"])
	}
	if len(rows) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(rows))
	}
	if rows[0]["status"] != "success" {
		t.Errorf("result[0].status = %v, want 'success'", rows[0]["status"])
	}
	affected, ok := rows[0]["affected_rows"].(int64)
	if !ok {
		t.Fatalf("affected_rows type = %T, want int64", rows[0]["affected_rows"])
	}
	if affected != 1 {
		t.Errorf("affected_rows = %d, want 1", affected)
	}
}

func TestRunQueryInTx_InsertReturningReturnsRows(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	res, err := runQueryInTx(context.Background(),
		db,
		"INSERT INTO users (name, email, status) VALUES ('Grace', 'grace@test.com', 'active') RETURNING id, name")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("status = %q, want success; message = %q", res.Status, res.Message)
	}
	rows := selectRows(t, res)
	if len(rows) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(rows))
	}
	if rows[0]["name"] != "Grace" {
		t.Errorf("returned row name = %v, want Grace", rows[0]["name"])
	}
	if id, ok := rows[0]["id"].(float64); !ok || id <= 0 {
		t.Errorf("returned row id = %v (%T), want positive float64 after JSON round-trip", rows[0]["id"], rows[0]["id"])
	}
}

func TestRunQueryInTx_InsertWithReturningInStringLiteralReturnsAffectedRows(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	res, err := runQueryInTx(context.Background(),
		db,
		"INSERT INTO users (name, email, status) VALUES ('Henry', 'henry@test.com', 'RETURNING to sender')")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("status = %q, want success; message = %q", res.Status, res.Message)
	}
	rows, ok := res.Data["result"].([]map[string]any)
	if !ok {
		t.Fatalf("data[\"result\"] type = %T, want []map[string]any", res.Data["result"])
	}
	if len(rows) != 1 {
		t.Fatalf("len(result) = %d, want 1; got %v", len(rows), rows)
	}
	if rows[0]["status"] != "success" {
		t.Errorf("result[0].status = %v, want 'success'", rows[0]["status"])
	}
	affected, ok := rows[0]["affected_rows"].(int64)
	if !ok {
		t.Fatalf("affected_rows type = %T, want int64", rows[0]["affected_rows"])
	}
	if affected != 1 {
		t.Errorf("affected_rows = %d, want 1", affected)
	}
}

func TestRunQueryInTx_CTEInsertWithUnbalancedParensInLiteral(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	res, err := runQueryInTx(context.Background(),
		db,
		"WITH cte AS (SELECT '((' AS tag) INSERT INTO users (name, email, status) SELECT 'Ivy', 'ivy@test.com', 'active' FROM cte")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	if res.Status != "success" {
		t.Fatalf("status = %q, want success; message = %q", res.Status, res.Message)
	}
	rows, ok := res.Data["result"].([]map[string]any)
	if !ok {
		t.Fatalf("data[\"result\"] type = %T, want []map[string]any", res.Data["result"])
	}
	if len(rows) != 1 {
		t.Fatalf("len(result) = %d, want 1; got %v", len(rows), rows)
	}
	if rows[0]["status"] != "success" {
		t.Errorf("result[0].status = %v, want 'success'", rows[0]["status"])
	}
}

func TestRunQueryInTx_MarshalErrorRollsBackBeforeCommit(t *testing.T) {
	db, fdb := openFakeDBWithHandle(t, "fakemarshalerr", []fakeRule{
		{
			query:   "SELECT val FROM t",
			columns: []string{"val"},
			rows:    [][]driver.Value{{math.NaN()}},
		},
	})

	res, err := runQueryInTx(context.Background(), db, "SELECT val FROM t")
	if err != nil {
		t.Fatalf("runQueryInTx returned transport error: %v", err)
	}
	if res.Status != "error" {
		t.Fatalf("status = %q, want error", res.Status)
	}
	if !strings.Contains(res.Message, "failed to encode query result") {
		t.Errorf("message = %q, want it to mention failed encoding", res.Message)
	}
	if len(res.DataObjects) != 0 {
		t.Errorf("expected no DataObjects on encode failure; got %d", len(res.DataObjects))
	}
	if fdb.lastTx == nil {
		t.Fatal("no transaction was begun")
	}
	if !fdb.lastTx.rolledBack || fdb.lastTx.committed {
		t.Errorf("tx state = {committed:%v rolledBack:%v}, want {false true}",
			fdb.lastTx.committed, fdb.lastTx.rolledBack)
	}
}

func TestRunQueryInTx_InvalidQueryRollsBack(t *testing.T) {
	db := setupTestDB(t)
	defer func() { _ = db.Close() }()

	res, err := runQueryInTx(context.Background(), db, "INSERT INTO nonexistent VALUES (1)")
	if err != nil {
		t.Fatalf("runQueryInTx returned transport error: %v", err)
	}
	if res.Status != "error" {
		t.Fatalf("status = %q, want error", res.Status)
	}
}
