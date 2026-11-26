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

var migrations = dbutil.MigrationSet{
	FS:           migrationFS,
	VersionTable: "product_analytics_goose_version",
	SQLiteDir:    "migrations/sqlite",
	PostgresDir:  "migrations/postgres",
}

type DB struct {
	*dbutil.DB
}

func Open(databaseURL string, logger *slog.Logger) (*DB, error) {
	inner, err := dbutil.Open(databaseURL, logger, dbutil.WithPoolName("product-analytics"))
	if err != nil {
		return nil, err
	}
	return &DB{DB: inner}, nil
}

func (db *DB) Migrate(ctx context.Context) error {
	return dbutil.MigrateSet(ctx, db.DB, migrations)
}

func (db *DB) migrateConfig() (dbutil.MigrateConfig, error) {
	return dbutil.MigrateConfigFor(db.DB, migrations)
}
