// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqltool

import (
	"strings"
	"testing"
)

func TestParseConnectionString_Postgres(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		dialect Dialect
		driver  string
	}{
		{"plain postgres", "postgres://user:pass@localhost:5432/mydb", DialectPostgres, "pgx"},
		{"postgresql scheme", "postgresql://user:pass@host/db", DialectPostgres, "pgx"},
		{"sqlalchemy psycopg2", "postgresql+psycopg2://user:pass@host:5432/db", DialectPostgres, "pgx"},
		{"with query params", "postgres://user:pass@host/db?sslmode=disable", DialectPostgres, "pgx"},
		{"password with plus", "postgres://user:p+assword@host:5432/db", DialectPostgres, "pgx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseConnectionString(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.dialect != tt.dialect {
				t.Errorf("dialect = %q, want %q", info.dialect, tt.dialect)
			}
			if info.driverName != tt.driver {
				t.Errorf("driver = %q, want %q", info.driverName, tt.driver)
			}
		})
	}
}

func TestParseConnectionString_MySQL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantInDSN string
	}{
		{"plain mysql", "mysql://user:pass@localhost:3306/mydb", "user:pass@tcp(localhost:3306)/mydb"},
		{"sqlalchemy pymysql", "mysql+pymysql://user:pass@host:3306/db", "user:pass@tcp(host:3306)/db"},
		{"default port", "mysql://user:pass@host/db", "user:pass@tcp(host:3306)/db"},
		{"special chars in password", "mysql://user:p%40ss@host/db", "user:p@ss@tcp(host:3306)/db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseConnectionString(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.dialect != DialectMySQL {
				t.Errorf("dialect = %q, want mysql", info.dialect)
			}
			if info.driverName != "mysql" {
				t.Errorf("driver = %q, want mysql", info.driverName)
			}
			if !strings.Contains(info.dsn, tt.wantInDSN) {
				t.Errorf("dsn = %q, want to contain %q", info.dsn, tt.wantInDSN)
			}
		})
	}
}

func TestParseConnectionString_SQLite(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantDSN string
	}{
		{"relative path", "sqlite:///test.db", "file:test.db"},
		{"absolute path", "sqlite:////tmp/test.db", "file:/tmp/test.db"},
		{"memory", "sqlite:///:memory:", "file::memory:?cache=shared"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseConnectionString(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.dialect != DialectSQLite {
				t.Errorf("dialect = %q, want sqlite", info.dialect)
			}
			if info.dsn != tt.wantDSN {
				t.Errorf("dsn = %q, want %q", info.dsn, tt.wantDSN)
			}
		})
	}
}

func TestParseConnectionString_MSSQL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantInDSN string
	}{
		{"sqlserver scheme", "sqlserver://user:pass@localhost:1433?database=mydb", "sqlserver://user:pass@localhost:1433?database=mydb"},
		{"mssql scheme", "mssql://user:pass@localhost:1433?database=mydb", "sqlserver://user:pass@localhost:1433?database=mydb"},
		{"sqlalchemy pyodbc", "mssql+pyodbc://user:pass@host:1433/db", "sqlserver://user:pass@host:1433/db"},
		{"with encrypt", "sqlserver://user:pass@host:1433?database=db&encrypt=true", "encrypt=true"},
		{"with trust cert", "sqlserver://user:pass@host:1433?database=db&TrustServerCertificate=true", "TrustServerCertificate=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseConnectionString(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.dialect != DialectMSSQL {
				t.Errorf("dialect = %q, want mssql", info.dialect)
			}
			if info.driverName != "sqlserver" {
				t.Errorf("driver = %q, want sqlserver", info.driverName)
			}
			if !strings.Contains(info.dsn, tt.wantInDSN) {
				t.Errorf("dsn = %q, want to contain %q", info.dsn, tt.wantInDSN)
			}
		})
	}
}

func TestParseConnectionString_Oracle(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantInDSN string
	}{
		{"plain oracle", "oracle://user:pass@localhost:1521/ORCL", "oracle://user:pass@localhost:1521/ORCL"},
		{"sqlalchemy oracledb", "oracle+oracledb://user:pass@host:1521/?service_name=ORCL", "/ORCL"},
		{"service_name query param", "oracle://user:pass@host:1521/?service_name=MYDB", "/MYDB"},
		{"default port", "oracle://user:pass@host/?service_name=SVC", ":1521"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := parseConnectionString(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.dialect != DialectOracle {
				t.Errorf("dialect = %q, want oracle", info.dialect)
			}
			if info.driverName != "oracle" {
				t.Errorf("driver = %q, want oracle", info.driverName)
			}
			if !strings.Contains(info.dsn, tt.wantInDSN) {
				t.Errorf("dsn = %q, want to contain %q", info.dsn, tt.wantInDSN)
			}
		})
	}
}

func TestParseConnectionString_Unsupported(t *testing.T) {
	_, err := parseConnectionString("mongodb://localhost/db")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}

func TestParseConnectionString_EmptySQLite(t *testing.T) {
	_, err := parseConnectionString("sqlite:///")
	if err == nil {
		t.Fatal("expected error for empty sqlite path")
	}
}

func TestOpenDB_SQLiteMemory(t *testing.T) {
	db, dialect, err := openDB("sqlite:///:memory:")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer func() { _ = db.Close() }()
	if dialect != DialectSQLite {
		t.Errorf("dialect = %q, want sqlite", dialect)
	}
	var result int
	if err := db.QueryRow("SELECT 1").Scan(&result); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if result != 1 {
		t.Errorf("result = %d, want 1", result)
	}
}
