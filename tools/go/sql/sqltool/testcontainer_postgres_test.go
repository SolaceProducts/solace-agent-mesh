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

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	pgUser     = "sam"
	pgPassword = "sam_test_password"
	pgRootDB   = "postgres"
)

type postgresHarness struct {
	container *postgres.PostgresContainer
	host      string
	port      string
}

func (h *postgresHarness) Dialect() Dialect { return DialectPostgres }

func (h *postgresHarness) dsn(dbName string) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		pgUser, pgPassword, h.host, h.port, dbName)
}

func (h *postgresHarness) NewDB(t *testing.T) *sql.DB {
	t.Helper()
	dbName := uniqueDBName(t, "pg")

	root, err := sql.Open("pgx", h.dsn(pgRootDB))
	if err != nil {
		t.Fatalf("open root db: %v", err)
	}
	defer func() { _ = root.Close() }()

	if _, err := root.ExecContext(t.Context(), fmt.Sprintf(`CREATE DATABASE %q`, dbName)); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		droot, derr := sql.Open("pgx", h.dsn(pgRootDB))
		if derr != nil {
			slog.Warn("postgres cleanup: open root db", "db", dbName, "err", derr)
			return
		}
		defer func() { _ = droot.Close() }()
		if _, err := droot.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS %q WITH (FORCE)`, dbName)); err != nil {
			slog.Warn("postgres cleanup: drop database", "db", dbName, "err", err)
		}
	})

	db, _, err := openDB(h.dsn(dbName))
	if err != nil {
		t.Fatalf("openDB %s: %v", dbName, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func startPostgresHarness(ctx context.Context) (dbHarness, func(context.Context) error, error) {
	c, err := postgres.Run(ctx, postgresImage,
		postgres.WithDatabase(pgRootDB),
		postgres.WithUsername(pgUser),
		postgres.WithPassword(pgPassword),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres run: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, nil, fmt.Errorf("postgres host: %w", err)
	}
	port, err := c.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, nil, fmt.Errorf("postgres mapped port: %w", err)
	}

	stop := func(ctx context.Context) error { return c.Terminate(ctx) }
	return &postgresHarness{container: c, host: host, port: port.Port()}, stop, nil
}

func TestPostgres_OpenDB_Pings(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectPostgres)
	db := h.NewDB(t)
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestPostgres_RunQueryInTx_Select(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectPostgres)
	db := h.NewDB(t)
	seedSimpleFixture(t, db)

	res, err := runQueryInTx(t.Context(), db, "SELECT id, name FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	requireOK(t, res, 2)
}

func TestPostgres_RunQueryInTx_Insert(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectPostgres)
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

func TestPostgres_RunQueryInTx_InsertReturning(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectPostgres)
	db := h.NewDB(t)
	seedSimpleFixture(t, db)

	res, err := runQueryInTx(t.Context(),
		db,
		"INSERT INTO users (id, name, email) VALUES (4, 'Dan', 'dan@test.com') RETURNING id, name")
	if err != nil {
		t.Fatalf("runQueryInTx: %v", err)
	}
	requireOK(t, res, 1)
	rows := selectRows(t, res)
	if rows[0]["name"] != "Dan" {
		t.Errorf("name = %v, want Dan", rows[0]["name"])
	}
}

func TestPostgres_RunQueryInTx_InvalidQueryRollsBack(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectPostgres)
	db := h.NewDB(t)

	res, err := runQueryInTx(t.Context(), db, "INSERT INTO nonexistent VALUES (1)")
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if res.Status != "error" {
		t.Fatalf("status = %q, want error", res.Status)
	}
}

func TestPostgres_IntrospectSchema(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectPostgres)
	db := h.NewDB(t)
	seedIntrospectionFixture(t, db, DialectPostgres)

	yaml, err := introspectSchema(t.Context(), db, DialectPostgres, IntrospectConfig{})
	if err != nil {
		t.Fatalf("introspectSchema: %v", err)
	}
	requireIntrospectionShape(t, yaml)
}

func TestPostgres_IntrospectSchema_RoleSchemaDoesNotHidePublic(t *testing.T) {
	t.Parallel()
	h := registry.get(t, DialectPostgres)
	db := h.NewDB(t)
	seedIntrospectionFixture(t, db, DialectPostgres)
	if _, err := db.ExecContext(t.Context(), fmt.Sprintf("CREATE SCHEMA %q", pgUser)); err != nil {
		t.Fatalf("create role schema: %v", err)
	}

	yaml, err := introspectSchema(t.Context(), db, DialectPostgres, IntrospectConfig{})
	if err != nil {
		t.Fatalf("introspectSchema: %v", err)
	}
	requireIntrospectionShape(t, yaml)
}
