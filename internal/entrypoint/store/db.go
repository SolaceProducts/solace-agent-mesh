// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

type DB struct {
	*dbutil.DB
}

func Open(databaseURL string, logger *slog.Logger, opts ...dbutil.OpenOption) (*DB, error) {
	inner, err := dbutil.Open(databaseURL, logger, opts...)
	if err != nil {
		return nil, err
	}
	return &DB{DB: inner}, nil
}

func (db *DB) migrateConfig() dbutil.MigrateConfig {
	return dbutil.MigrateConfig{
		DB:              db.RawDB(),
		Dialect:         db.Dialect(),
		TableName:       "gateway_goose_version",
		SQLiteFS:        sqliteMigrations(),
		PostgresFS:      postgresMigrations(),
		Logger:          db.Logger(),
		AsyncIndexQueue: db.asyncIndexConfig(),
	}
}

func (db *DB) Migrate(ctx context.Context) error {
	return dbutil.Migrate(ctx, db.migrateConfig())
}

func (db *DB) MigrateUpTo(ctx context.Context, version int64) error {
	return dbutil.MigrateUpTo(ctx, db.migrateConfig(), version)
}

func (db *DB) MigrateDown(ctx context.Context) error {
	return dbutil.MigrateDown(ctx, db.migrateConfig())
}

func (db *DB) MigrateDownTo(ctx context.Context, version int64) error {
	return dbutil.MigrateDownTo(ctx, db.migrateConfig(), version)
}

func (db *DB) asyncIndexConfig() dbutil.AsyncIndexConfig {
	return dbutil.AsyncIndexConfig{
		DB:         db.RawDB(),
		QueueTable: "gateway_async_indexes",
		Logger:     db.Logger(),
	}
}

func (db *DB) RunAsyncIndexes(ctx context.Context) {
	if db.Dialect() != "postgres" {
		return
	}
	dbutil.RunAsyncIndexes(ctx, db.asyncIndexConfig())
}

func (db *DB) WarnGivenUpAsyncIndexes(ctx context.Context) {
	if db.Dialect() != "postgres" {
		return
	}
	dbutil.WarnGivenUpAsyncIndexes(ctx, db.asyncIndexConfig())
}

func (db *DB) BuildAsyncIndexes(ctx context.Context) error {
	if db.Dialect() != "postgres" {
		return nil
	}
	return dbutil.BuildAsyncIndexes(ctx, db.asyncIndexConfig())
}
