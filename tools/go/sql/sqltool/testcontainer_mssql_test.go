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

	"github.com/testcontainers/testcontainers-go/modules/mssql"
)

const (
	mssqlUser     = "sa"
	mssqlPassword = "Strong@Passw0rd_SAM"
)

type mssqlHarness struct {
	container *mssql.MSSQLServerContainer
	host      string
	port      string
}

func (h *mssqlHarness) Dialect() Dialect { return DialectMSSQL }

func (h *mssqlHarness) NewDB(t *testing.T) *sql.DB {
	t.Helper()
	dbName := uniqueDBName(t, "mssql")

	root, err := sql.Open("sqlserver", mssqlRootDSN(h.host, h.port, "master"))
	if err != nil {
		t.Fatalf("open root db: %v", err)
	}
	defer func() { _ = root.Close() }()

	if _, err := root.ExecContext(t.Context(), fmt.Sprintf("CREATE DATABASE [%s]", dbName)); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		droot, derr := sql.Open("sqlserver", mssqlRootDSN(h.host, h.port, "master"))
		if derr != nil {
			slog.Warn("mssql cleanup: open root db", "db", dbName, "err", derr)
			return
		}
		defer func() { _ = droot.Close() }()
		if _, err := droot.Exec(fmt.Sprintf("ALTER DATABASE [%s] SET SINGLE_USER WITH ROLLBACK IMMEDIATE", dbName)); err != nil {
			slog.Warn("mssql cleanup: set single user", "db", dbName, "err", err)
		}
		if _, err := droot.Exec(fmt.Sprintf("DROP DATABASE [%s]", dbName)); err != nil {
			slog.Warn("mssql cleanup: drop database", "db", dbName, "err", err)
		}
	})

	dsn := mssqlRootDSN(h.host, h.port, dbName)
	db, _, err := openDB(dsn)
	if err != nil {
		t.Fatalf("openDB %s: %v", dbName, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mssqlRootDSN(host, port, dbName string) string {
	return fmt.Sprintf("sqlserver://%s:%s@%s:%s?database=%s&encrypt=disable",
		mssqlUser, mssqlPassword, host, port, dbName)
}

func startMSSQLHarness(ctx context.Context) (dbHarness, func(context.Context) error, error) {
	c, err := mssql.Run(ctx, mssqlImage,
		mssql.WithAcceptEULA(),
		mssql.WithPassword(mssqlPassword),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("mssql run: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, nil, fmt.Errorf("mssql host: %w", err)
	}
	port, err := c.MappedPort(ctx, "1433/tcp")
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, nil, fmt.Errorf("mssql mapped port: %w", err)
	}

	stop := func(ctx context.Context) error { return c.Terminate(ctx) }
	return &mssqlHarness{container: c, host: host, port: port.Port()}, stop, nil
}

func TestMSSQL_OpenDB_Pings(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectMSSQL)
	db := h.NewDB(t)
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestMSSQL_RunQueryInTx_Select(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectMSSQL)
	db := h.NewDB(t)
	seedSimpleFixture(t, db)

	res, err := runQueryInTx(t.Context(), db, "SELECT id, name FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	requireOK(t, res, 2)
}

func TestMSSQL_RunQueryInTx_Insert(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectMSSQL)
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

func TestMSSQL_RunQueryInTx_InvalidQueryRollsBack(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectMSSQL)
	db := h.NewDB(t)

	res, err := runQueryInTx(t.Context(), db, "INSERT INTO nonexistent VALUES (1)")
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.Status != "error" {
		t.Fatalf("status = %q, want error", res.Status)
	}
}

func TestMSSQL_IntrospectSchema(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectMSSQL)
	db := h.NewDB(t)
	seedIntrospectionFixture(t, db, DialectMSSQL)

	yaml, err := introspectSchema(t.Context(), db, DialectMSSQL, IntrospectConfig{})
	if err != nil {
		t.Fatalf("introspectSchema: %v", err)
	}
	requireIntrospectionShape(t, yaml)
}
