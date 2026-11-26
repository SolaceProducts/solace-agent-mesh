// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sqltool

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync"
	"testing"
)

type fakeRule struct {
	query   string
	columns []string
	rows    [][]driver.Value
	err     error
}

type fakeDB struct {
	rules  []fakeRule
	lastTx *fakeTx
}

var (
	fakeDBs         sync.Map
	fakeDriversMu   sync.Mutex
	registeredFakes = map[string]bool{}
)

func registerFakeDriver(name string) {
	fakeDriversMu.Lock()
	defer fakeDriversMu.Unlock()
	if registeredFakes[name] {
		return
	}
	sql.Register(name, &fakeDriver{})
	registeredFakes[name] = true
}

func openFakeDB(t *testing.T, driverName string, rules []fakeRule) *sql.DB {
	t.Helper()
	db, _ := openFakeDBWithHandle(t, driverName, rules)
	return db
}

func openFakeDBWithHandle(t *testing.T, driverName string, rules []fakeRule) (*sql.DB, *fakeDB) {
	t.Helper()
	registerFakeDriver(driverName)

	key := driverName + "/" + t.Name()
	fdb := &fakeDB{rules: rules}
	fakeDBs.Store(key, fdb)
	t.Cleanup(func() { fakeDBs.Delete(key) })

	db, err := sql.Open(driverName, key)
	if err != nil {
		t.Fatalf("sql.Open %s: %v", driverName, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, fdb
}

type fakeDriver struct{}

func (d *fakeDriver) Open(dsn string) (driver.Conn, error) {
	v, ok := fakeDBs.Load(dsn)
	if !ok {
		return nil, fmt.Errorf("fakedb: no fakeDB registered for %q", dsn)
	}
	return &fakeConn{db: v.(*fakeDB)}, nil
}

type fakeConn struct {
	db *fakeDB
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	for _, r := range c.db.rules {
		if query == r.query {
			if r.err != nil {
				return nil, r.err
			}
			return &fakeStmt{rule: r}, nil
		}
	}
	return &fakeStmt{rule: fakeRule{}}, nil
}

func (c *fakeConn) Close() error { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	tx := &fakeTx{}
	c.db.lastTx = tx
	return tx, nil
}
func (c *fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	for _, r := range c.db.rules {
		if query == r.query {
			if r.err != nil {
				return nil, r.err
			}
			return &fakeRows{columns: r.columns, rows: r.rows}, nil
		}
	}
	return &fakeRows{}, nil
}

type fakeTx struct {
	committed  bool
	rolledBack bool
}

func (t *fakeTx) Commit() error   { t.committed = true; return nil }
func (t *fakeTx) Rollback() error { t.rolledBack = true; return nil }

type fakeStmt struct {
	rule fakeRule
}

func (s *fakeStmt) Close() error  { return nil }
func (s *fakeStmt) NumInput() int { return -1 }
func (s *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	return &fakeResult{}, nil
}
func (s *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	return &fakeRows{columns: s.rule.columns, rows: s.rule.rows}, nil
}

type fakeResult struct{}

func (r *fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r *fakeResult) RowsAffected() (int64, error) { return 0, nil }

type fakeRows struct {
	columns []string
	rows    [][]driver.Value
	cursor  int
}

func (r *fakeRows) Columns() []string { return r.columns }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.cursor >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.cursor])
	r.cursor++
	return nil
}
