// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqltool

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
)

func openFakeOracle(t *testing.T, rules []fakeRule) *sql.DB {
	t.Helper()
	return openFakeDB(t, "fakeoracle", rules)
}

func TestOracle_ListTables(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleListTablesQuery,
			columns: []string{"TABLE_NAME"},
			rows: [][]driver.Value{
				{"EMPLOYEES"},
				{"ORDERS"},
			},
		},
	})

	tables, err := listTables(t.Context(), db, DialectOracle, "")
	if err != nil {
		t.Fatalf("listTables: %v", err)
	}
	if len(tables) != 2 || tables[0] != "EMPLOYEES" || tables[1] != "ORDERS" {
		t.Errorf("got %v, want [EMPLOYEES ORDERS]", tables)
	}
}

func TestOracle_ListTables_Empty(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleListTablesQuery,
			columns: []string{"TABLE_NAME"},
			rows:    nil,
		},
	})

	tables, err := listTables(t.Context(), db, DialectOracle, "")
	if err != nil {
		t.Fatalf("listTables: %v", err)
	}
	if tables != nil {
		t.Errorf("got %v, want nil", tables)
	}
}

func TestOracle_GetColumns_NullableMapping(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetColumnsQuery,
			columns: []string{"COLUMN_NAME", "DATA_TYPE", "NULLABLE"},
			rows: [][]driver.Value{
				{"ID", "NUMBER", "N"},
				{"NAME", "VARCHAR2", "Y"},
				{"EMAIL", "VARCHAR2", "N"},
			},
		},
	})

	cols, err := getColumnsOracle(t.Context(), db, "EMPLOYEES")
	if err != nil {
		t.Fatalf("getColumnsOracle: %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("got %d columns, want 3", len(cols))
	}

	want := []struct {
		name     string
		typeName string
		nullable bool
	}{
		{"ID", "NUMBER", false},
		{"NAME", "VARCHAR2", true},
		{"EMAIL", "VARCHAR2", false},
	}
	for i, w := range want {
		if cols[i].name != w.name {
			t.Errorf("col[%d].name = %q, want %q", i, cols[i].name, w.name)
		}
		if cols[i].typeName != w.typeName {
			t.Errorf("col[%d].typeName = %q, want %q", i, cols[i].typeName, w.typeName)
		}
		if cols[i].nullable != w.nullable {
			t.Errorf("col[%d].nullable = %v, want %v", i, cols[i].nullable, w.nullable)
		}
	}
}

func TestOracle_GetColumns_Empty(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetColumnsQuery,
			columns: []string{"COLUMN_NAME", "DATA_TYPE", "NULLABLE"},
			rows:    nil,
		},
	})

	cols, err := getColumnsOracle(t.Context(), db, "NONEXISTENT")
	if err != nil {
		t.Fatalf("getColumnsOracle: %v", err)
	}
	if cols != nil {
		t.Errorf("got %v, want nil", cols)
	}
}

func TestOracle_GetPrimaryKeys(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetPrimaryKeysQuery,
			columns: []string{"COLUMN_NAME"},
			rows: [][]driver.Value{
				{"ID"},
			},
		},
	})

	pks, err := getPrimaryKeys(t.Context(), db, DialectOracle, "EMPLOYEES", "")
	if err != nil {
		t.Fatalf("getPrimaryKeys: %v", err)
	}
	if len(pks) != 1 || pks[0] != "ID" {
		t.Errorf("got %v, want [ID]", pks)
	}
}

func TestOracle_GetPrimaryKeys_None(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetPrimaryKeysQuery,
			columns: []string{"COLUMN_NAME"},
			rows:    nil,
		},
	})

	pks, err := getPrimaryKeys(t.Context(), db, DialectOracle, "EMPLOYEES", "")
	if err != nil {
		t.Fatalf("getPrimaryKeys: %v", err)
	}
	if pks != nil {
		t.Errorf("got %v, want nil", pks)
	}
}

func TestOracle_GetForeignKeys(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetForeignKeysQuery,
			columns: []string{"COLUMN_NAME", "TABLE_NAME", "COLUMN_NAME"},
			rows: [][]driver.Value{
				{"DEPT_ID", "DEPARTMENTS", "ID"},
			},
		},
	})

	fks, err := getForeignKeys(t.Context(), db, DialectOracle, "EMPLOYEES", "")
	if err != nil {
		t.Fatalf("getForeignKeys: %v", err)
	}
	if len(fks) != 1 || fks[0] != "DEPT_ID → DEPARTMENTS.ID" {
		t.Errorf("got %v, want [DEPT_ID → DEPARTMENTS.ID]", fks)
	}
}

func TestOracle_GetForeignKeys_None(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetForeignKeysQuery,
			columns: []string{"COLUMN_NAME", "TABLE_NAME", "COLUMN_NAME"},
			rows:    nil,
		},
	})

	fks, err := getForeignKeys(t.Context(), db, DialectOracle, "EMPLOYEES", "")
	if err != nil {
		t.Fatalf("getForeignKeys: %v", err)
	}
	if fks != nil {
		t.Errorf("got %v, want nil", fks)
	}
}

func TestOracle_GetIndexedColumns(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetIndexedColumnsQuery,
			columns: []string{"COLUMN_NAME"},
			rows: [][]driver.Value{
				{"EMAIL"},
				{"NAME"},
			},
		},
	})

	indexed, err := getIndexedColumns(t.Context(), db, DialectOracle, "EMPLOYEES", "")
	if err != nil {
		t.Fatalf("getIndexedColumns: %v", err)
	}
	if !indexed["EMAIL"] || !indexed["NAME"] {
		t.Errorf("got %v, want EMAIL and NAME indexed", indexed)
	}
	if len(indexed) != 2 {
		t.Errorf("got %d indexed cols, want 2", len(indexed))
	}
}

func TestOracle_GetIndexedColumns_Empty(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetIndexedColumnsQuery,
			columns: []string{"COLUMN_NAME"},
			rows:    nil,
		},
	})

	indexed, err := getIndexedColumns(t.Context(), db, DialectOracle, "EMPLOYEES", "")
	if err != nil {
		t.Fatalf("getIndexedColumns: %v", err)
	}
	if len(indexed) != 0 {
		t.Errorf("got %v, want empty map", indexed)
	}
}

func TestOracle_GetApproximateRowCount(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetRowCountQuery,
			columns: []string{"NUM_ROWS"},
			rows: [][]driver.Value{
				{int64(42)},
			},
		},
	})

	count, err := getApproximateRowCount(t.Context(), db, DialectOracle, "EMPLOYEES", "")
	if err != nil {
		t.Fatalf("getApproximateRowCount: %v", err)
	}
	if count == nil || *count != 42 {
		t.Errorf("got %v, want *42", count)
	}
}

func TestOracle_GetApproximateRowCount_Null(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetRowCountQuery,
			columns: []string{"NUM_ROWS"},
			rows: [][]driver.Value{
				{nil},
			},
		},
	})

	count, err := getApproximateRowCount(t.Context(), db, DialectOracle, "EMPLOYEES", "")
	if err != nil {
		t.Fatalf("getApproximateRowCount: %v", err)
	}
	if count != nil {
		t.Errorf("got %v, want nil", count)
	}
}

func TestOracle_GetSampleRows(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   fmt.Sprintf("SELECT * FROM %s FETCH FIRST %d ROWS ONLY", quoteIdentifier(DialectOracle, "EMPLOYEES"), 5),
			columns: []string{"ID", "NAME"},
			rows: [][]driver.Value{
				{int64(1), "Alice"},
				{int64(2), "Bob"},
			},
		},
	})

	rows, err := getSampleRows(t.Context(), db, DialectOracle, "EMPLOYEES", 5)
	if err != nil {
		t.Fatalf("getSampleRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0]["ID"] != int64(1) || rows[0]["NAME"] != "Alice" {
		t.Errorf("row[0] = %v, want {ID:1 NAME:Alice}", rows[0])
	}
	if rows[1]["ID"] != int64(2) || rows[1]["NAME"] != "Bob" {
		t.Errorf("row[1] = %v, want {ID:2 NAME:Bob}", rows[1])
	}
}

func TestOracle_FetchDistinctValues(t *testing.T) {
	col := quoteIdentifier(DialectOracle, "STATUS")
	tbl := quoteIdentifier(DialectOracle, "EMPLOYEES")
	db := openFakeOracle(t, []fakeRule{
		{
			query:   fmt.Sprintf("SELECT DISTINCT %s FROM %s WHERE %s IS NOT NULL ORDER BY %s FETCH FIRST %d ROWS ONLY", col, tbl, col, col, 50),
			columns: []string{"STATUS"},
			rows: [][]driver.Value{
				{"active"},
				{"inactive"},
				{"pending"},
			},
		},
	})

	vals, err := fetchDistinctValues(t.Context(), db, DialectOracle, "EMPLOYEES", "STATUS", 50)
	if err != nil {
		t.Fatalf("fetchDistinctValues: %v", err)
	}
	if len(vals) != 3 {
		t.Fatalf("got %d values, want 3", len(vals))
	}
	want := []string{"active", "inactive", "pending"}
	for i, w := range want {
		if vals[i] != w {
			t.Errorf("vals[%d] = %q, want %q", i, vals[i], w)
		}
	}
}

func TestOracle_QuoteIdentifier(t *testing.T) {
	got := quoteIdentifier(DialectOracle, "users")
	want := `"users"`
	if got != want {
		t.Errorf("quoteIdentifier(Oracle, %q) = %q, want %q", "users", got, want)
	}
}

func TestOracle_QuoteIdentifier_Escape(t *testing.T) {
	got := quoteIdentifier(DialectOracle, `my"table`)
	want := `"my""table"`
	if got != want {
		t.Errorf("quoteIdentifier(Oracle, %q) = %q, want %q", `my"table`, got, want)
	}
}

func TestOracle_IntrospectSchema_AllTablesFail_WarningEmitted(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleListTablesQuery,
			columns: []string{"TABLE_NAME"},
			rows: [][]driver.Value{
				{"GOOD_TABLE"},
				{"BAD_TABLE"},
			},
		},
		{
			query: oracleGetColumnsQuery,
			err:   fmt.Errorf("ORA-00942: table or view does not exist"),
		},
	})

	result, err := introspectSchema(t.Context(), db, DialectOracle, IntrospectConfig{})
	if err != nil {
		t.Fatalf("introspectSchema: %v", err)
	}
	if !strings.Contains(result, "WARNING") {
		t.Errorf("expected WARNING comment for failed tables, got:\n%s", result)
	}
	if !strings.Contains(result, "2 of 2") {
		t.Errorf("expected '2 of 2 tables failed', got:\n%s", result)
	}
}

func TestOracle_IntrospectSchema_EmptyDatabase(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleListTablesQuery,
			columns: []string{"TABLE_NAME"},
			rows:    nil,
		},
	})

	result, err := introspectSchema(t.Context(), db, DialectOracle, IntrospectConfig{})
	if err != nil {
		t.Fatalf("introspectSchema: %v", err)
	}
	if strings.TrimSpace(result) != noTables {
		t.Errorf("expected %q, got: %q", noTables, result)
	}
}

func TestOracle_GetPrimaryKeys_Composite(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetPrimaryKeysQuery,
			columns: []string{"COLUMN_NAME"},
			rows: [][]driver.Value{
				{"ORDER_ID"},
				{"LINE_ID"},
			},
		},
	})

	pks, err := getPrimaryKeys(t.Context(), db, DialectOracle, "ORDER_LINES", "")
	if err != nil {
		t.Fatalf("getPrimaryKeys: %v", err)
	}
	if len(pks) != 2 {
		t.Fatalf("got %d PKs, want 2", len(pks))
	}
	if pks[0] != "ORDER_ID" || pks[1] != "LINE_ID" {
		t.Errorf("got %v, want [ORDER_ID LINE_ID]", pks)
	}
}

func TestOracle_GetForeignKeys_Multiple(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetForeignKeysQuery,
			columns: []string{"COLUMN_NAME", "TABLE_NAME", "COLUMN_NAME"},
			rows: [][]driver.Value{
				{"ORDER_ID", "ORDERS", "ID"},
				{"PRODUCT_ID", "PRODUCTS", "ID"},
			},
		},
	})

	fks, err := getForeignKeys(t.Context(), db, DialectOracle, "ORDER_LINES", "")
	if err != nil {
		t.Fatalf("getForeignKeys: %v", err)
	}
	if len(fks) != 2 {
		t.Fatalf("got %d FKs, want 2", len(fks))
	}
	if fks[0] != "ORDER_ID → ORDERS.ID" {
		t.Errorf("fks[0] = %q, want %q", fks[0], "ORDER_ID → ORDERS.ID")
	}
	if fks[1] != "PRODUCT_ID → PRODUCTS.ID" {
		t.Errorf("fks[1] = %q, want %q", fks[1], "PRODUCT_ID → PRODUCTS.ID")
	}
}

func TestOracle_GetColumns_AllDataTypes(t *testing.T) {
	db := openFakeOracle(t, []fakeRule{
		{
			query:   oracleGetColumnsQuery,
			columns: []string{"COLUMN_NAME", "DATA_TYPE", "NULLABLE"},
			rows: [][]driver.Value{
				{"ID", "NUMBER", "N"},
				{"NAME", "VARCHAR2", "Y"},
				{"DESCRIPTION", "CLOB", "Y"},
				{"NOTES", "NCLOB", "Y"},
				{"CREATED_AT", "TIMESTAMP", "N"},
				{"BIRTH_DATE", "DATE", "Y"},
				{"PHOTO", "BLOB", "Y"},
				{"LEGACY", "LONG", "Y"},
				{"UNICODE_NAME", "NVARCHAR2", "Y"},
				{"CODE", "CHAR", "N"},
				{"AMOUNT", "FLOAT", "N"},
				{"RAW_DATA", "RAW", "Y"},
			},
		},
	})

	cols, err := getColumnsOracle(t.Context(), db, "ALL_TYPES_TABLE")
	if err != nil {
		t.Fatalf("getColumnsOracle: %v", err)
	}
	if len(cols) != 12 {
		t.Fatalf("got %d columns, want 12", len(cols))
	}

	for _, c := range cols {
		if c.typeName != strings.ToUpper(c.typeName) {
			t.Errorf("col %q typeName = %q, want uppercase", c.name, c.typeName)
		}
	}

	stringTypes := map[string]bool{
		"VARCHAR2": true, "CLOB": true, "NCLOB": true,
		"LONG": true, "NVARCHAR2": true, "CHAR": true,
	}
	nonStringTypes := map[string]bool{
		"NUMBER": true, "TIMESTAMP": true, "DATE": true,
		"BLOB": true, "FLOAT": true, "RAW": true,
	}
	for _, c := range cols {
		if stringTypes[c.typeName] && !isStringType(c.typeName) {
			t.Errorf("isStringType(%q) = false, want true", c.typeName)
		}
		if nonStringTypes[c.typeName] && isStringType(c.typeName) {
			t.Errorf("isStringType(%q) = true, want false", c.typeName)
		}
	}
}
