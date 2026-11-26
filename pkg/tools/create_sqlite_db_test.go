// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestCreateSQLiteDB_CSV(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	csvData := "name,age,city\nAlice,30,NYC\nBob,25,LA\nCharlie,35,NYC"
	saveDataToolArtifact(t, ctx, facade, "people.csv", []byte(csvData), "text/csv")

	tool := &CreateSQLiteDBTool{}
	result, err := tool.Execute(ctx, map[string]any{
		"input_filename":     "people.csv",
		"output_db_filename": "people_db",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}

	outFilename, _ := result.Data["output_filename"].(string)
	if outFilename != "people_db.sqlite" {
		t.Errorf("expected output filename 'people_db.sqlite', got %q", outFilename)
	}
	if tn, _ := result.Data["table_name"].(string); tn != "data" {
		t.Errorf("expected default table_name 'data', got %q", tn)
	}
	if rc := result.Data["row_count"]; rc != 3 {
		t.Errorf("expected row_count 3, got %v", rc)
	}

	content, mime, loadErr := facade.LoadArtifact(ctx, outFilename, nil)
	if loadErr != nil {
		t.Fatalf("failed to load saved artifact: %v", loadErr)
	}
	if mime != sqliteDBMIMEType {
		t.Errorf("expected MIME %q, got %q", sqliteDBMIMEType, mime)
	}
	assertSQLiteHasRows(t, content, "data", 3)
}

func TestCreateSQLiteDB_JSON(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	jsonData := `[{"name":"Alice","score":90},{"name":"Bob","score":85},{"name":"Charlie","score":95}]`
	saveDataToolArtifact(t, ctx, facade, "scores.json", []byte(jsonData), "application/json")

	tool := &CreateSQLiteDBTool{}
	result, err := tool.Execute(ctx, map[string]any{
		"input_filename":     "scores.json",
		"output_db_filename": "scores.sqlite",
		"table_name":         "scores",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}

	outFilename, _ := result.Data["output_filename"].(string)
	if outFilename != "scores.sqlite" {
		t.Errorf("expected output filename 'scores.sqlite', got %q", outFilename)
	}
	if tn, _ := result.Data["table_name"].(string); tn != "scores" {
		t.Errorf("expected table_name 'scores', got %q", tn)
	}

	content, _, loadErr := facade.LoadArtifact(ctx, outFilename, nil)
	if loadErr != nil {
		t.Fatalf("failed to load saved artifact: %v", loadErr)
	}
	assertSQLiteHasRows(t, content, "scores", 3)
}

func TestCreateSQLiteDB_ColumnarJSON(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	columnar := `{"name":["Alice","Bob"],"age":[30,25]}`
	saveDataToolArtifact(t, ctx, facade, "people.json", []byte(columnar), "application/json")

	tool := &CreateSQLiteDBTool{}
	result, err := tool.Execute(ctx, map[string]any{
		"input_filename":     "people.json",
		"output_db_filename": "people",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	content, _, loadErr := facade.LoadArtifact(ctx, "people.sqlite", nil)
	if loadErr != nil {
		t.Fatalf("failed to load saved artifact: %v", loadErr)
	}
	assertSQLiteHasRows(t, content, "data", 2)
}

func TestCreateSQLiteDB_UnsupportedMIMETypeRejected(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	yamlData := "- name: Alice\n  age: 30"
	saveDataToolArtifact(t, ctx, facade, "people.yaml", []byte(yamlData), "application/yaml")

	tool := &CreateSQLiteDBTool{}
	result, err := tool.Execute(ctx, map[string]any{
		"input_filename":     "people.yaml",
		"output_db_filename": "people.sqlite",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error for unsupported MIME type, got %s: %s", result.Status, result.Message)
	}
	if !strings.Contains(result.Message, "Unsupported input MIME type") {
		t.Errorf("expected unsupported-MIME error message, got: %s", result.Message)
	}
}

func TestCreateSQLiteDB_MissingArtifact(t *testing.T) {
	toolCtx, _ := newDataToolContext(t)
	ctx := context.Background()

	tool := &CreateSQLiteDBTool{}
	result, err := tool.Execute(ctx, map[string]any{
		"input_filename":     "does-not-exist.csv",
		"output_db_filename": "out.sqlite",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "error" {
		t.Fatalf("expected error for missing artifact, got %s", result.Status)
	}
}

func TestCreateSQLiteDB_MissingRequiredParams(t *testing.T) {
	toolCtx, _ := newDataToolContext(t)
	ctx := context.Background()

	tool := &CreateSQLiteDBTool{}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"missing input_filename", map[string]any{"output_db_filename": "out.sqlite"}},
		{"missing output_db_filename", map[string]any{"input_filename": "x.csv"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := tool.Execute(ctx, tc.args, toolCtx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Status != "error" {
				t.Fatalf("expected error, got %s", r.Status)
			}
		})
	}
}

func TestCreateSQLiteDB_WithVersionedInput(t *testing.T) {
	toolCtx, facade := newDataToolContext(t)
	ctx := context.Background()

	saveDataToolArtifact(t, ctx, facade, "data.csv", []byte("x\n1"), "text/csv")
	saveDataToolArtifact(t, ctx, facade, "data.csv", []byte("x\n1\n2"), "text/csv")

	tool := &CreateSQLiteDBTool{}
	result, err := tool.Execute(ctx, map[string]any{
		"input_filename":     "data.csv:0",
		"output_db_filename": "out",
	}, toolCtx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "success" {
		t.Fatalf("expected success, got %s: %s", result.Status, result.Message)
	}
	if rc := result.Data["row_count"]; rc != 1 {
		t.Errorf("expected row_count 1 from version 0, got %v", rc)
	}
}

func TestEnsureCorrectExtension(t *testing.T) {
	cases := []struct {
		in, ext, want string
	}{
		{"data", "sqlite", "data.sqlite"},
		{"data.sqlite", "sqlite", "data.sqlite"},
		{"data.SQLITE", "sqlite", "data.SQLITE"},
		{"data.db", "sqlite", "data.sqlite"},
		{"", "sqlite", "unnamed.sqlite"},
		{"  spaced  ", "sqlite", "spaced.sqlite"},
	}
	for _, c := range cases {
		got := ensureCorrectExtension(c.in, c.ext)
		if got != c.want {
			t.Errorf("ensureCorrectExtension(%q, %q) = %q, want %q", c.in, c.ext, got, c.want)
		}
	}
}

func assertSQLiteHasRows(t *testing.T, dbBytes []byte, tableName string, wantRows int) {
	t.Helper()
	if len(dbBytes) == 0 {
		t.Fatal("sqlite bytes are empty")
	}
	if !strings.HasPrefix(string(dbBytes[:min(16, len(dbBytes))]), "SQLite format 3") {
		t.Fatalf("content does not look like a SQLite database (first 16 bytes: %q)", dbBytes[:min(16, len(dbBytes))])
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "check.sqlite")
	if err := os.WriteFile(dbPath, dbBytes, 0o600); err != nil {
		t.Fatalf("write temp db: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	query := "SELECT COUNT(*) FROM \"" + tableName + "\""
	if err := db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("count rows in %q: %v", tableName, err)
	}
	if count != wantRows {
		t.Errorf("expected %d rows in table %q, got %d", wantRows, tableName, count)
	}
}

func useUnwritableTempDir(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions, so an unwritable TMPDIR cannot be simulated")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod temp dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	t.Setenv("TMPDIR", dir)
}

func TestSQLiteArtifactRoundTrip_WithoutWritableTempDir(t *testing.T) {
	useUnwritableTempDir(t)
	ctx := t.Context()

	rows := []map[string]any{
		{"name": "Alice", "age": "30"},
		{"name": "Bob", "age": "25"},
	}
	dbBytes, err := writeRowsToSQLiteBytes(ctx, rows, "people")
	if err != nil {
		t.Fatalf("writeRowsToSQLiteBytes: %v", err)
	}
	if !bytes.HasPrefix(dbBytes, sqliteMagicHeader) {
		t.Fatalf("serialized bytes lack the SQLite header: %q", dbBytes[:min(16, len(dbBytes))])
	}

	got, srcTable, err := loadRowsFromSQLiteArtifact(ctx, dbBytes)
	if err != nil {
		t.Fatalf("loadRowsFromSQLiteArtifact: %v", err)
	}
	if srcTable != "people" {
		t.Errorf("source table = %q, want %q", srcTable, "people")
	}
	want := []map[string]any{
		{"name": "Alice", "age": int64(30)},
		{"name": "Bob", "age": int64(25)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v", got, want)
	}
}

func TestWriteRowsToSQLiteBytes_ProducesStandaloneDatabaseFile(t *testing.T) {
	rows := []map[string]any{{"id": "1"}, {"id": "2"}, {"id": "3"}}
	dbBytes, err := writeRowsToSQLiteBytes(t.Context(), rows, "items")
	if err != nil {
		t.Fatalf("writeRowsToSQLiteBytes: %v", err)
	}
	assertSQLiteHasRows(t, dbBytes, "items", 3)
}

func TestCreateSQLiteDB_OutputNameStaysInTheRunsOwnSession(t *testing.T) {
	for _, tc := range []struct {
		name, requested, want string
	}{
		{"a user scope prefix", "user:memory.sqlite", "user_memory.sqlite"},
		{"a path", "../escape/people.sqlite", "people.sqlite"},
		{"a colon in the middle", "team:people", "team_people.sqlite"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx, facade := newDataToolContext(t)
			ctx := context.Background()
			saveDataToolArtifact(t, ctx, facade, "people.csv", []byte("name,age\nAlice,30\n"), "text/csv")

			result, err := (&CreateSQLiteDBTool{}).Execute(ctx, map[string]any{
				"input_filename":     "people.csv",
				"output_db_filename": tc.requested,
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute(%q): %v", tc.requested, err)
			}
			if result.Status != "success" {
				t.Fatalf("Execute(%q) = %s: %s", tc.requested, result.Status, result.Message)
			}
			if got, _ := result.Data["output_filename"].(string); got != tc.want {
				t.Fatalf("output_db_filename %q saved as %q, want %q", tc.requested, got, tc.want)
			}
		})
	}
}
