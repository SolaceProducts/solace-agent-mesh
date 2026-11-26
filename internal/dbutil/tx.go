// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package dbutil

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func rebind(dialect, query string) string {
	if dialect != "postgres" {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 16)
	idx := 1
	for i := range len(query) {
		if query[i] == '?' {
			fmt.Fprintf(&b, "$%d", idx)
			idx++
		} else {
			b.WriteByte(query[i])
		}
	}
	return b.String()
}

type Tx struct {
	*sql.Tx
	dialect     string
	postgresSQL bool
}

func (tx *Tx) Dialect() string {
	return tx.dialect
}

func (tx *Tx) Rebind(query string) string {
	if tx.postgresSQL {
		query = postgresToSQLite(query)
	}
	return rebind(tx.dialect, query)
}

func (tx *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.Tx.ExecContext(ctx, tx.Rebind(query), args...)
}

func (tx *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.Tx.QueryContext(ctx, tx.Rebind(query), args...)
}

func (tx *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.Tx.QueryRowContext(ctx, tx.Rebind(query), args...)
}

func WithTxVal[T any](ctx context.Context, db *DB, fn func(tx *Tx) (T, error)) (T, error) {
	var result T
	err := db.WithTx(ctx, func(tx *Tx) error {
		var fnErr error
		result, fnErr = fn(tx)
		return fnErr
	})
	return result, err
}
