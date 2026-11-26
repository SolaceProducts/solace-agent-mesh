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
		DB:         db.RawDB(),
		Dialect:    db.Dialect(),
		TableName:  "platform_goose_version",
		SQLiteFS:   sqliteMigrations(),
		PostgresFS: postgresMigrations(),
		Logger:     db.Logger(),
	}
}

func (db *DB) Migrate(ctx context.Context) error {
	return dbutil.Migrate(ctx, db.migrateConfig())
}

func (db *DB) MigrateDown(ctx context.Context) error {
	return dbutil.MigrateDown(ctx, db.migrateConfig())
}
