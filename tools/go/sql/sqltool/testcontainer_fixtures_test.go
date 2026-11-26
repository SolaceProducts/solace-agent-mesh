// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build dbintegration

package sqltool

import (
	"database/sql"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func seedSimpleFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(64) NOT NULL, email VARCHAR(128))`,
		`INSERT INTO users (id, name, email) VALUES (1, 'Alice', 'alice@test.com')`,
		`INSERT INTO users (id, name, email) VALUES (2, 'Bob', 'bob@test.com')`,
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(t.Context(), s); err != nil {
			t.Fatalf("seed simple fixture %q: %v", s, err)
		}
	}
}

func seedIntrospectionFixture(t *testing.T, db *sql.DB, d Dialect) {
	t.Helper()
	for _, s := range introspectionFixtureDDL(d) {
		if _, err := db.ExecContext(t.Context(), s); err != nil {
			t.Fatalf("seed introspection fixture %q: %v", s, err)
		}
	}
}

func introspectionFixtureDDL(d Dialect) []string {
	switch d {
	case DialectPostgres:
		return []string{
			`CREATE TABLE customers (id SERIAL PRIMARY KEY, name VARCHAR(64) NOT NULL, status VARCHAR(16) NOT NULL)`,
			`CREATE INDEX idx_customers_status ON customers (status)`,
			`CREATE TABLE orders (id SERIAL PRIMARY KEY, customer_id INT NOT NULL REFERENCES customers(id), total NUMERIC(10,2))`,
			`INSERT INTO customers (name, status) VALUES ('Alice', 'active')`,
			`INSERT INTO customers (name, status) VALUES ('Bob', 'inactive')`,
			`INSERT INTO customers (name, status) VALUES ('Carol', 'active')`,
		}
	case DialectMySQL:
		return []string{
			`CREATE TABLE customers (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(64) NOT NULL, status VARCHAR(16) NOT NULL)`,
			`CREATE INDEX idx_customers_status ON customers (status)`,
			`CREATE TABLE orders (id INT AUTO_INCREMENT PRIMARY KEY, customer_id INT NOT NULL, total DECIMAL(10,2), FOREIGN KEY (customer_id) REFERENCES customers(id))`,
			`INSERT INTO customers (name, status) VALUES ('Alice', 'active')`,
			`INSERT INTO customers (name, status) VALUES ('Bob', 'inactive')`,
			`INSERT INTO customers (name, status) VALUES ('Carol', 'active')`,
		}
	case DialectMSSQL:
		return []string{
			`CREATE TABLE customers (id INT IDENTITY(1,1) PRIMARY KEY, name VARCHAR(64) NOT NULL, status VARCHAR(16) NOT NULL)`,
			`CREATE INDEX idx_customers_status ON customers (status)`,
			`CREATE TABLE orders (id INT IDENTITY(1,1) PRIMARY KEY, customer_id INT NOT NULL, total DECIMAL(10,2), FOREIGN KEY (customer_id) REFERENCES customers(id))`,
			`INSERT INTO customers (name, status) VALUES ('Alice', 'active')`,
			`INSERT INTO customers (name, status) VALUES ('Bob', 'inactive')`,
			`INSERT INTO customers (name, status) VALUES ('Carol', 'active')`,
		}
	default:
		return nil
	}
}

func requireOK(t *testing.T, res *sdk.Result, wantRows int) {
	t.Helper()
	if res == nil {
		t.Fatal("result is nil")
	}
	if res.Status != "success" {
		t.Fatalf("status = %q, want success; message = %q", res.Status, res.Message)
	}
	rows := selectRows(t, res)
	if len(rows) != wantRows {
		t.Fatalf("len(rows) = %d, want %d", len(rows), wantRows)
	}
}

func requireInsertAffected(t *testing.T, res *sdk.Result, want int64) {
	t.Helper()
	if res == nil {
		t.Fatal("result is nil")
	}
	if res.Status != "success" {
		t.Fatalf("status = %q, want success; message = %q", res.Status, res.Message)
	}
	rows, ok := res.Data["result"].([]map[string]any)
	if !ok {
		t.Fatalf("data[\"result\"] type = %T, want []map[string]any", res.Data["result"])
	}
	got, ok := rows[0]["affected_rows"].(int64)
	if !ok {
		t.Fatalf("affected_rows type = %T, want int64", rows[0]["affected_rows"])
	}
	if got != want {
		t.Errorf("affected_rows = %d, want %d", got, want)
	}
}

func requireIntrospectionShape(t *testing.T, yamlOut string) {
	t.Helper()
	lower := strings.ToLower(yamlOut)
	for _, tbl := range []string{"customers", "orders"} {
		if !strings.Contains(lower, tbl) {
			t.Errorf("introspection YAML missing table %q\nfull output:\n%s", tbl, yamlOut)
		}
	}
	if !strings.Contains(yamlOut, " PK") {
		t.Errorf("introspection output missing a primary key marker\nfull output:\n%s", yamlOut)
	}
	if !strings.Contains(yamlOut, " → ") {
		t.Errorf("introspection output missing a foreign key reference\nfull output:\n%s", yamlOut)
	}
}
