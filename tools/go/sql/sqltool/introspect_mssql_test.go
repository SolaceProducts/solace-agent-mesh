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

func openFakeMSSQL(t *testing.T, rules []fakeRule) *sql.DB {
	t.Helper()
	return openFakeDB(t, "fakemssql", rules)
}

func TestMSSQL_ListTables(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlListTablesQuery,
			columns: []string{"TABLE_NAME"},
			rows: [][]driver.Value{
				{"Employees"},
				{"Orders"},
			},
		},
	})

	tables, err := listTables(t.Context(), db, DialectMSSQL, "dbo")
	if err != nil {
		t.Fatalf("listTables: %v", err)
	}
	if len(tables) != 2 || tables[0] != "Employees" || tables[1] != "Orders" {
		t.Errorf("got %v, want [Employees Orders]", tables)
	}
}

func TestMSSQL_ListTables_Empty(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlListTablesQuery,
			columns: []string{"TABLE_NAME"},
			rows:    nil,
		},
	})

	tables, err := listTables(t.Context(), db, DialectMSSQL, "dbo")
	if err != nil {
		t.Fatalf("listTables: %v", err)
	}
	if tables != nil {
		t.Errorf("got %v, want nil", tables)
	}
}

func TestMSSQL_GetColumns_NullableMapping(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetColumnsQuery,
			columns: []string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE"},
			rows: [][]driver.Value{
				{"ID", "int", "NO"},
				{"Name", "nvarchar", "YES"},
				{"Email", "varchar", "NO"},
			},
		},
	})

	cols, err := getColumnsMSSQL(t.Context(), db, "Employees", "dbo")
	if err != nil {
		t.Fatalf("getColumnsMSSQL: %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("got %d columns, want 3", len(cols))
	}

	want := []struct {
		name     string
		typeName string
		nullable bool
	}{
		{"ID", "INT", false},
		{"Name", "NVARCHAR", true},
		{"Email", "VARCHAR", false},
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

func TestMSSQL_GetColumns_Empty(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetColumnsQuery,
			columns: []string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE"},
			rows:    nil,
		},
	})

	cols, err := getColumnsMSSQL(t.Context(), db, "Nonexistent", "dbo")
	if err != nil {
		t.Fatalf("getColumnsMSSQL: %v", err)
	}
	if cols != nil {
		t.Errorf("got %v, want nil", cols)
	}
}

func TestMSSQL_GetPrimaryKeys(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetPrimaryKeysQuery,
			columns: []string{"COLUMN_NAME"},
			rows: [][]driver.Value{
				{"ID"},
			},
		},
	})

	pks, err := getPrimaryKeys(t.Context(), db, DialectMSSQL, "Employees", "dbo")
	if err != nil {
		t.Fatalf("getPrimaryKeys: %v", err)
	}
	if len(pks) != 1 || pks[0] != "ID" {
		t.Errorf("got %v, want [ID]", pks)
	}
}

func TestMSSQL_GetPrimaryKeys_None(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetPrimaryKeysQuery,
			columns: []string{"COLUMN_NAME"},
			rows:    nil,
		},
	})

	pks, err := getPrimaryKeys(t.Context(), db, DialectMSSQL, "Employees", "dbo")
	if err != nil {
		t.Fatalf("getPrimaryKeys: %v", err)
	}
	if pks != nil {
		t.Errorf("got %v, want nil", pks)
	}
}

func TestMSSQL_GetForeignKeys(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetForeignKeysQuery,
			columns: []string{"COLUMN_NAME", "TABLE_NAME", "COLUMN_NAME"},
			rows: [][]driver.Value{
				{"DeptID", "Departments", "ID"},
			},
		},
	})

	fks, err := getForeignKeys(t.Context(), db, DialectMSSQL, "Employees", "dbo")
	if err != nil {
		t.Fatalf("getForeignKeys: %v", err)
	}
	if len(fks) != 1 || fks[0] != "DeptID → Departments.ID" {
		t.Errorf("got %v, want [DeptID → Departments.ID]", fks)
	}
}

func TestMSSQL_GetForeignKeys_None(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetForeignKeysQuery,
			columns: []string{"COLUMN_NAME", "TABLE_NAME", "COLUMN_NAME"},
			rows:    nil,
		},
	})

	fks, err := getForeignKeys(t.Context(), db, DialectMSSQL, "Employees", "dbo")
	if err != nil {
		t.Fatalf("getForeignKeys: %v", err)
	}
	if fks != nil {
		t.Errorf("got %v, want nil", fks)
	}
}

func TestMSSQL_GetIndexedColumns(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetIndexedColumnsQuery,
			columns: []string{"COLUMN_NAME"},
			rows: [][]driver.Value{
				{"Email"},
				{"Name"},
			},
		},
	})

	indexed, err := getIndexedColumns(t.Context(), db, DialectMSSQL, "Employees", "dbo")
	if err != nil {
		t.Fatalf("getIndexedColumns: %v", err)
	}
	if !indexed["Email"] || !indexed["Name"] {
		t.Errorf("got %v, want Email and Name indexed", indexed)
	}
	if len(indexed) != 2 {
		t.Errorf("got %d indexed cols, want 2", len(indexed))
	}
}

func TestMSSQL_GetIndexedColumns_Empty(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetIndexedColumnsQuery,
			columns: []string{"COLUMN_NAME"},
			rows:    nil,
		},
	})

	indexed, err := getIndexedColumns(t.Context(), db, DialectMSSQL, "Employees", "dbo")
	if err != nil {
		t.Fatalf("getIndexedColumns: %v", err)
	}
	if len(indexed) != 0 {
		t.Errorf("got %v, want empty map", indexed)
	}
}

func TestMSSQL_GetApproximateRowCount(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetRowCountQuery,
			columns: []string{"ROWS"},
			rows: [][]driver.Value{
				{int64(42)},
			},
		},
	})

	count, err := getApproximateRowCount(t.Context(), db, DialectMSSQL, "Employees", "dbo")
	if err != nil {
		t.Fatalf("getApproximateRowCount: %v", err)
	}
	if count == nil || *count != 42 {
		t.Errorf("got %v, want *42", count)
	}
}

func TestMSSQL_GetApproximateRowCount_Null(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetRowCountQuery,
			columns: []string{"ROWS"},
			rows: [][]driver.Value{
				{nil},
			},
		},
	})

	count, err := getApproximateRowCount(t.Context(), db, DialectMSSQL, "Employees", "dbo")
	if err != nil {
		t.Fatalf("getApproximateRowCount: %v", err)
	}
	if count != nil {
		t.Errorf("got %v, want nil", count)
	}
}

func TestMSSQL_GetSampleRows(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   fmt.Sprintf("SELECT TOP %d * FROM %s", 5, quoteIdentifier(DialectMSSQL, "Employees")),
			columns: []string{"ID", "Name"},
			rows: [][]driver.Value{
				{int64(1), "Alice"},
				{int64(2), "Bob"},
			},
		},
	})

	rows, err := getSampleRows(t.Context(), db, DialectMSSQL, "Employees", 5)
	if err != nil {
		t.Fatalf("getSampleRows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0]["ID"] != int64(1) || rows[0]["Name"] != "Alice" {
		t.Errorf("row[0] = %v, want {ID:1 Name:Alice}", rows[0])
	}
	if rows[1]["ID"] != int64(2) || rows[1]["Name"] != "Bob" {
		t.Errorf("row[1] = %v, want {ID:2 Name:Bob}", rows[1])
	}
}

func TestMSSQL_FetchDistinctValues(t *testing.T) {
	col := quoteIdentifier(DialectMSSQL, "Status")
	tbl := quoteIdentifier(DialectMSSQL, "Employees")
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   fmt.Sprintf("SELECT DISTINCT TOP %d %s FROM %s WHERE %s IS NOT NULL ORDER BY %s", 50, col, tbl, col, col),
			columns: []string{"Status"},
			rows: [][]driver.Value{
				{"active"},
				{"inactive"},
				{"pending"},
			},
		},
	})

	vals, err := fetchDistinctValues(t.Context(), db, DialectMSSQL, "Employees", "Status", 50)
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

func TestMSSQL_QuoteIdentifier(t *testing.T) {
	got := quoteIdentifier(DialectMSSQL, "users")
	want := "[users]"
	if got != want {
		t.Errorf("quoteIdentifier(MSSQL, %q) = %q, want %q", "users", got, want)
	}
}

func TestMSSQL_QuoteIdentifier_Escape(t *testing.T) {
	got := quoteIdentifier(DialectMSSQL, "my]table")
	want := "[my]]table]"
	if got != want {
		t.Errorf("quoteIdentifier(MSSQL, %q) = %q, want %q", "my]table", got, want)
	}
}

func TestMSSQL_IntrospectSchema_AllTablesFail_WarningEmitted(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlListTablesQuery,
			columns: []string{"TABLE_NAME"},
			rows: [][]driver.Value{
				{"GoodTable"},
				{"BadTable"},
			},
		},
		{
			query: mssqlGetColumnsQuery,
			err:   fmt.Errorf("mssql: invalid object name"),
		},
	})

	result, err := introspectSchema(t.Context(), db, DialectMSSQL, IntrospectConfig{})
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

func TestMSSQL_IntrospectSchema_EmptyDatabase(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlListTablesQuery,
			columns: []string{"TABLE_NAME"},
			rows:    nil,
		},
	})

	result, err := introspectSchema(t.Context(), db, DialectMSSQL, IntrospectConfig{})
	if err != nil {
		t.Fatalf("introspectSchema: %v", err)
	}
	if strings.TrimSpace(result) != noTables {
		t.Errorf("expected %q, got: %q", noTables, result)
	}
}

func TestMSSQL_GetPrimaryKeys_Composite(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetPrimaryKeysQuery,
			columns: []string{"COLUMN_NAME"},
			rows: [][]driver.Value{
				{"OrderID"},
				{"LineID"},
			},
		},
	})

	pks, err := getPrimaryKeys(t.Context(), db, DialectMSSQL, "OrderLines", "dbo")
	if err != nil {
		t.Fatalf("getPrimaryKeys: %v", err)
	}
	if len(pks) != 2 {
		t.Fatalf("got %d PKs, want 2", len(pks))
	}
	if pks[0] != "OrderID" || pks[1] != "LineID" {
		t.Errorf("got %v, want [OrderID LineID]", pks)
	}
}

func TestMSSQL_GetForeignKeys_Multiple(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetForeignKeysQuery,
			columns: []string{"COLUMN_NAME", "TABLE_NAME", "COLUMN_NAME"},
			rows: [][]driver.Value{
				{"OrderID", "Orders", "ID"},
				{"ProductID", "Products", "ID"},
			},
		},
	})

	fks, err := getForeignKeys(t.Context(), db, DialectMSSQL, "OrderLines", "dbo")
	if err != nil {
		t.Fatalf("getForeignKeys: %v", err)
	}
	if len(fks) != 2 {
		t.Fatalf("got %d FKs, want 2", len(fks))
	}
	if fks[0] != "OrderID → Orders.ID" {
		t.Errorf("fks[0] = %q, want %q", fks[0], "OrderID → Orders.ID")
	}
	if fks[1] != "ProductID → Products.ID" {
		t.Errorf("fks[1] = %q, want %q", fks[1], "ProductID → Products.ID")
	}
}

func TestMSSQL_GetColumns_AllDataTypes(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlGetColumnsQuery,
			columns: []string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE"},
			rows: [][]driver.Value{
				{"ID", "int", "NO"},
				{"Name", "nvarchar", "YES"},
				{"Description", "text", "YES"},
				{"Notes", "ntext", "YES"},
				{"CreatedAt", "datetime", "NO"},
				{"BirthDate", "date", "YES"},
				{"Photo", "varbinary", "YES"},
				{"Code", "char", "NO"},
				{"Amount", "decimal", "NO"},
				{"Price", "money", "YES"},
				{"UnicodeName", "nchar", "YES"},
				{"SmallVal", "smallint", "NO"},
			},
		},
	})

	cols, err := getColumnsMSSQL(t.Context(), db, "AllTypesTable", "dbo")
	if err != nil {
		t.Fatalf("getColumnsMSSQL: %v", err)
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
		"NVARCHAR": true, "TEXT": true, "NTEXT": true,
		"CHAR": true, "NCHAR": true,
	}
	nonStringTypes := map[string]bool{
		"INT": true, "DATETIME": true, "DATE": true,
		"VARBINARY": true, "DECIMAL": true, "MONEY": true,
		"SMALLINT": true,
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

func TestMSSQL_IntrospectSchema_DefaultsDboSchema(t *testing.T) {
	db := openFakeMSSQL(t, []fakeRule{
		{
			query:   mssqlListTablesQuery,
			columns: []string{"TABLE_NAME"},
			rows: [][]driver.Value{
				{"Users"},
			},
		},
		{
			query:   mssqlGetColumnsQuery,
			columns: []string{"COLUMN_NAME", "DATA_TYPE", "IS_NULLABLE"},
			rows: [][]driver.Value{
				{"ID", "int", "NO"},
			},
		},
		{
			query:   mssqlGetPrimaryKeysQuery,
			columns: []string{"COLUMN_NAME"},
			rows: [][]driver.Value{
				{"ID"},
			},
		},
		{query: mssqlGetForeignKeysQuery, columns: []string{"COLUMN_NAME", "TABLE_NAME", "COLUMN_NAME"}, rows: nil},
		{query: mssqlGetIndexedColumnsQuery, columns: []string{"COLUMN_NAME"}, rows: nil},
		{query: mssqlGetRowCountQuery, columns: []string{"ROWS"}, rows: [][]driver.Value{{int64(10)}}},
		{
			query:   fmt.Sprintf("SELECT TOP %d * FROM %s", 100, quoteIdentifier(DialectMSSQL, "Users")),
			columns: []string{"ID"},
			rows:    [][]driver.Value{{int64(1)}},
		},
	})

	result, err := introspectSchema(t.Context(), db, DialectMSSQL, IntrospectConfig{})
	if err != nil {
		t.Fatalf("introspectSchema: %v", err)
	}
	if !strings.Contains(result, "Users") {
		t.Errorf("expected schema to contain 'Users', got:\n%s", result)
	}
	if strings.Contains(result, "WARNING") {
		t.Errorf("unexpected WARNING in output:\n%s", result)
	}
}
