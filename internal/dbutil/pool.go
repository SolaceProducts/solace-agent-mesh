// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/stdlib"
)

var ErrNotPostgres = errors.New("not a postgres database URL")

func OpenPostgresPool(ctx context.Context, databaseURL string, maxConns int, logger *slog.Logger, opts ...OpenOption) (*DB, error) {
	return openPostgresPool(ctx, databaseURL, maxConns, logger, false, opts)
}

func OpenReadOnlyPostgresPool(ctx context.Context, databaseURL string, maxConns int, logger *slog.Logger, opts ...OpenOption) (*DB, error) {
	return openPostgresPool(ctx, databaseURL, maxConns, logger, true, opts)
}

func openPostgresPool(ctx context.Context, databaseURL string, maxConns int, logger *slog.Logger, readOnly bool, opts []OpenOption) (*DB, error) {
	if !strings.HasPrefix(databaseURL, "postgres://") && !strings.HasPrefix(databaseURL, "postgresql://") {
		return nil, ErrNotPostgres
	}
	var o openOptions
	for _, opt := range opts {
		opt(&o)
	}
	cfg, err := buildPostgresConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	setApplicationName(cfg, o.poolName)
	if readOnly {
		cfg.RuntimeParams["default_transaction_read_only"] = "on"
	}
	db := stdlib.OpenDB(*cfg)
	sizePostgresPool(db, maxConns, maxConns)
	if err := pingOrClose(ctx, db); err != nil {
		return nil, err
	}
	trackPool(db, o.poolName)
	return &DB{DB: db, dialect: "postgres", logger: logger}, nil
}
