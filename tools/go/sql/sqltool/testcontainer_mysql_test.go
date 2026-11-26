// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build dbintegration

package sqltool

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/mysql"
)

const (
	mysqlUser     = "sam"
	mysqlPassword = "sam_test_password"
	mysqlRootDB   = "sam"
)

type mysqlHarness struct {
	container *mysql.MySQLContainer
	host      string
	port      string
}

func (h *mysqlHarness) Dialect() Dialect { return DialectMySQL }

func (h *mysqlHarness) NewDB(t *testing.T) *sql.DB {
	t.Helper()
	dbName := uniqueDBName(t, "mysql")

	root, err := sql.Open("mysql", mysqlRootDSN(h.host, h.port))
	if err != nil {
		t.Fatalf("open root db: %v", err)
	}
	defer func() { _ = root.Close() }()

	if _, err := root.ExecContext(t.Context(), fmt.Sprintf("CREATE DATABASE `%s`", dbName)); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}
	if _, err := root.ExecContext(t.Context(),
		fmt.Sprintf("GRANT ALL ON `%s`.* TO '%s'@'%%'", dbName, mysqlUser)); err != nil {
		t.Fatalf("grant on %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		droot, derr := sql.Open("mysql", mysqlRootDSN(h.host, h.port))
		if derr != nil {
			slog.Warn("mysql cleanup: open root db", "db", dbName, "err", derr)
			return
		}
		defer func() { _ = droot.Close() }()
		if _, err := droot.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName)); err != nil {
			slog.Warn("mysql cleanup: drop database", "db", dbName, "err", err)
		}
	})

	dsn := fmt.Sprintf("mysql://%s:%s@%s:%s/%s",
		mysqlUser, mysqlPassword, h.host, h.port, dbName)
	db, _, err := openDB(dsn)
	if err != nil {
		t.Fatalf("openDB %s: %v", dbName, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mysqlRootDSN(host, port string) string {
	return fmt.Sprintf("root:%s@tcp(%s:%s)/", mysqlPassword, host, port)
}

func startMySQLHarness(ctx context.Context) (dbHarness, func(context.Context) error, error) {
	c, err := mysql.Run(ctx, mysqlImage,
		mysql.WithDatabase(mysqlRootDB),
		mysql.WithUsername(mysqlUser),
		mysql.WithPassword(mysqlPassword),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("mysql run: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, nil, fmt.Errorf("mysql host: %w", err)
	}
	port, err := c.MappedPort(ctx, "3306/tcp")
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, nil, fmt.Errorf("mysql mapped port: %w", err)
	}

	stop := func(ctx context.Context) error { return c.Terminate(ctx) }
	return &mysqlHarness{container: c, host: host, port: port.Port()}, stop, nil
}

func TestMySQL_OpenDB_Pings(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectMySQL)
	db := h.NewDB(t)
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestMySQL_RunQueryInTx_Select(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectMySQL)
	db := h.NewDB(t)
	seedSimpleFixture(t, db)

	res, err := runQueryInTx(t.Context(), db, "SELECT id, name FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	requireOK(t, res, 2)
}

func TestMySQL_RunQueryInTx_Insert(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectMySQL)
	db := h.NewDB(t)
	seedSimpleFixture(t, db)

	res, err := runQueryInTx(t.Context(),
		db,
		"INSERT INTO users (id, name, email) VALUES (3, 'Carol', 'carol@test.com')")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	requireInsertAffected(t, res, 1)
}

func TestMySQL_RunQueryInTx_InvalidQueryRollsBack(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectMySQL)
	db := h.NewDB(t)

	res, err := runQueryInTx(t.Context(), db, "INSERT INTO nonexistent VALUES (1)")
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.Status != "error" {
		t.Fatalf("status = %q, want error", res.Status)
	}
}

func TestMySQL_IntrospectSchema(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectMySQL)
	db := h.NewDB(t)
	seedIntrospectionFixture(t, db, DialectMySQL)

	yaml, err := introspectSchema(t.Context(), db, DialectMySQL, IntrospectConfig{})
	if err != nil {
		t.Fatalf("introspectSchema: %v", err)
	}
	requireIntrospectionShape(t, yaml)
}
